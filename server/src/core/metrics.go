package core

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

type histogram struct {
	name    string
	limits  []time.Duration
	buckets []atomic.Uint64
}

func NewLatencyHistogram(name string, limits []time.Duration) *histogram {
	buckets := make([]atomic.Uint64, len(limits)+1)
	return &histogram{
		name:    name,
		limits:  limits[:],
		buckets: buckets,
	}
}

func (h *histogram) observer(value time.Duration) {
	for i, limit := range h.limits {
		if value <= limit {
			h.buckets[i].Add(1)
			return
		}
	}
	h.buckets[len(h.buckets)-1].Add(1)
}

func (h *histogram) toAttrs() (attrs []any) {
	counts := make([]uint64, len(h.buckets))
	var totalCount uint64
	for i := range h.buckets {
		c := h.buckets[i].Swap(0)
		counts[i] = c
		totalCount += c
	}

	for i, limit := range h.limits {
		value := counts[i]
		var fraction float64
		if totalCount != 0 {
			fraction = (float64(value) / float64(totalCount)) * 100
		}
		val := fmt.Sprintf("%d_%.2f%%", value, fraction)
		attrs = append(attrs, fmt.Sprintf("<=%s", limit.String()), val)

	}

	c := counts[len(h.buckets)-1]
	var fraction float64
	if totalCount != 0 {
		fraction = (float64(c) / float64(totalCount)) * 100
	}
	val := fmt.Sprintf("%d_%.2f%%", c, fraction)
	attrs = append(attrs, "spill", val)
	return
}

func (h *histogram) log(every time.Duration) {
	ticker := time.NewTicker(every)
	for {
		<-ticker.C
		attrs := h.toAttrs()
		slog.Info(h.name, attrs...)
	}
}

type Metrics struct {
	websocketWriteLatency *histogram
	gameLoopTiming        *histogram
}

func newMetrics() *Metrics {
	return &Metrics{
		websocketWriteLatency: NewLatencyHistogram("websocket_write_latency", []time.Duration{
			100 * time.Microsecond,
			500 * time.Microsecond,
			1 * time.Millisecond,
			5 * time.Millisecond,
			10 * time.Millisecond,
			25 * time.Millisecond,
			50 * time.Millisecond,
			100 * time.Millisecond,
			250 * time.Millisecond,
			500 * time.Millisecond,
			1 * time.Second,
		}),
		gameLoopTiming: NewLatencyHistogram("game_loop_timing", []time.Duration{
			FRAME_TIME_SECONDS,
			FRAME_TIME_SECONDS + time.Microsecond,
			FRAME_TIME_SECONDS + 10*time.Microsecond,
			FRAME_TIME_SECONDS + 100*time.Microsecond,
			FRAME_TIME_SECONDS + time.Millisecond,
			FRAME_TIME_SECONDS + time.Millisecond + 10*time.Microsecond,
		}),
	}
}

func (m *Metrics) log(every time.Duration) {
	ticker := time.NewTicker(every)
	for {
		<-ticker.C
		slog.Info("Metrics:")
		slog.Info(m.websocketWriteLatency.name, m.websocketWriteLatency.toAttrs()...)
		slog.Info(m.gameLoopTiming.name, m.gameLoopTiming.toAttrs()...)
	}
}
