package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/phrasing"
	"github.com/niparis/agent-net/internal/protocol"
)

type phraseFake struct {
	req    []protocol.WorkerStartRequest
	report string
	err    error
}

func (f *phraseFake) Run(_ context.Context, r protocol.WorkerStartRequest) (string, error) {
	f.req = append(f.req, r)
	return f.report, f.err
}
func TestLearnPhraseBeforeApply(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	fake := &phraseFake{report: `{"backups":"daily backup operations","ops for backups":"ops for backup operations"}`}
	s.phraseCleaner = phrasing.New(fake)
	if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(2 * time.Minute)
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	if len(fake.req) != 1 || fake.req[0].Model != "" || fake.req[0].Harness != "" || fake.req[0].Difficulty != "trivial" {
		t.Fatalf("chooser request: %+v", fake.req)
	}
	v := learnedRead(t, s, false)
	if learnedFind(v, "ops", "accepts", "daily backup operations") == nil || learnedFind(v, "app", "needs", "ops for backup operations") == nil {
		t.Fatalf("phrases: %+v", v)
	}
	h, err := s.CharterHistory("ops", 10)
	if err != nil || len(h) != 1 || h[0].NewValue != "daily backup operations" {
		t.Fatalf("history %+v err %v", h, err)
	}
}
func TestLearnPhraseRejectedRawIsNotReoffered(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	fake := &phraseFake{report: `{"backups":"daily backup operations","ops for backups":"ops for backup operations"}`}
	s.phraseCleaner = phrasing.New(fake)
	if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(2 * time.Minute)
	if err := s.SetCharter("ops", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	p := learnedFind(learnedRead(t, s, false), "ops", "accepts", "daily backup operations")
	if p == nil {
		t.Fatal("missing suggestion")
	}
	if err := s.RejectLearnedSuggestion(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	if learnedFind(learnedRead(t, s, false), "ops", "accepts", "backups") != nil {
		t.Fatal("raw rejected phrase reoffered")
	}
}

type blockingPhraseWorker struct {
	started chan struct{}
	release chan struct{}
}

func (f *blockingPhraseWorker) Run(ctx context.Context, _ protocol.WorkerStartRequest) (string, error) {
	close(f.started)
	select {
	case <-f.release:
		return `{"backups":"daily backup operations","ops for backups":"ops for backup operations"}`, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func TestLearnPhraseHiddenUntilComplete(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	fake := &blockingPhraseWorker{started: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-fake.release:
		default:
			close(fake.release)
		}
	}()
	s.phraseCleaner = phrasing.New(fake)
	if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(2 * time.Minute)
	done := make(chan error, 1)
	go func() { done <- s.ProcessLearned() }()
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	if v := learnedRead(t, s, true); len(v) != 0 {
		t.Fatalf("unfinished phrases shown: %+v", v)
	}
	close(fake.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLearnPhraseLimiterPersistsAndDebounces(t *testing.T) {
	s, c := newTestStore(t)
	l := phraseLimiter{s}
	for i := 0; i < 6; i++ {
		if ok, err := l.Reserve(c.t); err != nil || !ok {
			t.Fatalf("reservation %d: %t %v", i, ok, err)
		}
		if ok, err := l.Reserve(c.t.Add(time.Minute)); err != nil || ok {
			t.Fatalf("window %d: %t %v", i, ok, err)
		}
		c.t = c.t.Add(2 * time.Minute)
	}
	if ok, err := (phraseLimiter{s}).Reserve(c.t); err != nil || ok {
		t.Fatalf("hourly cap: %t %v", ok, err)
	}
	c.t = c.t.Add(time.Hour)
	if ok, err := l.Reserve(c.t); err != nil || !ok {
		t.Fatalf("expired cap: %t %v", ok, err)
	}
}

func TestLearnPhraseKeepsRawRestriction(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	fake := &phraseFake{report: `{"only backups":"backup operations","ops for only backups":"ops for backup operations"}`}
	s.phraseCleaner = phrasing.New(fake)
	if err := s.RememberOwnerPrompt(a, "ops: only backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(2 * time.Minute)
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	for _, p := range v {
		if p.Status != "pending" || p.Severity != "major" {
			t.Fatalf("lost restriction: %+v", v)
		}
	}
}

func TestLearnPhraseMergesNewEvidenceByRawPhrase(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	fake := &phraseFake{report: `{"backups":"daily backup operations","ops for backups":"ops for backup operations"}`}
	s.phraseCleaner = phrasing.New(fake)
	if err := s.SetCharter("ops", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(2 * time.Minute)
	if err := s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := send(s, a, b, "", ""); err != nil {
		t.Fatal(err)
	}
	v := learnedRead(t, s, false)
	n := 0
	for _, p := range v {
		if p.Node == "ops" && p.Field == "accepts" && p.Status == "pending" {
			n++
			if len(p.Evidence) != 2 {
				t.Fatalf("evidence: %+v", v)
			}
		}
	}
	if n != 1 {
		t.Fatalf("duplicate: %+v", v)
	}
}

func TestLearnPhraseDoesNotRepeatAppliedItems(t *testing.T) {
	s, c, a, b := learnedProjects(t)
	fake := &phraseFake{report: `{"backups":"daily backup operations","ops for backups":"ops for backup operations"}`}
	s.phraseCleaner = phrasing.New(fake)
	for i := 0; i < 2; i++ {
		if err := s.RememberOwnerPrompt(a, "ops: backups are his job"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := send(s, a, b, "", ""); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(2 * time.Minute)
		if err := s.ProcessLearned(); err != nil {
			t.Fatal(err)
		}
	}
	v := learnedRead(t, s, false)
	if len(fake.req) != 1 || len(v) != 2 {
		t.Fatalf("repeat call=%d suggestions=%+v", len(fake.req), v)
	}
}
