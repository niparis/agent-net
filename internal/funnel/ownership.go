package funnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Record is the port and exact proxy target installed by agent-net.
type Record struct {
	Port   int    `json:"port"`
	Target string `json:"target"`
}

// SaveRecord records intent before Funnel is changed, including across a crash.
func SaveRecord(dir string, record Record) error {
	if record.Port != 443 && record.Port != 8443 && record.Port != 10000 || record.Target == "" {
		return errors.New("invalid Funnel record")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".funnel-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, "funnel.json"))
}

// ReadRecord returns os.ErrNotExist when no managed route is recorded.
func ReadRecord(dir string) (Record, error) {
	var record Record
	data, err := os.ReadFile(filepath.Join(dir, "funnel.json"))
	if err != nil {
		return record, err
	}
	if err = json.Unmarshal(data, &record); err != nil {
		return record, fmt.Errorf("decode Funnel record: %w", err)
	}
	if record.Port != 443 && record.Port != 8443 && record.Port != 10000 || record.Target == "" {
		return record, errors.New("invalid Funnel record")
	}
	return record, nil
}

// Cleanup removes the route only when the entire port still belongs to our
// recorded root proxy. A repurposed or shared port is left alone.
func Cleanup(ctx context.Context, r Runner, dir string) (bool, error) {
	record, err := ReadRecord(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	info, err := Status(ctx, r)
	if err != nil && !errors.Is(err, ErrFunnelNotAllowed) {
		return false, err
	}
	count := 0
	match := false
	for _, route := range info.Routes {
		if route.Port == record.Port {
			count++
			match = route.Path == "/" && route.Target == record.Target
		}
	}
	removed := false
	if count == 1 && match {
		if err := Off(ctx, r, record.Port); err != nil {
			return false, err
		}
		removed = true
	}
	if err := os.Remove(filepath.Join(dir, "funnel.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return removed, err
	}
	return removed, nil
}
