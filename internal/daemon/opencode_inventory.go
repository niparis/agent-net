package daemon

import (
	"context"
	"net/http"
	"time"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
	"github.com/niparis/agent-net/internal/protocol"
)

func checkOpenCodeCapabilities(ctx context.Context, list []protocol.HarnessInfo) {
	service, err := ownerOpenCodeService()
	if err != nil {
		return
	}
	for i := range list {
		if list[i].Name != "opencode" {
			continue
		}
		routes, err := opencodeapi.Discover(ctx, &http.Client{Timeout: 10 * time.Second}, service.URL, service.Password, list[i].Version)
		if err != nil {
			return
		}
		for _, op := range routes.Missing() {
			list[i].MissingOperations = append(list[i].MissingOperations, string(op))
		}
	}
}
