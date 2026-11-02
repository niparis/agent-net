package protocol

import "fmt"

// WorkerAssessment is one judge result for a worker run. RunID identifies the
// start so a retried gateway write cannot create a second row.
type WorkerAssessment struct {
	RunID        string            `json:"run_id"`
	SessionID    string            `json:"session_id"`
	StartedAt    int64             `json:"started_at"`
	AssessedAt   int64             `json:"assessed_at"`
	Reasoning    int               `json:"reasoning"`
	Scope        int               `json:"scope"`
	Coupling     int               `json:"coupling"`
	Uncertainty  int               `json:"uncertainty"`
	Verification int               `json:"verification"`
	Risk         int               `json:"risk"`
	D            float64           `json:"d"`
	Label        string            `json:"label"`
	Bottleneck   string            `json:"bottleneck,omitempty"`
	Confidence   string            `json:"confidence,omitempty"`
	RulesVersion string            `json:"rules_version"`
	JudgeHarness string            `json:"judge_harness"`
	JudgeModel   string            `json:"judge_model"`
	Reasons      map[string]string `json:"reasons,omitempty"`
	PickerLabel  string            `json:"picker_label"`
	Error        string            `json:"error,omitempty"`
}

func (a WorkerAssessment) Summary(picker string) string {
	if a.Error != "" {
		return "no assessment (" + a.Error + ")"
	}
	return fmt.Sprintf("R%d S%d C%d U%d V%d K%d · D %.2f · %s (%s: %s)", a.Reasoning, a.Scope, a.Coupling, a.Uncertainty, a.Verification, a.Risk, a.D, a.Label, picker, a.PickerLabel)
}

// WorkerAssessmentNotice is a non-final notice, separate from a hand-back.
func WorkerAssessmentNotice(session string, a WorkerAssessment) string {
	body := "[agent-net worker notice]\nstatus: difficulty\nworker: " + session + "\ndifficulty: " + a.Summary("picker used")
	if a.Error == "" && a.Bottleneck != "" {
		body += "\nbottleneck: " + a.Bottleneck
	}
	if a.Error == "" && a.Confidence != "" {
		body += "\nconfidence: " + a.Confidence
	}
	return body
}
