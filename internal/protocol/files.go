package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits of a message with files (decision 0054). The daemon enforces them
// before it sends; the gateway checks the manifest again when it stores the
// message.
const (
	// MaxFileBytes is the largest file one message may carry.
	MaxFileBytes int64 = 100 << 20
	// MaxFilesPerMessage is the most files one message may carry.
	MaxFilesPerMessage = 10
	// FileRetention is how long a receiving daemon keeps received files.
	FileRetention = 7 * 24 * time.Hour
	// MaxFileNameBytes is the longest file name in a manifest. The unique
	// name a file travels under adds about 40 bytes, and stays under the
	// 255-byte limit of file systems and of the gateway's upload name.
	MaxFileNameBytes = 200
)

// FileEntry is one file of a message's manifest: the name the receiver sees,
// the size, the SHA-256 of the bytes and an ID unique inside the message.
type FileEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// TaildropPrefix is the start of the unique name of every file of a message.
func TaildropPrefix(messageID string) string { return "agent-net-" + messageID + "-" }

// TaildropName is the unique name a file travels under, over Taildrop and
// through the gateway: agent-net-<message id>-<file id>-<name>. The receiving
// daemon takes only files whose name and hash match the manifest.
func TaildropName(messageID string, f FileEntry) string {
	return TaildropPrefix(messageID) + f.ID + "-" + f.Name
}

// ValidFileID tells whether id can name a file inside a message: 1 to 16
// lower-case letters and digits.
func ValidFileID(id string) bool {
	if id == "" || len(id) > 16 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// CheckFileName refuses a name that is not a plain file name: empty, longer
// than MaxFileNameBytes, not UTF-8, with a path separator or a control
// character, "." or "..", or with spaces at either end.
func CheckFileName(name string) error {
	switch {
	case name == "":
		return errors.New("a file name is empty")
	case len(name) > MaxFileNameBytes:
		return fmt.Errorf("the file name %q is longer than %d bytes", name, MaxFileNameBytes)
	case !utf8.ValidString(name):
		return errors.New("a file name is not valid UTF-8")
	case name == "." || name == "..":
		return fmt.Errorf("%q is not a file name", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("the file name %q holds a path separator", name)
	case strings.TrimSpace(name) != name:
		return fmt.Errorf("the file name %q starts or ends with a space", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("the file name %q holds a control character", name)
		}
	}
	return nil
}

// ValidSHA256 tells whether s is a lower-case hexadecimal SHA-256.
func ValidSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// ValidateFiles checks a manifest: at most MaxFilesPerMessage files, each
// with a valid ID, name, size and hash, and no two with the same ID or name.
// An empty manifest is valid: the message has no files.
func ValidateFiles(files []FileEntry) error {
	if len(files) > MaxFilesPerMessage {
		return fmt.Errorf("%d files, the limit is %d per message", len(files), MaxFilesPerMessage)
	}
	ids := make(map[string]bool, len(files))
	names := make(map[string]bool, len(files))
	for _, f := range files {
		if !ValidFileID(f.ID) {
			return fmt.Errorf("the file ID %q is not valid", f.ID)
		}
		if err := CheckFileName(f.Name); err != nil {
			return err
		}
		if f.Size < 0 || f.Size > MaxFileBytes {
			return fmt.Errorf("%s is %d bytes, the limit is %d MB", f.Name, f.Size, MaxFileBytes>>20)
		}
		if !ValidSHA256(f.SHA256) {
			return fmt.Errorf("the sha256 of %s is not 64 lower-case hexadecimal characters", f.Name)
		}
		if ids[f.ID] {
			return fmt.Errorf("two files have the ID %q", f.ID)
		}
		if names[f.Name] {
			return fmt.Errorf("two files are named %q", f.Name)
		}
		ids[f.ID], names[f.Name] = true, true
	}
	return nil
}

// FileSendRequest is a session's request to send local files to one peer.
type FileSendRequest struct {
	To    string   `json:"to"`
	Paths []string `json:"paths"`
	Note  string   `json:"note"`
}

// FileSendDestination names a file's destination relative to the receiving
// machine's agent-net state directory. Delivery may still be pending.
type FileSendDestination struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FileSendResult identifies the message and its planned destinations. Error
// reports a transfer failure after the message was stored, if any.
type FileSendResult struct {
	MessageID string                `json:"message_id"`
	Files     []FileSendDestination `json:"files"`
	Error     string                `json:"error,omitempty"`
}
