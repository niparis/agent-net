package model

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func gateFacts() Facts {
	return Facts{
		Now:      time.Unix(1_800_000_000, 0),
		Hostname: "box",
		Harnesses: map[string]HarnessFact{
			"opencode": {Hostname: "box", Installed: true, SignedIn: "yes"},
			"codex":    {Hostname: "box", Installed: true, SignedIn: "yes"},
		},
	}
}

// A provider limit that a live session reported is a locked check: the gate
// skips that provider and says why, even with every optional check off.
func TestGateSkipsALiveSessionLimit(t *testing.T) {
	facts := gateFacts()
	facts.Limits = map[string]Limit{
		"opencode-go": {Until: facts.Now.Add(time.Hour).Unix(), Reason: "monthly limit"},
	}
	list := []Candidate{
		{Name: "go", Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash"},
		{Name: "plain", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash"},
	}
	got, skips, err := Gate(list, Checks{}, facts)
	if err != nil {
		t.Fatalf("gate refused a usable provider: %v", err)
	}
	if got.Candidate().Name != "plain" {
		t.Fatalf("chose %q, want the provider without a reported limit", got.Candidate().Name)
	}
	if len(skips) != 1 || skips[0].Candidate.Name != "go" {
		t.Fatalf("skips: %+v", skips)
	}
	for _, want := range []string{"opencode-go", "limited until", "monthly limit"} {
		if !strings.Contains(skips[0].Reason, want) {
			t.Errorf("skip reason %q lacks %q", skips[0].Reason, want)
		}
	}
}

// The four locked checks cannot be switched off: Checks has no fields for
// them, and an empty Checks still refuses each kind of broken candidate.
func TestGateLockedChecksHaveNoSwitch(t *testing.T) {
	tests := []struct {
		name   string
		facts  func() Facts
		broken Candidate
		good   Candidate
		want   string
	}{
		{
			name:   "not installed",
			facts:  gateFacts,
			broken: Candidate{Name: "missing", Harness: "claude", Model: "claude-opus-5-5"},
			want:   "not installed",
		},
		{
			name: "not signed in",
			facts: func() Facts {
				facts := gateFacts()
				facts.Harnesses["codex"] = HarnessFact{Hostname: "box", Installed: true, SignedIn: "no"}
				return facts
			},
			broken: Candidate{Name: "signed-out", Harness: "codex", Model: "gpt-6.1-terra"},
			good:   Candidate{Name: "good", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash"},
			want:   "not signed in",
		},
		{
			name: "auth blocked",
			facts: func() Facts {
				facts := gateFacts()
				facts.AuthBlocked = map[string]bool{"opencode\x00auth/bad": true}
				return facts
			},
			broken: Candidate{Name: "bad auth", Harness: "opencode", Model: "auth/bad"},
			want:   "authentication failed",
		},
		{
			name: "exhausted",
			facts: func() Facts {
				facts := gateFacts()
				facts.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{
					Provider: "deepseek", Windows: []protocol.QuotaWindow{{Kind: "weekly", Exhausted: true}},
				}}}
				return facts
			},
			broken: Candidate{Name: "empty", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash"},
			want:   "exhausted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			good := tt.good
			if good.Name == "" {
				good = Candidate{Name: "good", Harness: "codex", Model: "gpt-6.1-terra"}
			}
			got, skips, err := Gate([]Candidate{tt.broken, good}, Checks{}, tt.facts())
			if err != nil || got.Candidate().Name != good.Name {
				t.Fatalf("got %+v, skips %+v, err %v", got, skips, err)
			}
			if len(skips) != 1 || !strings.Contains(skips[0].Reason, tt.want) {
				t.Fatalf("skips: %+v, want a reason containing %q", skips, tt.want)
			}
		})
	}
}

func TestGateOptionalChecks(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	good := Candidate{Name: "good", Harness: "codex", Model: "gpt-6.1-terra"}
	tests := []struct {
		name   string
		checks Checks
		facts  func() Facts
		broken Candidate
		want   string
	}{
		{
			name:   "floor",
			checks: Checks{Floor: true},
			facts: func() Facts {
				facts := gateFacts()
				facts.Floors = protocol.WorkerFloors{Floors: map[string]int{"deepseek": 40}}
				facts.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{
					Provider: "deepseek", Windows: []protocol.QuotaWindow{{Kind: "weekly", PercentUsed: 70}},
				}}}
				return facts
			},
			broken: Candidate{Name: "floor", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash"},
			want:   "worker floor",
		},
		{
			name:   "90 percent",
			checks: Checks{Block90: true},
			facts: func() Facts {
				facts := gateFacts()
				facts.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{
					Provider: "deepseek", Windows: []protocol.QuotaWindow{{Kind: "weekly", PercentUsed: 95, ResetsAt: now.Add(24 * time.Hour).Unix()}},
				}}}
				return facts
			},
			broken: Candidate{Name: "hot", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash"},
			want:   "95% used",
		},
		{
			name:   "cooling",
			checks: Checks{Cooling: true},
			facts: func() Facts {
				facts := gateFacts()
				facts.Cooling = map[string]CoolingFact{"opencode\x00deepseek/deepseek-v4.1-flash": {Failures: 2, Until: now.Add(10 * time.Minute)}}
				return facts
			},
			broken: Candidate{Name: "cool", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash"},
			want:   "cooling down",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, skips, err := Gate([]Candidate{tt.broken, good}, Checks{}, tt.facts()); err != nil || len(skips) != 0 {
				t.Fatalf("check off: skips %+v, err %v", skips, err)
			}
			got, skips, err := Gate([]Candidate{tt.broken, good}, tt.checks, tt.facts())
			if err != nil || got.Candidate().Name != good.Name {
				t.Fatalf("check on: got %+v, skips %+v, err %v", got, skips, err)
			}
			if len(skips) != 1 || !strings.Contains(skips[0].Reason, tt.want) {
				t.Fatalf("skips: %+v, want a reason containing %q", skips, tt.want)
			}
		})
	}
}

func TestGateRefusesWhenEveryCandidateIsSkipped(t *testing.T) {
	facts := gateFacts()
	list := []Candidate{{Name: "go", Harness: "opencode", Model: "opencode-go/deepseek-v4.1-flash"}}
	facts.Limits = map[string]Limit{"opencode-go": {Until: facts.Now.Add(time.Hour).Unix()}}
	_, skips, err := Gate(list, Checks{}, facts)
	if err == nil {
		t.Fatal("the only candidate is limited: the gate must refuse")
	}
	if len(skips) != 1 || !strings.Contains(err.Error(), "opencode-go") {
		t.Fatalf("skips %+v, err %v", skips, err)
	}
}
