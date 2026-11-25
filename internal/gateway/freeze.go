package gateway

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	freezeDefaultAfter    = 10 * time.Second
	freezeDefaultCooldown = 10 * time.Minute
	freezeDefaultPoll     = time.Second
	freezeMaxFiles        = 5
)

type activeWork struct {
	name    string
	start   time.Time
	trigger bool
}

type freezeSnapshot struct {
	oldestName string
	oldestAge  time.Duration
	waiting    int
	writerHeld bool
	writerPCs  []uintptr
	writerAge  time.Duration
}

type freezeTracker struct {
	after        time.Duration
	cooldown     time.Duration
	poll         time.Duration
	writeProfile func(path string, data []byte) error

	mu       sync.Mutex
	nextID   uint64
	active   map[uint64]activeWork
	wDepth   int
	wPCs     [32]uintptr
	wPCsLen  int
	wSince   time.Time
	inFreeze bool
	lastDump time.Time
}

func newFreezeTracker(opts Options) *freezeTracker {
	after := opts.FreezeAfter
	if after <= 0 {
		after = freezeDefaultAfter
	}
	cooldown := opts.FreezeCooldown
	if cooldown <= 0 {
		cooldown = freezeDefaultCooldown
	}
	poll := opts.FreezePollInterval
	if poll <= 0 {
		poll = freezeDefaultPoll
	}
	return &freezeTracker{
		after:    after,
		cooldown: cooldown,
		poll:     poll,
		active:   make(map[uint64]activeWork),
	}
}

func (f *freezeTracker) begin(name string, trigger bool) func() {
	now := time.Now()
	f.mu.Lock()
	f.nextID++
	id := f.nextID
	f.active[id] = activeWork{name: name, start: now, trigger: trigger}
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.active, id)
		f.mu.Unlock()
	}
}

func (f *freezeTracker) acquireWriter() {
	now := time.Now()
	f.mu.Lock()
	if f.wDepth == 0 {
		f.wSince = now
		f.wPCsLen = runtime.Callers(2, f.wPCs[:])
	}
	f.wDepth++
	f.mu.Unlock()
}

func resolveWriterName(pcs []uintptr) string {
	if len(pcs) == 0 {
		return "unknown"
	}
	frames := runtime.CallersFrames(pcs)
	var writerFn, callerFn string
	for {
		f, more := frames.Next()
		if f.Function != "" {
			if writerFn == "" {
				if isGatewayWriterFrame(f.Function) {
					writerFn = shortFuncName(f.Function)
				}
			} else if isGatewayFrame(f.Function) {
				callerFn = shortFuncName(f.Function)
				break
			} else {
				break
			}
		}
		if !more {
			break
		}
	}
	if writerFn == "" {
		return "unknown"
	}
	if callerFn != "" {
		return writerFn + "<-" + callerFn
	}
	return writerFn
}

func isGatewayFrame(fn string) bool {
	return strings.Contains(fn, "/internal/gateway.")
}

func isGatewayWriterFrame(fn string) bool {
	return isGatewayFrame(fn) &&
		!strings.Contains(fn, "(*storeDB).") &&
		!strings.Contains(fn, "(storeDB).") &&
		!strings.Contains(fn, "(*freezeTracker).")
}

func shortFuncName(fn string) string {
	if i := strings.Index(fn, "/internal/gateway."); i >= 0 {
		return fn[i+len("/internal/"):]
	}
	if i := strings.LastIndexByte(fn, '/'); i >= 0 {
		return fn[i+1:]
	}
	return fn
}

func (f *freezeTracker) releaseWriter() {
	f.mu.Lock()
	if f.wDepth > 0 {
		f.wDepth--
	}
	if f.wDepth == 0 {
		f.wPCsLen = 0
		f.wSince = time.Time{}
	}
	f.mu.Unlock()
}

