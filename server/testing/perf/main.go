package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"math"
	"server/src/core"
	"server/testing/client"
	"sync"
	"time"
)

func main() {
	url := flag.String("url", WS_URL, "websocket url")
	duration := flag.Duration("duration", 2*time.Minute, "duration of the test")
	clients := flag.Uint("clients", 10, "number of clients to run")
	ramp := flag.Duration("ramp", time.Second*20, "ramp up time for clients to connect")
	silent := flag.Bool("silent", false, "silent mode")
	flag.Parse()

	fmt.Printf("Starting stress test with %d clients for %s\n", *clients, *duration)

	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	stats := newStats(!*silent)
	go stats.printRegularly(ctx)

	var wg sync.WaitGroup
	connectionDelay := *ramp / time.Duration(*clients)

	for i := 0; i < int(*clients); i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			c := SingleplayerClient{
				stats: stats,
			}
			c.Simulate(ctx, *url)
		}()

		time.Sleep(connectionDelay)
	}

	wg.Wait()
	cancel()
	<-stats.done

	stats.handshakeLatency.PrintDashboard()
	stats.pingPongLatency.PrintDashboard()

	fmt.Printf("Test duration: %s\n", time.Since(start))
}

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

func (c *SingleplayerClient) start() (ok bool) {
	const timeout = 2 * time.Second

	// drain lobby state
	msgType, _, err := c.c.Read(timeout)
	if err != nil {
		c.stats.wsReadErrorsTotal.Add(1)
		return
	}
	switch msgType {
	case core.MessageTypeFull:
		c.stats.lobbiesFullTotal.Add(1)
		return
	case core.MessageTypeLobbyState:
		c.stats.lobbiesJoinedTotal.Add(1)
	default:
		c.stats.unexpectedMessagesTotal.Add(1)
		return
	}

	c.stats.lobbiesConnectedTotal.Add(1)

	// send start
	if err := c.c.SendStart(timeout); err != nil {
		c.stats.wsWriteErrorsTotal.Add(1)
		return
	}

	// drain ready
	msgType, _, err = c.c.Read(timeout)
	if err != nil {
		c.stats.wsReadErrorsTotal.Add(1)
		return
	}
	if msgType != core.MessageTypeReady {
		c.stats.unexpectedMessagesTotal.Add(1)
		return
	}

	// drain started
	msgType, _, err = c.c.Read(timeout)
	if err != nil {
		c.stats.wsReadErrorsTotal.Add(1)
		return
	}
	if msgType != core.MessageTypeStarted {
		c.stats.unexpectedMessagesTotal.Add(1)
		return
	}

	c.stats.gamesStartedTotal.Add(1)

	ok = true
	return
}

func (c *SingleplayerClient) handleGameState(data []byte) (done bool) {
	if data[0] == byte(core.GameEventTypeWinner) {
		done = true
		return
	}
	if data[0] != byte(core.GameEventTypeState) {
		return
	}

	c.stats.gameStatesEventsReceivedTotal.Add(1)

	offset := 5

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
			if err := c.c.Send(core.MessageTypeKey, d, 1*time.Second); err != nil {
				c.stats.wsWriteErrorsTotal.Add(1)
			}
			c.movingDirection = movingDirection
			c.stats.keydownsSentTotal.Add(1)
		}
	} else if !ps.Move && c.movingDirection != movingDirectionNone {
		// send stop
		d := []byte{byte(c.movingDirection.keyCode()), 0}
		if err := c.c.Send(core.MessageTypeKey, d, 1*time.Second); err != nil {
			c.stats.wsWriteErrorsTotal.Add(1)
		}
		c.movingDirection = movingDirectionNone
		c.stats.keyupsSentTotal.Add(1)
	}
	return
}

func (c *SingleplayerClient) Simulate(ctx context.Context, url string) {
	c.stats.clientsConnectionsAttemptedTotal.Add(1)

	handshakeStart := time.Now()
	var err error
	c.c, err = client.ClientConnect(url+"?singleplayer=1", ctx)
	c.stats.handshakeLatency.Add(time.Since(handshakeStart))

	if err != nil {
		c.stats.wsConnectionErrorsTotal.Add(1)
		return
	}
	defer c.c.Close()

	c.stats.clientsConnectedTotal.Add(1)
	c.stats.connectionsActive.Add(1)
	defer c.stats.connectionsActive.Add(-1)

	if !c.start() {
		return
	}

	c.stats.gamesActive.Add(1)
	defer c.stats.gamesActive.Add(-1)

	//
	pingTicker := time.NewTicker(1 * time.Second)
	var pingId uint32
	pings := make(map[uint32]time.Time)

	for {
		select {
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			pings[pingId] = time.Now()

			d := make([]byte, 4)
			binary.LittleEndian.PutUint32(d, uint32(pingId))
			pingId += 1

			if err := c.c.Send(core.MessageTypePing, d, 1*time.Second); err != nil {
				c.stats.wsWriteErrorsTotal.Add(1)
			}
		default:
			msgType, data, err := c.c.Read(5 * time.Second)
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			if err != nil {
				c.stats.wsReadErrorsTotal.Add(1)
				return
			}

			switch msgType {
			case core.MessageTypeGameState:
				if c.handleGameState(data) {
					return
				}
			case core.MessageTypePong:
				mid := binary.LittleEndian.Uint32(data)
				sentAt, ok := pings[mid]
				if !ok {
					c.stats.unexpectedMessagesTotal.Add(1)
					continue
				}

				latency := time.Since(sentAt)
				c.stats.pingPongLatency.Add(latency)
				delete(pings, mid)
			}
		}
	}
}
