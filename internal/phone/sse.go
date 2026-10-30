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

// stream periodically reads the same snapshot used by the TUI. Full sections
// make reconnects self-healing and avoid depending on a browser-side cache.
func (a *app) stream(w http.ResponseWriter, r *http.Request) {
	flush, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	previous := ""
	for {
		ctx, cancel := context.WithTimeout(r.Context(), operator.Timeout)
		p := a.data(ctx)
		cancel()
		var b bytes.Buffer
		if err := shell.ExecuteTemplate(&b, "sections", p); err != nil {
			return
		}
		current := b.String()
		if current != previous {
			if _, err := fmt.Fprint(w, "event: update\n"); err != nil {
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
