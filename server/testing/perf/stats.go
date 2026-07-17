package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

func writeSection(b *strings.Builder, name, color string) {
	fmt.Fprintf(
		b,
		"\n%s%s── %s ─────────────────────────────────────%s\n",
		colorBold,
		color,
		name,
		colorReset,
	)
}

type LatencyBuckets struct {
	name    string
	buckets []atomic.Uint64
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
	buckets := make([]atomic.Uint64, len(limits)+1)
	return &LatencyBuckets{
		name:    name,
		limits:  limits[:],
		buckets: buckets,
	}
}

func (h *LatencyBuckets) Add(value time.Duration) {
	for i, limit := range h.limits {
		if value <= limit {
			h.buckets[i].Add(1)
			return
		}
	}

	h.buckets[len(h.buckets)-1].Add(1)
}

func (h *LatencyBuckets) PrintDashboard() {
	b := strings.Builder{}

	writeSection(&b, h.name, colorGreen)

	fmt.Fprintf(&b, "\n")

	var maxCount uint64 = 0
	for i := range h.limits {
		count := h.buckets[i].Load()
		if count > maxCount {
			maxCount = count
		}
	}

	for i, limit := range h.limits {
		// label
		var label string
		if i == len(h.limits)-1 {
			label = ">  " + limit.String()
		} else {
			label = "<= " + limit.String()
		}

		// bar
		c := h.buckets[i].Load()
		var bar string
		if c != 0 {
			size := int(float64(c) / float64(maxCount) * 20.0)
			if size == 0 {
				size = 1
			}
			bar = strings.Repeat("█", size)
		}

		//
		fmt.Fprintf(
			&b,
			"  %-12s %-20s %8d\n",
			label,
			bar,
			c,
		)
	}

	fmt.Println(b.String())
}

const WS_URL = "ws://localhost:8080/api/game"

type Stats struct {
	handshakeLatency *LatencyBuckets
	pingPongLatency  *LatencyBuckets

	wsConnectionErrorsTotal atomic.Uint64
	wsReadErrorsTotal       atomic.Uint64
	wsWriteErrorsTotal      atomic.Uint64
	unexpectedMessagesTotal atomic.Int64

	lobbiesJoinedTotal atomic.Uint64
	lobbiesFullTotal   atomic.Uint64

	clientsConnectionsAttemptedTotal atomic.Int64
	clientsConnectedTotal            atomic.Int64

	lobbiesConnectedTotal atomic.Int64
	gamesStartedTotal     atomic.Int64

	connectionsActive atomic.Int64
	gamesActive       atomic.Int64

	keydownsSentTotal             atomic.Int64
	keyupsSentTotal               atomic.Int64
	gameStatesEventsReceivedTotal atomic.Int64

	//
	done  chan struct{}
	print bool
}

func newStats(p bool) *Stats {
	return &Stats{
		handshakeLatency: NewLatencyBuckets("handshake"),
		pingPongLatency:  NewLatencyBuckets("ping-pong"),
		done:             make(chan struct{}),
		print:            p,
	}
}

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
)

func (s *Stats) printRegularly(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)

	const (
		showCursor = "\033[?25h"
		hideCursor = "\033[?25l"
	)

	fmt.Fprint(os.Stdout, hideCursor)
	defer func() {
		fmt.Fprint(os.Stdout, colorReset, showCursor, "\n")
	}()

	lines := 0

	print := func() {
		if s.print {
			output := s.dashboard()

			if lines > 0 {
				// Move to the beginning of the first previously printed line.
				fmt.Fprintf(os.Stdout, "\r\033[%dA", lines)
			}

			fmt.Fprint(os.Stdout, output)
			lines = strings.Count(output, "\n")
		}
	}

	for {
		select {
		case <-ctx.Done():
			print()
			close(s.done)
			return
		case <-ticker.C:
			print()
		}
	}
}

func (s *Stats) dashboard() string {
	var b strings.Builder

	fmt.Fprintf(
		&b,
		"%s%s┌──────────────────────────────────────────────┐%s\n",
		colorBold,
		colorCyan,
		colorReset,
	)
	fmt.Fprintf(
		&b,
		"%s%s│               SERVER STATISTICS              │%s\n",
		colorBold,
		colorCyan,
		colorReset,
	)
	fmt.Fprintf(
		&b,
		"%s%s└──────────────────────────────────────────────┘%s\n",
		colorBold,
		colorCyan,
		colorReset,
	)
	fmt.Fprintf(
		&b,
		"%sUpdated: %s%s\n",
		colorDim,
		time.Now().Format("15:04:05"),
		colorReset,
	)

	writeMetric := func(
		b *strings.Builder,
		name string,
		value any,
		color string,
	) {
		fmt.Fprintf(
			b,
			"  %-28s %s%12v%s\n",
			name,
			color,
			value,
			colorReset,
		)
	}

	errorColor := func(value int64) string {
		if value == 0 {
			return colorGreen
		}

		return colorRed
	}

	writeSection(&b, "ACTIVE", colorGreen)
	writeMetric(
		&b,
		"Connections",
		s.connectionsActive.Load(),
		colorGreen,
	)
	writeMetric(
		&b,
		"Games",
		s.gamesActive.Load(),
		colorGreen,
	)

	writeSection(&b, "CLIENTS", colorCyan)
	writeMetric(
		&b,
		"Connection attempts",
		s.clientsConnectionsAttemptedTotal.Load(),
		colorCyan,
	)
	writeMetric(
		&b,
		"Connected total",
		s.clientsConnectedTotal.Load(),
		colorCyan,
	)

	writeSection(&b, "LOBBIES & GAMES", colorYellow)
	writeMetric(
		&b,
		"Lobbies joined",
		s.lobbiesJoinedTotal.Load(),
		colorYellow,
	)
	writeMetric(
		&b,
		"Lobbies full",
		s.lobbiesFullTotal.Load(),
		colorYellow,
	)
	writeMetric(
		&b,
		"Lobbies connected",
		s.lobbiesConnectedTotal.Load(),
		colorYellow,
	)
	writeMetric(
		&b,
		"Games started",
		s.gamesStartedTotal.Load(),
		colorYellow,
	)
	writeMetric(
		&b,
		"Keydowns",
		s.keydownsSentTotal.Load(),
		colorYellow,
	)
	writeMetric(
		&b,
		"Keyups",
		s.keyupsSentTotal.Load(),
		colorYellow,
	)
	writeMetric(
		&b,
		"Game states events received",
		s.gameStatesEventsReceivedTotal.Load(),
		colorYellow,
	)

	writeSection(&b, "WEBSOCKET ERRORS", colorRed)

	connectionErrors := s.wsConnectionErrorsTotal.Load()
	writeMetric(
		&b,
		"Connection errors",
		connectionErrors,
		errorColor(int64(connectionErrors)),
	)

	readErrors := s.wsReadErrorsTotal.Load()
	writeMetric(
		&b,
		"Read errors",
		readErrors,
		errorColor(int64(readErrors)),
	)

	writeErrors := s.wsWriteErrorsTotal.Load()
	writeMetric(
		&b,
		"Write errors",
		writeErrors,
		errorColor(int64(writeErrors)),
	)

	unexpectedMessages := s.unexpectedMessagesTotal.Load()
	writeMetric(
		&b,
		"Unexpected messages",
		unexpectedMessages,
		errorColor(unexpectedMessages),
	)

	return b.String()
}
