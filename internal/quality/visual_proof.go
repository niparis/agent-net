package quality

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
)

// VisualSignals warns about missing proof and declarations at odds with a diff.
func VisualSignals(diff string, v protocol.VisualEvidence) []protocol.QualitySignal {
	if v.UserVisible == "yes" && len(v.VisualProof) == 0 {
		return []protocol.QualitySignal{{Code: "visual_proof_missing", Strength: protocol.SignalStrong, Evidence: "user_visible yes without visual proof"}}
	}
	if v.UserVisible != "no" {
		return nil
	}
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+++ b/") {
			continue
		}
		file := strings.TrimPrefix(line, "+++ b/")
		visible := false
		for _, part := range []string{"/tui/", "/templates/", "/frontend/", "/front-end/", "/screens/", "/web/", "/ui/", "/views/", "/styles/"} {
			if strings.Contains("/"+file, part) {
				visible = true
			}
		}
		for _, ext := range []string{".golden", ".css", ".scss", ".html", ".tsx", ".jsx", ".vue", ".svelte", ".tmpl", ".tpl", ".hbs", ".ejs", ".jinja2", "_view.go"} {
			if strings.HasSuffix(file, ext) {
				visible = true
			}
		}
		if visible {
			return []protocol.QualitySignal{{Code: "visual_declared_no", Strength: protocol.SignalWeak, Evidence: "user_visible no but diff touches user-facing files"}}
		}
	}
	return nil
}