// check inspects active calls and returns a snapshot if a new freeze dump
// should be written now. Only triggering work (answered frames and loops) can
// start a dump or use the cooldown.
func (f *freezeTracker) check(now time.Time) (freezeSnapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var oldestName string
	var oldestStart time.Time
	var oldestTriggerStart time.Time
	for _, w := range f.active {
		if oldestStart.IsZero() || w.start.Before(oldestStart) {
			oldestStart = w.start
			oldestName = w.name
		}
		if w.trigger && (oldestTriggerStart.IsZero() || w.start.Before(oldestTriggerStart)) {
			oldestTriggerStart = w.start
		}
	}
	if oldestTriggerStart.IsZero() || now.Sub(oldestTriggerStart) <= f.after {
		f.inFreeze = false
		return freezeSnapshot{}, false
	}
	if f.inFreeze {
		return freezeSnapshot{}, false
	}
	if !f.lastDump.IsZero() && now.Sub(f.lastDump) < f.cooldown {
		return freezeSnapshot{}, false
	}
	f.inFreeze = true
	f.lastDump = now

	var writerHeld bool
	var writerPCs []uintptr
	var writerAge time.Duration
	if f.wDepth > 0 && !f.wSince.IsZero() {
		writerHeld = true
		writerAge = now.Sub(f.wSince)
		if f.wPCsLen > 0 {
			writerPCs = make([]uintptr, f.wPCsLen)
			copy(writerPCs, f.wPCs[:f.wPCsLen])
		}
	}
	return freezeSnapshot{
		oldestName: oldestName,
		oldestAge:  now.Sub(oldestStart),
		waiting:    len(f.active),
		writerHeld: writerHeld,
		writerPCs:  writerPCs,
		writerAge:  writerAge,
	}, true
}

func (g *Gateway) startRequest(method, _ string) func() {
	if g.freeze == nil {
		return func() {}
	}
	return g.freeze.begin(method, true)
}

func (g *Gateway) startWait(method, _ string) func() {
	if g.freeze == nil {
		return func() {}
	}
	return g.freeze.begin(method, false)
}

func (g *Gateway) startLoop(name string) func() {
	start := time.Now()
	if g.freeze == nil {
		return func() { g.slowLoop(name, start) }
	}
	end := g.freeze.begin(name, true)
	return func() {
		end()
		g.slowLoop(name, start)
	}
}

func (g *Gateway) startFreezeMonitor() {
	if g.freeze == nil || g.freezeStop == nil {
		return
	}
	g.freezeWG.Add(1)
	go func() {
		defer g.freezeWG.Done()
		t := time.NewTicker(g.freeze.poll)
		defer t.Stop()
		for {
			select {
			case <-g.freezeStop:
				return
			case <-t.C:
				if snap, ok := g.freeze.check(time.Now()); ok {
					g.writeFreezeDump(snap)
				}
			}
		}
	}()
}

func (g *Gateway) stopFreezeMonitor() {
	if g.freezeStop != nil {
		g.freezeStopOnce.Do(func() { close(g.freezeStop) })
	}
	g.freezeWG.Wait()
}

func (g *Gateway) writeFreezeDump(snap freezeSnapshot) {
	var buf bytes.Buffer
	if p := pprof.Lookup("goroutine"); p != nil {
		_ = p.WriteTo(&buf, 2)
	}
	var walBytes int64
	if g.store != nil {
		walBytes = walSizeBytes(g.store.path)
	}
	ioPressure, memPressure := freezeSystemStats()
	profPath := ""
	if g.opts.DataDir != "" {
		dir := filepath.Join(g.opts.DataDir, "freeze-profiles")
		name := fmt.Sprintf("freeze-%s.txt", g.store.now().UTC().Format("20060102-150405.000"))
		profPath = filepath.Join(dir, name)
	}
	writer := "none"
	if snap.writerHeld {
		writer = resolveWriterName(snap.writerPCs)
	}
	g.logLine("freeze",
		"oldest", snap.oldestName,
		"oldest_ms", snap.oldestAge.Milliseconds(),
		"waiting", snap.waiting,
		"writer", writer,
		"writer_ms", snap.writerAge.Milliseconds(),
		"wal_bytes", walBytes,
		"io_pressure", ioPressure,
		"mem", memPressure,
		"profile", profPath,
	)
	if profPath != "" {
		writeFn := writeFreezeProfileFile
		if g.freeze != nil && g.freeze.writeProfile != nil {
			writeFn = g.freeze.writeProfile
		}
		_ = writeFn(profPath, buf.Bytes())
	}
}

func writeFreezeProfileFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	_ = pruneFreezeProfiles(dir, freezeMaxFiles)
	return nil
}

func walSizeBytes(dbPath string) int64 {
	if dbPath == "" {
		return 0
	}
	info, err := os.Stat(dbPath + "-wal")
	if err != nil {
		return 0
	}
	return info.Size()
}

func pruneFreezeProfiles(dir string, keep int) error {
	files, err := filepath.Glob(filepath.Join(dir, "freeze-*.txt"))
	if err != nil {
		return err
	}
	if len(files) <= keep {
		return nil
	}
	sort.Strings(files)
	for _, f := range files[:len(files)-keep] {
		_ = os.Remove(f)
	}
	return nil
}
