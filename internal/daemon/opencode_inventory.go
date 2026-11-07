package daemon

import (
	"context"
	"net/http"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) checkOpenCodeCapabilities(ctx context.Context, list []protocol.HarnessInfo) {
	service, err := d.ownerOpenCodeService()
	if err != nil {
		return
	}
	for i := range list {
		if list[i].Name != "opencode" {
			continue
		}
		routes, err := d.openCodeRoutes.Discover(ctx, &http.Client{Timeout: 10 * time.Second}, service.URL, service.Password, list[i].Version, service.identity)
		if err != nil {
			return
		}
		for _, op := range routes.Missing() {
			list[i].MissingOperations = append(list[i].MissingOperations, string(op))
		}
	}
}
