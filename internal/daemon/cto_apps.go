package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/mcpserver"
)

// executeCTOApps is reached only after the private CTO process check and action
// classification. It reuses the owner's app operations; OAuth approval remains
// an owner action. No token or credential is returned by the list operation.
func (d *Daemon) executeCTOApps(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	var reason string
	if value, ok := fields["reason"]; ok {
		if err := json.Unmarshal(value, &reason); err != nil {
			return nil, fmt.Errorf("reason must be a string")
		}
		delete(fields, "reason")
		var err error
		raw, err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}
	if err := mcpserver.CTOArguments(method, raw); err != nil {
		return nil, err
	}
	if method == "apps.ask" {
		var args map[string]any
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		args["owner"] = d.ownerSessionID()
		data, err := d.call(ctx, method, args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"state": "awaiting_owner", "result": map[string]any{"summary": "Consent ask opened for the owner.", "data": data}}, nil
	}
	var q struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Grant string `json:"grant"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	lc := &localConn{d: d, client: "cli", cto: true}
	call := func(method string, args json.RawMessage) (any, error) {
		result, handled, err := lc.dispatchOwner(ctx, method, args)
		if !handled {
			return nil, fmt.Errorf("unknown CTO app operation %s", method)
		}
		return result, err
	}
	if method == "apps.list" || method == "apps.public.status" {
		return call(method, raw)
	}
	var undoMethod, diff string
	var undo any
	switch method {
	case "apps.grant", "apps.ungrant", "apps.rename", "apps.revoke":
		if strings.TrimSpace(q.ID) == "" {
			return nil, fmt.Errorf("%s needs id", method)
		}
		if (method == "apps.grant" || method == "apps.ungrant") && strings.TrimSpace(q.Grant) == "" {
			return nil, fmt.Errorf("%s needs grant", method)
		}
		before, err := call("apps.list", json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(before)
		if err != nil {
			return nil, err
		}
		var list struct {
			Apps []struct {
				ID, Name  string
				Grants    []string
				RevokedAt *string
			}
		}
		if err = json.Unmarshal(data, &list); err != nil {
			return nil, err
		}
		found := false
		for _, app := range list.Apps {
			if app.ID != q.ID || app.RevokedAt != nil {
				continue
			}
			found = true
			switch method {
			case "apps.rename":
				undoMethod = "apps.rename"
				undo = map[string]string{"id": q.ID, "name": app.Name}
				diff = app.Name + " -> " + q.Name
			case "apps.grant", "apps.ungrant":
				granted := false
				for _, grant := range app.Grants {
					if grant == q.Grant {
						granted = true
						break
					}
				}
				undoMethod = "apps.ungrant"
				if granted {
					undoMethod = "apps.grant"
				}
				undo = map[string]string{"id": q.ID, "grant": q.Grant}
				diff = fmt.Sprintf("%s: %t -> %t", q.Grant, granted, method == "apps.grant")
			case "apps.revoke":
				diff = "credentials invalidated; reconnect the app to use it again"
			}
			break
		}
		if !found {
			return nil, fmt.Errorf("no active app %q", q.ID)
		}
	case "apps.public.on", "apps.public.off":
		before, err := call("apps.public.status", json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(before)
		if err != nil {
			return nil, err
		}
		var status struct {
			On bool `json:"on"`
		}
		if err = json.Unmarshal(data, &status); err != nil {
			return nil, err
		}
		undoMethod = "apps.public.off"
		if status.On {
			undoMethod = "apps.public.on"
		}
		undo = map[string]any{}
		diff = fmt.Sprintf("internet access: %t -> %t", status.On, method == "apps.public.on")
	default:
		return nil, fmt.Errorf("unknown CTO app operation %s", method)
	}
	result, err := call(method, raw)
	if err != nil {
		return nil, err
	}
	var options []string
	if undoMethod != "" {
		data, err := json.Marshal(undo)
		if err != nil {
			return result, err
		}
		options = []string{undoMethod, string(data)}
	}
	text := "CTO applied " + method
	if q.ID != "" {
		text += " " + cleanLine(q.ID)
	}
	if reason = cleanLine(reason); reason != "" {
		text += ": " + reason
	}
	_, err = d.call(ctx, "inbox.create", map[string]any{"session_id": d.ctoSessionID(), "kind": "check", "text": text, "recommendation": diff, "options": options})
	if err != nil {
		return result, fmt.Errorf("%s; could not add Inbox notice: %w", text, err)
	}
	return result, nil
}
