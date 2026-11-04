package daemon

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestLocalDeliveryRequiresConfirmation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"write-error", "timeout", "closed", "canceled", "ack", "refused", "missing-turn-ack"} {
		t.Run(mode, func(t *testing.T) {
			client, plugin := net.Pipe()
			defer client.Close()
			defer plugin.Close()
			lc := &localConn{c: client, closed: make(chan struct{}), acks: map[string]chan ack{}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "write-error" {
				plugin.Close()
			} else {
				go func() {
					bufio.NewReader(plugin).ReadBytes('\n')
					switch mode {
					case "closed":
						lc.close()
					case "canceled":
						cancel()
					case "ack", "refused", "missing-turn-ack":
						lc.mu.Lock()
						ch := lc.acks["m_test"]
						lc.mu.Unlock()
						ch <- ack{ok: mode != "refused", detail: "test"}
					}
				}()
			}
			res := lc.pushWithConfirmation(ctx, protocol.Message{ID: "m_test"}, 50*time.Millisecond, mode == "missing-turn-ack")
			if mode == "ack" {
				if !res.Delivered || res.Uncertain {
					t.Fatalf("ack: %+v", res)
				}
			} else if mode == "refused" {
				if res.Delivered || res.Uncertain {
					t.Fatalf("refusal: %+v", res)
				}
			} else if res.Delivered || !res.Uncertain || !strings.Contains(res.Detail, "may have arrived") {
				t.Fatalf("unconfirmed write: %+v", res)
			}
		})
	}
}
