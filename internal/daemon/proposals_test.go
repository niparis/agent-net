package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestProposalLifecycle(t *testing.T) {
	s, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	now := time.Unix(100000, 0)
	var calls []string
	p := proposalRunner{state: s, now: func() time.Time { return now }, validate: func(_ context.Context, call proposalCall) error {
		if call.Tool == "bad" {
			return errors.New("bad ID")
		}
		return nil
	}, run: func(_ context.Context, call proposalCall, actor string) (json.RawMessage, error) {
		calls = append(calls, actor)
		if call.Tool == "fail" {
			return nil, errors.New("failed")
		}
		return json.RawMessage(`{"secret":"result-marker"}`), nil
	}}
	ctx := context.Background()
	call := func(name string) proposalCall {
		return proposalCall{Tool: name, Action: "set", Args: json.RawMessage(`{"id":"one"}`)}
	}
	if _, err := p.propose(ctx, call("bad"), ""); err == nil || !strings.Contains(err.Error(), "bad ID") {
		t.Fatalf("bad ID: %v", err)
	}
	id, err := p.propose(ctx, call("ok"), "reason")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.propose(ctx, call("ok"), "another reason"); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := p.decide(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := p.decide(ctx, id, true); err == nil {
		t.Fatal("second approval accepted")
	}
	if len(calls) != 1 || calls[0] != "owner (CTO proposal "+id+")" {
		t.Fatalf("calls: %v", calls)
	}
	items, err := p.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || strings.Contains(items[0].Note, "result-marker") || !strings.Contains(string(items[0].Result), "result-marker") {
		t.Fatalf("result isolation: %+v", items)
	}
	id, err = p.propose(ctx, call("decline"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.decide(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	_, err = p.propose(ctx, call("expiry"), "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	items, err = p.list()
	if err != nil {
		t.Fatal(err)
	}
	if items[len(items)-1].Status != "expired" || len(calls) != 1 {
		t.Fatalf("expiry: %+v calls %v", items, calls)
	}
}

func TestProposalLimitAndApproveAllStops(t *testing.T) {
	s, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	var calls []string
	p := proposalRunner{state: s, now: time.Now, validate: func(_ context.Context, _ proposalCall) error { return nil }, run: func(_ context.Context, c proposalCall, _ string) (json.RawMessage, error) {
		calls = append(calls, c.Tool)
		if c.Tool == "fail" {
			return nil, errors.New("failed")
		}
		return nil, nil
	}}
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("tool-%d", i)
		if i == 1 {
			name = "fail"
		}
		if _, err := p.propose(ctx, proposalCall{Tool: name, Args: json.RawMessage(`{}`)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.propose(ctx, proposalCall{Tool: "overflow", Args: json.RawMessage(`{}`)}, ""); err == nil {
		t.Fatal("21st accepted")
	}
	if err := p.approveAll(ctx); err == nil {
		t.Fatal("failure ignored")
	}
	if strings.Join(calls, ",") != "tool-0,fail" {
		t.Fatalf("calls: %v", calls)
	}
}
