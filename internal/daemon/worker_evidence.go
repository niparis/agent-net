package daemon

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// measureWorkerEvidence reads the whole diff against the worker's start, not
// just HEAD. Capture before any daemon commit: a small worker commit counts
// as an attempt, while a daemon save of unfinished work does not.
func (d *Daemon) measureWorkerEvidence(ctx context.Context, w *worker, complete *bool) *protocol.WorkerEvidence {
	d.wmu.Lock()
	dir, base, project, started := w.worktree, w.base, w.project, w.startedAt
	d.wmu.Unlock()
	if dir == "" || base == "" || started.IsZero() {
		return nil
	}
	elapsed := time.Since(started)
	diff, err := gitOut(ctx, dir, "diff", "--no-renames", "--numstat", "-z", base, "--")
	if err != nil {
		return nil
	}
	committed, err := gitOut(ctx, dir, "diff", "--name-only", base, "HEAD", "--")
	if err != nil {
		return nil
	}
	untracked, err := gitOut(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil
	}
	e := &protocol.WorkerEvidence{Complete: complete, RunSeconds: int64(elapsed / time.Second)}
	if e.RunSeconds < 0 {
		e.RunSeconds = 0
	}
	for _, record := range strings.Split(diff, "\x00") {
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\t", 3)
		if len(fields) != 3 {
			return nil
		}
		e.FilesChanged++
		// Binary files have '-' for both counts: they are files, with zero text lines.
		for _, count := range fields[:2] {
			if count == "-" {
				continue
			}
			n, err := strconv.Atoi(count)
			if err != nil {
				return nil
			}
			e.LinesChanged += n
		}
	}
	for _, name := range strings.Split(untracked, "\x00") {
		if name == "" {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil
		}
		e.FilesChanged++
		if info.Mode()&os.ModeSymlink != 0 {
			e.LinesChanged++
			continue
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		lines, err := workerFileLines(path)
		if err != nil {
			return nil
		}
		e.LinesChanged += lines
	}
	threshold := d.SettingsSnapshot().Duration("workers.early_quit_threshold", settings.Where{Repo: project})
	e.EarlyQuit = (complete == nil || !*complete) && elapsed < threshold &&
		(e.FilesChanged == 0 || e.LinesChanged < 20 && committed == "")
	return e
}

// workerFileLines counts unterminated final lines too, without loading a
// potentially large untracked file into memory. Binary files have no text lines.
func workerFileLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 32*1024)
	lines := 0
	last := byte('\n')
	binary := false
	for {
		n, err := f.Read(buf)
		if n > 0 {
			binary = binary || bytes.IndexByte(buf[:n], 0) >= 0
			lines += bytes.Count(buf[:n], []byte{'\n'})
			last = buf[n-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	if binary {
		return 0, nil
	}
	if last != '\n' {
		lines++
	}
	return lines, nil
}

func (d *Daemon) captureWorkerEvidence(w *worker, complete *bool) {
	e := d.measureWorkerEvidence(context.Background(), w, complete)
	d.wmu.Lock()
	w.evidence = e
	d.wmu.Unlock()
}
