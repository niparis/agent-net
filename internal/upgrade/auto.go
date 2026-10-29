package upgrade

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// RetryAfter is how long a version that failed to install is left alone.
const RetryAfter = time.Hour

var releaseRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// ParseRelease reads a release version like v1.2.3. "dev", describe strings
// (v1.2.3-4-gabc-dirty) and anything else are not releases.
func ParseRelease(v string) ([3]int, bool) {
	var out [3]int
	m := releaseRe.FindStringSubmatch(v)
	if m == nil {
		return out, false
	}
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// IsNewer reports whether a is a newer release than b. Both must be releases.
func IsNewer(a, b string) bool {
	pa, oka := ParseRelease(a)
	pb, okb := ParseRelease(b)
	if !oka || !okb {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

// AutoFailure remembers a failed automatic upgrade.
type AutoFailure struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	Reason  string    `json:"reason,omitempty"`
}

// AutoState is what a machine remembers about automatic upgrades. It lives in
// auto-upgrade.json in the daemon state dir.
type AutoState struct {
	// Failed is the last version an automatic upgrade could not install.
	Failed *AutoFailure `json:"failed,omitempty"`
	// Hold is the version this machine rolled back from. It is not installed
	// automatically, and neither is anything older.
	Hold string `json:"hold,omitempty"`
}

func autoPath(dir string) string { return filepath.Join(dir, "auto-upgrade.json") }

// LoadAuto reads the state. A missing or broken file is an empty state.
func LoadAuto(dir string) AutoState {
	var s AutoState
	if b, err := os.ReadFile(autoPath(dir)); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

// Save writes the state atomically.
func (s AutoState) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := autoPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, autoPath(dir))
}

// SetHold makes automatic upgrades skip version v (and older).
func SetHold(dir, v string) error {
	s := LoadAuto(dir)
	s.Hold = v
	return s.Save(dir)
}

// ClearHold removes the hold. It writes nothing when there is none.
func ClearHold(dir string) error {
	s := LoadAuto(dir)
	if s.Hold == "" {
		return nil
	}
	s.Hold = ""
	return s.Save(dir)
}

// RecordFailure remembers that an automatic upgrade to v failed.
func RecordFailure(dir, v string, at time.Time, reason error) error {
	s := LoadAuto(dir)
	s.Failed = &AutoFailure{Version: v, At: at, Reason: reason.Error()}
	return s.Save(dir)
}

// HoldNote is the line shown by `status` and `upgrade --check`; empty when
// nothing is held.
func (s AutoState) HoldNote() string {
	if s.Hold == "" {
		return ""
	}
	return fmt.Sprintf("auto-upgrade held at %s after rollback", s.Hold)
}

// Decision is the outcome of DecideAuto.
type Decision struct {
	Upgrade bool
	// ClearHold is set when the served version is newer than the held one.
	ClearHold bool
	// Reason says why not, for the log.
	Reason string
}

// DecideAuto decides whether a daemon running current should install served.
func DecideAuto(current, served string, off bool, st AutoState, now time.Time) Decision {
	no := func(format string, a ...any) Decision { return Decision{Reason: fmt.Sprintf(format, a...)} }
	if off {
		return no("automatic upgrade is turned off")
	}
	if current == "" || current == "dev" {
		return no("this is a dev build")
	}
	if _, ok := ParseRelease(current); !ok {
		return no("%s is not a release version", current)
	}
	if _, ok := ParseRelease(served); !ok {
		return no("the gateway serves %q, which is not a release version", served)
	}
	if !IsNewer(served, current) {
		return no("the gateway serves %s, not newer than %s", served, current)
	}
	if f := st.Failed; f != nil && f.Version == served && now.Sub(f.At) < RetryAfter {
		return no("upgrade to %s failed %s ago, trying again after %s", served, now.Sub(f.At).Round(time.Second), RetryAfter)
	}
	d := Decision{Upgrade: true, Reason: "newer release"}
	if st.Hold != "" {
		if !IsNewer(served, st.Hold) {
			return no("held at %s after rollback", st.Hold)
		}
		d.ClearHold = true
	}
	return d
}
