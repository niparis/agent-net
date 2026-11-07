package gateway

import (
	"context"
	"fmt"
	"net"
	"time"
)

// CheckExposureWithAddrs checks if the listener is exposed on any public IP address
// using ifaceAddrs to query interface addresses if listening on 0.0.0.0 or ::.
func (g *Gateway) CheckExposureWithAddrs(listenAddr string, ifaceAddrs func() ([]net.Addr, error)) {
	publicIPs, err := ListenerPublicIPs(listenAddr, ifaceAddrs)
	if err != nil {
		g.logLine("warning", "where", "exposure_check", "err", err)
		return
	}

	var warnings []string
	for _, ip := range publicIPs {
		w := fmt.Sprintf("gateway reachable on public address %s", ip.String())
		warnings = append(warnings, w)
		g.logLine("warning", "where", "listener", "warning", w)
	}

	g.exposureMu.Lock()
	g.exposureWarnings = warnings
	g.exposureMu.Unlock()
}

// CheckExposure checks if the gateway's listen address is reachable on any public IP.
func (g *Gateway) CheckExposure(listenAddr string) {
	defer g.slowLoop("exposure", time.Now())
	g.CheckExposureWithAddrs(listenAddr, net.InterfaceAddrs)
}

// ExposureWarnings returns a copy of the current gateway exposure warnings.
func (g *Gateway) ExposureWarnings() []string {
	g.exposureMu.RLock()
	defer g.exposureMu.RUnlock()
	if len(g.exposureWarnings) == 0 {
		return nil
	}
	out := make([]string, len(g.exposureWarnings))
	copy(out, g.exposureWarnings)
	return out
}

// RunExposure runs an initial exposure check and checks periodically every hour.
func (g *Gateway) RunExposure(ctx context.Context, listenAddr string) {
	g.CheckExposure(listenAddr)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.CheckExposure(listenAddr)
		}
	}
}
