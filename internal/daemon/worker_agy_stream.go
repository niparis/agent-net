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
	StreamID  string
	Offset    int64
	Usage     agyUsage
	Completed bool
	Failure   string
	Handback  *protocol.WorkerHandbackRequest
	// HandbackFiles and PendingFiles are the checked snapshots of the files a
	// hand-back carries. They are saved with the request, so an adopted stream
	// sends the files the tool call checked and never re-checks a file that
	// left the worktree after the worker handed back.
	HandbackFiles *OutgoingFiles
	Pending       *protocol.WorkerHandbackRequest
	PendingFiles  *OutgoingFiles
	PendingKey    string
}

func (c *agyConn) saveStreamLocked() error {
	b, err := json.Marshal(agyStreamState{
		StreamID: c.streamID, Offset: c.offset, Usage: c.usage, Completed: c.completed, Failure: c.failure,
		Handback: c.handback, HandbackFiles: c.handbackFiles, Pending: c.pending, PendingFiles: c.pendingFiles, PendingKey: c.pendingKey,
	})
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
		streamID: s.StreamID, offset: s.Offset, usage: s.Usage, completed: s.Completed, failure: s.Failure,
		handback: s.Handback, handbackFiles: s.HandbackFiles, pending: s.Pending, pendingFiles: s.PendingFiles, pendingKey: s.PendingKey}
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
		d.setWorkerStatus(w, "busy")
	}
	d.startAgyStream(c)
	if s.Completed && s.Failure == "" && s.Pending != nil {
		c.publishHandback(*s.Pending)
	}
	return nil
}

// turnKeyLocked names the turn whose result was read last. It is the pending
// hand-back's notice key.
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
		// A link that drops after this wait is waited for in handbackFlow.
		if !d.waitForLink(ctx) {
			return // leave the saved pending report for the next adoption
		}
		if out, err := d.handbackToNoteWithFiles(ctx, w, req, "", "", "", true, files); err != nil {
			d.Logf("worker %s: Antigravity handback: %v", w.id, err)
			if ctx.Err() != nil || errors.Is(err, errHandbackDeferred) {
				return // the next adoption publishes the saved report
			}
			c.refused(ctx, req, key, err)
		} else {
			c.mu.Lock()
			if c.pendingKey == key {
				c.pending = nil
				c.pendingKey = ""
				c.pendingFiles = nil
				_ = c.saveStreamLocked()
			}
			c.mu.Unlock()
			if out.AutoContinued {
				d.openCodeTurnIdle(w.sessionID)
			}
		}
	})
}

// refused drops the pending hand-back named key and tells the caller why
// agent-net could not take it, with the worker's report.
func (c *agyConn) refused(ctx context.Context, req protocol.WorkerHandbackRequest, key string, err error) {
	d, w := c.d, c.w
	c.mu.Lock()
	var refusedFiles *OutgoingFiles
	if c.pending != nil && c.pendingKey == key {
		c.pending, c.pendingKey = nil, ""
		refusedFiles, c.pendingFiles = c.pendingFiles, nil
		if err := c.saveStreamLocked(); err != nil {
			d.Logf("worker %s: save the Antigravity stream: %v", w.id, err)
		}
	}
	c.mu.Unlock()
	refusedFiles.Remove()
	// The refused hand-back ended the turn: let the next message in.
	d.openCodeTurnIdle(w.sessionID)
	d.tellCallerRefused(ctx, w, req, cleanFailure(err.Error()))
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
	d.wmu.Lock()
	processExited := c.w.exited
	exited := make(chan struct{})
	c.w.exited = exited
	d.wmu.Unlock()
	c.mu.Lock()
	c.stop, c.stopped = make(chan struct{}), make(chan struct{})
	c.processExited = processExited
	c.mu.Unlock()
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
			if c.processEnded() {
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

// processEnded tells whether the agy process ended: the Wait of a process
// this daemon started, or the process table for an adopted one.
func (c *agyConn) processEnded() bool {
	c.mu.Lock()
	exited := c.processExited
	c.mu.Unlock()
	if exited != nil {
		select {
		case <-exited:
			return true
		default:
			return false
		}
	}
	c.d.wmu.Lock()
	pid, token := c.w.pid, c.w.token
	c.d.wmu.Unlock()
	tok, alive := procToken(pid)
	return !alive || tok != token
}

// agyEnded tells whether the worker's agy process ended. gone says no until
// the stream reader has read all that an ended process wrote, so its last
// result is never lost. A message written in that time reaches no process,
// so this waits for the reader.
func (d *Daemon) agyEnded(ctx context.Context, w *worker) bool {
	d.wmu.Lock()
	c := w.agy
	d.wmu.Unlock()
	if c != nil && c.processEnded() {
		c.mu.Lock()
		stopped := c.stopped
		c.mu.Unlock()
		if stopped != nil {
			select {
			case <-stopped:
			case <-ctx.Done():
			}
		}
	}
	return d.gone(w)
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
