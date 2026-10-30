package protocol

// WorkerProfile is a named model and harness in the network's worker roster.
type WorkerProfile struct {
	Name    string `json:"name"`
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort,omitempty"`
	AddedBy string `json:"added_by"`
	AddedAt int64  `json:"added_at"`
}

type WorkerProfileRequest struct {
	Name    string `json:"name,omitempty"`
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}
type WorkerProfileFrame struct {
	WorkerProfileRequest
	Requester string `json:"requester"`
}

const (
	OpWorkerProfiles      = "worker_profiles"
	OpWorkerProfileAdd    = "worker_profile_add"
	OpWorkerProfileRemove = "worker_profile_remove"
)
