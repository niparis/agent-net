package daemon

import (
	"testing"
	"time"
)

// ps pads a one-digit day with a second space, so the same start time can come
// back with one or two spaces. Both sides of a token comparison must agree.
func TestStartTokenIgnoresPsPadding(t *testing.T) {
	cases := []struct{ name, lstart, want string }{
		{"single-digit day is padded", "Mon Nov  9 02:29:26 2026", "Mon Nov 9 02:29:26 2026"},
		{"two-digit day", "Mon Nov 12 02:29:26 2026", "Mon Nov 12 02:29:26 2026"},
		{"trailing newline and blanks", "  Mon Nov  9 02:29:26 2026\n", "Mon Nov 9 02:29:26 2026"},
		{"linux tick token is unchanged", "123456789", "123456789"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := startToken(c.lstart); got != c.want {
				t.Fatalf("startToken(%q) = %q, want %q", c.lstart, got, c.want)
			}
		})
	}
}

func TestStartTokenIsStable(t *testing.T) {
	once := startToken("Mon Nov  9 02:29:26 2026")
	if twice := startToken(once); twice != once {
		t.Fatalf("startToken is not idempotent: %q then %q", once, twice)
	}
}

// A worker row saved before the fix can hold a token with the padded day. It
// must read back in the form procToken makes now, or a restarted daemon would
// take a live worker for a process that has ended.
func TestWorkerStartTokenReadsBackInProcTokenForm(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := st.saveWorker(workerRow{ID: "w1", Harness: "opencode", PID: 4242, PGID: 4242,
		StartToken: "Mon Nov  9 02:29:26 2026", StartedAt: now, LastActive: now}); err != nil {
		t.Fatal(err)
	}
	st.close()

	st, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	rows, err := st.workers()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d workers, want 1", len(rows))
	}
	if want := "Mon Nov 9 02:29:26 2026"; rows[0].StartToken != want {
		t.Fatalf("StartToken = %q, want %q", rows[0].StartToken, want)
	}
}
