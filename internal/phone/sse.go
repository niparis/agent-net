package phone

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/operator"
)

// stream periodically reads the same data as the TUI and sends the open screen
// again when it changed. The screen is named by the page that opened the stream.
// Full sections make reconnects self-healing and avoid a browser-side cache.
func (a *app) stream(w http.ResponseWriter, r *http.Request) {
	flush, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	interval := a.interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	section := r.URL.Query().Get("section")
	screen := r.URL.Query().Get("screen")
	q := queryFrom(r)
	// The panel has its own section and its own id, so a screen and the panel
	// update independently. A screen stream never reads the CTO conversation.
	q.CTO = section == "cto"
	q.OnlyCTO = section == "cto"
	event, tmpl := "update", "stream"
	if section == "cto" {
		event, tmpl = "cto", "ctobody"
	}
	previous := ""
	for {
		ctx, cancel := context.WithTimeout(r.Context(), operator.Timeout)
		p := a.load(ctx, screen, q)
		cancel()
		var b bytes.Buffer
		if err := shell.ExecuteTemplate(&b, tmpl, p); err != nil {
			return
		}
		current := b.String()
		if current != previous {
			if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
				return
			}
			for _, line := range strings.Split(current, "\n") {
				if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
					return
				}
			}
			if _, err := fmt.Fprint(w, "\n"); err != nil {
				return
			}
			flush.Flush()
			previous = current
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
