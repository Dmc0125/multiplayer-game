package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"server/testing/client"
	"sync"
	"sync/atomic"
	"time"
)

type LatencyBuckets struct {
	name    string
	mu      sync.Mutex
	buckets []uint64
	limits  []time.Duration
}

func NewLatencyBuckets(name string) *LatencyBuckets {
	limits := [...]time.Duration{
		1 * time.Millisecond,
		3 * time.Millisecond,
		5 * time.Millisecond,
		8 * time.Millisecond,
		10 * time.Millisecond,
		15 * time.Millisecond,
		20 * time.Millisecond,
		25 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
		250 * time.Millisecond,
		500 * time.Millisecond,
	}
	buckets := make([]uint64, len(limits))
	return &LatencyBuckets{
		name:    name,
		limits:  limits[:],
		buckets: buckets,
	}
}

func (h *LatencyBuckets) Add(value time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i, limit := range h.limits {
		if value <= limit {
			h.buckets[i]++
			return
		}
	}

	h.buckets[len(h.buckets)-1]++
}

func (h *LatencyBuckets) Print() {
	h.mu.Lock()
	defer h.mu.Unlock()

	fmt.Printf("[%s] latency buckets:\n", h.name)
	for i, limit := range h.limits {
		fmt.Printf("  <= %s: %d\n", limit.String(), h.buckets[i])
	}
}

const WS_URL = "ws://localhost:8080/api/game"

type Stats struct {
	handshakeLatency      *LatencyBuckets
	pingPongLatency       *LatencyBuckets
	startToReadyLatency   *LatencyBuckets
	startToStartedLatency *LatencyBuckets

	wsReadErrors  atomic.Uint64
	wsWriteErrors atomic.Uint64
	unexpectedMessages atomic.Int64

	handshakeFailures  atomic.Uint64
	lobbyJoinResponses atomic.Uint64
	lobbyFullResponses atomic.Uint64

	clientsConnectionsAttempted atomic.Int64
	clientsConnected            atomic.Int64

	lobbiesActive atomic.Int64
	gamesActive   atomic.Int64
}

func main() {
	url := flag.String("url", WS_URL, "websocket url")
	duration := flag.Duration("duration", 2*time.Minute, "duration of the test")
	clients := flag.Uint("clients", 10, "number of clients to run")
	ramp := flag.Duration("ramp", time.Second*20, "ramp up time for clients to connect")
	flag.Parse()

	fmt.Printf("Starting stress test with %d clients for %s\n", *clients, *duration)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	stats := Stats{
		handshakeLatency:      NewLatencyBuckets("handshake"),
		pingPongLatency:       NewLatencyBuckets("ping-pong"),
		startToReadyLatency:   NewLatencyBuckets("start-to-ready"),
		startToStartedLatency: NewLatencyBuckets("start-to-started"),
	}

	reportDone := make(chan struct{}, 1)
	go report(ctx, start, &stats, reportDone)
	var wg sync.WaitGroup
	connectionDelay := *ramp / time.Duration(*clients)

	for i := 0; i < int(*clients); i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			run(ctx, &stats, *url)
		}()

		time.Sleep(connectionDelay)
	}

	wg.Wait()
	cancel()
	<-reportDone

	stats.handshakeLatency.Print()
	stats.pingPongLatency.Print()
	stats.startToReadyLatency.Print()
	stats.startToStartedLatency.Print()

	fmt.Printf("Lobbies joined: %d\n", stats.lobbyJoinResponses.Load())
	fmt.Printf("Lobbies full: %d\n", stats.lobbyFullResponses.Load())
}

