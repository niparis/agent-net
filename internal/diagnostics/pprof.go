package diagnostics

import (
	"log"
	"net"
	"net/http"
	"net/http/pprof"
)

// StartPprof starts a private profiling listener. The caller closes the listener.
func StartPprof(addr string, logger *log.Logger) net.Listener {
	if addr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		logger.Printf("event=pprof.refused address=%q reason=loopback-required", addr)
		return nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Printf("event=pprof.error err=%q", err)
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	go func() { _ = http.Serve(ln, mux) }()
	return ln
}
