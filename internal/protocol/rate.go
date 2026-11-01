package protocol

import (
	"strings"
	"time"
	"unicode"
)

// Difficulty calibration, part 4: the caller's satisfaction rating of a
// finished worker (docs/design/difficulty-calibration.md, section 7b).
//
// agent-net asks now and then. The ask is one line in the worker's end notice,
// and the caller answers with one agent_rate call. A rating never changes a
// model choice in this part; it is stored for the analysis of section 7c.

const (
	// RateScaleMin and RateScaleMax are the score a caller may give.
	RateScaleMin = 1
	RateScaleMax = 5
	// RateEvery asks for every Nth finished worker of a caller.
	RateEvery = 5
	// RateLittleRatings is the rating count below which a model and effort
	// still counts as little rated.
	RateLittleRatings = 5
	// RateFirstTasks is how many finished tasks of a little-rated model and
	// effort are asked about.
	RateFirstTasks = 3
	// RateNoteMax is the bytes kept from the caller's one-line note.
	RateNoteMax = 200
	// RateKindRating is agent_rate's default: a satisfaction score.
	RateKindRating = "rating"
	// RateKindDefectLater is a defect found after the hand-back (or after
	// merge). Each report is one defect and counts as one fix on the task.
	RateKindDefectLater = "defect_found_later"
)

// RateRequest is agent_rate: one caller's rating of one finished worker, or
// one defect found later on that worker's task.
type RateRequest struct {
	// Worker is the session ID of the finished worker, as the end notice names
	// it.
	Worker string `json:"worker"`
	// Kind is RateKindRating (the default, also when empty) or
	// RateKindDefectLater.
	Kind string `json:"kind,omitempty"`
	// Score is 1 to 5 for the result. A defect report has no score.
	Score int `json:"score,omitempty"`
	// Note is one optional line on why. A defect report uses it as the
	// one-line description of the defect.
	Note string `json:"note,omitempty"`
}

// DefectRequest is the owner's OpWorkerDefect: one defect found later on a
// worker's task. The owner path never takes a score; the caller's agent_rate
// path takes the same shape with kind defect_found_later.
type DefectRequest struct {
	Worker string `json:"worker"`
	Note   string `json:"note,omitempty"`
}

// RateFrame is "rate.add", daemon to gateway: the rating and the session that
// gives it. The gateway checks the session belongs to the sending node.
type RateFrame struct {
	RateRequest
	Rater string `json:"rater_session"`
}

// RateResult answers agent_rate. Accepted is false when the worker was not
// started by the caller, was already rated, or is unknown; Reason says why.
type RateResult struct {
	Accepted   bool   `json:"accepted"`
	Reason     string `json:"reason,omitempty"`
	Worker     string `json:"worker,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Score      int    `json:"score,omitempty"`
	Model      string `json:"model,omitempty"`
	Harness    string `json:"harness,omitempty"`
	Effort     string `json:"effort,omitempty"`
	Difficulty string `json:"difficulty,omitempty"`
	At         int64  `json:"at,omitempty"`
}

// ValidRateScore tells whether a score is on the 1 to 5 scale.
func ValidRateScore(score int) bool { return score >= RateScaleMin && score <= RateScaleMax }

// CleanRateNote makes a rating note one short, printable line: control
// characters become spaces, whitespace collapses, and the text is cut to
// RateNoteMax bytes. An empty note stays empty.
func CleanRateNote(note string) string {
	note = strings.ToValidUTF8(note, "")
	note = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, note)
	note = strings.Join(strings.Fields(note), " ")
	if len(note) > RateNoteMax {
		note = note[:RateNoteMax]
		note = strings.ToValidUTF8(note, "")
		note = strings.TrimSpace(note)
	}
	return note
}

// FormatRateAsk is the one line agent-net adds to a worker's end notice. It
// names the worker session and the scale.
func FormatRateAsk(worker, model, difficulty string, run time.Duration) string {
	line := "rate worker " + strings.TrimSpace(worker)
	var details []string
	if model = strings.TrimSpace(model); model != "" {
		details = append(details, model)
	}
	if difficulty = strings.TrimSpace(difficulty); difficulty != "" {
		details = append(details, difficulty)
	}
	if run >= time.Minute {
		details = append(details, run.Round(time.Minute).String())
	}
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}
	return line + " with agent_rate: 1 to 5 for the result, and one line on why"
}

// RateOfferFrame is "rate.offer", daemon to gateway: one worker has finished
// and its end notice is about to go out.
type RateOfferFrame struct {
	SessionID string `json:"session_id"`
}

// RateOfferResult says whether the caller should be asked to rate this
// worker, with the facts the ask names.
type RateOfferResult struct {
	Ask        bool   `json:"ask"`
	SessionID  string `json:"session_id,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	Harness    string `json:"harness,omitempty"`
	Difficulty string `json:"difficulty,omitempty"`
}

// CalibrationRating is one model's rating count in the read-only table, with
// how many defects were reported later on its tasks.
type CalibrationRating struct {
	Model   string `json:"model"`
	Count   int    `json:"count"`
	Defects int    `json:"defects,omitempty"`
}
