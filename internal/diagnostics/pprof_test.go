package diagnostics

import (
	"io"
	"log"
	"net/http"
	"testing"
)

func TestPprof(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	for _, addr := range []string{"", "0.0.0.0:0", "example.com:6061"} {
		s := StartPprof(addr, logger)
		if s != nil {
			s.Close()
			t.Fatalf("accepted %q", addr)
		}
	}
	s := StartPprof("127.0.0.1:0", logger)
	if s == nil {
		t.Fatal("no listener")
	}
	defer s.Close()
	r, err := http.Get("http://" + s.Addr().String() + "/debug/pprof/")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.Status)
	}
}
