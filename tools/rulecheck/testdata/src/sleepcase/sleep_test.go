package sleepcase

import (
	"testing"
	"time"
)

// pause is a named constant, so the check reads its value through the types.
const pause = 3 * time.Second

// The operator client tests once used this shape: a fake handler that never
// answers, written as a one-hour sleep.
func TestNeverAnswers(t *testing.T) {
	handler := func() {
		time.Sleep(time.Hour) // want `in a test`
	}
	handler()
}

func TestNamedConstant(t *testing.T) {
	time.Sleep(pause) // want `in a test`
}

// The fake codex run hung for ten seconds.
func TestHangingFake(t *testing.T) {
	time.Sleep(10 * time.Second) // want `in a test`
}

func TestAtTheLimit(t *testing.T) {
	time.Sleep(2 * time.Second)
}

func TestVariableDuration(t *testing.T) {
	d := time.Duration(5) * time.Second
	time.Sleep(d)
}
