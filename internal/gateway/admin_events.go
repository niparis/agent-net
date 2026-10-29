package gateway

import (
	"net/http"
	"strconv"
	"time"
)

// recordRoutes adds the admin calls that read the gateway's record.
func (g *Gateway) recordRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/admin/events", g.admin(g.handleAdminEvents))
}

// handleAdminEvents answers GET /v1/admin/events?node=&session=&kind=&since=&after_id=&limit=.
// since is an RFC 3339 time. Events come oldest first.
func (g *Gateway) handleAdminEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := EventFilter{Node: q.Get("node"), Session: q.Get("session"), Kind: q.Get("kind")}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "bad since: use an RFC 3339 time", http.StatusBadRequest)
			return
		}
		f.Since = t
	}
	if v := q.Get("after_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "bad after_id", http.StatusBadRequest)
			return
		}
		f.AfterID = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			http.Error(w, "bad limit", http.StatusBadRequest)
			return
		}
		f.Limit = n
	}
	evs, err := g.store.QueryEvents(f)
	writeJSON(w, map[string]any{"events": evs}, err)
}
