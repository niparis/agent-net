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
	if w := request(http.MethodPut, token, `{"model":"provider/model"}`); w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	w := request(http.MethodGet, token, "")
	var out struct {
		Model string `json:"model"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Model != "provider/model" {
		t.Fatalf("GET: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodPut, token, `{"model":"bad\nmodel"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad model: %d", w.Code)
	}
}
