package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func (d *Daemon) nodeToken() string { d.mu.Lock(); defer d.mu.Unlock(); return d.cfg.Token }

// Preserve unknown settings and replace the file only after a durable write.
func saveRotatedToken(dir, nodeID, old, token string) error {
	path := filepath.Join(dir, "node.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var id, current string
	_ = json.Unmarshal(fields["node_id"], &id)
	_ = json.Unmarshal(fields["token"], &current)
	if id != nodeID || current != old || token == "" {
		return errors.New("node.json identity or token changed; rotation refused")
	}
	fields["token"], _ = json.Marshal(token)
	raw, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".node-token-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(raw, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
