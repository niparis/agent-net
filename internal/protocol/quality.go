package protocol

// QualityAnalysis is a gateway-cached additive fit. Counts are raw tasks;
// weak ratings carry half weight. Effects below five tasks are tentative.
type QualityAnalysis struct {
	WeightingEnabled bool             `json:"weighting_enabled"`
	Status           string           `json:"status"`
	Ratings          int              `json:"ratings"`
	Average          float64          `json:"average"`
	ReworkAverage    float64          `json:"rework_average"`
	Effects          []QualityEffect  `json:"effects,omitempty"`
	Summary          []QualityFactor  `json:"summary,omitempty"`
	Levels           []QualityLevel   `json:"levels,omitempty"`
	Agents           []QualityAgent   `json:"agents,omitempty"`
	Profiles         []ProfileQuality `json:"profiles,omitempty"`
}
type QualityEffect struct {
	Kind    string  `json:"kind"`
	Name    string  `json:"name"`
	Quality float64 `json:"quality"`
	Rework  float64 `json:"rework"`
	Ratings int     `json:"ratings"`
	Runs    int     `json:"runs"`
}

// QualityFactor is the size of one factor's effect, largest first. The TUI
// reads it for the "what matters most" part of the ratings line.
type QualityFactor struct {
	Kind string  `json:"kind"`
	Size float64 `json:"size"`
}

// QualityFamily is one model family's work at one requested difficulty. The
// gateway folds harnesses and efforts together and sums one task per worker
// session.
type QualityFamily struct {
	Family             string  `json:"family"`
	Tasks              int     `json:"tasks"`
	Fixes              int     `json:"fixes"`
	Continues          int     `json:"continues"`
	FollowUps          float64 `json:"follow_ups"`
	LateDefects        int     `json:"late_defects"`
	LateDefectSeverity float64 `json:"late_defect_severity"`
	Headache           float64 `json:"headache"`
}

// QualityLevel is one requested difficulty's table. Difficulty is a level
// name or "unknown".
type QualityLevel struct {
	Difficulty string          `json:"difficulty"`
	Tasks      int             `json:"tasks"`
	Families   []QualityFamily `json:"families,omitempty"`
}

// QualityAgent is one caller's level tables. The TUI selector shows Name.
type QualityAgent struct {
	Caller string         `json:"caller"`
	Name   string         `json:"name,omitempty"`
	Levels []QualityLevel `json:"levels,omitempty"`
}

type ProfileQuality struct {
	Weights    map[string]float64 `json:"weights,omitempty"`
	Name       string             `json:"name"`
	Quality    float64            `json:"quality"`
	Ratings    int                `json:"ratings"`
	Runs       int                `json:"runs"`
	Unexpected bool               `json:"unexpected,omitempty"`
}

const (
	// QualityMinRatings is the least a registered profile needs before its
	// own average rating and the fitted prediction are shown.
	QualityMinRatings = 5
	// QualityAnalysisMinRatings is the least the fit needs before the TUI
	// shows the "what matters most" part of the ratings line.
	QualityAnalysisMinRatings = 20
)

const OpQualityGet = "quality_get"
const OpQualitySet = "quality_set"

// QualitySettings is owner controlled and defaults to off.
type QualitySettings struct {
	Enabled bool `json:"enabled"`
}
