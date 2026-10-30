package daemon

import (
	"context"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoHarness isolates the owner conversation from the harness that runs it.
type ctoHarness interface {
	name() string
	send(context.Context, *Daemon, string) (any, error)
	newConversation(context.Context, *Daemon) (any, error)
	events(context.Context, *Daemon, int) (protocol.CTOEvents, error)
}

type openCodeCTOHarness struct{}

func (openCodeCTOHarness) name() string { return "opencode" }
func (openCodeCTOHarness) send(ctx context.Context, d *Daemon, text string) (any, error) {
	return d.openCodeCTOSend(ctx, text)
}
func (openCodeCTOHarness) newConversation(ctx context.Context, d *Daemon) (any, error) {
	return d.openCodeCTONew(ctx)
}
func (openCodeCTOHarness) events(ctx context.Context, d *Daemon, index int) (protocol.CTOEvents, error) {
	return d.openCodeCTOEvents(ctx, index)
}

func (d *Daemon) ctoSend(ctx context.Context, text string) (any, error) {
	return (openCodeCTOHarness{}).send(ctx, d, text)
}
func (d *Daemon) ctoNew(ctx context.Context) (any, error) {
	return (openCodeCTOHarness{}).newConversation(ctx, d)
}
func (d *Daemon) ctoEvents(ctx context.Context, index int) (protocol.CTOEvents, error) {
	return (openCodeCTOHarness{}).events(ctx, d, index)
}
