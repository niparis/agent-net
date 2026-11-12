package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// etagOf is a short hash of what an answer would send. The TUI polls lists
// whose tables are written all day (every session heartbeat moves sessions),
// while the rows it draws rarely change. A caller that holds the ETag of its
// rows sends it back, and an answer that hashes to the same value is replaced
// by NotModified. The topic version cannot do this: it counts writes, not
// changes in what is sent. An empty ETag means the rows could not be hashed,
// and the caller then reads them in full.
func etagOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}
