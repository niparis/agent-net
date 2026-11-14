package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
)

// replyOpenCodePermission answers one pending OpenCode permission request with
// "once" (decision 0166). It is called only after tool_decide said allow, and it
// never sends "always" or "reject": a decision it does not make stays with the
// owner. The service is the one that runs the session: the worker's own service
// for a worker, the owner's service otherwise. A service without the reply route
// (older than 2.3) is an error, and nothing is sent.
func (d *Daemon) replyOpenCodePermission(ctx context.Context, ls *localSession, session, request, folder string) error {
	info := d.infoOf(ls)
	if info.Harness != "opencode" {
		return errors.New("not an OpenCode session")
	}
	if session == "" {
		session = info.NativeID
	}
	if session == "" || request == "" {
		return errors.New("the permission request or its session is missing")
	}
	var base, password, version, identity string
	if w := d.workerBySession(d.currentSessionID(ls)); w != nil {
		d.wmu.Lock()
		base, password, version, identity = w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity
		d.wmu.Unlock()
	} else {
		service, err := d.ownerOpenCodeService()
		if err != nil {
			return fmt.Errorf("the OpenCode service is not running on %s", d.hostName())
		}
		base, password, identity = service.URL, service.Password, service.identity
	}
	if base == "" {
		return errors.New("the OpenCode service of this session is not known")
	}
	routes, err := d.discoverOpenCodeRoutes(ctx, base, password, version, identity)
	if err != nil {
		return err
	}
	path, err := routes.PermissionReplyPath(session, request)
	if err != nil {
		return err
	}
	api := openCodeWorkerAPI(ctx, strings.TrimRight(base, "/"), password)
	directory := strings.ReplaceAll(url.QueryEscape(folder), "+", "%20")
	_, status, err := api(opencodeapi.PermissionReply.Method(), path, map[string]string{"decision": "once"}, "x-opencode-directory", directory)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return routes.HTTPError(opencodeapi.PermissionReply, status)
	}
	return nil
}
