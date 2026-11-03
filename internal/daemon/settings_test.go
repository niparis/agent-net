package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/linkkeepalive"
	"github.com/niparis/agent-net/internal/settings"
)

func TestSettingsSnapshotOfflineAndOrder(t *testing.T) {
	dir := t.TempDir()
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{st: st}
	if err = d.loadSettingsSnapshot(); err != nil {
		t.Fatal(err)
	}
	e, err := d.SettingsSnapshot().Resolve("workers.auto_continue", settings.Where{})
	if err != nil || string(e.Value) != "true" {
		t.Fatalf("default %+v %v", e, err)
	}
	frame := []byte(`{"revision":5,"recipe":"x","values":[{"key":"workers.auto_continue","level":"network","scope":"network","item":"","value":false},{"key":"future","level":"network","scope":"network","value":1}],"maturity":{}}`)
	if err = d.acceptSettingsSnapshot(frame, true); err != nil {
		t.Fatal(err)
	}
	snap := d.SettingsSnapshot()
	if len(snap.Values) != 1 {
		t.Fatalf("unknown key not dropped: %+v", snap)
	}
	snap.Values[0].Value = json.RawMessage("true")
	e, _ = d.SettingsSnapshot().Resolve("workers.auto_continue", settings.Where{})
	if string(e.Value) != "false" {
		t.Fatal("accessor was mutable")
	}
	if err = d.acceptSettingsSnapshot([]byte(`{"revision":4,"values":[],"maturity":{}}`), false); err != nil {
		t.Fatal(err)
	}
	if d.SettingsSnapshot().Revision != 5 {
		t.Fatal("accepted older revision")
	}
	if err = d.acceptSettingsSnapshot([]byte(`{`), false); err == nil {
		t.Fatal("accepted malformed frame")
	}
	st.close()
	st, err = openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d = &Daemon{st: st}
	if err = d.loadSettingsSnapshot(); err != nil {
		t.Fatal(err)
	}
	if d.SettingsSnapshot().Revision != 5 {
		t.Fatal("lost offline snapshot")
	}
	if err = d.acceptSettingsSnapshot([]byte(`{"revision":2,"values":[],"maturity":{}}`), true); err != nil || d.SettingsSnapshot().Revision != 2 {
		t.Fatalf("reconnect: %v", err)
	}
}

func TestSettingsSnapshotLinkIgnoresUnknownFrames(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st}
	sent := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			sent <- err
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		for _, frame := range []string{`{"type":"settings.sync","revision":9,"values":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false}],"maturity":{}}`, `{"type":"future.unknown"}`, `{"type":"node.name","hostname":"fixture"}`} {
			if err = ws.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				sent <- err
				return
			}
		}
		sent <- nil
		<-ctx.Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	l := newLink(ws, linkkeepalive.New())
	go l.readLoop(ctx, d)
	if err = <-sent; err != nil {
		t.Fatal(err)
	}
	waitDaemonLink(t, "settings and subsequent frame", func() bool { return d.shownHost.Load() == "fixture" })
	if snap := d.SettingsSnapshot(); snap.Revision != 9 || len(snap.Values) != 1 {
		t.Fatal(snap)
	}
	cancel()
	ws.CloseNow()
	<-l.dead
}
