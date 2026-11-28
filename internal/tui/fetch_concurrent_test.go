package tui

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// gatedCTO answers the four reads of a CTO screen poll. SessionText does not
// answer until the graph history was asked, which comes last in a poll that
// asks one read after the other.
type gatedCTO struct {
	*fakeCTOSource
	mu       sync.Mutex
	settings bool
	history  bool
	window   bool
	ready    chan struct{}
}

func (g *gatedCTO) mark(which *bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	*which = true
	if g.settings && g.history && g.window {
		select {
		case <-g.ready:
		default:
			close(g.ready)
		}
	}
}

func (g *gatedCTO) CTOGraphWindow(_ context.Context, _ bool) (protocol.CTOGraphWindow, error) {
	g.mark(&g.window)
	return protocol.CTOGraphWindow{}, nil
}

func (g *gatedCTO) CTOSettings(ctx context.Context) (protocol.CTOSettings, error) {
	g.mark(&g.settings)
	return g.fakeCTOSource.CTOSettings(ctx)
}

func (g *gatedCTO) CTOGraphHistory(ctx context.Context, after int64) ([]protocol.CTOGraphChange, error) {
	g.mark(&g.history)
	return g.fakeCTOSource.CTOGraphHistory(ctx, after)
}

// The reads of one CTO screen poll are independent. They run together, so a
// slow settings read does not delay the conversation and the other way round.
func TestCTOPollAsksItsReadsTogether(t *testing.T) {
	src := &gatedCTO{fakeCTOSource: &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}, ready: make(chan struct{})}
	src.textFn = func(protocol.TextQuery) (protocol.TextPage, error) {
		select {
		case <-src.ready:
			return protocol.TextPage{}, nil
		case <-time.After(1500 * time.Millisecond):
			return protocol.TextPage{}, errors.New("the conversation waited for the other reads: they were asked one after the other")
		}
	}
	m := New(src)
	m.width, m.height = 100, 30
	msg, ok := m.ctoPollCmd()().(ctoPollMsg)
	if !ok {
		t.Fatal("no ctoPollMsg")
	}
	if msg.err != nil {
		t.Fatalf("poll failed: %v", msg.err)
	}
}

// The Desk loads active, done and stopped changes. The three reads are
// independent, so the Desk does not wait for one before it asks the next: with
// one 5 s deadline for all, a slow first read left nothing for the others and
// the list never loaded.
func TestChangesStatesAreReadTogether(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]bool{}
	allAsked := make(chan struct{})
	call := func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		args, _ := params.(protocol.ChangesFindArgs)
		mu.Lock()
		asked[args.State] = true
		if len(asked) == 3 {
			select {
			case <-allAsked:
			default:
				close(allAsked)
			}
		}
		mu.Unlock()
		if args.State == protocol.ChangeListActive {
			select {
			case <-allAsked:
			case <-time.After(1500 * time.Millisecond):
				return nil, errors.New("the active read waited for the other states: they were asked one after the other")
			}
		}
		return json.RawMessage(`{"items":[]}`), nil
	}
	src := newGatewayDelivery(call)
	if _, err := src.Changes(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}
