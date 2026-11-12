package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
)

// openCodeServe describes one private `opencode serve`: a worker's, or a
// role's (the CTO). serveOpenCodePrivate is the one place that picks a port,
// retries a lost port race, and waits until the server answers. What differs
// is who owns the process, so the caller gives that as functions.
type openCodeServe struct {
	// label starts the error text: "opencode serve" or "CTO OpenCode".
	label, password string
	// spawn starts the process on port and records it. It returns the base URL,
	// the version known so far and the route identity of this attempt.
	spawn func(attempt, port int) (base, version, identity string, err error)
	// setVersion records the version the server reported.
	setVersion func(string)
	// gone says the process of the last spawn has exited.
	gone func() bool
	// kill stops the process and its group. The reason is for the log.
	kill func(reason string)
	// ready checks the server before it is used. An error stops it.
	ready func(info []byte) error
}

func (d *Daemon) serveOpenCodePrivate(ctx context.Context, s openCodeServe) (opencodeAPI, error) {
	// A child can lose the race for an ephemeral port after it is selected.
	// Retry when it exits during startup, choosing a new port each time.
	for attempt := 0; attempt < 3; attempt++ {
		port, err := d.workerFreePort()
		if err != nil {
			return nil, err
		}
		base, version, identity, err := s.spawn(attempt, port)
		if err != nil {
			return nil, err
		}
		api := openCodeWorkerAPI(ctx, base, s.password)
	wait:
		for {
			if routes, routeErr := d.discoverOpenCodeRoutes(ctx, base, s.password, version, identity); routeErr == nil {
				infoPath, pathErr := routes.Path(opencodeapi.Info, "")
				if pathErr != nil {
					s.kill("startup_failure")
					return nil, pathErr
				}
				if info, code, infoErr := api(opencodeapi.Info.Method(), infoPath, nil); infoErr == nil && code == http.StatusOK {
					if v := parseOpenCodeInfoVersion(info); v != "" {
						s.setVersion(v)
					}
					if s.ready != nil {
						if err := s.ready(info); err != nil {
							s.kill("startup_failure")
							return nil, err
						}
					}
					return api, nil
				}
			}
			select {
			case <-ctx.Done():
				s.kill("startup_timeout")
				return nil, fmt.Errorf("%s did not start in time", s.label)
			case <-time.After(100 * time.Millisecond):
			}
			if s.gone() {
				s.kill("crash")
				break wait
			}
		}
	}
	return nil, errors.New(s.label + " exited at start")
}

func portURL(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }
