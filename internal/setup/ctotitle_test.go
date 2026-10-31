package setup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

type fakeTitleStore struct {
	title  protocol.CTOTitle
	saved  []string
	gets   int
	setErr error
}

func (f *fakeTitleStore) Title(context.Context) (protocol.CTOTitle, error) {
	f.gets++
	return f.title, nil
}

func (f *fakeTitleStore) SetTitle(_ context.Context, title string) error {
	f.saved = append(f.saved, title)
	if f.setErr == nil {
		f.title = protocol.CTOTitle{Title: title, Set: true}
	}
	return f.setErr
}

func TestCTOTitleQuestionOnlyOnAFreshGatewayWithATerminal(t *testing.T) {
	tests := []struct {
		name        string
		gateway     bool
		joining     bool
		interactive bool
		stored      bool
		ttyErr      bool
		ask         bool
		reads       int
		opens       int
	}{
		{name: "fresh gateway with a terminal", gateway: true, interactive: true, ask: true, reads: 1, opens: 1},
		{name: "joining machine", gateway: false, joining: true, interactive: true},
		{name: "rerun without the gateway", gateway: false, interactive: true},
		{name: "rerun with a title", gateway: true, interactive: true, stored: true, reads: 1},
		{name: "upgrade without a terminal", gateway: true},
		{name: "gateway run whose terminal is gone", gateway: true, interactive: true, ttyErr: true, reads: 1, opens: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			store := &fakeTitleStore{title: protocol.CTOTitle{Title: protocol.DefaultCTOTitle, Set: tc.stored}}
			r := &runner{
				Options:     Options{Out: &out, Gateway: tc.gateway},
				joining:     tc.joining,
				interactive: tc.interactive,
				titleStore:  store,
			}
			old := openTTY
			opened := 0
			openTTY = func() (io.ReadCloser, error) {
				opened++
				if tc.ttyErr {
					return nil, errors.New("no tty")
				}
				return io.NopCloser(strings.NewReader("Atlas\n")), nil
			}
			t.Cleanup(func() { openTTY = old })

			r.askCTOTitle(context.Background())

			if tc.ask {
				if opened != 1 || len(store.saved) != 1 || store.saved[0] != "Atlas" {
					t.Fatalf("asked=%d saved=%v", opened, store.saved)
				}
				for _, want := range []string{"What should your CTO be called? [CTO]", "cto title", "Atlas"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("output lacks %q:\n%s", want, out.String())
					}
				}
				return
			}
			if opened != tc.opens || len(store.saved) != 0 {
				t.Fatalf("asked question on %s: opened=%d saved=%v gets=%d\n%s", tc.name, opened, store.saved, store.gets, out.String())
			}
			if store.gets != tc.reads {
				t.Fatalf("gateway reads on %s = %d; want %d", tc.name, store.gets, tc.reads)
			}
			if strings.Contains(out.String(), "CTO title") {
				t.Fatalf("output mentions the title question:\n%s", out.String())
			}
		})
	}
}

func TestCTOTitleValueRulesInSetup(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		said  string
	}{
		{name: "name", input: "Atlas\n", want: "Atlas"},
		{name: "trimmed", input: "   Atlas   \n", want: "Atlas"},
		{name: "enter keeps the default", input: "\n", want: "CTO"},
		{name: "too long is asked again", input: strings.Repeat("a", 25) + "\nBoss\n", want: "Boss", said: "24"},
		{name: "agent-net marker is refused", input: "Atlas [agent-net\nBoss\n", want: "Boss", said: "agent-net"},
		{name: "cross session marker is refused", input: "Atlas <cross-session\nBoss\n", want: "Boss", said: "cross-session"},
		{name: "control characters are refused", input: "At\tlas\nBoss\n", want: "Boss", said: "printable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			store := &fakeTitleStore{title: protocol.CTOTitle{Title: protocol.DefaultCTOTitle}}
			r := &runner{Options: Options{Out: &out, Gateway: true}, interactive: true, titleStore: store}
			old := openTTY
			openTTY = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(tc.input)), nil }
			t.Cleanup(func() { openTTY = old })

			r.askCTOTitle(context.Background())

			if len(store.saved) != 1 || store.saved[0] != tc.want {
				t.Fatalf("saved %v; want %q\n%s", store.saved, tc.want, out.String())
			}
			if tc.said != "" && !strings.Contains(out.String(), tc.said) {
				t.Fatalf("output does not explain the rule %q:\n%s", tc.said, out.String())
			}
		})
	}
}
