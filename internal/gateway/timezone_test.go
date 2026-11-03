package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestTimeZoneSettingsRoutes(t *testing.T) {
	gw, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	h := gw.Handler()
	request := func(method, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/timezone-settings", bytes.NewBufferString(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request(http.MethodGet, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized GET: %d", w.Code)
	}
	_, token, err := gw.Store().CreateNode("laptop", "linux")
	if err != nil {
		t.Fatal(err)
	}
	var out protocol.TimeZoneSettings
	w := request(http.MethodGet, token, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Name != protocol.TimeZoneAuto {
		t.Fatalf("default: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodPut, token, `{"name":"Europe/Paris"}`); w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	w = request(http.MethodGet, token, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Name != "Europe/Paris" {
		t.Fatalf("GET after PUT: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodPut, token, `{"name":"Mars/Olympus"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad zone accepted: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodGet, token, ""); json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Name != "Europe/Paris" {
		t.Fatalf("a refused zone changed the setting: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodPut, token, `{"name":"auto"}`); w.Code != http.StatusOK {
		t.Fatalf("PUT auto: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodGet, token, ""); json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Name != protocol.TimeZoneAuto {
		t.Fatalf("auto did not clear the name: %d %s", w.Code, w.Body)
	}
}

func TestSettingTheTimeZoneChangesNoStoredTime(t *testing.T) {
	gw, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	_, token, err := gw.Store().CreateNode("laptop", "linux")
	if err != nil {
		t.Fatal(err)
	}
	a, b := twoSessions(t, gw.Store())
	msg, _, err := gw.Store().StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "hi", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	read := func(query string, arg any) int64 {
		t.Helper()
		var v int64
		if err := gw.Store().db.QueryRow(query, arg).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	messageAt := read(`SELECT created_at FROM messages WHERE id=?`, msg.ID)
	var nodeID string
	if err := gw.Store().db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, a).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if err := gw.Store().SetNodeHello(nodeID, "h1", "v1"); err != nil {
		t.Fatal(err)
	}
	nodeSeen := read(`SELECT seen_at FROM nodes WHERE id=?`, nodeID)
	if nodeSeen == 0 {
		t.Fatal("node seen_at was not stored")
	}

	r := httptest.NewRequest(http.MethodPut, "/v1/timezone-settings", bytes.NewBufferString(`{"name":"Europe/Paris"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	gw.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	// The setting is stored, and the timestamps it displays are untouched.
	out, err := gw.Store().timeZoneSetting()
	if err != nil || out.Name != "Europe/Paris" {
		t.Fatalf("setting: %+v %v", out, err)
	}
	if got := read(`SELECT created_at FROM messages WHERE id=?`, msg.ID); got != messageAt {
		t.Fatalf("message created_at changed from %d to %d", messageAt, got)
	}
	if got := read(`SELECT seen_at FROM nodes WHERE id=?`, nodeID); got != nodeSeen {
		t.Fatalf("node seen_at changed from %d to %d", nodeSeen, got)
	}
	if msg.CreatedAt.UnixNano() != messageAt {
		t.Fatalf("the stored message time is not the one the setting was set against: %d vs %d", msg.CreatedAt.UnixNano(), messageAt)
	}
}

func TestNodesCarryTheReportedTimeZone(t *testing.T) {
	gw, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	id, _, err := gw.Store().CreateNode("Nicolas-MBP", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Store().SetNodeHello(id, "Nicolas-MBP", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := gw.Store().SetNodeTimeZone(id, "Europe/Paris"); err != nil {
		t.Fatal(err)
	}
	list, err := gw.Store().Nodes()
	if err != nil || len(list) != 1 {
		t.Fatalf("nodes: %v %v", list, err)
	}
	if list[0].TimeZone != "Europe/Paris" {
		t.Fatalf("zone: %q", list[0].TimeZone)
	}
	// An older daemon sends nothing and leaves the stored zone alone.
	if err := gw.Store().SetNodeTimeZone(id, ""); err != nil {
		t.Fatal(err)
	}
	list, err = gw.Store().Nodes()
	if err != nil || list[0].TimeZone != "Europe/Paris" {
		t.Fatalf("empty zone cleared it: %v %v", list, err)
	}
}
