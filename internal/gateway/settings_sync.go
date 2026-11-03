package gateway

import (
	"context"
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func (g *Gateway) sendSettingsSnapshot(ctx context.Context, nc *nodeConn, snap settings.Snapshot) {
	// Marshal directly: EncodeRequest's map conversion rounds large JSON integers.
	frame, err := json.Marshal(struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		settings.Snapshot
	}{"settings.sync", protocol.NewID("d"), snap})
	if err == nil {
		nc.write(ctx, frame)
	}
}
func (g *Gateway) sendSettingsSync(ctx context.Context, nc *nodeConn) {
	snap, err := g.store.SettingsSnapshot()
	if err != nil {
		g.logLine("error", "where", "settings sync", "err", err)
		return
	}
	g.sendSettingsSnapshot(ctx, nc, snap)
}
func (g *Gateway) settingsChanged() {
	snap, err := g.store.SettingsSnapshot()
	if err != nil {
		g.logLine("error", "where", "settings sync", "err", err)
		return
	}
	g.mu.Lock()
	conns := make([]*nodeConn, 0, len(g.conns))
	for _, c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, c := range conns {
		g.sendSettingsSnapshot(ctx, c, snap)
	}
}
