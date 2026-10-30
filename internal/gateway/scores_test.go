package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScoresRoutes(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	_, token, err := g.Store().CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, tok, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/scores", bytes.NewBufferString(body))
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized: %d", got.Code)
	}
	if got := request(http.MethodPut, token, `{"model":"test/model","score":42}`); got.Code != http.StatusOK {
		t.Fatalf("set: %d %s", got.Code, got.Body)
	}
	if got := request(http.MethodGet, token, ""); !bytes.Contains(got.Body.Bytes(), []byte(`"score":42`)) {
		t.Fatalf("get: %s", got.Body)
	}
	if got := request(http.MethodPut, token, `{"model":"test/model","score":101}`); got.Code != http.StatusBadRequest {
		t.Fatalf("invalid: %d", got.Code)
	}
	if got := request(http.MethodPut, token, `{"model":"test/model"}`); got.Code != http.StatusOK || bytes.Contains(got.Body.Bytes(), []byte(`"score":42`)) {
		t.Fatalf("clear: %d %s", got.Code, got.Body)
	}
}
