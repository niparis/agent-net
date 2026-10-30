package phone

import (
	"context"
	"fmt"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

type changingOperator struct {
	fakeOperator
	count int
}

func (f *changingOperator) Snapshot(ctx context.Context) (operator.Snapshot, error) {
	f.count++
	return operator.Snapshot{Connected: true, Nodes: []protocol.NodeView{{Hostname: fmt.Sprintf("machine-%d", f.count)}}}, nil
}

type twoFlushes struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	count  int
}

func (w *twoFlushes) Flush() {
	w.ResponseRecorder.Flush()
	w.count++
	if w.count == 2 {
		w.cancel()
	}
}
func TestStreamSendsChangedSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := &twoFlushes{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	a := &app{src: &changingOperator{}, interval: time.Millisecond}
	a.stream(w, httptest.NewRequest("GET", "/events", nil).WithContext(ctx))
	if !strings.Contains(w.Body.String(), "machine-1") || !strings.Contains(w.Body.String(), "machine-2") {
		t.Fatal(w.Body.String())
	}
}
