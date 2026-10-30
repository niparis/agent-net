package phone

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

type cancelFlusher struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w cancelFlusher) Flush() { w.ResponseRecorder.Flush(); w.cancel() }
func TestStreamSendsSections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := cancelFlusher{httptest.NewRecorder(), cancel}
	New(&fakeOperator{}).ServeHTTP(w, httptest.NewRequest("GET", "/events", nil).WithContext(ctx))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(w.Body.String(), "event: update") || !strings.Contains(w.Body.String(), "test-machine") {
		t.Fatalf("stream: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
}
