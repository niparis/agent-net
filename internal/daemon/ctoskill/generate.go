package ctoskill

import (
	"os"
	"path/filepath"
)

// Generate rewrites the generated reference.md from the code lists.
func Generate() error {
	return os.WriteFile(filepath.Join(dir, "reference.md"), []byte(Reference()), 0o644)
}
