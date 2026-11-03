// Package logrotate keeps a service log file from growing without bound.
//
// On macOS launchd points the daemon and gateway stdout and stderr at
// ~/Library/Logs/agent-net-<name>.log and never rotates that file. Each
// process opens the same path itself, writes its log lines there through
// Writer, and renames the file when it passes the size limit. Linux runs the
// services under journald, which rotates for us, so nothing here is used
// there.
package logrotate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const (
	// MaxBytes is the size a log file may reach before it is rotated.
	MaxBytes = 10 << 20
	// Keep is the number of old files kept next to the current one.
	Keep = 5
)

// Writer is an append-only log file that rotates itself by size.
type Writer struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
	max  int64
	keep int
}

// Open opens path for appending and creates its folder. A max or keep that is
// not positive uses MaxBytes or Keep.
func Open(path string, max int64, keep int) (*Writer, error) {
	if max <= 0 {
		max = MaxBytes
	}
	if keep <= 0 {
		keep = Keep
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &Writer{path: path, file: file, size: info.Size(), max: max, keep: keep}, nil
}

// Write appends p. When p would pass the size limit, the current file is
// renamed first and a new one is opened.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size > 0 && w.size+int64(len(p)) > w.max {
		w.rotateLocked()
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotateLocked shifts the old files and starts a new current file. When the
// rename or the reopen fails, it keeps the open file so no line is lost.
func (w *Writer) rotateLocked() {
	oldest := w.path + "." + fmt.Sprint(w.keep)
	os.Remove(oldest)
	for i := w.keep - 1; i >= 1; i-- {
		os.Rename(w.path+"."+fmt.Sprint(i), w.path+"."+fmt.Sprint(i+1))
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	w.file.Close()
	w.file = file
	w.size = 0
}

// Close closes the current log file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// ServicePath is where launchd writes a service log on macOS. It is empty on
// other systems, where journald keeps the output.
func ServicePath(name string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Logs", "agent-net-"+name+".log")
}

// IsFile reports whether f is the file at path. The services use it to make
// sure they rotate the launchd log and never a terminal or a pipe.
func IsFile(f *os.File, path string) bool {
	want, err := os.Stat(path)
	if err != nil {
		return false
	}
	got, err := f.Stat()
	if err != nil {
		return false
	}
	return os.SameFile(got, want)
}
