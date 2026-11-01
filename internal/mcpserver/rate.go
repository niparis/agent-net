package mcpserver

// The agent_rate input. The tool is an alias of agent_worker_rate; the old
// arguments and the old rate answer are kept for one release.

type rateIn struct {
	Worker   string `json:"worker" jsonschema:"the finished worker's session ID, as its end notice names it"`
	Kind     string `json:"kind,omitempty" jsonschema:"rating (default) for a 1 to 5 score, or defect_found_later for a defect found after the hand-back"`
	Score    int    `json:"score,omitempty" jsonschema:"1 to 5 for the result; leave it out for a defect_found_later report"`
	Severity int    `json:"severity,omitempty" jsonschema:"1 to 4, required on a defect_found_later report: 1 cosmetic or test-only, caught before release; 2 a visible annoyance or a latent performance problem that had not hit the owner yet; 3 a broken feature or a real performance regression in a release; 4 an outage, data loss or a security problem"`
	Note     string `json:"note,omitempty" jsonschema:"one optional line on why, or the one-line defect; it is cut to one short line"`
}
