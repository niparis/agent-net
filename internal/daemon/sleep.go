package daemon

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Sleep detection has two parts. The daemon's own ticker notices that the wall
// clock moved further than the monotonic clock, which happens on every sleep
// and works on every OS. After such a jump it asks the OS for the exact span:
// pmset on macOS and the kernel journal on Linux. When the command fails or
// says nothing, the estimate from the clock jump is used.
const (
	sleepTick      = 30 * time.Second
	sleepMinGap    = 2 * time.Minute
	sleepCommand   = 10 * time.Second
	sleepTolerance = 2 * time.Minute
)

// sleepWatch keeps the last tick and the spans that still need a report.
type sleepWatch struct {
	mu       sync.Mutex
	lastWall time.Time     // wall clock without a monotonic reading
	lastMono time.Duration // process monotonic clock
	pending  []protocol.NodeSleep
	read     func(since time.Time) []protocol.NodeSleep
	now      func() time.Time
	mono     func() time.Duration
}

func newSleepWatch() *sleepWatch {
	origin := time.Now()
	return &sleepWatch{
		lastWall: origin.Round(0),
		read:     readOSSleeps,
		now:      func() time.Time { return time.Now().Round(0) },
		mono:     func() time.Duration { return time.Since(origin) },
	}
}

// observe compares two ticks. A gap above sleepMinGap means the machine slept
// (or the process was paused), and the returned spans say from when to when.
func (w *sleepWatch) observe(wall time.Time, mono time.Duration) []protocol.NodeSleep {
	wall = wall.Round(0)
	w.mu.Lock()
	lastWall := w.lastWall
	gap := wall.Sub(lastWall) - (mono - w.lastMono)
	w.lastWall, w.lastMono = wall, mono
	w.mu.Unlock()
	if gap < sleepMinGap {
		return nil
	}
	// The OS read may take a while, so it runs outside the lock.
	since := lastWall.Add(-sleepTolerance)
	found := spansSince(w.read(since), since)
	exact := false
	for _, s := range found {
		if s.To == 0 || time.Unix(0, s.To).After(lastWall) {
			exact = true
			break
		}
	}
	if !exact {
		found = append(found, protocol.NodeSleep{From: lastWall.UnixNano(), To: wall.UnixNano(), Source: "clock"})
	}
	return found
}

func (w *sleepWatch) addPending(spans []protocol.NodeSleep) {
	if len(spans) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, spans...)
}

func (w *sleepWatch) takePending() []protocol.NodeSleep {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.pending
	w.pending = nil
	return out
}

func (w *sleepWatch) restorePending(spans []protocol.NodeSleep) {
	if len(spans) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(spans, w.pending...)
}

// spansSince keeps the spans that end at or after since, or that are still
// ongoing.
func spansSince(spans []protocol.NodeSleep, since time.Time) []protocol.NodeSleep {
	out := spans[:0]
	for _, s := range spans {
		if s.From <= 0 {
			continue
		}
		if s.To == 0 || !time.Unix(0, s.To).Before(since) {
			out = append(out, s)
		}
	}
	return out
}

var pmsetLine = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) ([+-]\d{4})\s+(Sleep|Wake)\b`)

// parsePMSetLog reads the lines of `pmset -g log`. A Sleep line starts a span
// and the next Wake line ends it.
func parsePMSetLog(text string, since time.Time) []protocol.NodeSleep {
	var out []protocol.NodeSleep
	var from time.Time
	for _, line := range strings.Split(text, "\n") {
		m := pmsetLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		at, err := time.Parse("2006-01-02 15:04:05 -0700", m[1]+" "+m[2])
		if err != nil {
			continue
		}
		switch m[3] {
		case "Sleep":
			from = at
		case "Wake":
			if from.IsZero() {
				continue
			}
			if !at.Before(since) {
				out = append(out, protocol.NodeSleep{From: from.UnixNano(), To: at.UnixNano(), Source: "pmset"})
			}
			from = time.Time{}
		}
	}
	return out
}

var journalTime = []string{"2006-01-02T15:04:05-0700", time.RFC3339}

// parseJournalSleep reads kernel journal lines. "PM: suspend entry" starts a
// span and "PM: suspend exit" ends it.
func parseJournalSleep(text string, since time.Time) []protocol.NodeSleep {
	var out []protocol.NodeSleep
	var from time.Time
	for _, line := range strings.Split(text, "\n") {
		var entry bool
		switch {
		case strings.Contains(line, "PM: suspend entry"):
			entry = true
		case strings.Contains(line, "PM: suspend exit"):
		default:
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		var at time.Time
		var err error
		for _, layout := range journalTime {
			if at, err = time.Parse(layout, fields[0]); err == nil {
				break
			}
		}
		if err != nil {
			continue
		}
		if entry {
			from = at
			continue
		}
		if from.IsZero() {
			continue
		}
		if !at.Before(since) {
			out = append(out, protocol.NodeSleep{From: from.UnixNano(), To: at.UnixNano(), Source: "journal"})
		}
		from = time.Time{}
	}
	return out
}

// runSleepCommand runs an OS command and returns its output. A missing tool or
// a slow command yields nothing; the clock estimate then stands alone.
func runSleepCommand(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), sleepCommand)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// sleepLoop watches for sleeps until ctx ends.
func (d *Daemon) sleepLoop(ctx context.Context) {
	if d.sleep == nil {
		return
	}
	t := time.NewTicker(sleepTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.checkSleep(ctx)
		}
	}
}

func (d *Daemon) checkSleep(ctx context.Context) {
	spans := d.sleep.observe(d.sleep.now(), d.sleep.mono())
	if len(spans) == 0 {
		return
	}
	d.mu.Lock()
	l := d.lk
	d.mu.Unlock()
	if l == nil {
		d.sleep.addPending(spans)
		return
	}
	if err := d.sendSleepReports(ctx, l, spans); err != nil {
		d.sleep.addPending(spans)
	}
}

func (d *Daemon) sendSleepReports(ctx context.Context, l *link, spans []protocol.NodeSleep) error {
	for _, s := range spans {
		if _, err := l.call(ctx, protocol.FrameNodeSleep, s); err != nil {
			return err
		}
	}
	return nil
}

// flushSleepReports sends the spans a wake left behind with the hello.
func (d *Daemon) flushSleepReports(ctx context.Context, l *link) {
	if d.sleep == nil {
		return
	}
	spans := d.sleep.takePending()
	if len(spans) == 0 {
		return
	}
	if err := d.sendSleepReports(ctx, l, spans); err != nil {
		d.sleep.restorePending(spans)
	}
}

// sleepJournalSeconds formats a time as the --since argument of journalctl.
func sleepJournalSeconds(t time.Time) string {
	return "@" + strconv.FormatInt(t.Unix(), 10)
}
