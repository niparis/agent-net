package ctoskill

import "strings"

// Setup tiers say how much a setting matters when the owner sets up a repo,
// session or team.
const (
	// TierOwner is decided with the owner: a wrong value changes who does what.
	TierOwner = 1
	// TierApplicable is raised in one line when the repo's facts make it apply.
	TierApplicable = 2
	// TierDefault is left at its default and only mentioned if asked.
	TierDefault = 3
)

// tierKeys names keys whose tier differs from the tier of their prefix.
var tierKeys = map[string]int{
	"max_agents": TierApplicable,

	"charter.judge_consumers": TierApplicable,

	"delivery.tests_before_merge":      TierOwner,
	"delivery.merge_authority":         TierOwner,
	"delivery.ci_mode":                 TierOwner,
	"delivery.deploy_rule":             TierOwner,
	"delivery.caller_review_and_merge": TierApplicable,
	"delivery.handback_check":          TierApplicable,
	"delivery.handback_check_timeout":  TierDefault,

	"workers.sandbox_level":     TierOwner,
	"workers.isolation_minimum": TierApplicable,
}

// tierPrefixes gives the tier of every key under a prefix. The longest match
// wins, so a pipeline key is tier 1 even though delivery keys default lower.
var tierPrefixes = []struct {
	prefix string
	tier   int
}{
	{"delivery.pipeline.", TierOwner},
	{"delivery.", TierDefault},
	{"workers.", TierDefault},
	{"sessions.", TierDefault},
	{"charter.", TierDefault},
	{"messages.", TierDefault},
	{"cto.", TierDefault},
	{"loop.", TierDefault},
	{"cleanup.", TierDefault},
	{"files.", TierDefault},
	{"picker.", TierDefault},
	{"guidance.", TierDefault},
	{"merger.", TierDefault},
	{"chief.", TierDefault},
}

// TierFor returns the setup tier of a setting. ok is false when the tiers do
// not name it, which the currency test treats as a failure.
func TierFor(key string) (tier int, ok bool) {
	if tier, ok := tierKeys[key]; ok {
		return tier, true
	}
	longest := ""
	for _, candidate := range tierPrefixes {
		if strings.HasPrefix(key, candidate.prefix) && len(candidate.prefix) > len(longest) {
			longest, tier, ok = candidate.prefix, candidate.tier, true
		}
	}
	return tier, ok
}

// internalKeys are settings that reference.md does not list, each with the
// reason. The list is empty while every setting has a generated row.
var internalKeys = map[string]string{}

func keyPrefix(key string) string {
	if i := strings.IndexByte(key, '.'); i > 0 {
		return key[:i]
	}
	return ""
}
