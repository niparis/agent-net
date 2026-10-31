package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCTOSettingsRoutes(t *testing.T) {
	gw, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	h := gw.Handler()
	request := func(method, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/cto-settings", bytes.NewBufferString(body))
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
	w := request(http.MethodGet, token, "")
	var defaults struct {
		Compaction struct {
			Threshold int   `json:"threshold_percent"`
			Floor     int64 `json:"floor_tokens"`
			Growth    int64 `json:"growth_tokens"`
		} `json:"compaction"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &defaults) != nil || defaults.Compaction.Threshold != 70 || defaults.Compaction.Floor != 40000 || defaults.Compaction.Growth != 60000 {
		t.Fatalf("defaults: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodPut, token, `{"model":"provider/model"}`); w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	w = request(http.MethodGet, token, "")
	var out struct {
		Model string `json:"model"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Model != "provider/model" {
		t.Fatalf("GET: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodPut, token, `{"model":"bad\nmodel"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad model: %d", w.Code)
	}
	// Compaction settings are partial updates and default when unset.
	if w := request(http.MethodPut, token, `{"compaction":{"threshold_percent":80,"floor_tokens":50000,"growth_tokens":70000,"cache_window_seconds":{"fake":120}}}`); w.Code != http.StatusOK {
		t.Fatalf("compaction PUT: %d %s", w.Code, w.Body)
	}
	var full struct {
		Model      string `json:"model"`
		Compaction struct {
			Threshold int            `json:"threshold_percent"`
			Floor     int64          `json:"floor_tokens"`
			Growth    int64          `json:"growth_tokens"`
			Windows   map[string]int `json:"cache_window_seconds"`
		} `json:"compaction"`
	}
	w = request(http.MethodGet, token, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &full) != nil ||
		full.Model != "provider/model" || full.Compaction.Threshold != 80 || full.Compaction.Floor != 50000 ||
		full.Compaction.Growth != 70000 || full.Compaction.Windows["fake"] != 120 {
		t.Fatalf("partial update: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodPut, token, `{"model":""}`); w.Code != http.StatusOK {
		t.Fatalf("model clear: %d", w.Code)
	}
	w = request(http.MethodGet, token, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &full) != nil || full.Model != "" || full.Compaction.Threshold != 80 {
		t.Fatalf("clear kept compaction: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodPut, token, `{"compaction":{"threshold_percent":5}}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad threshold: %d", w.Code)
	}
	if w := request(http.MethodPut, token, `{"compaction":{"cache_window_seconds":{"fake":0}}}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d", w.Code)
	}
	if w := request(http.MethodPut, token, `{"compaction":{"growth_tokens":-1}}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad growth: %d", w.Code)
	}
	if w := request(http.MethodPut, token, `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty update: %d", w.Code)
	}
}
