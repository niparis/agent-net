package sleepcase

import "time"

// Production code is not a test, so the rule does not apply to it.
func Retry() {
	time.Sleep(10 * time.Second)
}
