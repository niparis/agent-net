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
type ProfileQuality struct {
	Weights    map[string]float64 `json:"weights,omitempty"`
	Name       string             `json:"name"`
	Quality    float64            `json:"quality"`
	Ratings    int                `json:"ratings"`
	Runs       int                `json:"runs"`
	Unexpected bool               `json:"unexpected,omitempty"`
}

const OpQualityGet = "quality_get"
const OpQualitySet = "quality_set"

// QualitySettings is owner controlled and defaults to off.
type QualitySettings struct {
	Enabled bool `json:"enabled"`
}
