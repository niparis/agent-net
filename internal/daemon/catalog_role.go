package daemon

// Role enforcement for catalog tools. Hiding a tool from an MCP list is not
// authority: a caller that names a catalog tool directly is refused here when
// its role may not use it.

import (
	"fmt"

	"github.com/niparis/agent-net/internal/toolcatalog"
)

// catalogRoleCheck refuses a catalog tool the session's role may not use.
// Methods outside the catalog are not this check's business.
func (lc *localConn) catalogRoleCheck(method string, ls *localSession) error {
	var roles []toolcatalog.Role
	for _, tool := range toolcatalog.Tools {
		if tool.Name == method {
			roles = tool.Roles
			break
		}
	}
	if roles == nil {
		return nil
	}
	role := toolcatalog.RoleAgent
	if ls != nil {
		lc.d.mu.Lock()
		worker := ls.info.Worker != nil
		lc.d.mu.Unlock()
		if worker {
			role = toolcatalog.RoleWorker
		}
	}
	for _, r := range roles {
		if r == role {
			return nil
		}
	}
	return fmt.Errorf("%s is not available to %s sessions", method, role)
}
