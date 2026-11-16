package gateway

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// A read answers in pages, so every answer stays far under the link's frame
// limit and a CTO tool result is small enough to read in one go (decision 0169).
const (
	// skillArchiveTextPage is the most text one answer carries for one file.
	skillArchiveTextPage = 64 << 10
	// skillArchiveListRows and skillArchiveListBytes bound one page of the file
	// list: its rows, and the JSON size of those rows.
	skillArchiveListRows  = 200
	skillArchiveListBytes = 128 << 10
)

// skillMDPath is the file that every overview starts with.
const skillMDPath = "SKILL.md"

// skillArchiveFile is one file of an unpacked version.
type skillArchiveFile struct {
	path    string
	size    int64
	abs     string
	regular bool
}

// skillFileAfter reports whether path b comes after path a in the file list.
// SKILL.md comes first, and the other paths follow in byte order.
func skillFileAfter(a, b string) bool {
	switch {
	case a == skillMDPath:
		return b != skillMDPath
	case b == skillMDPath:
		return false
	}
	return a < b
}

// skillArchiveReadFiles lists the files of an unpacked version in list order.
func skillArchiveReadFiles(dir string) ([]skillArchiveFile, error) {
	var files []skillArchiveFile
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, skillArchiveFile{path: filepath.ToSlash(rel), size: info.Size(), abs: path, regular: info.Mode().IsRegular()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return skillFileAfter(files[i].path, files[j].path) })
	return files, nil
}

// archiveFileRow answers one file. Its text comes only when withText is set,
// one page from offset. A file is text when all of it is UTF-8 without NUL
// bytes. Links and other special files are never text.
func archiveFileRow(f skillArchiveFile, withText bool, offset int64) (protocol.SkillArchiveFile, error) {
	row := protocol.SkillArchiveFile{Path: f.path, Size: f.size}
	if !f.regular {
		row.Binary = true
		return row, nil
	}
	data, err := os.ReadFile(f.abs)
	if err != nil {
		return row, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		row.Binary = true
		return row, nil
	}
	if !withText {
		return row, nil
	}
	if offset > int64(len(data)) {
		return row, reject("offset is past the end of the file")
	}
	start, end := archiveTextPage(data, int(offset))
	row.Offset = int64(start)
	row.Text = string(data[start:end])
	if end < len(data) {
		row.NextOffset = int64(end)
	}
	return row, nil
}

// archiveTextPage returns the bounds of the page that starts at offset. The
// page never splits a character, so both ends fall on a character start.
func archiveTextPage(data []byte, offset int) (start, end int) {
	start = offset
	for start < len(data) && !utf8.RuneStart(data[start]) {
		start++
	}
	end = min(start+skillArchiveTextPage, len(data))
	for end < len(data) && !utf8.RuneStart(data[end]) {
		end--
	}
	return start, end
}

// skillArchiveRead answers one stored version (decision 0169). With File set it
// answers one page of that file's text, from Offset. Otherwise it answers one
// page of the file list, from After, and SKILL.md's first page of text.
func (g *Gateway) skillArchiveRead(p protocol.SkillArchiveVersion) (protocol.SkillArchiveReadResult, error) {
	out := protocol.SkillArchiveReadResult{Name: p.Name, Version: p.Version, Files: []protocol.SkillArchiveFile{}}
	if !validSkillName(p.Name) {
		return out, reject("bad skill name")
	}
	if p.Offset < 0 {
		return out, reject("offset cannot be negative")
	}
	if p.File == "" && p.Offset != 0 {
		return out, reject("offset needs a file")
	}
	if p.File != "" && p.After != "" {
		return out, reject("read takes a file or a list cursor, not both")
	}
	version := p.Version
	if version <= 0 {
		latest, err := archiveLatestOf(g.store.db, p.Name)
		if err != nil {
			return out, err
		}
		if latest == 0 {
			return out, reject("skill version is not in the archive")
		}
		version = latest
	}
	var bundle []byte
	if err := g.store.db.QueryRow(`SELECT version, hash, content FROM skill_archive WHERE name=? AND version=? AND removed_at=0`, p.Name, version).Scan(&out.Version, &out.Hash, &bundle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, reject("skill version is not in the archive")
		}
		return out, err
	}
	dir, err := os.MkdirTemp("", "agent-net-archive-read-")
	if err != nil {
		return out, errors.New("cannot stage skill version")
	}
	defer os.RemoveAll(dir)
	if inventory.UnpackSkillLimit(bundle, dir, inventory.ArchiveLimit) != nil {
		return out, errors.New("invalid stored skill bundle")
	}
	if got, err := inventory.SkillHash(dir); err != nil || got != out.Hash {
		return out, errors.New("stored skill hash mismatch")
	}
	files, err := skillArchiveReadFiles(dir)
	if err != nil {
		return out, err
	}
	out.Total = len(files)
	if p.File != "" {
		for _, f := range files {
			if f.path != p.File {
				continue
			}
			row, err := archiveFileRow(f, true, p.Offset)
			if err != nil {
				return out, err
			}
			out.Files = append(out.Files, row)
			return out, nil
		}
		return out, reject("file is not in the skill version")
	}
	start := 0
	if p.After != "" {
		start = sort.Search(len(files), func(i int) bool { return skillFileAfter(p.After, files[i].path) })
	}
	used := 0
	i := start
	for ; i < len(files) && i-start < skillArchiveListRows; i++ {
		row, err := archiveFileRow(files[i], files[i].path == skillMDPath, 0)
		if err != nil {
			return out, err
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return out, err
		}
		if i > start && used+len(encoded) > skillArchiveListBytes {
			break
		}
		used += len(encoded)
		out.Files = append(out.Files, row)
	}
	if i < len(files) {
		out.NextAfter = files[i-1].path
	}
	return out, nil
}
