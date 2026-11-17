package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// emDash is the character B9 forbids in docs, messages and screens.
const emDash = "\u2014"

// checkDocs reports each line of each Markdown file under dir that has an em
// dash. The files are plain text, so they are read, not parsed.
func checkDocs(dir string) ([]finding, error) {
	var out []finding
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			col := strings.Index(line, emDash)
			if col < 0 {
				continue
			}
			out = append(out, finding{
				file: path,
				line: i + 1,
				col:  col + 1,
				rule: EmDashAnalyzer.Name,
				msg:  "em dash in a doc (B9): use a plain hyphen",
			})
		}
		return nil
	})
	return out, err
}
