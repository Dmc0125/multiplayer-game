package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"server/src/core"
	"server/testing/client"
	"slices"
	"sync"
	"time"
)

type MovingDirection int

const (
	movingDirectionNone MovingDirection = iota
	movingDirectionUp
	movingDirectionDown
)

func (m MovingDirection) keyCode() core.KeyCode {
	switch m {
	case movingDirectionUp:
		return core.KeyCodeArrowUp
	case movingDirectionDown:
		return core.KeyCodeArrowDown
	default:
		return 255
	}
}

type SingleplayerClient struct {
	c               *client.Client
	stats           *Stats
	movingDirection MovingDirection
}

func (c *SingleplayerClient) wsRead(
	timeout time.Duration,
	wanted []core.MessageType,
	allowed []core.MessageType,
) (msgType core.MessageType, data []byte, err error) {
	for {
		msgType, data, err = c.c.Read(timeout)
		if errors.Is(err, context.DeadlineExceeded) {
			c.stats.wsReadTimeoutsTotal.Add(1)
			return
		}
		if err != nil {
			c.stats.wsReadErrorsTotal.Add(1)
			return
		}

		if slices.Contains(wanted, msgType) {
			return
		}

		if slices.Contains(allowed, msgType) {
			continue
		}

		log.Output(2, fmt.Sprintf("unexpected message: %d", msgType))
		c.stats.unexpectedMessagesTotal.Add(1)
		err = fmt.Errorf("unexpected message")
		return
	}
}

func (c *SingleplayerClient) wsWrite(timeout time.Duration, msgType core.MessageType, data []byte) error {
	err := c.c.Send(msgType, data, timeout)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		c.stats.wsWriteTimeoutsTotal.Add(1)
		return err
	}
	if err != nil {
		c.stats.wsWriteErrorsTotal.Add(1)
		return err
	}
	return nil
}

func (c *SingleplayerClient) waitForLobby() (ok bool) {
	const timeout = 2 * time.Second

	msgType, _, err := c.wsRead(
		timeout,
		[]core.MessageType{core.MessageTypeFull, core.MessageTypeLobbyState},
		[]core.MessageType{core.MessageTypePong},
	)
	if err != nil {
		return
	}

	switch msgType {
	case core.MessageTypeFull:
		c.stats.lobbiesFullTotal.Add(1)
	case core.MessageTypeLobbyState:
		c.stats.lobbiesJoinedTotal.Add(1)
		ok = true
	}

	return
}

func (c *SingleplayerClient) startGame() (ok bool) {
	const timeout = 2 * time.Second

	// send start
	if err := c.c.SendStart(timeout); err != nil {
		c.stats.wsWriteErrorsTotal.Add(1)
		return
	}

	// drain ready
	_, _, err := c.wsRead(
		timeout,
		[]core.MessageType{core.MessageTypeReady},
		[]core.MessageType{core.MessageTypePong},
	)
	if err != nil {
		return
	}

	// drain started
	_, _, err = c.wsRead(
		timeout,
		[]core.MessageType{core.MessageTypeStarted},
		[]core.MessageType{core.MessageTypePong},
	)
	if err != nil {
		return
	}

	c.stats.gamesStartedTotal.Add(1)
	ok = true
	return
}

func (c *SingleplayerClient) handleGameState(data []byte) {
	c.stats.gameStatesEventsReceivedTotal.Add(1)

	offset := 4
	decodeFloat32 := func(data []byte) float32 {
		f := math.Float32frombits(binary.LittleEndian.Uint32(data))
		offset += 4
		return f
	}

	rightPaddleY := decodeFloat32(data[offset:])
	ball := core.GameBall{
		X:  decodeFloat32(data[offset:]),
		Y:  decodeFloat32(data[offset:]),
		Vx: decodeFloat32(data[offset:]),
		Vy: decodeFloat32(data[offset:]),
	}

	// fmt.Println(leftPaddleY, rightPaddleY, ball)

	ps := core.GamePlayerState{}
	core.PredictBallY(ball, &ps, false)

	if ps.Move && c.movingDirection == movingDirectionNone {
		var movingDirection MovingDirection
		if ball.Y < rightPaddleY {
			movingDirection = movingDirectionUp
		} else if ball.Y > rightPaddleY+core.PADDLE_HEIGHT {
			movingDirection = movingDirectionDown
		}

		// send move
		if movingDirection != movingDirectionNone {
			d := []byte{byte(movingDirection.keyCode()), 1}
			if err := c.wsWrite(1*time.Second, core.MessageTypeKey, d); err != nil {
				return
			}
			c.movingDirection = movingDirection
			c.stats.keydownsSentTotal.Add(1)
		}
	} else if !ps.Move && c.movingDirection != movingDirectionNone {
		// send stop
		d := []byte{byte(c.movingDirection.keyCode()), 0}
		if err := c.wsWrite(1*time.Second, core.MessageTypeKey, d); err != nil {
			return
		}
		c.movingDirection = movingDirectionNone
		c.stats.keyupsSentTotal.Add(1)
	}
}

