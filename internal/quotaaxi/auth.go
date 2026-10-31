package quotaaxi

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// AuthProvider holds only the outcome, not credential values or source paths.
type AuthProvider struct {
	Provider string `json:"provider"`
	Status   string `json:"status"`
}
type AuthResult struct {
	Path           string         `json:"path"`
	Version        string         `json:"version"`
	CheckedAt      time.Time      `json:"checked_at"`
	KeychainPrompt bool           `json:"keychain_prompt"`
	Error          string         `json:"error,omitempty"`
	Providers      []AuthProvider `json:"providers,omitempty"`
}

func LoadAuth(dir string) (AuthResult, error) {
	var result AuthResult
	b, err := os.ReadFile(filepath.Join(dir, "quota-axi-auth.json"))
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(b, &result)
	return result, err
}
func SaveAuth(dir string, result AuthResult) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".quota-auth-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "quota-axi-auth.json"))
}
func RemoveAuth(dir string) error {
	err := os.Remove(filepath.Join(dir, "quota-axi-auth.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
