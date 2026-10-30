package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestIntegrationsStoreAndRoutes(t *testing.T) {
	gw, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	in, set, err := gw.Store().Integrations()
	if err != nil || set || in.CountOn() != 0 {
		t.Fatalf("empty store: %+v set=%v err=%v", in, set, err)
	}

	// No node token, no answers.
	h := gw.Handler()
	req := httptest.NewRequest("GET", "/v1/integrations", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("GET without a token was allowed")
	}

	_, token, err := gw.Store().CreateNode("laptop", "linux")
	if err != nil {
		t.Fatal(err)
	}
	get := func() protocol.IntegrationsResult {
		req := httptest.NewRequest("GET", "/v1/integrations", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: %d %s", rec.Code, rec.Body)
		}
		var res protocol.IntegrationsResult
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := get(); res.Set {
		t.Fatal("a fresh gateway has answers")
	}

	answers := protocol.Integrations{HintHook: true, NeedsHook: true}
	body, _ := json.Marshal(answers)
	req = httptest.NewRequest("PUT", "/v1/integrations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	res := get()
	if !res.Set || res.Integrations != answers {
		t.Fatalf("after PUT: %+v", res)
	}
	stored, set, err := gw.Store().Integrations()
	if err != nil || !set || stored != answers {
		t.Fatalf("store after PUT: %+v set=%v err=%v", stored, set, err)
	}
}
