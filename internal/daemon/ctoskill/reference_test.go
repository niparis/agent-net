package ctoskill

import (
	"bytes"
	"flag"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/settings"
)

var update = flag.Bool("update", false, "rewrite generated skill files")

// TestReferenceMatchesCode fails when a code list or default changed and
// reference.md was not regenerated. Run go generate ./internal/daemon/ctoskill
// (or this test with -update) to rewrite it.
func TestReferenceMatchesCode(t *testing.T) {
	if *update {
		if err := Generate(); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := File("reference.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := Reference(); string(got) != want {
		t.Fatalf("reference.md is out of date; run go generate ./internal/daemon/ctoskill\n--- want\n%s", want)
	}
}

// TestReferenceCoversEverySetting is the currency check for the recipe: every
// key has a row in reference.md or a reason on the internal list, and every
// key has a setup tier.
func TestReferenceCoversEverySetting(t *testing.T) {
	reference := Reference()
	definitions := settings.Standard().Definitions
	tiers := map[int]int{}
	covered, internal := 0, 0
	for _, def := range definitions {
		tier, ok := TierFor(def.Key)
		if !ok {
			t.Errorf("%s has no tier", def.Key)
		} else {
			tiers[tier]++
		}
		if reason, ok := internalKeys[def.Key]; ok {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s is on the internal list without a reason", def.Key)
			}
			internal++
			continue
		}
		if !strings.Contains(reference, "`"+def.Key+"`") {
			t.Errorf("%s is neither in reference.md nor on the internal list", def.Key)
			continue
		}
		covered++
	}
	for key := range internalKeys {
		if _, ok := settings.DefinitionFor(key); !ok {
			t.Errorf("internal list names %s, which is not a setting", key)
		}
	}
	for key := range tierKeys {
		if _, ok := settings.DefinitionFor(key); !ok {
			t.Errorf("tier map names %s, which is not a setting", key)
		}
	}
	t.Logf("keys covered: %d, keys on the internal list: %d, tier 1: %d, tier 2: %d, tier 3: %d", covered, internal, tiers[TierOwner], tiers[TierApplicable], tiers[TierDefault])
}

// TestSkillNamesRealSettings guards the skill text against a key name that
// standard.yaml does not accept, and keeps the three part 2 edits in place.
func TestSkillNamesRealSettings(t *testing.T) {
	skill, err := File("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## What matters: guide on tier 1, default the rest",
		"16. **Auto compaction**",
		"sessions.idle_compaction",
		"sessions.idle_compaction_min_tokens",
		"sessions.stall_timeout",
		"sessions.cache_window_seconds",
		"delivery.caller_review_and_merge",
		"delivery.handback_check",
		"delivery.save_unfinished",
		"delivery.pipeline.merge.requires",
		"delivery.pipeline.merge.approver",
		"delivery.pipeline.deploy.approver",
		"delivery.contract.<step>",
		"delivery.worker_branch_prefix",
		"workers.max_per_machine",
		"workers.brief_approval",
		"workers.start_gap",
		"workers.caller_gone_grace",
		"messages.inbox_limit",
		"chief.brief_time",
		"picker.*",
		"guidance.*",
	} {
		if !bytes.Contains(skill, []byte(want)) {
			t.Errorf("SKILL.md does not mention %s", want)
		}
	}
	for _, key := range []string{
		"sessions.idle_compaction",
		"sessions.idle_compaction_min_tokens",
		"sessions.stall_timeout",
		"sessions.cache_window_seconds",
		"delivery.caller_review_and_merge",
		"delivery.handback_check",
		"delivery.save_unfinished",
		"delivery.pipeline.merge.requires",
		"delivery.pipeline.merge.approver",
		"delivery.pipeline.deploy.approver",
		"delivery.worker_branch_prefix",
		"workers.max_per_machine",
		"workers.brief_approval",
		"workers.start_gap",
		"workers.caller_gone_grace",
		"messages.inbox_limit",
		"chief.brief_time",
	} {
		if _, ok := settings.DefinitionFor(key); !ok {
			t.Errorf("SKILL.md names %s, which is not a setting", key)
		}
	}
}
