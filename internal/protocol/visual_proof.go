package protocol

import (
	"regexp"
	"strings"
)

// VisualProof names a transferred frame and the action or state it proves.
// File is an absolute worktree path at hand-back and a manifest name on a change.
type VisualProof struct {
	File     string     `json:"file"`
	Manifest *FileEntry `json:"manifest,omitempty"`
	Caption  string     `json:"caption"`
}

// VisualEvidence is advisory. Missing proof never prevents delivery.
type VisualEvidence struct {
	UserVisible string        `json:"user_visible,omitempty"`
	VisualProof []VisualProof `json:"visual_proof,omitempty"`
	// ProofUnavailable is the worker's reason for being unable to capture proof.
	ProofUnavailable string `json:"proof_unavailable,omitempty"`
}

func (v VisualEvidence) Lines() string {
	if v.UserVisible == "" && len(v.VisualProof) == 0 && v.ProofUnavailable == "" {
		return ""
	}
	var b strings.Builder
	if v.UserVisible != "" {
		b.WriteString("user_visible: " + v.UserVisible + "\n")
	}
	for _, p := range v.VisualProof {
		b.WriteString("visual proof: " + p.File + " - " + p.Caption + "\n")
	}
	if v.ProofUnavailable != "" {
		b.WriteString("proof unavailable: " + v.ProofUnavailable + "\n")
	}
	return b.String()
}

var visualTUIWord = regexp.MustCompile(`\btui\b`)

// VisualBriefWarning is separate from CheckWorkerBrief: it never refuses a start.
func VisualBriefWarning(task string) string {
	lower := strings.ToLower(task)
	visible := visualTUIWord.MatchString(lower)
	for _, term := range []string{"user-visible", "user visible", "frontend", "front-end", "screen", "scroll", "layout", "template", ".css", ".tsx", ".html"} {
		if strings.Contains(lower, term) {
			visible = true
			break
		}
	}
	if !visible {
		return ""
	}
	for _, term := range []string{"expected visible outcome:", "expected appearance:", "should display", "should show", "must display", "must show"} {
		if strings.Contains(lower, term) {
			return ""
		}
	}
	return "WARNING: user-visible task has no expected visible outcome; describe what the reviewer should see"
}

func (o ChangeOrigin) Visual() VisualEvidence {
	if o.VisualEvidence == nil {
		return VisualEvidence{}
	}
	return *o.VisualEvidence
}
