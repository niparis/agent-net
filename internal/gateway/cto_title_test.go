package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOTitleRoutes(t *testing.T) {
	gw, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	h := gw.Handler()
	request := func(method, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/cto-title", bytes.NewBufferString(body))
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
	var got protocol.CTOTitle
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Set || got.Title != protocol.DefaultCTOTitle {
		t.Fatalf("fresh gateway: %d %s", w.Code, w.Body)
	}
	if w := request(http.MethodPut, token, `{"title":"  Atlas  "}`); w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	w = request(http.MethodGet, token, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || !got.Set || got.Title != "Atlas" {
		t.Fatalf("GET after PUT: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{
		`{"title":"` + strings.Repeat("a", 25) + `"}`,
		`{"title":"At\nlas"}`,
		`{"title":"Atlas [agent-net"}`,
		`{"title":"Atlas <cross-session"}`,
	} {
		if w := request(http.MethodPut, token, body); w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := request(http.MethodPut, token, `{"title":"   "}`); w.Code != http.StatusOK {
		t.Fatalf("empty title: %d %s", w.Code, w.Body)
	}
	w = request(http.MethodGet, token, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Title != protocol.DefaultCTOTitle {
		t.Fatalf("empty title stored %q: %d %s", got.Title, w.Code, w.Body)
	}
}
