package tui

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/niparis/agent-net/internal/tui/palette"
)

type graphOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *graphOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}
func (o *graphOutput) String() string { o.mu.Lock(); defer o.mu.Unlock(); return o.buf.String() }

type graphProfileBarrier struct{}

// Exercise Bubble Tea's actual renderer and color-profile notification, not
// just the string before the runtime downsamples or strips its ANSI colors.
func TestGraphProfilesThroughBubbleTeaRenderer(t *testing.T) {
	defer palette.SetProfile(colorprofile.TrueColor)
	cases := []struct {
		name    string
		profile colorprofile.Profile
		env     []string
		detect  bool
		pixels  bool
	}{
		{"ANSI", colorprofile.ANSI, nil, false, false},
		{"ANSI256", colorprofile.ANSI256, nil, false, false},
		{"NoTTY", colorprofile.NoTTY, nil, false, false},
		{"NO_COLOR", colorprofile.ASCII, []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TTY_FORCE=1", "NO_COLOR=1"}, true, false},
		{"TrueColor", colorprofile.TrueColor, nil, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := idleGraphModel(80, 24)
			m.graphPixel.profile = colorprofile.Unknown
			output := &graphOutput{}
			profileSeen := make(chan colorprofile.Profile, 1)
			barrier := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := []tea.ProgramOption{
				tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(output),
				tea.WithWindowSize(80, 24), tea.WithoutSignals(),
				tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
					switch v := msg.(type) {
					case tea.ColorProfileMsg:
						select {
						case profileSeen <- v.Profile:
						default:
						}
					case graphProfileBarrier:
						select {
						case barrier <- struct{}{}:
						default:
						}
					}
					return msg
				}),
			}
			if tc.detect {
				opts = append(opts, tea.WithEnvironment(tc.env))
			} else {
				opts = append(opts, tea.WithColorProfile(tc.profile))
			}
			program := tea.NewProgram(m, opts...)
			done := make(chan error, 1)
			go func() { _, err := program.Run(); done <- err }()
			select {
			case got := <-profileSeen:
				if got != tc.profile {
					t.Fatalf("profile %v, want %v", got, tc.profile)
				}
			case err := <-done:
				t.Fatalf("program stopped before profile: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("program did not announce the profile")
			}
			go program.Send(graphProfileBarrier{})
			select {
			case <-barrier:
			case <-time.After(5 * time.Second):
				t.Fatal("program did not process the profile")
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				out := output.String()
				if tc.pixels && strings.Contains(out, "\x1b_Ga=T") && strings.Contains(out, "\U0010EEEE") {
					break
				}
				if !tc.pixels && (strings.Contains(out, "\u2800") || strings.ContainsAny(out, "⠁⠂⠄⡀⢀⠈⠐⠠")) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			program.Quit()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("program: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("program did not quit")
			}
			out := output.String()
			if tc.pixels {
				if !strings.Contains(out, "\x1b_Ga=T") || !strings.Contains(out, "\U0010EEEE") || !strings.Contains(out, "38;2;78;69;84") {
					t.Fatal("true-color output did not preserve the image and its placeholder ID")
				}
			} else {
				if strings.Contains(out, "\x1b_Ga=T") || strings.Contains(out, "\U0010EEEE") {
					t.Fatal("non-true-color output contained pixel graphics")
				}
				if !strings.Contains(out, "\u2800") && !strings.ContainsAny(out, "⠁⠂⠄⡀⢀⠈⠐⠠") {
					t.Fatal("non-true-color output did not contain braille")
				}
			}
		})
	}
}
