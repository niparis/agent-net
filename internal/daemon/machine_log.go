package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/logrotate"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

const maxLogLine = 1 << 20
const maxLogInput = 80 << 20

// logCollector redacts complete lines before filtering or output truncation.
// A private key block is masked across lines, including filtered-out lines.
type logCollector struct {
	q        protocol.MachineLogRequest
	since    time.Time
	result   protocol.MachineLogResult
	bytes    int
	private  bool
	inWindow bool
}

func newLogCollector(q protocol.MachineLogRequest, since time.Time) *logCollector {
	return &logCollector{q: q, since: since, result: protocol.MachineLogResult{Machine: q.Machine, Log: q.Log, Since: q.Since, Lines: []string{}}}
}

func (c *logCollector) add(line string, timestamped bool) {
	if timestamped {
		if at, ok := logLineTime(line); ok {
			c.inWindow = !at.Before(c.since)
		}
		if !c.inWindow { // Still update private-key state before ignoring this line.
			c.mask(line)
			return
		}
	}
	line = c.mask(line)
	if c.q.Filter != "" && !strings.Contains(line, c.q.Filter) {
		return
	}
	// Reserve enough for JSON metadata, separators and escaping in machine/since.
	encoded, _ := json.Marshal(line)
	size := len(encoded) + 1
	if size > protocol.MachineLogMaxBytes-4096 {
		c.result.Truncated = true
		return
	}
	for len(c.result.Lines) > 0 && (len(c.result.Lines) >= c.q.Limit || c.bytes+size > protocol.MachineLogMaxBytes-4096) {
		old, _ := json.Marshal(c.result.Lines[0])
		c.bytes -= len(old) + 1
		c.result.Lines = c.result.Lines[1:]
		c.result.Truncated = true
	}
	c.result.Lines = append(c.result.Lines, line)
	c.bytes += size
}

func (c *logCollector) mask(line string) string {
	// Mask whole key lines. The key may start or end in a timestamped line.
	if c.private || strings.Contains(line, "-----BEGIN ") && strings.Contains(line, "PRIVATE KEY-----") {
		if strings.Contains(line, "-----END ") && strings.Contains(line, "PRIVATE KEY-----") {
			c.private = false
		} else {
			c.private = true
		}
		return redact.Mask
	}
	return redact.Secrets(line)
}

func logLineTime(line string) (time.Time, bool) {
	// The daemon's standard logger uses local time; gateway logfmt uses UTC.
	if strings.HasPrefix(line, "daemon: ") && len(line) >= len("daemon: 2006/01/02 15:04:05") {
		t, err := time.ParseInLocation("2006/01/02 15:04:05", line[len("daemon: "):len("daemon: 2006/01/02 15:04:05")], time.Local)
		return t, err == nil
	}
	if strings.HasPrefix(line, "ts=") {
		first, _, _ := strings.Cut(line, " ")
		t, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(first, "ts="))
		return t, err == nil
	}
	return time.Time{}, false
}

func scanLog(ctx context.Context, r io.Reader, c *logCollector, timestamped bool) error {
	return scanLogLimit(ctx, r, c, timestamped, maxLogInput)
}

func scanLogLimit(ctx context.Context, r io.Reader, c *logCollector, timestamped bool, maxInput int64) error {
	limited := &io.LimitedReader{R: r, N: maxInput + 1}
	scan := bufio.NewScanner(limited)
	scan.Buffer(make([]byte, 4096), maxLogLine)
	// A service may be writing its final line while we read. Never return a
	// fragment before the newline: it could be an incomplete secret.
	scan.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if atEOF && bytes.IndexByte(data, '\n') < 0 {
			return len(data), nil, nil
		}
		return bufio.ScanLines(data, atEOF)
	})
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.add(scan.Text(), timestamped)
	}
	if limited.N == 0 {
		return errors.New("log input exceeds scan limit")
	}
	if err := scan.Err(); err != nil {
		return errors.New("log contains an oversized line or cannot be read")
	}
	return nil
}

// readMachineLogFiles takes a directory only as an internal test seam. The
// public request cannot select it; production always uses the fixed log paths.
func readMachineLogFiles(ctx context.Context, dir string, q protocol.MachineLogRequest, now time.Time) (protocol.MachineLogResult, error) {
	q, since, err := protocol.NormalizeMachineLog(q, now)
	if err != nil {
		return protocol.MachineLogResult{}, err
	}
	c := newLogCollector(q, since)
	found := false
	for i := logrotate.Keep; i >= 0; i-- {
		path := filepath.Join(dir, "agent-net-"+q.Log+".log")
		if i > 0 {
			path += fmt.Sprintf(".%d", i)
		}
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return protocol.MachineLogResult{}, errors.New("cannot open service log")
		}
		found = true
		err = scanLog(ctx, f, c, true)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return protocol.MachineLogResult{}, errors.New("cannot read service log")
		}
	}
	if !found {
		return protocol.MachineLogResult{}, errors.New("service log not found on this machine")
	}
	return c.result, nil
}

func (d *Daemon) readMachineLog(ctx context.Context, q protocol.MachineLogRequest) (protocol.MachineLogResult, error) {
	q, since, err := protocol.NormalizeMachineLog(q, time.Now())
	if err != nil {
		return protocol.MachineLogResult{}, err
	}
	if d.cfg.testLogRead != nil {
		return d.cfg.testLogRead(ctx, q)
	}
	switch runtime.GOOS {
	case "darwin":
		path := logrotate.ServicePath(q.Log)
		if path == "" {
			return protocol.MachineLogResult{}, errors.New("service log path unavailable")
		}
		return readMachineLogFiles(ctx, filepath.Dir(path), q, time.Now())
	case "linux":
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "journalctl", "--user", "--no-pager", "--output=short-iso-precise", "--unit=agent-net-"+q.Log, "--since="+since.UTC().Format(time.RFC3339Nano))
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return protocol.MachineLogResult{}, errors.New("cannot read journal")
		}
		if err = cmd.Start(); err != nil {
			return protocol.MachineLogResult{}, errors.New("cannot start journalctl")
		}
		c := newLogCollector(q, since)
		scanErr := scanLog(ctx, stdout, c, false)
		if scanErr != nil {
			_ = cmd.Process.Kill()
		}
		waitErr := cmd.Wait()
		if scanErr != nil {
			return protocol.MachineLogResult{}, scanErr
		}
		if waitErr != nil {
			return protocol.MachineLogResult{}, errors.New("journalctl could not read the service log")
		}
		return c.result, nil
	default:
		return protocol.MachineLogResult{}, errors.New("service logs are not supported on this system")
	}
}
