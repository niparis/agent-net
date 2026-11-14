package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

func isProtected(harness, model string, setting protocol.ProtectedProviders) bool {
	provider := protocol.ProfileProvider(harness, model)
	for _, p := range setting.Providers {
		if strings.EqualFold(p, provider) {
			return true
		}
	}
	return false
}
