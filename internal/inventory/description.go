package inventory

import (
	"bytes"
	"io"
	"os"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

func skillDescription(data []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return ""
	}
	end := 1
	for end < len(lines) && lines[end] != "---" && lines[end] != "..." {
		end++
	}
	if end == len(lines) {
		return ""
	}
	var doc struct {
		Description yaml.Node `yaml:"description"`
	}
	if yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &doc) != nil || doc.Description.Tag != "!!str" {
		return ""
	}
	s := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, doc.Description.Value)
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 1024 {
		end := 1024
		for end > 0 && r[end] != ' ' {
			end--
		}
		s = strings.TrimSpace(string(r[:end]))
	}
	return s
}

func skillDescriptionRoot(root *os.Root) string {
	f, err := root.Open("SKILL.md")
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return ""
	}
	return skillDescription(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf}))
}
