package phone

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppShellAndAssets(t *testing.T) {
	h := New(nil)
	for _, tt := range []struct{ path, contains string }{{"/", "Agent net"}, {"/manifest.webmanifest", "standalone"}, {"/static/htmx.min.js", "htmx"}, {"/static/sse.js", "EventSource"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), tt.contains) {
			t.Errorf("%s: status %d, body missing %q", tt.path, w.Code, tt.contains)
		}
	}
}
