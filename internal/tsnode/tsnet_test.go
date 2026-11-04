package tsnode

import (
	"crypto/tls"
	"net"
	"net/netip"
	"testing"

	"tailscale.com/ipn"
)

type oneConnListener struct {
	net.Listener
	c net.Conn
}

func (l *oneConnListener) Accept() (net.Conn, error) { return l.c, nil }

func TestSourceListenerReportsFunnelClient(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	src := netip.MustParseAddrPort("203.0.113.7:4444")
	funnel := &ipn.FunnelConn{Conn: a, Src: src, Target: "agent-net.example.ts.net:443"}
	// tsnet hands out TLS connections over the Funnel connection.
	c, err := sourceListener{&oneConnListener{c: tls.Server(funnel, &tls.Config{})}}.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.RemoteAddr().String(); got != src.String() {
		t.Fatalf("remote %s, want %s", got, src)
	}
	// Any other connection keeps its own address.
	plain, err := sourceListener{&oneConnListener{c: b}}.Accept()
	if err != nil || plain != b {
		t.Fatalf("plain conn changed: %v", err)
	}
}

func TestOpenNeedsNoNetwork(t *testing.T) {
	dir := t.TempDir()
	n, err := Open(dir, Choice{Name: "agent-net"})
	if err != nil {
		t.Fatal(err)
	}
	// Close before Up must not start the node.
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, Choice{Name: "agent-net", AuthKeyFile: dir + "/missing"}); err == nil {
		t.Fatal("missing key file accepted before the first login")
	}
}