func waitForStart(c *client.Client, stats *Stats) (ok bool) {
	// drain lobby state
	msgType, _, err := c.Read(1 * time.Second)
	if err != nil {
		stats.wsReadErrors.Add(1)
		return
	}
	switch msgType {
	case client.MessageTypeFull:
		stats.lobbyFullResponses.Add(1)
		return
	case client.MessageTypeLobbyState:
		stats.lobbyJoinResponses.Add(1)
	default:
		stats.unexpectedMessages.Add(1)
		return
	}

	stats.lobbiesActive.Add(1)

	startSendTime := time.Now()

	// send start
	if err := c.SendStart(1 * time.Second); err != nil {
		stats.wsWriteErrors.Add(1)
		return
	}

	// drain ready
	msgType, _, err = c.Read(1 * time.Second)
	if err != nil {
		stats.wsReadErrors.Add(1)
		return
	}
	if msgType != client.MessageTypeReady {
		stats.unexpectedMessages.Add(1)
		return
	}

	stats.startToReadyLatency.Add(time.Since(startSendTime))

	// drain started
	msgType, _, err = c.Read(1 * time.Second)
	if err != nil {
		stats.wsReadErrors.Add(1)
		return
	}
	if msgType != client.MessageTypeStarted {
		stats.unexpectedMessages.Add(1)
		return
	}

	stats.startToStartedLatency.Add(time.Since(startSendTime))

	ok = true
	return
}

type PongForward struct {
	receivedAt time.Time
	messageId  uint32
}

func run(ctx context.Context, stats *Stats, url string) {
	stats.clientsConnectionsAttempted.Add(1)
	start := time.Now()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	c, err := client.ClientConnect(fmt.Sprintf("%s?singleplayer=1", url), runCtx)
	if err != nil {
		stats.handshakeFailures.Add(1)
		return
	}
	defer func() {
		c.Close()
		stats.clientsConnected.Add(-1)
	}()

	stats.handshakeLatency.Add(time.Since(start))
	stats.clientsConnected.Add(1)

	if !waitForStart(c, stats) {
		return
	}

	defer func() {
		stats.lobbiesActive.Add(-1)
		stats.gamesActive.Add(-1)
	}()

	stats.gamesActive.Add(1)

	pingTicker := time.NewTicker(1 * time.Second)
	defer pingTicker.Stop()

	messageId := uint32(0)
	pongMessages := make(map[uint32]time.Time)

	pongChan := make(chan PongForward)
	gameEndChan := make(chan struct{})
	go read(runCtx, c, stats, pongChan, gameEndChan)

	for {
		select {
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			sentAt := time.Now()
			pongMessages[messageId] = sentAt

			data := binary.LittleEndian.AppendUint32(nil, messageId)
			messageId += 1

			if err := c.Send(client.MessageTypePing, data, 1*time.Second); err != nil {
				stats.wsWriteErrors.Add(1)
				return
			}
		case <-gameEndChan:
			return
		case m := <-pongChan:
			sentAt, ok := pongMessages[m.messageId]
			if !ok {
				continue
			}

			delete(pongMessages, m.messageId)
			stats.pingPongLatency.Add(m.receivedAt.Sub(sentAt))
		}
	}
}

func read(ctx context.Context, c *client.Client, stats *Stats, pongChannel chan PongForward, gameEndChan chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msgType, data, err := c.Read(5 * time.Second)
			if err != nil {
				stats.wsReadErrors.Add(1)
				continue
			}

			switch msgType {
			case client.MessageTypeGameEnd:
				gameEndChan <- struct{}{}
				return
			case client.MessageTypePong:
				messageId := binary.LittleEndian.Uint32(data)
				pongChannel <- PongForward{
					receivedAt: time.Now(),
					messageId:  messageId,
				}
			}
		}
	}
}

func report(ctx context.Context, start time.Time, stats *Stats, doneChan chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)

	for {
		select {
		case <-ctx.Done():
			doneChan <- struct{}{}
			return
		case <-ticker.C:
			fmt.Println("------------------")
			fmt.Printf("Time elapsed: %s\n", time.Since(start))
			fmt.Printf("Clients connections attempted: %d\n", stats.clientsConnectionsAttempted.Load())
			fmt.Printf("Clients connected: %d\n", stats.clientsConnected.Load())
			fmt.Printf("Lobbies active: %d\n", stats.lobbiesActive.Load())
			fmt.Printf("Games active: %d\n", stats.gamesActive.Load())
			fmt.Printf("Unexpected messages: %d\n", stats.unexpectedMessages.Load())
			fmt.Printf("WS read errors: %d\n", stats.wsReadErrors.Load())
			fmt.Printf("WS write errors: %d\n", stats.wsWriteErrors.Load())
			fmt.Println("------------------")
		}
	}
}
