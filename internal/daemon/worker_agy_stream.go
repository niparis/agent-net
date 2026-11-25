package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A single checkpoint keeps the consumed byte offset, cumulative usage and
// acknowledged handback together. The child owns both ends of its stdin FIFO
// and appends stdout directly to a file, so neither stream depends on us.
type agyStreamState struct {
	StreamID   string
	Offset     int64
	Usage      agyUsage
	Completed  bool
	Failure    string
	Handback   *protocol.WorkerHandbackRequest
	Pending    *protocol.WorkerHandbackRequest
	PendingKey string
}

func (c *agyConn) saveStreamLocked() error {
	b, err := json.Marshal(agyStreamState{c.streamID, c.offset, c.usage, c.completed, c.failure, c.handback, c.pending, c.pendingKey})
	if err != nil {
		return err
	}
	path := filepath.Join(c.w.dir, "agy-stream.json")
	if err := os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (agyLauncher) adopt(ctx context.Context, d *Daemon, w *worker) error {
	b, err := os.ReadFile(filepath.Join(w.dir, "agy-stream.json"))
	if err != nil {
		return err
	}
	var s agyStreamState
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	// O_RDWR also opens without blocking if the process finished during downtime.
	in, err := os.OpenFile(filepath.Join(w.dir, "agy-stdin"), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	c := &agyConn{d: d, w: w, in: in, initialized: make(chan string, 1),
		streamID: s.StreamID, offset: s.Offset, usage: s.Usage, completed: s.Completed, failure: s.Failure, handback: s.Handback, pending: s.Pending, pendingKey: s.PendingKey}
	d.wmu.Lock()
	w.agy = c
	w.turnActive = !s.Completed
	w.turnAwaitingStart = !s.Completed
	d.wmu.Unlock()
	if err := d.registerAgySession(ctx, w); err != nil {
		in.Close()
		return err
	}
	if !s.Completed {
		d.setAgyStatus(w, "busy")
	}
	d.startAgyStream(c)
	refused, ok := d.loadRefusedHandback(w.id)
	switch {
	case s.Completed && s.Failure == "" && s.Pending != nil && (!ok || refused.Turn != s.PendingKey):
		c.publishHandback(*s.Pending)
	case ok:
		// The refusal of this pending hand-back, or of an earlier one, is
		// saved: do what it still owes.
		d.goBackground(func() { c.refusalNext(d.streamCtx(), refused) })
	}
	return nil
}

// turnKeyLocked names the turn whose result was read last. It is the pending
// hand-back's notice key, and a refused hand-back saves it as its Turn.
func (c *agyConn) turnKeyLocked() string {
	return fmt.Sprintf("agy-%s-%s-%d", c.w.id, c.streamID, c.offset)
}

func (c *agyConn) publishHandback(req protocol.WorkerHandbackRequest) {
	d, w := c.d, c.w
	c.mu.Lock()
	key := c.pendingKey
	files := c.pendingFiles
	c.pendingFiles = nil
	c.mu.Unlock()
	d.goBackground(func() {
		defer files.Remove()
		ctx := d.streamCtx()
		// Adoption replays results before Start launches the gateway link.
		// File notices need that link and cannot use the offline outbox.
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for !d.Connected() {
			select {
			case <-ctx.Done():
				return // leave the saved pending report for the next adoption
			case <-tick.C:
			}
		}
		if out, err := d.handbackToNoteWithFiles(ctx, w, req, "", "", "", true, files); err != nil {
			d.Logf("worker %s: Antigravity handback: %v", w.id, err)
			if ctx.Err() != nil || errors.Is(err, errHandbackDeferred) {
				return // the next adoption or connect publishes the saved report
			}
			c.refused(ctx, req, key, err)
		} else {
			c.mu.Lock()
			if c.pendingKey == key {
				c.pending = nil
				c.pendingKey = ""
				_ = c.saveStreamLocked()
			}
			c.mu.Unlock()
			if out.AutoContinued {
				d.openCodeTurnIdle(w.sessionID)
			}
		}
	})
}

// refused saves the refusal of the pending hand-back named key, then tells
// the worker or the caller.
func (c *agyConn) refused(ctx context.Context, req protocol.WorkerHandbackRequest, key string, err error) {
	d, w := c.d, c.w
	d.refusedStep("refused")
	r := d.saveRefusal(w, req, err, key, true)
	d.refusedStep("saved")
	// The refused hand-back ended the turn: let the next message in.
	d.openCodeTurnIdle(w.sessionID)
	c.refusalNext(ctx, r)
}

// refusalNext does what a saved refusal still owes, read from the stream
// state, so a live refusal, an adoption and the end of a later turn take the
// same path. The steps of a refused hand-back, each on disk before the next:
//
//   - refused: nothing is saved. Adoption publishes the pending hand-back again.
//   - saved: the record names the pending turn. Adoption finds both and goes on
//     here, so the hand-back is not published again.
//   - cleared: the stream state no longer holds the pending hand-back.
//   - the worker's message: deliverAgy saves the stream as not completed before
//     it writes, so adoption never sends it twice. The result of that turn
//     comes here again when it carries no hand-back.
//   - told: the caller's notice is sent. Adoption sends it again with the same
//     key, and the gateway keeps the first. Then the record is removed.
func (c *agyConn) refusalNext(ctx context.Context, r refusedHandback) {
	d, w := c.d, c.w
	c.mu.Lock()
	if c.pending != nil && c.pendingKey == r.Turn {
		c.pending, c.pendingKey = nil, ""
		if err := c.saveStreamLocked(); err != nil {
			d.Logf("worker %s: save the Antigravity stream: %v", w.id, err)
		}
	}
	completed, failed, sameTurn := c.completed, c.failure != "", c.turnKeyLocked() == r.Turn
	c.mu.Unlock()
	d.refusedStep("cleared")
	switch {
	case !r.Worker:
		d.tellCallerRefused(ctx, w, r)
	case !completed || failed:
		// A turn runs: its result comes here again, or carries a new
		// hand-back. A failed run ends the worker, whose notice carries the
		// saved report.
	case sameTurn:
		// No turn ran after the refused one, so the message is still owed.
		if err := d.tellWorkerRefused(ctx, w, r.Reason); err != nil {
			d.Logf("worker %s: refused hand-back message: %v", w.id, err)
			d.tellCallerRefused(ctx, w, r)
		}
	default:
		// The worker had its message and ended a later turn without a new
		// hand-back.
		d.tellCallerRefused(ctx, w, r)
	}
}

// streamCtx ends when the daemon stops.
func (d *Daemon) streamCtx() context.Context {
	if d.workerStreamCtx == nil {
		return context.Background()
	}
	return d.workerStreamCtx
}

func (d *Daemon) startAgyStream(c *agyConn) {
	ctx := d.streamCtx()
	c.stop, c.stopped = make(chan struct{}), make(chan struct{})
	d.wmu.Lock()
	processExited := c.w.exited
	exited := make(chan struct{})
	c.w.exited = exited
	d.wmu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer close(c.stopped)
		defer close(exited)
		defer func() {
			// Once detached, exit means the process exited, rather than the
			// reader stopped. This also keeps process cleanup after Stop valid.
			d.wmu.Lock()
			if c.w.exited == exited {
				c.w.exited = processExited
			}
			d.wmu.Unlock()
		}()
		defer func() {
			c.mu.Lock()
			if c.in != nil {
				c.in.Close()
				c.in = nil
			}
			c.mu.Unlock()
		}()
		f, err := os.Open(filepath.Join(c.w.dir, "agy-events.jsonl"))
		if err != nil {
			c.mu.Lock()
			c.failure = err.Error()
			c.mu.Unlock()
			return
		}
		defer f.Close()
		offset := c.offset
		_, _ = f.Seek(offset, io.SeekStart)
		reader := bufio.NewReader(f)
		var partial []byte
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-ctx.Done():
				return
			default:
			}
			line, err := reader.ReadBytes('\n')
			partial = append(partial, line...)
			if len(partial) > 16<<20 {
				c.mu.Lock()
				c.failure = "Antigravity emitted a stream event larger than 16 MiB"
				c.mu.Unlock()
				return
			}
			if len(partial) > 0 && partial[len(partial)-1] == '\n' {
				offset += int64(len(partial))
				c.eventAt(partial, offset)
				partial = nil
				continue
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return
			}
			dead := false
			if processExited != nil {
				select {
				case <-processExited:
					dead = true
				default:
				}
			} else {
				tok, alive := procToken(c.w.pid)
				dead = !alive || tok != c.w.token
			}
			if dead {
				// Re-read after observing exit: the previous EOF may precede
				// the child's last write.
				line, _ := reader.ReadBytes('\n')
				if len(line) > 0 {
					partial = append(partial, line...)
					if partial[len(partial)-1] == '\n' {
						offset += int64(len(partial))
						c.eventAt(partial, offset)
						partial = nil
					}
					continue
				}
				return
			}
			select {
			case <-c.stop:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

func (c *agyConn) closeStream() {
	if c.stop != nil {
		c.stopOnce.Do(func() { close(c.stop) })
		<-c.stopped
	}
	c.mu.Lock()
	handbackFiles, pendingFiles := c.handbackFiles, c.pendingFiles
	c.handbackFiles, c.pendingFiles = nil, nil
	c.mu.Unlock()
	handbackFiles.Remove()
	pendingFiles.Remove()
}
