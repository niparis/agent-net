// Package ctoskill holds the agent-net-setup skill shipped to the CTO. The
// files are embedded in the binary so the daemon can write them into the CTO's
// private folder without another download.
package ctoskill

import (
	"bytes"
	"embed"
	"os"
	"path"
	"path/filepath"
)

// Name is the skill's folder name, and Dir is its path inside the package.
const Name = "agent-net-setup"

const dir = Name

// Names are the skill files, in the order they are written.
var Names = []string{"SKILL.md", "reference.md"}

//go:embed agent-net-setup/SKILL.md agent-net-setup/reference.md
var files embed.FS

//go:generate go run ../../../cmd/genctoskill

// File returns one embedded skill file.
func File(name string) ([]byte, error) {
	return files.ReadFile(path.Join(dir, name))
}

// Write puts the skill files into folder, creating it when needed. A file
// whose content is already current is left alone, so a CTO restart does not
// churn the folder.
func Write(folder string) error {
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	for _, name := range Names {
		data, err := File(name)
		if err != nil {
			return err
		}
		if err := writeIfChanged(filepath.Join(folder, name), data); err != nil {
			return err
		}
	}
	return nil
}

func writeIfChanged(file string, data []byte) error {
	old, err := os.ReadFile(file)
	if err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(file, data, 0600)
}
