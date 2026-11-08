package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func charterCaller(text string) Caller {
	return func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method != protocol.MethodCharterText {
			return nil, errors.New("unexpected method " + method)
		}
		return json.Marshal(protocol.CharterTextResult{Text: text})
	}
}

// A starting session's instructions are the fixed text, then the charter text
// the daemon gave.
func TestStartInstructionsAddTheCharterAfterTheFixedText(t *testing.T) {
	got := StartInstructions(context.Background(), charterCaller("[agent-net] Your effective charter\nrules: report late defects"))
	if !strings.HasPrefix(got, Instructions+"\n\n") || !strings.HasSuffix(got, "rules: report late defects") {
		t.Fatalf("instructions:\n%s", got)
	}
	if got := StartInstructions(context.Background(), charterCaller("")); got != Instructions {
		t.Fatalf("no charter must give the fixed text:\n%s", got)
	}
}

// The fixed text is the fallback for every failure: an old daemon that does not
// know the method, a daemon that cannot be reached, an answer that is not JSON
// and a daemon that is too slow.
func TestStartInstructionsFallBackToTheFixedText(t *testing.T) {
	fail := func(err error) Caller {
		return func(context.Context, string, any) (json.RawMessage, error) { return nil, err }
	}
	for name, call := range map[string]Caller{
		"old daemon":  fail(errors.New(`method "charter_text" is not available`)),
		"unreachable": fail(ErrUnreachable),
		"bad answer":  func(context.Context, string, any) (json.RawMessage, error) { return json.RawMessage(`[1]`), nil },
	} {
		if got := StartInstructions(context.Background(), call); got != Instructions {
			t.Errorf("%s:\n%s", name, got)
		}
	}
	old := charterTextBudget
	charterTextBudget = 50 * time.Millisecond
	defer func() { charterTextBudget = old }()
	slow := func(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	if got := StartInstructions(context.Background(), slow); got != Instructions {
		t.Errorf("slow daemon:\n%s", got)
	}
	if time.Since(start) > time.Second {
		t.Errorf("waited %v for a slow daemon", time.Since(start))
	}
}

// A long charter is cut so the total stays under the client's limit, and the
// fixed text is never the part that is cut.
func TestStartInstructionsStayUnderTheClientLimit(t *testing.T) {
	got := StartInstructions(context.Background(), charterCaller(strings.Repeat("rules: a long rule that goes on and on\n", 200)))
	if n := len([]rune(got)); n > protocol.MaxInstructionsRunes {
		t.Fatalf("%d runes, cap %d", n, protocol.MaxInstructionsRunes)
	}
	if !strings.HasPrefix(got, Instructions) || !strings.Contains(got, "rules: a long rule") {
		t.Fatalf("instructions:\n%s", got)
	}
}
