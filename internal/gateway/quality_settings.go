package gateway

import (
	"net/http"

	"github.com/niparis/agent-net/internal/protocol"
)

// Quality weighting is off for good (decision 0105): the cell picker replaced it.
// The endpoint stays so an older CLI or daemon gets an answer. It stores
// nothing and always says disabled.
func (g *Gateway) handleQualitySettings(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method != http.MethodGet {
		g.store.Event("quality.set", node, "", "", "ignored: quality weighting is off for good")
	}
	writeJSON(w, protocol.QualitySettings{}, nil)
}
