package gateway

import "context"

// goBackground runs fn in a goroutine that belongs to the gateway. The context
// fn gets is cancelled by Close, and Close waits for fn before it closes the
// database. Once Close has begun, goBackground starts nothing and returns
// false. The store hooks use it, so a settings or charter write never leaves a
// broadcast running after the gateway (or the test that owns it) is gone.
func (g *Gateway) goBackground(fn func(ctx context.Context)) bool {
	g.bgMu.Lock()
	defer g.bgMu.Unlock()
	if g.bgStopped {
		return false
	}
	if g.bgCtx == nil {
		g.bgCtx, g.bgCancel = context.WithCancel(context.Background())
	}
	ctx := g.bgCtx
	g.bgWG.Add(1)
	go func() {
		defer g.bgWG.Done()
		fn(ctx)
	}()
	return true
}

// stopBackground cancels the background jobs and waits for them. Add happens
// under bgMu with bgStopped false, and bgStopped is set under bgMu before the
// Wait, so no job can start after the Wait begins.
func (g *Gateway) stopBackground() {
	g.bgMu.Lock()
	g.bgStopped = true
	if g.bgCancel != nil {
		g.bgCancel()
	}
	g.bgMu.Unlock()
	g.bgWG.Wait()
}