func (c *SingleplayerClient) simulate(ctx context.Context, url string) {
	c.stats.clientsConnectionsAttemptedTotal.Add(1)

	handshakeStart := time.Now()
	var err error
	c.c, err = client.ClientConnect(url+"?singleplayer=1", context.Background())
	c.stats.handshakeLatency.Add(time.Since(handshakeStart))

	if err != nil {
		c.stats.wsConnectionErrorsTotal.Add(1)
		return
	}
	defer c.c.Close()

	c.stats.clientsConnectedTotal.Add(1)
	c.stats.connectionsActive.Add(1)
	defer c.stats.connectionsActive.Add(-1)

	if !(c.waitForLobby() && c.startGame()) {
		return
	}

	c.stats.gamesActive.Add(1)
	defer c.stats.gamesActive.Add(-1)

	//
	pingTicker := time.NewTicker(1 * time.Second)
	var pingId uint32
	pings := make(map[uint32]time.Time)
	stop := false

	for {
		select {
		case <-pingTicker.C:
			pings[pingId] = time.Now()

			d := make([]byte, 4)
			binary.LittleEndian.PutUint32(d, uint32(pingId))
			pingId += 1

			c.wsWrite(1*time.Second, core.MessageTypePing, d)
		default:
			select {
			case <-ctx.Done():
				stop = true
			default:
			}

			msgType, data, err := c.c.Read(5 * time.Second)
			if errors.Is(err, context.DeadlineExceeded) {
				c.stats.wsReadTimeoutsTotal.Add(1)
				continue
			}
			if err != nil {
				log.Println(err)
				c.stats.wsReadErrorsTotal.Add(1)
				return
			}

			switch msgType {
			case core.MessageTypeGameState:
				if len(data) < 1 {
					log.Println("invalid game state message", msgType, data)
					c.stats.invalidMessagesTotal.Add(1)
					continue
				}

				switch data[0] {
				case byte(core.GameEventTypeState):
					c.handleGameState(data[1:])
				}
			case core.MessageTypeSaved:
				if stop || !c.startGame() {
					return
				}
			case core.MessageTypePong:
				mid := binary.LittleEndian.Uint32(data)
				sentAt, ok := pings[mid]
				if !ok {
					log.Println("invalid ping message", msgType, data)
					c.stats.invalidMessagesTotal.Add(1)
					continue
				}

				latency := time.Since(sentAt)
				c.stats.pingPongLatency.Add(latency)
				delete(pings, mid)
			}
		}
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	url := flag.String("url", WS_URL, "websocket url")
	clients := flag.Uint("clients", 10, "number of clients to run")
	rampTime := flag.Duration("ramp", time.Minute*5, "ramp up time for clients to connect")
	holdTime := flag.Duration("hold", time.Minute*10, "how long should clients hold their connections")
	silent := flag.Bool("silent", false, "silent mode")
	flag.Parse()

	fmt.Printf("Starting stress test with %d clients: ramp up for %s, hold for %s\n", *clients, *rampTime, *holdTime)

	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), *rampTime+*holdTime)
	stats := newStats(!*silent)

	testStateChan := make(chan TestState)
	go stats.printRegularly(testStateChan)

	// ramp up
	testStateChan <- TestStateRampUp
	var wg sync.WaitGroup
	connectionDelay := *rampTime / time.Duration(*clients)
	for i := 0; i < int(*clients); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := SingleplayerClient{
				stats: stats,
			}
			c.simulate(ctx, *url)
		}()
		time.Sleep(connectionDelay)
	}

	// hold
	testStateChan <- TestStateHold
	time.Sleep(*holdTime)

	testStateChan <- TestStateWaitingForSimulations
	cancel()
	wg.Wait()

	testStateChan <- TestStateWaitingForStats
	sd := make(chan struct{})
	stats.done <- sd
	<-sd

	stats.handshakeLatency.PrintDashboard()
	stats.pingPongLatency.PrintDashboard()

	fmt.Printf("Test duration: %s\n", time.Since(start))
}
