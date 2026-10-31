package setup

import (
	"os"

	"golang.org/x/term"
)

// setupColor only uses SGR attributes and the 16 basic foreground colors. It
// deliberately does not inspect the terminal palette or send terminal queries.
type setupColor struct {
	enabled bool
}

func setupColorEnabled(stdoutIsTerminal bool) bool {
	return stdoutIsTerminal && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func defaultSetupColor() setupColor {
	return setupColor{enabled: setupColorEnabled(term.IsTerminal(int(os.Stdout.Fd())))}
}

func (c setupColor) wrap(sgr, text string) string {
	if !c.enabled || text == "" {
		return text
	}
	return "\x1b[" + sgr + "m" + text + "\x1b[0m"
}

func (c setupColor) good(text string) string          { return c.wrap("32;1", text) }
func (c setupColor) label(text string) string         { return c.wrap("36", text) }
func (c setupColor) attention(text string) string     { return c.wrap("33", text) }
func (c setupColor) attentionBold(text string) string { return c.wrap("33;1", text) }
func (c setupColor) failure(text string) string       { return c.wrap("31", text) }
func (c setupColor) failureBold(text string) string   { return c.wrap("31;1", text) }
func (c setupColor) faint(text string) string         { return c.wrap("2", text) }
func (c setupColor) heading(text string) string       { return c.wrap("1", text) }
