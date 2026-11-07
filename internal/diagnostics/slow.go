package diagnostics

import "time"

// Slow reports elapsed work exceeding 200 ms. Use defer Slow(time.Now(), report)
// so timing uses the monotonic clock and excludes subsequent waits.
func Slow(start time.Time, report func(ms int64)) {
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		report(elapsed.Milliseconds())
	}
}
