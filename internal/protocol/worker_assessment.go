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

// DifficultyLine is the one-line score profile: the six scores, D and label.
func (a WorkerAssessment) DifficultyLine() string {
	return fmt.Sprintf("R%d S%d C%d U%d V%d K%d · D %.2f · %s", a.Reasoning, a.Scope, a.Coupling, a.Uncertainty, a.Verification, a.Risk, a.D, a.Label)
}

func (a WorkerAssessment) Summary(picker string) string {
	if a.Error != "" {
		return "no assessment (" + a.Error + ")"
	}
	return a.DifficultyLine() + " (" + picker + ": " + a.PickerLabel + ")"
}

// WorkerOwnerLine is the line the caller passes on to its user when it starts
// a worker: the model and the difficulty the judge gave. Empty when the start
// was not assessed (a continued worker keeps its earlier level).
func WorkerOwnerLine(a *WorkerAssessment, model string) string {
	if a == nil {
		return ""
	}
	if model == "" {
		model = "the chosen model"
	}
	level := WorkerLevel(a)
	switch {
	case a.Error != "":
		return fmt.Sprintf("Tell your user: worker on %s, difficulty %s (no assessment: %s).", model, level, a.Error)
	case level != a.Label:
		return fmt.Sprintf("Tell your user: worker on %s, difficulty %s (the judge said %s, D %.2f, with low confidence).", model, level, a.Label, a.D)
	}
	return fmt.Sprintf("Tell your user: worker on %s, difficulty %s (D %.2f; R%d S%d C%d U%d V%d K%d).", model, level, a.D, a.Reasoning, a.Scope, a.Coupling, a.Uncertainty, a.Verification, a.Risk)
}

// WorkerLevel is the level the worker chooser uses: the judge's label, or
// normal when the judge is missing, failed, timed out or has low confidence.
func WorkerLevel(a *WorkerAssessment) string {
	if a == nil || a.Error != "" || a.Label == "" || a.Confidence == "low" {
		return "normal"
	}
	if _, err := DifficultyFit(0, a.Label, nil); err != nil {
		return "normal"
	}
	return a.Label
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
