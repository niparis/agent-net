package tsnode

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"tailscale.com/ipn"
	"tailscale.com/tsnet"
	"tailscale.com/types/logger"
)

// Open makes the real embedded node. Nothing runs until Up. The auth key file
// is read only while the node has no saved state, that is before its first
// login.
func Open(stateDir string, c Choice) (Node, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	s := &tsnet.Server{Dir: stateDir, Hostname: c.Name, Logf: logger.Discard, UserLogf: logger.Discard}
	if c.AuthKeyFile != "" {
		if _, err := os.Stat(filepath.Join(stateDir, "tailscaled.state")); errors.Is(err, os.ErrNotExist) {
			key, err := readAuthKey(c.AuthKeyFile)
			if err != nil {
				return nil, err
			}
			s.AuthKey = key
		}
	}
	return &tsNode{s: s}, nil
}

type tsNode struct {
	s       *tsnet.Server
	mu      sync.Mutex
	started bool
}

func (n *tsNode) start() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.started {
		return nil
	}
	if err := n.s.Start(); err != nil {
		return err
	}
	n.started = true
	return nil
}

func (n *tsNode) Up(ctx context.Context) (string, error) {
	if err := n.start(); err != nil {
		return "", err
	}
	lc, err := n.s.LocalClient()
	if err != nil {
		return "", err
	}
	tick := time.NewTicker(upPoll)
	defer tick.Stop()
	for {
		st, err := lc.StatusWithoutPeers(ctx)
		if err == nil {
			if st.BackendState == ipn.Running.String() {
				full, err := n.s.Up(ctx)
				if err != nil {
					return "", err
				}
				if len(full.CertDomains) == 0 {
					return "", errors.New("HTTPS certificates are off for this tailnet, and Funnel needs them. See https://tailscale.com/s/https")
				}
				return full.CertDomains[0], nil
			}
			if st.AuthURL != "" {
				return "", &NeedsLoginError{URL: st.AuthURL}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
		}
	}
}

func (n *tsNode) ListenFunnel(port int) (net.Listener, error) {
	ln, err := n.s.ListenFunnel("tcp", ":"+strconv.Itoa(port), tsnet.FunnelOnly())
	if err != nil {
		return nil, err
	}
	return sourceListener{ln}, nil
}

func (n *tsNode) Logout(ctx context.Context) error {
	lc, err := n.s.LocalClient()
	if err != nil {
		return err
	}
	return lc.Logout(ctx)
}

func (n *tsNode) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.started {
		return nil
	}
	n.started = false
	return n.s.Close()
}

// sourceListener reports the internet client as the remote address. Without
// it, every request would seem to come from the Funnel relay, and all clients
// would share one rate limit.
type sourceListener struct{ net.Listener }

func (l sourceListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if addr, ok := funnelSource(c); ok {
		return sourceConn{Conn: c, remote: addr}, nil
	}
	return c, nil
}

func funnelSource(c net.Conn) (net.Addr, bool) {
	for i := 0; i < 8 && c != nil; i++ {
		switch v := c.(type) {
		case *ipn.FunnelConn:
			if v.Src.IsValid() {
				return net.TCPAddrFromAddrPort(v.Src), true
			}
			return nil, false
		case *tls.Conn:
			c = v.NetConn()
		case interface{ NetConn() net.Conn }:
			c = v.NetConn()
		default:
			return nil, false
		}
	}
	return nil, false
}

type sourceConn struct {
	net.Conn
	remote net.Addr
}

func (c sourceConn) RemoteAddr() net.Addr { return c.remote }
