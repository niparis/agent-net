package tui

import "context"

// outcomeResolver is the operator call that decides a request outcome.
type outcomeResolver interface {
	ResolveOutcome(context.Context, string, string) error
}
