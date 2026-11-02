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
	Areas            []QualityFamily  `json:"areas,omitempty"`
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
// gateway folds harnesses and efforts together and sums one row per task.
type QualityFamily struct {
	Family             string   `json:"family"`
	NeededHelp         float64  `json:"needed_help"`
	WorstSeverity      int      `json:"worst_severity,omitempty"`
	AvgRating          *float64 `json:"avg_rating,omitempty"`
	Tasks              int      `json:"tasks"`
	Fixes              int      `json:"fixes"`
	Continues          int      `json:"continues"`
	Midrun             int      `json:"midrun"`
	FollowUps          float64  `json:"follow_ups"`
	LateDefects        int      `json:"late_defects"`
	LateDefectSeverity float64  `json:"late_defect_severity"`
	Headache           float64  `json:"headache"`
	// TasksWeek counts the tasks that ended in the last 7 days. The Quality
	// cards list the most used models first.
	TasksWeek int `json:"tasks_week,omitempty"`
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
	Areas  []QualityFamily `json:"areas,omitempty"`
	Caller string          `json:"caller"`
	Name   string          `json:"name,omitempty"`
	Levels []QualityLevel  `json:"levels,omitempty"`
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

// Orchestrator minutes one task's rework costs. The owner set these costs on
// 2026-11-01: a continue costs 2 minutes, a fix 10 minutes, and a late defect
// costs 10, 30, 60 or 180 minutes by severity 1 to 4.
const (
	QualityContinueMinutes        = 2
	QualityFixMinutes             = 10
	QualityDefectMinutesSeverity1 = 10
	QualityDefectMinutesSeverity2 = 30
	QualityDefectMinutesSeverity3 = 60
	QualityDefectMinutesSeverity4 = 180
)

// DefectMinutes maps a late-defect severity to its orchestrator minutes.
// Unknown severities add nothing; the gateway validates them on the way in.
func DefectMinutes(severity int) float64 {
	switch severity {
	case 1:
		return QualityDefectMinutesSeverity1
	case 2:
		return QualityDefectMinutesSeverity2
	case 3:
		return QualityDefectMinutesSeverity3
	case 4:
		return QualityDefectMinutesSeverity4
	default:
		return 0
	}
}

const OpQualityGet = "quality_get"
const OpQualitySet = "quality_set"

// QualitySettings is owner controlled and defaults to off.
type QualitySettings struct {
	Enabled bool `json:"enabled"`
}
