// Package linkkeepalive monitors the daemon-gateway WebSocket in both directions.
package linkkeepalive

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	DefaultPingInterval = 20 * time.Second
	DefaultTimeout      = 45 * time.Second
)

// Monitor tracks incoming traffic. Ping and pong handlers must call Received;
// Read does the same for data messages, including while reading their payload.
type Monitor struct {
	mu       sync.Mutex
	last     time.Time
	activity chan struct{}
}

func New() *Monitor {
	return &Monitor{last: time.Now(), activity: make(chan struct{}, 1)}
}

func (m *Monitor) Received() {
	m.mu.Lock()
	m.last = time.Now()
	m.mu.Unlock()
	select {
	case m.activity <- struct{}{}:
	default:
	}
}

func (m *Monitor) remaining(timeout time.Duration) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return timeout - time.Since(m.last)
}

type activityReader struct {
	io.Reader
	monitor *Monitor
}

func (r activityReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.monitor.Received()
	}
	return n, err
}

// Read is like websocket.Conn.Read, but records traffic as the message arrives.
func (m *Monitor) Read(ctx context.Context, ws *websocket.Conn) (websocket.MessageType, []byte, error) {
	typ, r, err := ws.Reader(ctx)
	if err != nil {
		return 0, nil, err
	}
	m.Received()
	b, err := io.ReadAll(activityReader{Reader: r, monitor: m})
	return typ, b, err
}

// Run sends pings until ctx ends, and closes the connection after timeout with
// no incoming traffic. Ping waits for its own pong, so each call runs separately:
// data or a peer ping also proves the link is alive, even without that pong.
func (m *Monitor) Run(ctx context.Context, ws *websocket.Conn, interval, timeout time.Duration) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			go func() {
				pingCtx, stop := context.WithTimeout(ctx, timeout)
				defer stop()
				_ = ws.Ping(pingCtx)
			}()
		case <-m.activity:
			remaining := m.remaining(timeout)
			if remaining < 0 {
				remaining = 0
			}
			timer.Reset(remaining)
		case <-timer.C:
			if remaining := m.remaining(timeout); remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			ws.CloseNow()
			return
		}
	}
}
