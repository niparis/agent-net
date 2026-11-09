package daemon

import (
	"context"
	"testing"
)

// The owner's undo and marks (decision 0111, part 2) are recorded as the owner,
// or as the CTO when the CTO runs them for the owner.
func TestOperatorActorFollowsTheCaller(t *testing.T) {
	if got := operatorActor(context.Background()); got != "owner" {
		t.Fatalf("owner call acts as %q", got)
	}
	ctx := context.WithValue(context.Background(), proposalActorKey{}, "cto")
	if got := operatorActor(ctx); got != "cto" {
		t.Fatalf("CTO call acts as %q", got)
	}
}
