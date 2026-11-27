package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// This file is the owner-only data: the recent conversation of a session, and
// the CPU and memory figures asked for on request. Decision 0007 and
// docs/security.md say who may ask:
//
//   - only the owner's CLI or TUI, which says "cli" in hello. A bound agent
//     connection (Claude Code MCP, Pi, OpenCode) is always refused;
//   - and not a CLI that runs inside an agent session. The daemon looks at the
//     parents of the calling process and refuses it when one of them is a
//     local session's process. An agent that runs `agent-net tui` from its
//     shell tool is stopped here.
//
// Another machine's owner asks through the gateway, which relays the request
// to the machine that holds the session. The text is masked and cut on the
// machine that holds it, it passes through the gateway without being stored or
// logged, and it never goes into the events table.

// operatorMethods are the methods that return owner-only data.
var operatorMethods = map[string]bool{
	protocol.OpRoleSetup:  true,
	"worker_floors_reset": true, "operator_undo": true, "merger_get": true, "merger_restore": true, "apps.public.restore": true, "sessions_archive": true, "sessions_archived": true, "session_pin": true,
	protocol.OpWorkerLog: true,
	"apps.public.status": true, "apps.public.on": true, "apps.public.off": true, "apps.public.url": true,
	"apps.list": true, "apps.pending": true, "apps.approve": true, "apps.refuse": true, "apps.revoke": true, protocol.OpAppsRename: true, protocol.OpAppsGrant: true, protocol.OpAppsUngrant: true,
	"tools_mcp_add": true, "tools_mcp_remove": true, "tools_mcp_test": true, "tools_mcp_list": true,
	protocol.OpCTOGraphWindow:   true,
	"notification_settings":     true,
	protocol.OpSkillArchiveUndo: true, protocol.OpSkillsUndo: true, "gateway_backup": true,
	protocol.OpSessionStop: true, protocol.OpSessionStart: true, protocol.OpSessionStartGet: true, protocol.OpSessionResume: true,
	"inbox_list": true, protocol.OpAsksList: true, protocol.OpChiefGet: true, protocol.OpChiefSet: true, protocol.OpAskHistory: true, protocol.OpAskUndo: true, protocol.OpAskMark: true, protocol.OpAskDiscuss: true, protocol.OpDeskLog: true, protocol.OpDeskSettle: true, "inbox_answer": true, "outcome_inbox": true, "outcome_overrule": true, "learned_list": true, "learned_answer": true, "learned_view": true, "usage": true, "usage_top": true, "session_text": true, "counts": true, "session_enter": true,
	protocol.OpConversations:        true,
	protocol.OpConversationMessages: true,
	protocol.OpProjectDisplayGet:    true, protocol.OpProjectDisplaySet: true,
	protocol.OpGraphView: true, protocol.OpGraphTalk: true, protocol.OpSessionTrust: true, protocol.OpFolderTrust: true, protocol.OpFolderMCP: true, protocol.OpProcessStop: true, protocol.OpStandingOrders: true, protocol.OpJudgmentAdd: true, protocol.OpSessionAgentSet: true, protocol.OpSessionAgentList: true, protocol.OpNotesCopy: true, protocol.OpNotesUncopy: true, protocol.OpSessionRename: true, protocol.OpSessionReplace: true, protocol.OpNodeRename: true, protocol.OpMachineRemove: true, protocol.OpMachineRotate: true,
	protocol.OpIntegrationsGet: true, protocol.OpIntegrationsSet: true,
	protocol.OpHiddenGet: true, protocol.OpHiddenSet: true, "onboarding_get": true, "onboarding_set": true, "onboarding_restart": true,
	protocol.OpTimeZoneGet: true, protocol.OpTimeZoneSet: true, protocol.OpProtectedProvidersGet: true, protocol.OpProtectedProvidersSet: true, protocol.OpWorkerFloorsGet: true, protocol.OpWorkerFloorsSet: true, protocol.OpWorkerProvidersGet: true, protocol.OpWorkerProvidersSet: true, "difficulty_get": true, "difficulty_set": true, protocol.OpDifficultyJudgePick: true, protocol.OpScoresGet: true, protocol.OpScoresSet: true, protocol.OpScoresWeekly: true, protocol.OpWorkerRotationGet: true, protocol.OpPickerGet: true, protocol.OpCTOSettings: true, protocol.OpCTOEnter: true, protocol.OpCTOSend: true, protocol.OpCTONew: true, protocol.OpCTOEvents: true, protocol.OpCTOGraphHistory: true,
	protocol.OpHeldList: true, protocol.OpHeldAnswer: true,
	protocol.OpSignInOpen: true, protocol.OpSignInComplete: true, "sign_in_list": true, "sign_in_raise": true,
	protocol.OpLeadSet: true, protocol.OpTeamWrite: true, protocol.OpTeamHistory: true, protocol.OpGraphGet: true, protocol.OpCharterSet: true, protocol.OpCharterHistory: true, protocol.OpNodeAdd: true, protocol.OpNodeRemove: true, protocol.OpEdgeSet: true, protocol.OpEdgeRemove: true, protocol.OpEdgeHistory: true, protocol.OpDeliveryOwnerSet: true, protocol.OpMergerSet: true, protocol.OpGraphUndo: true, protocol.OpRepoCreate: true, protocol.OpRepoArchive: true, protocol.OpAgentCreate: true, protocol.OpAgentTombstone: true, protocol.OpScopeSet: true, protocol.OpScopeRemove: true,
	protocol.OpNodeSettingsGet: true, protocol.OpNodeSettingsSet: true,
	"settings_write": true, "settings_undo": true, "settings_owner_read": true, protocol.OpChangesOwnerFind: true, protocol.OpChangesOwnerReport: true,
	protocol.OpPlaybookGet: true, protocol.OpPlaybookSet: true,
	protocol.OpPermissionUndo:  true,
	protocol.OpPermissionApply: true,
	protocol.OpNudgeUndo:       true,
	protocol.OpNudgeStats:      true,
	protocol.OpFrictionFind:    true,
	protocol.OpFrictionMark:    true,
}

// The owner as a sender (owner_send, and the command line's worker calls) uses
// the same gate, operatorGate, but is served by dispatchActions (operator.go).

// TextProvider is implemented by an adapter that can read the conversation of
// its sessions itself (Claude Code reads the transcript file).
type TextProvider interface {
	Text(ctx context.Context, info protocol.SessionInfo, q protocol.TextQuery) (protocol.TextPage, error)
}

// textState holds the requests the daemon has sent to plugins and waits for.
type textState struct {
	mu   sync.Mutex
	wait map[string]chan textResult
}

type textResult struct {
	page protocol.TextPage
	err  string
}

func (t *textState) register(id string) chan textResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.wait == nil {
		t.wait = map[string]chan textResult{}
	}
	ch := make(chan textResult, 1)
	t.wait[id] = ch
	return ch
}

func (t *textState) drop(id string) {
	t.mu.Lock()
	delete(t.wait, id)
	t.mu.Unlock()
}

func (t *textState) deliver(id string, r textResult) {
	t.mu.Lock()
	ch := t.wait[id]
	t.mu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}

// operatorGate checks that a method that returns owner-only data may be
// called on this connection.
func (lc *localConn) operatorGate(method string) error {
	lc.mu.Lock()
	client, pid := lc.client, lc.peerPID
	lc.mu.Unlock()
	if client != "cli" {
		return fmt.Errorf("method %q is only available to the owner's command line or TUI, not to agent sessions", method)
	}
	if lc.d.inSession(pid) {
		return fmt.Errorf("method %q is not available from inside an agent session: run it in your own terminal. %s", method, insideSessionAdvice(method))
	}
	return nil
}

// insideSessionAdvice tells an agent that ran an owner-only command what to
// use instead. An agent that lacks the tool runs old agent-net tools.
func insideSessionAdvice(method string) string {
	const reconnect = "ask your user to reconnect agent-net (in Claude Code: /mcp)."
	if method == protocol.OpWorkerStart {
		return "agents start workers with the agent_worker_start tool, not this command. If you do not have agent_worker_start, your agent-net tools are out of date: " + reconnect
	}
	return "agents use the agent-net tools, not this command. If a tool you need is missing, your agent-net tools are out of date: " + reconnect
}

// insideSession tells whether pid is a local session's process or a descendant
// of one. It walks the parents of pid. A process that detaches itself from its
// parent (a double fork) is not caught; this is a barrier against an agent that
// simply runs the command line, not against a program that tries to hide.
func (d *Daemon) insideSession(pid int) bool {
	if pid <= 0 {
		return false
	}
	d.mu.Lock()
	sess := map[int]bool{}
	for _, ls := range d.sessions {
		if ls.info.PID > 1 {
			sess[ls.info.PID] = true
		}
	}
	d.mu.Unlock()
	// A worker is a session, but it is registered only when its plugin says
	// hello. Count its process from the moment it starts.
	d.wmu.Lock()
	for _, w := range d.workers {
		if w.pid > 1 {
			sess[w.pid] = true
		}
	}
	d.wmu.Unlock()
	if root := d.claudeCTO.root.Load(); root != nil {
		sess[root.pid] = true
	}
	// The private CTO is also an agent process, even though it is not a
	// registered session or worker. Its children cannot use the owner CLI.
	if root := int(d.cto.pid.Load()); root > 1 {
		sess[root] = true
	}
	// Interactive Codex threads share the control socket's server process.
	if a, ok := d.adapters["codex"].(*codexAdapter); ok {
		if root := a.serverPID(); root > 1 {
			sess[root] = true
		}
	}
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		if sess[p] || sessionHostCommand(processArgs(p)) {
			return true
		}
		parent, err := processParentPID(p)
		if err != nil {
			return false
		}
		p = parent
	}
	return false
}

// Also catch servers before the adapter connects, and shared OpenCode services
// whose session PID does not identify the process hosting shell commands.
func sessionHostCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	name := filepath.Base(args[0])
	return (name == "codex" && args[1] == "app-server") ||
		(name == "opencode" && args[1] == "serve")
}

// difficultyLevelNames lists the known difficulty levels in a stable order for
// an error a person reads.
func difficultyLevelNames() string {
	names := make([]string, 0, len(protocol.DifficultyMinimums))
	for name := range protocol.DifficultyMinimums {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// operatorActor is who an owner operation acts as: the CTO when it runs the
// operation, the owner otherwise (decision 0114, part 2).
func operatorActor(ctx context.Context) string {
	if actor := ctoActor(ctx); actor != "" {
		return actor
	}
	return "owner"
}

// dispatchOperator serves the owner-only methods. ok is false for any other method.
func (lc *localConn) dispatchOperator(ctx context.Context, method string, raw json.RawMessage) (res any, ok bool, err error) {
	if !operatorMethods[method] {
		return nil, false, nil
	}
	if err := lc.operatorGate(method); err != nil {
		return nil, true, err
	}
	return lc.dispatchOwner(ctx, method, raw)
}

// dispatchOwner runs a checked owner operation. Only the owner gate or the
// private CTO process-tree gate may call it.
func (lc *localConn) dispatchOwner(ctx context.Context, method string, raw json.RawMessage) (res any, ok bool, err error) {
	d := lc.d
	switch method {
	case protocol.OpRoleSetup:
		var request protocol.RoleSetupRequest
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &request) != nil {
			return nil, true, errors.New("bad role setup request")
		}
		result, err := d.setupRoleModels(ctx)
		if err == nil && request.Start {
			err = d.startSetupRoles(ctx, result.Added)
		}
		return result, true, err
	case "bar_edit":
		// The CTO's cto_op form of agent_bar_edit. It acts as the owner session,
		// which the gateway accepts for any project the call names.
		var p protocol.BarEditRequest
		if err := decodeCTOArgs(method, raw, &p, "project"); err != nil {
			return nil, true, err
		}
		sid, _, err := lc.callerSession("cli", method)
		if err != nil {
			return nil, true, err
		}
		out, err := lc.d.editBar(ctx, sid, p)
		if err != nil {
			return nil, true, err
		}
		return out, true, nil
	case "worker_floors_reset":
		result, err := d.call(ctx, "owner.worker_floors.reset", map[string]string{"owner": d.ownerSessionID()})
		return result, true, err
	case "operator_undo":
		result, err := lc.operatorUndo(ctx, raw)
		return result, true, err
	case "merger_restore":
		var p map[string]any
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad merger_restore arguments")
		}
		p["owner_session"] = d.ownerSessionID()
		p["by"] = ctoActor(ctx)
		result, err := d.call(ctx, "merger.restore", p)
		return result, true, err
	case "merger_get":
		var p map[string]any
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad merger_get arguments")
		}
		p["owner_session"] = d.ownerSessionID()
		result, err := d.call(ctx, "merger.get", p)
		return result, true, err
	case protocol.OpCTOGraphWindow:
		v, err := lc.ctoGraphWindow(raw)
		return v, true, err
	case "notification_settings":
		var p struct {
			Disabled *bool `json:"disabled"`
		}
		if err := decodeCTOArgs(method, raw, &p); err != nil {
			return nil, true, err
		}
		if p.Disabled != nil {
			if _, err := d.st.db.Exec(`INSERT INTO owner_preferences(key,value) VALUES('notifications_disabled',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, *p.Disabled); err != nil {
				return nil, true, err
			}
		}
		disabled, err := d.notificationsDisabled()
		return map[string]bool{"disabled": disabled}, true, err
	case "tools_mcp_add", "tools_mcp_remove", "tools_mcp_test", "tools_mcp_list":
		return lc.dispatchMCP(ctx, "cli", strings.TrimPrefix(method, "tools_"), raw)
	case "gateway_backup":
		res, err = d.call(withCallWait(ctx, 10*time.Minute), "gateway.backup", json.RawMessage(raw))
	case protocol.OpSkillArchiveUndo:
		var p struct {
			Change string `json:"change"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Change == "" {
			return nil, true, errors.New("skill_archive_undo needs change")
		}
		res, err = d.skillArchiveOwnerUndo(ctx, p.Change)
	case protocol.OpSkillsUndo:
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &p) != nil || p.ID == "" {
			return nil, true, errors.New("skills_undo needs id")
		}
		res, err = d.skillsOwnerUndo(ctx, p.ID)
	case "apps.public.status", "apps.public.on", "apps.public.off", "apps.public.url", "apps.public.restore":
		res, err = d.call(ctx, method, json.RawMessage(raw))
	case "apps.list", "apps.pending", "apps.approve", "apps.refuse", "apps.revoke", protocol.OpAppsRename, protocol.OpAppsGrant, protocol.OpAppsUngrant:
		var p struct {
			ID    string `json:"id"`
			Code  string `json:"code"`
			Kind  string `json:"kind"`
			Name  string `json:"name"`
			Grant string `json:"grant"`
		}
		write := method == "apps.approve" || method == "apps.refuse" || method == "apps.revoke" ||
			method == protocol.OpAppsRename || method == protocol.OpAppsGrant || method == protocol.OpAppsUngrant
		if write {
			if err := decodeCTOArgs(method, raw, &p, "id"); err != nil {
				return nil, true, err
			}
		} else if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad apps request")
		}
		frame := struct {
			AppID string `json:"app_id"`
			Code  string `json:"code"`
			Kind  string `json:"kind"`
			Name  string `json:"name"`
			Grant string `json:"grant,omitempty"`
		}{p.ID, p.Code, p.Kind, p.Name, p.Grant}
		res, err = d.call(ctx, method, frame)

	case "inbox_list":
		res, err = d.call(ctx, "inbox.list", struct{}{})
	case protocol.OpAsksList:
		res, err = d.call(ctx, "asks.list", struct{}{})
	case protocol.OpSignInOpen:
		var p protocol.SignInOpenRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad sign_in_open arguments")
		}
		if ctoActor(ctx) == "cto" {
			// The CTO opens a login on a named machine, on the owner's words.
			res, err = d.ctoSignInOpen(ctx, p)
			break
		}
		if p.AskID == "" {
			return nil, true, errors.New("sign_in_open needs an ask")
		}
		res, err = d.signInOpen(ctx, p)
	case protocol.OpSignInComplete:
		var p protocol.SignInOpenRequest
		if json.Unmarshal(raw, &p) != nil || p.AskID == "" {
			return nil, true, errors.New("sign_in_complete needs an ask")
		}
		err = d.signInComplete(ctx, p.AskID, p.Cancel)
		res = map[string]bool{"ok": err == nil}
	case "sign_in_list":
		res, err = d.call(ctx, "sign_in.list", struct{}{})
	case "sign_in_raise":
		var p protocol.SignInRaise
		if err := decodeCTOArgs(method, raw, &p, "provider"); err != nil {
			return nil, true, err
		}
		if p.Provider == "" {
			return nil, true, errors.New("sign_in_raise needs a provider")
		}
		if len(p.Command) != 0 {
			return nil, true, errors.New("sign_in_raise takes a provider, never a command")
		}
		res, err = d.call(ctx, "sign_in.raise", p)
	case protocol.OpChiefGet:
		res, err = d.call(ctx, "chief.get", struct{}{})
	case protocol.OpChiefSet:
		// Strict: a field chief_set does not have is refused by name, never
		// dropped, so a call can only do what it names. The gateway decides
		// whether the request names anything.
		var p protocol.ChiefSetRequest
		if len(raw) > 0 && string(raw) != "null" {
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, true, fmt.Errorf("bad chief_set request: %v", err)
			}
		}
		res, err = d.call(ctx, "chief.set", struct {
			protocol.ChiefSetRequest
			By string `json:"by"`
		}{p, operatorActor(ctx)})
	case protocol.OpAskHistory:
		var p protocol.AskHistoryRequest
		if json.Unmarshal(raw, &p) != nil || p.Ask == "" {
			return nil, true, errors.New("ask_history needs ask")
		}
		res, err = d.call(ctx, "ask.history", p)
	case protocol.OpAskUndo:
		var p protocol.AskUndoRequest
		if err := decodeCTOArgs(method, raw, &p, "ask"); err != nil {
			return nil, true, err
		}
		if p.Ask == "" {
			return nil, true, errors.New("ask_undo needs ask")
		}
		res, err = d.call(ctx, "ask.undo", map[string]any{"ask": p.Ask, "by": operatorActor(ctx)})
	case protocol.OpAskDiscuss:
		var p protocol.AskDiscussRequest
		if json.Unmarshal(raw, &p) != nil || p.Ask == "" {
			return nil, true, errors.New("ask_discuss needs ask")
		}
		res, err = d.call(ctx, "ask.discuss", map[string]any{"ask": p.Ask, "by": operatorActor(ctx)})
	case protocol.OpAskMark:
		var p protocol.AskMarkRequest
		if err := decodeCTOArgs(method, raw, &p); err != nil {
			return nil, true, err
		}
		if p.Validate() != nil {
			return nil, true, errors.New("ask_mark needs ask and mark (didnt_need_me or should_have_asked), and an optional why")
		}
		res, err = d.call(ctx, "ask.mark", map[string]any{"ask": p.Ask, "mark": p.Mark, "why": p.Why, "by": operatorActor(ctx)})
	case protocol.OpDeskSettle:
		var p struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(raw, &p) != nil || p.ID <= 0 {
			return nil, true, errors.New("desk_settle needs id")
		}
		res, err = d.call(ctx, "desk.settle", p)
	case protocol.OpDeskLog:
		var p protocol.DeskLogQuery
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad desk_log request")
		}
		res, err = d.call(ctx, "desk.log", p)
	case protocol.OpNudgeStats:
		var p protocol.NudgeStatsArgs
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad nudge_stats request")
		}
		if err = p.Validate(); err != nil {
			return nil, true, err
		}
		res, err = d.call(ctx, protocol.FrameNudgeStats, p)
	case protocol.OpFrictionFind:
		var p protocol.FrictionQuery
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad friction_find request")
		}
		if err = p.Validate(); err != nil {
			return nil, true, err
		}
		res, err = d.call(ctx, protocol.FrameFrictionFind, p)
	case protocol.OpFrictionMark:
		var p protocol.FrictionMarkRequest
		if err := decodeCTOArgs(method, raw, &p); err != nil {
			return nil, true, err
		}
		if err = p.Validate(); err != nil {
			return nil, true, err
		}
		// The CTO's marks are recorded as the CTO; the owner's as the owner.
		actor := operatorActor(ctx)
		if lc.cto {
			actor = "cto"
		}
		res, err = d.call(ctx, protocol.FrameFrictionMark, protocol.FrictionMarkCall{Actor: actor, FrictionMarkRequest: p})
	case "learned_list", "learned_answer", "learned_view":
		var p struct {
			ID        int64  `json:"suggestion_id"`
			Node      string `json:"node"`
			Action    string `json:"action"`
			IfVersion uint64 `json:"if_version,omitempty"`
			Lite      bool   `json:"lite,omitempty"`
		}
		var decodeErr error
		if method == "learned_answer" {
			decodeErr = decodeCTOArgs(method, raw, &p)
		} else if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			decodeErr = errors.New("bad learned request")
		}
		if decodeErr != nil {
			return nil, true, decodeErr
		}
		owner := d.ownerSessionID()
		if owner == "" {
			return nil, true, errors.New("owner session not registered")
		}
		// The gateway records the CTO as the actor of its own answer.
		actor := ""
		if lc.cto || ctoActor(ctx) == "cto" {
			actor = "cto"
		}
		frame := struct {
			Owner     string `json:"owner"`
			ID        int64  `json:"suggestion_id"`
			Node      string `json:"node"`
			Action    string `json:"action"`
			Actor     string `json:"actor,omitempty"`
			IfVersion uint64 `json:"if_version,omitempty"`
			Lite      bool   `json:"lite,omitempty"`
		}{owner, p.ID, p.Node, p.Action, actor, p.IfVersion, p.Lite}
		res, err = d.call(ctx, strings.Replace(method, "_", ".", 1), frame)
	case "inbox_answer":
		var p struct {
			ID     string             `json:"id"`
			Answer string             `json:"answer"`
			Scope  protocol.RuleScope `json:"scope"`
		}
		if err := decodeCTOArgs(method, raw, &p); err != nil {
			return nil, true, err
		}
		if p.ID == "" || p.Answer == "" {
			return nil, true, errors.New("inbox_answer needs id and answer")
		}
		// from_cto keeps the CTO from approving an app consent ask.
		fromCTO := lc.cto || ctoActor(ctx) != ""
		actor := ctoActor(ctx)
		if actor == "" {
			actor = "owner"
			if lc.cto {
				actor = "cto"
			}
		}
		res, err = d.call(ctx, "inbox.answer", map[string]any{"item_id": p.ID, "answer": p.Answer, "actor": actor, "from_cto": fromCTO, "scope": p.Scope})
	case "outcome_overrule":
		var p protocol.OutcomeResolution
		if err := decodeCTOArgs(method, raw, &p); err != nil {
			return nil, true, err
		}
		if p.ReplyID == "" {
			return nil, true, errors.New("outcome_overrule needs reply_id")
		}
		p.Owner = d.ownerSessionID()
		if p.Owner == "" {
			return nil, true, errors.New("owner session not registered")
		}
		res, err = d.call(ctx, "outcome.resolve", p)
	case "outcome_inbox":
		res, err = d.call(ctx, "query.outcome_inbox", struct{}{})
	case protocol.OpHeldList:
		res, err = d.call(ctx, "held.list", struct{}{})
	case protocol.OpHeldAnswer:
		var p protocol.HeldAnswer
		if err := decodeCTOArgs(method, raw, &p); err != nil {
			return nil, true, err
		}
		if p.MessageID == "" {
			return nil, true, errors.New("held_answer needs message_id")
		}
		res, err = d.call(ctx, "held.answer", p)
	case "usage":
		res, err = d.localUsage(ctx)
	case protocol.OpWorkerLog:
		var p protocol.WorkerLogQuery
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad worker log request")
		}
		res, err = d.call(ctx, "query.worker_log", p)
	case protocol.OpConversations:
		var p protocol.QueryConversations
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad conversations request")
		}
		res, err = d.call(ctx, "query.conversations", p)
	case protocol.OpConversationMessages:
		var p protocol.QueryConversationMessages
		if json.Unmarshal(raw, &p) != nil || p.ConversationID == "" {
			return nil, true, errors.New("conversation_messages needs conversation_id")
		}
		res, err = d.call(ctx, "query.conversation_messages", p)
	case "counts":
		var data json.RawMessage
		if data, err = d.call(ctx, "query.counts", struct{}{}); err == nil {
			res = data
		}
	case protocol.OpGraphTalk:
		res, err = d.call(ctx, "query.graph_talk", struct{}{})
	case protocol.OpGraphView:
		res, _, err = lc.dispatchGraphView(ctx, raw)
	case "usage_top":
		var p protocol.QueryUsageTop
		if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
			return nil, true, errors.New("usage_top needs session_id")
		}
		if d.sessionByID(p.SessionID) != nil {
			res, err = d.localUsageTop(ctx, p.SessionID)
		} else {
			var data json.RawMessage
			if data, err = d.call(ctx, "query.usage_top", p); err == nil {
				res = data
			}
		}
	case protocol.OpSessionTrust:
		// Only the owner at the keyboard may change what a session does on a
		// peer's request: the gate above refuses agent sessions, and no agent
		// tool exists for it (decision 0012).
		var q protocol.SessionTrustRequest
		if err := decodeCTOArgs(method, raw, &q, "session_id", "on"); err != nil {
			return nil, true, err
		}
		if q.SessionID == "" {
			return nil, true, errors.New("session_trust needs session_id")
		}
		if _, err = d.call(ctx, "session.trust", protocol.SessionTrust{SessionID: q.SessionID, On: q.On}); err == nil {
			res = protocol.SessionTrustResult{SessionID: q.SessionID, On: q.On}
			d.Logf("session.trust session=%s on=%t", q.SessionID, q.On)
		}
	case protocol.OpStandingOrders:
		// The owner's own words: a save from the TUI is source owner. The
		// same op restores an order when the owner undoes a removal.
		var q protocol.StandingOrdersRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		res, err = d.standingOrdersOp(ctx, q, protocol.StandingSourceOwner)
	case protocol.OpJudgmentAdd:
		var q protocol.JudgmentAddRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		data, err := d.call(ctx, "judgment.add", q)
		if err != nil {
			return nil, true, err
		}
		var o protocol.StandingOrder
		if err := json.Unmarshal(data, &o); err != nil {
			return nil, true, err
		}
		return o, true, nil
	case protocol.OpFolderMCP:
		// The choice for a project's .mcp.json servers decides which programs
		// Claude Code runs: only the owner at the keyboard, or the CTO on the
		// owner's instruction, makes it.
		var q protocol.FolderMCPRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		res, err = d.folderMCP(ctx, q)
	case protocol.OpProcessStop:
		// Stopping a process cannot be undone: only the owner at the keyboard,
		// or the CTO on the owner's instruction, may do it (decision 0102).
		var q protocol.ProcessStopRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		res, err = d.processStop(ctx, q)
	case protocol.OpFolderTrust:
		// Claude Code trust is a security setting: only the owner at the
		// keyboard, or the CTO on the owner's instruction, changes it.
		var q protocol.FolderTrustRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		res, err = d.folderTrust(ctx, q)
	case protocol.OpSessionAgentList:
		res, err = d.call(ctx, "session.agent.list", map[string]string{"owner_session": d.ownerSessionID()})
	case protocol.OpSessionAgentSet:
		var q protocol.SessionAgentSetRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		if q.SessionID == "" || q.Agent == "" {
			return nil, true, errors.New("session_agent_set needs session_id and agent")
		}
		q.Owner = d.ownerSessionID()
		if res, err = d.call(ctx, "session.agent.set", q); err == nil {
			d.policy.forget(q.SessionID)
		}
	case protocol.OpNotesCopy:
		var q protocol.NotesCopyRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		if q.FromAgent == "" || q.ToAgent == "" {
			return nil, true, errors.New("notes_copy needs from_agent and to_agent")
		}
		q.Owner = d.ownerSessionID()
		res, err = d.call(ctx, "notes.copy", q)
	case protocol.OpNotesUncopy:
		var q protocol.NotesUncopyRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		if len(q.IDs) == 0 {
			return nil, true, errors.New("notes_uncopy needs ids")
		}
		q.Owner = d.ownerSessionID()
		res, err = d.call(ctx, "notes.uncopy", q)
	case "session_pin":
		var q struct {
			SessionID string `json:"session_id"`
			Pinned    bool   `json:"pinned"`
			Owner     string `json:"owner"`
		}
		if err := decodeCTOArgs(method, raw, &q, "session_id", "pinned"); err != nil {
			return nil, true, err
		}
		if q.SessionID == "" {
			return nil, true, errors.New("session_pin needs session_id")
		}
		q.Owner = d.ownerSessionID()
		res, err = d.call(ctx, "session.pin", q)
	case "sessions_archive", "sessions_archived":
		var q struct {
			protocol.SessionsArchiveRequest
			Owner string `json:"owner"`
		}
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		q.Owner = d.ownerSessionID()
		frame := "sessions.archive"
		if method == "sessions_archived" {
			frame = "sessions.archived"
		}
		res, err = d.call(ctx, frame, q)
	case protocol.OpSessionRename:
		// Only the owner names a session (decision 0033). The gate above
		// refuses agent sessions; the CTO uses the same method through its
		// process-tree-checked channel.
		var q protocol.SessionRenameRequest
		if err := decodeCTOArgs(method, raw, &q, "session_id", "name"); err != nil {
			return nil, true, err
		}
		if q.SessionID == "" {
			return nil, true, errors.New("session_rename needs session_id")
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "session.rename", q); err == nil {
			var out protocol.SessionRenameResult
			if err = json.Unmarshal(data, &out); err == nil {
				res = out
				d.Logf("session.rename session=%s name=%q", q.SessionID, out.Name)
			}
		}
	case protocol.OpSessionReplace:
		// The owner, or the CTO through its checked operator path, repairs a
		// restart that already orphaned a session. Both sessions must be on
		// the same machine; the gateway passes the old session's node.
		var q protocol.SessionReplaceRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		if q.OldSession == "" || q.NewSession == "" {
			return nil, true, errors.New("session_replace needs old_session and new_session")
		}
		actor := "owner"
		if ctoActor(ctx) == "cto" {
			actor = "cto"
		}
		var remoteNode string
		if d.sessionByID(q.OldSession) == nil {
			if a, viewErr := d.agentView(ctx, q.OldSession); viewErr == nil && a.Online && a.NodeID != "" && a.NodeID != d.cfg.NodeID {
				remoteNode = a.NodeID
			}
		}
		var data json.RawMessage
		if data, err = d.call(ctx, protocol.FrameSessionOwnerReplace, protocol.SessionOwnerReplace{
			OldSession: q.OldSession, NewSession: q.NewSession, Actor: actor,
		}); err == nil {
			var out protocol.SessionReplaceResult
			if err = json.Unmarshal(data, &out); err == nil {
				res = out
				if out.Moved {
					// A hand-back and a step task name the caller from the
					// daemon's own worker records too.
					d.reassignWorkerOwner(q.OldSession, q.NewSession)
					// Stop the old session best-effort if it is still running so
					// it does not stay live alongside its replacement.
					if ls := d.sessionByID(q.OldSession); ls != nil {
						if d.infoOf(ls).Status != "exited" {
							if _, stopErr := d.sessionStop(ctx, q.OldSession); stopErr != nil {
								d.Logf("session.replace: could not stop old session %s: %v", q.OldSession, stopErr)
							}
						}
					} else if remoteNode != "" && d.ownerSessionID() != "" {
						if _, stopErr := d.relaySessionStop(ctx, remoteNode, q.OldSession); stopErr != nil {
							d.Logf("session.replace: could not stop old session %s on %s: %v", q.OldSession, remoteNode, stopErr)
						}
					}
				}
				d.Logf("session.replace old=%s new=%s moved=%t", q.OldSession, q.NewSession, out.Moved)
			}
		}
	case protocol.OpProjectDisplayGet, protocol.OpProjectDisplaySet:
		var q protocol.ProjectDisplayRequest
		var decodeErr error
		if method == protocol.OpProjectDisplaySet {
			decodeErr = decodeCTOArgs(method, raw, &q, "project", "name")
		} else if json.Unmarshal(raw, &q) != nil {
			decodeErr = errors.New("project display needs project")
		}
		if decodeErr != nil {
			return nil, true, decodeErr
		}
		if q.Project == "" {
			return nil, true, errors.New("project display needs project")
		}
		frame := "project.display.get"
		if method == protocol.OpProjectDisplaySet {
			frame = "project.display.set"
		}
		res, err = d.call(ctx, frame, q)
	case protocol.OpMachineRemove, protocol.OpMachineRotate:
		var q struct {
			NodeID string `json:"node_id"`
		}
		if err := decodeCTOArgs(method, raw, &q, "node_id"); err != nil {
			return nil, true, err
		}
		if q.NodeID == "" {
			return nil, true, errors.New("machine action needs node_id")
		}
		actor := ctoActor(ctx)
		if actor == "" {
			actor = "owner"
		}
		typ := "machine.remove"
		if method == protocol.OpMachineRotate {
			typ = "machine.rotate"
		}
		res, err = d.call(ctx, typ, map[string]string{"node_id": q.NodeID, "owner": d.ownerSessionID(), "actor": actor})
	case protocol.OpNodeRename:
		// The name appears in every machine's lists, hints and plans: only the
		// owner at the keyboard sets it (decision 0015). The gate above refuses
		// agent sessions, and no agent tool exists for it.
		var q protocol.NodeRenameRequest
		if err := decodeCTOArgs(method, raw, &q, "node_id", "name"); err != nil {
			return nil, true, err
		}
		if q.NodeID == "" {
			return nil, true, errors.New("node_rename needs node_id")
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "node.rename", protocol.NodeRename{NodeID: q.NodeID, Name: q.Name}); err == nil {
			var out protocol.NodeRenameResult
			if err = json.Unmarshal(data, &out); err == nil {
				res = out
				if q.NodeID == d.cfg.NodeID && out.Hostname != "" {
					d.shownHost.Store(out.Hostname)
				}
				d.Logf("node.rename node=%s name=%q", q.NodeID, out.DisplayName)
			}
		}
	case "settings_owner_read":
		var q protocol.SettingsReadRequest
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
			return nil, true, errors.New("bad settings read")
		}
		res, err = d.readSettings(ctx, q, true)
	case protocol.OpChangesOwnerFind:
		var q protocol.ChangesFindArgs
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
			return nil, true, errors.New("bad changes_owner_find params")
		}
		res, err = d.changesFind(ctx, "", q)
	case protocol.OpChangesOwnerReport:
		var q protocol.ChangesOwnerReportArgs
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		if err := q.Validate(); err != nil {
			return nil, true, err
		}
		res, err = d.ownerStepReport(ctx, q)
	case "settings_write":
		var q protocol.SettingsWrite
		if json.Unmarshal(raw, &q) != nil {
			return nil, true, errors.New("bad settings write")
		}
		q.By = "owner"
		res, err = d.call(ctx, "settings.write", q)
	case "settings_undo":
		var q protocol.SettingsUndo
		if json.Unmarshal(raw, &q) != nil {
			return nil, true, errors.New("bad settings undo")
		}
		q.By = "owner"
		res, err = d.call(ctx, "settings.undo", q)
	case protocol.OpNudgeUndo:
		var q protocol.NudgeUndoArgs
		if json.Unmarshal(raw, &q) != nil || q.Pause() == "" || q.Receipt == "" {
			return nil, true, errors.New("nudge_undo needs id and receipt")
		}
		res, err = d.call(ctx, protocol.FrameNudgeUndo, protocol.NudgeUndoRequest{PauseID: q.Pause(), Receipt: q.Receipt, Owner: d.ownerSessionID()})
	case protocol.OpPermissionUndo:
		var q protocol.PermissionUndoRequest
		if err := decodeCTOArgs(method, raw, &q, "id"); err != nil {
			return nil, true, err
		}
		if q.ID == "" {
			return nil, true, errors.New("permission_undo needs id")
		}
		res, err = d.call(ctx, "permission.undo", protocol.PermissionUndoFrame{ProposalID: q.ID, Owner: d.ownerSessionID()})
	case protocol.OpPermissionApply:
		// Strict: a field the op does not have is refused by name, so a call
		// can only grant what it names. The gateway requires owner_words.
		var q protocol.PermissionOwnerApplyRequest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&q); err != nil {
			return nil, true, fmt.Errorf("bad permission_apply arguments: %w", err)
		}
		frame := protocol.PermissionOwnerApplyFrame{Request: q, Owner: d.ownerSessionID(), Session: d.ownerSessionID(), By: "owner", WordsVerified: true}
		if ctoActor(ctx) == "cto" {
			// The CTO's quote counts only when the owner typed it in the CTO chat.
			frame.By, frame.WordsVerified = "cto", false
			if cto := d.ctoSessionID(); cto != "" {
				frame.Session = cto
			}
			words, verr := d.verifyCTOOwnerWords(q.OwnerWords)
			if verr != nil {
				return nil, true, verr
			}
			frame.WordsVerified = words != ""
		}
		res, err = d.call(ctx, protocol.FramePermissionOwnerApply, frame)
	case protocol.OpPermissionReject:
		var q protocol.PermissionRejectRequest
		if err := decodeCTOArgs(method, raw, &q, "id", "owner_words"); err != nil {
			return nil, true, err
		}
		frame := protocol.PermissionRejectFrame{Request: q, Owner: d.ownerSessionID(), By: "owner", WordsVerified: true}
		if ctoActor(ctx) == "cto" {
			frame.By, frame.WordsVerified = "cto", false
			words, verr := d.verifyCTOOwnerWords(q.OwnerWords)
			if verr != nil {
				return nil, true, verr
			}
			frame.WordsVerified = words != ""
		}
		res, err = d.call(ctx, protocol.FramePermissionReject, frame)
	case protocol.OpDeliveryOwnerAnswer:
		var q protocol.DeliveryOwnerAnswerRequest
		if err := decodeCTOArgs(method, raw, &q, "change", "step", "result", "commit", "owner_words"); err != nil {
			return nil, true, err
		}
		if err := q.Validate(); err != nil {
			return nil, true, err
		}
		frame := protocol.DeliveryOwnerAnswerFrame{Request: q, Owner: d.ownerSessionID(), Session: d.ownerSessionID(), By: "owner", WordsVerified: true}
		if ctoActor(ctx) == "cto" {
			cto := d.ctoSessionID()
			if cto == "" {
				return nil, true, errors.New("the CTO session is not registered yet")
			}
			frame.By, frame.Session, frame.WordsVerified = "cto", cto, false
			words, verr := d.verifyCTOOwnerWords(q.OwnerWords)
			if verr != nil {
				return nil, true, verr
			}
			frame.WordsVerified = words != ""
		}
		var out protocol.StepReportResult
		if err := d.deliveryCall(ctx, protocol.FrameDeliveryOwnerAnswer, frame, &out); err != nil {
			return nil, true, err
		}
		res = out
	case protocol.OpLoopResume:
		var q protocol.LoopResumeRequest
		if err := decodeCTOArgs(method, raw, &q, "node", "owner_words"); err != nil {
			return nil, true, err
		}
		frame := protocol.LoopResumeFrame{Node: q.Node, Owner: d.ownerSessionID(), By: "owner", OwnerWords: q.OwnerWords, WordsVerified: true}
		if ctoActor(ctx) == "cto" {
			frame.By, frame.WordsVerified = "cto", false
			words, verr := d.verifyCTOOwnerWords(q.OwnerWords)
			if verr != nil {
				return nil, true, verr
			}
			frame.WordsVerified = words != ""
		}
		res, err = d.call(ctx, protocol.FrameLoopResume, frame)
	case protocol.OpLeadSet, protocol.OpTeamWrite, protocol.OpTeamHistory, protocol.OpGraphGet, protocol.OpCharterSet, protocol.OpCharterHistory, protocol.OpNodeAdd, protocol.OpNodeRemove, protocol.OpEdgeSet, protocol.OpEdgeRemove, protocol.OpEdgeHistory, protocol.OpDeliveryOwnerSet, protocol.OpMergerSet, protocol.OpGraphUndo, protocol.OpRepoCreate, protocol.OpRepoArchive, protocol.OpAgentCreate, protocol.OpAgentTombstone, protocol.OpScopeSet, protocol.OpScopeRemove, protocol.OpNodeSettingsGet, protocol.OpNodeSettingsSet:
		// The gateway trusts enrolled daemons as with session.policy. The owner
		// gate above must run before forwarding any graph operation.
		frame := strings.ReplaceAll(method, "_", ".")
		var p map[string]any
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad graph params")
		}
		if p == nil {
			p = map[string]any{}
		}
		if method == protocol.OpLeadSet || method == protocol.OpScopeSet || method == protocol.OpScopeRemove || method == protocol.OpRepoCreate || method == protocol.OpRepoArchive || method == protocol.OpAgentCreate || method == protocol.OpAgentTombstone {
			p["owner"] = d.ownerSessionID()
		}
		if method == protocol.OpMergerSet || method == protocol.OpTeamWrite || method == protocol.OpTeamHistory {
			p["owner_session"] = d.ownerSessionID()
		}
		delete(p, "by")
		if actor := ctoActor(ctx); actor != "" {
			p["by"] = actor
		} else if method == protocol.OpNodeSettingsSet {
			p["by"] = "owner"
		}
		var params any = p
		var q protocol.GraphUndoRequest
		if method == protocol.OpGraphUndo {
			if len(raw) > 0 && json.Unmarshal(raw, &q) != nil {
				return nil, true, errors.New("bad graph_undo params")
			}
			params = protocol.GraphUndoFrame{HistoryID: q.ID}
			if actor := ctoActor(ctx); actor != "" {
				params = map[string]any{"history_id": q.ID, "by": actor}
			}
		}
		var data json.RawMessage
		if data, err = d.call(ctx, frame, params); err == nil {
			res = data
			if method == protocol.OpGraphUndo && q.CTOChat {
				err = d.recordCTOAction(fmt.Sprintf("undid graph change #%d", q.ID))
			}
		}
	case protocol.OpPlaybookGet:
		// The note goes into every start result for that model: only the
		// owner at the keyboard writes or reads it (decision 0022). A read
		// sends the get request alone; the gateway refuses a set-only field
		// such as note here.
		var q protocol.PlaybookGetRequest
		if err := json.Unmarshal(raw, &q); err != nil || strings.TrimSpace(q.Model) == "" {
			return nil, true, errors.New(method + " needs model")
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "playbook.get", q); err == nil {
			res = data
			d.Logf("playbook.get model=%s harness=%s", q.Model, q.Harness)
		}
	case protocol.OpPlaybookSet:
		var q protocol.PlaybookSetRequest
		if err := decodeCTOArgs(method, raw, &q, "model", "note"); err != nil {
			return nil, true, err
		}
		if strings.TrimSpace(q.Model) == "" {
			return nil, true, errors.New(method + " needs model")
		}
		var data json.RawMessage
		if data, err = d.call(ctx, "playbook.set", q); err == nil {
			res = data
			d.Logf("playbook.set model=%s harness=%s", q.Model, q.Harness)
		}
	case protocol.OpCTOGraphHistory:
		var p struct {
			After int64 `json:"after"`
		}
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &p) != nil || p.After < 0 {
			return nil, true, errors.New("bad CTO graph history params")
		}
		res, err = d.call(ctx, "cto.graph_history", p)
	case protocol.OpCTOEnter:
		plan, err := d.ctoEnter(ctx)
		return plan, true, err
	case protocol.OpCTOSend:
		var q protocol.CTOSendRequest
		if err := decodeCTOArgs(method, raw, &q, "text"); err != nil {
			return nil, true, err
		}
		if strings.TrimSpace(q.Text) == "" {
			return nil, true, errors.New("cto_send needs text")
		}
		if err := d.registerCTOSession(ctx); err != nil {
			return nil, true, err
		}
		// The CTO can reach this call itself through the operator tool. Its own
		// text is never the owner's words: it is neither remembered as them nor
		// labelled as them.
		if ctoActor(ctx) == "" {
			d.rememberCTOOwnerPrompt(q.Text)
			res, err = d.ctoSendTracked(ctx, q.Text, "owner")
		} else {
			res, err = d.ctoSendTriggered(ctx, q.Text, "", "cto")
		}
	case protocol.OpCTONew:
		res, err = d.ctoNew(ctx)
	case protocol.OpCTOEvents:
		var q protocol.CTOEventsRequest
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &q) != nil {
			return nil, true, errors.New("cto_events needs a valid index")
		}
		res, err = d.ctoEvents(ctx, q.Index)
	case protocol.OpTimeZoneGet:
		res, err = d.timeZoneSettings(ctx, nil)
	case protocol.OpTimeZoneSet:
		var q protocol.TimeZoneSettings
		if err := decodeCTOArgs(method, raw, &q, "name"); err != nil {
			return nil, true, err
		}
		name, zerr := protocol.NormalizeTimeZone(q.Name)
		if zerr != nil {
			return nil, true, zerr
		}
		res, err = d.timeZoneSettings(ctx, &protocol.TimeZoneSettings{Name: name})
	case protocol.OpProtectedProvidersGet:
		res, err = d.protectedProviders(ctx, nil)
	case protocol.OpProtectedProvidersSet:
		var q protocol.ProtectedProviders
		if err := decodeCTOArgs(method, raw, &q, "protected_providers"); err != nil {
			return nil, true, err
		}
		res, err = d.protectedProviders(ctx, &q)
	case protocol.OpWorkerFloorsGet:
		res, err = d.workerFloors(ctx, nil)
	case protocol.OpWorkerFloorsSet:
		var q protocol.WorkerFloors
		if err := decodeCTOArgs(method, raw, &q, "floors"); err != nil {
			return nil, true, err
		}
		res, err = d.workerFloors(ctx, &q)
	case protocol.OpWorkerProvidersGet:
		res, err = d.workerProviders(ctx, nil)
	case protocol.OpWorkerProvidersSet:
		var q protocol.WorkerProviders
		if err := decodeCTOArgs(method, raw, &q, "providers"); err != nil {
			return nil, true, err
		}
		res, err = d.workerProviders(ctx, &q)
	case "difficulty_get":
		res, err = d.difficulty(ctx, nil)
	case "difficulty_set":
		var q map[string]float64
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		if q == nil {
			return nil, true, errors.New("bad difficulty settings")
		}
		// The known levels are the keys of protocol.DifficultyMinimums, the
		// default minimum capability scores that DifficultyFit and the
		// gateway's PUT /v1/difficulty read. From the CTO an empty map would
		// clear every override, so it must name at least one level.
		if lc.cto || ctoActor(ctx) == "cto" {
			if len(q) == 0 {
				return nil, true, errors.New("difficulty_set needs at least one level")
			}
			for level := range q {
				if _, ok := protocol.DifficultyMinimums[level]; !ok {
					return nil, true, fmt.Errorf("difficulty_set has no level %q; use one of %s", level, difficultyLevelNames())
				}
			}
		}
		res, err = d.difficulty(ctx, q)
	case protocol.OpScoresGet:
		res, err = d.scores(ctx, nil)
	case protocol.OpScoresWeekly:
		res, err = d.scoresWeekly(ctx)
	case protocol.OpWorkerRotationGet:
		res, err = d.workerRotation(ctx)
	case protocol.OpPickerGet:
		res, err = d.pickerAnalysis(ctx)
	case protocol.OpDifficultyJudgePick:
		var req protocol.DifficultyJudgePick
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &req) != nil {
			return nil, true, errors.New("bad judge pick request")
		}
		res, err = d.pickDifficultyJudge(ctx, req.ExcludeProvider)
	case protocol.OpScoresSet:
		var q protocol.ScoreSetRequest
		if err := decodeCTOArgs(method, raw, &q, "model"); err != nil {
			return nil, true, err
		}
		res, err = d.scores(ctx, &q)
	case protocol.OpCTOSettings:
		var q protocol.CTOSettingsRequest
		if err := decodeCTOArgs(method, raw, &q); err != nil {
			return nil, true, err
		}
		if q.Model != nil || q.Harness != nil {
			if err = d.ctoDispatchMu.lock(ctx); err != nil {
				return nil, true, err
			}
			var settings protocol.CTOSettings
			settings, err = d.ctoSettings(ctx, q)
			if err == nil {
				_, err = d.prepareCTOHarness(settings)
			}
			d.ctoDispatchMu.unlock()
			res = settings
		} else {
			res, err = d.ctoSettings(ctx, q)
		}
	case protocol.OpIntegrationsGet:
		// The answers decide which integrations run with the owner's consent:
		// only the owner at the keyboard reads or changes them (decision 0021).
		res, err = d.integrationsGet(ctx)
		if err == nil {
			d.Logf("integrations.get")
		}
	case protocol.OpIntegrationsSet:
		var in protocol.Integrations
		if err := decodeCTOArgs(method, raw, &in); err != nil {
			return nil, true, err
		}
		res, err = d.integrationsSet(ctx, in)
		if err == nil {
			d.Logf("integrations.set on=%d", in.CountOn())
		}
	case "onboarding_get":
		res, err = d.call(ctx, "onboarding.get", nil)
	case "onboarding_set":
		var update protocol.OnboardingUpdate
		if err := decodeCTOArgs(method, raw, &update); err != nil {
			return nil, true, err
		}
		res, err = d.call(ctx, "onboarding.set", update)
	case "onboarding_restart":
		res, err = d.call(ctx, "onboarding.restart", nil)
	case protocol.OpHiddenGet:
		res, err = d.call(ctx, "hidden.get", nil)
	case protocol.OpHiddenSet:
		var items protocol.HiddenItems
		if err := decodeCTOArgs(method, raw, &items); err != nil {
			return nil, true, err
		}
		// Both lists carry omitempty, so the TUI sends {} for a real clear.
		// From the CTO that would also make a dropped field clear both lists.
		// The CTO names a list instead, and [] clears it.
		if (lc.cto || ctoActor(ctx) == "cto") && !ctoArgsHasAny(raw, "sessions", "nodes") {
			return nil, true, errors.New("hidden_set needs sessions or nodes; name a list with [] to clear it")
		}
		res, err = d.call(ctx, "hidden.set", items)
	case protocol.OpSessionStop:
		var p protocol.SessionResumeRequest
		if err := decodeCTOArgs(method, raw, &p, "session_id"); err != nil {
			return nil, true, err
		}
		if p.SessionID == "" {
			return nil, true, errors.New("session_stop needs session_id")
		}
		res, err = d.sessionStop(ctx, p.SessionID)
	case protocol.OpSessionResume:
		var p protocol.SessionResumeRequest
		if err := decodeCTOArgs(method, raw, &p, "session_id"); err != nil {
			return nil, true, err
		}
		if p.SessionID == "" {
			return nil, true, errors.New("session_resume needs session_id")
		}
		res, err = d.sessionResume(ctx, p)
		if err == nil {
			d.recordOwnerOpen(ctx, p.SessionID)
		}
	case protocol.OpSessionStart:
		var p protocol.SessionStartRequest
		if json.Unmarshal(raw, &p) != nil {
			return nil, true, errors.New("bad session_start params")
		}
		// Only the owner's own TUI or command line gives the owner's words. The CTO
		// reaches this call through its operator tool and an approved
		// proposal: its text is never the owner's.
		p.FirstMessageByOwner = !lc.cto && ctoActor(ctx) == ""
		res, err = d.sessionStart(ctx, p)
	case protocol.OpSessionStartGet:
		var p protocol.SessionStartQuery
		if json.Unmarshal(raw, &p) != nil || p.ID == "" {
			return nil, true, errors.New("session_start_get needs id")
		}
		res, err = d.sessionStartGet(ctx, p.ID)

	case "session_enter":
		var q protocol.EnterRequest
		if err := decodeCTOArgs(method, raw, &q, "session_id"); err != nil {
			return nil, true, err
		}
		if q.SessionID == "" {
			return nil, true, errors.New("session_enter needs session_id")
		}
		res, err = d.enterPlan(ctx, q)
		if err != nil {
			d.logEnterFailure(protocol.EnterFailure{SessionID: q.SessionID, Err: err.Error()})
		} else if plan, ok := res.(protocol.EnterPlan); ok && (plan.OpenedHerdr || len(plan.Argv) > 0 || plan.HerdrPane != "") {
			d.recordOwnerOpen(ctx, q.SessionID)
		}
	case "session_text":
		var q protocol.TextQuery
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			return nil, true, errors.New("session_text needs session_id")
		}
		if handled, page, ctoErr := d.ctoTextQuery(ctx, q); handled {
			res, err = page, ctoErr
		} else if d.sessionByID(q.SessionID) != nil {
			res, err = d.localText(ctx, q)
		} else {
			var data json.RawMessage
			if data, err = d.call(ctx, "query.session_text", q); err == nil {
				// The other machine masked and cut the text. Do it again here:
				// that machine may run an older or a broken agent-net.
				var page protocol.TextPage
				if err = json.Unmarshal(data, &page); err == nil {
					res = sanitizePage(page, q)
				}
			}
		}
	}
	if err != nil && strings.Contains(err.Error(), "unknown frame type") {
		err = errors.New("the gateway is too old for this (upgrade the gateway with agent-net upgrade)")
	}
	return res, true, err
}

// localText reads the conversation of a session on this machine.
func (d *Daemon) localText(ctx context.Context, q protocol.TextQuery) (protocol.TextPage, error) {
	ls := d.sessionByID(q.SessionID)
	if ls == nil {
		return protocol.TextPage{}, errors.New("no such session on this machine")
	}
	info := d.infoOf(ls)
	q.Limit = clampLimit(q.Limit)
	var page protocol.TextPage
	var err error
	d.mu.Lock()
	conn := ls.conn
	d.mu.Unlock()
	switch {
	case conn != nil:
		page, err = conn.askText(ctx, q, d.textTimeout())
	default:
		tp, ok := d.adapters[info.Harness].(TextProvider)
		if !ok {
			return protocol.TextPage{}, fmt.Errorf("reading the conversation is not supported for %s sessions", info.Harness)
		}
		page, err = tp.Text(ctx, info, q)
	}
	if err != nil {
		return protocol.TextPage{}, err
	}
	page = sanitizePage(page, q)
	page.SessionID, page.Harness = q.SessionID, info.Harness
	return page, nil
}

func (d *Daemon) textTimeout() time.Duration {
	if d.cfg.TextTimeout > 0 {
		return d.cfg.TextTimeout
	}
	return 8 * time.Second
}

func clampLimit(n int) int {
	switch {
	case n <= 0:
		return protocol.TextDefaultLimit
	case n > protocol.TextMaxLimit:
		return protocol.TextMaxLimit
	}
	return n
}

// sanitizePage enforces the rules on what a harness returned: only known
// roles, secrets masked, control characters removed, text cut, at most the
// asked number of entries (the newest ones). The harness cannot widen them.
func sanitizePage(p protocol.TextPage, q protocol.TextQuery) protocol.TextPage {
	limit := clampLimit(q.Limit)
	out := protocol.TextPage{Older: p.Older, Newer: p.Newer, Entries: make([]protocol.TextEntry, 0, min(len(p.Entries), limit))}
	for _, e := range p.Entries {
		if !protocol.ValidRole(e.Role) {
			continue
		}
		e.Text, e.Cut = cleanText(e.Text, e.Cut)
		e.Tool = redact.Cut(cleanLine(e.Tool), 80)
		e.Cursor = redact.Cut(e.Cursor, 200)
		out.Entries = append(out.Entries, e)
	}
	if len(out.Entries) > limit {
		out.Entries = out.Entries[len(out.Entries)-limit:]
		if q.After == "" {
			out.Older = out.Entries[0].Cursor
		}
	}
	out.Older, out.Newer = redact.Cut(out.Older, 200), redact.Cut(out.Newer, 200)
	return out
}

// cleanText masks secrets, removes control characters (keeping newlines and
// tabs) and cuts the text to protocol.TextMaxEntry characters.
func cleanText(s string, wasCut bool) (string, bool) {
	s = redact.Secrets(s)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len([]rune(s)) > protocol.TextMaxEntry {
		return redact.Cut(s, protocol.TextMaxEntry), true
	}
	return s, wasCut
}

func cleanLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)), " ")
}

// askText asks the plugin behind this connection for a page of the session and
// waits for the answer. A plugin from before this feature ignores the request,
// so the wait ends with a hint.
func (lc *localConn) askText(ctx context.Context, q protocol.TextQuery, wait time.Duration) (protocol.TextPage, error) {
	id := protocol.NewID("t")
	ch := lc.tx.register(id)
	defer lc.tx.drop(id)
	params := map[string]any{"req": id, "limit": q.Limit}
	if q.Before != "" {
		params["before"] = q.Before
	}
	if q.After != "" {
		params["after"] = q.After
	}
	if err := lc.writeJSON(map[string]any{"method": "session_text", "params": params}); err != nil {
		return protocol.TextPage{}, errors.New("the plugin connection is lost")
	}
	select {
	case r := <-ch:
		if r.err != "" {
			return protocol.TextPage{}, errors.New(r.err)
		}
		return r.page, nil
	case <-time.After(wait):
		return protocol.TextPage{}, errors.New("the plugin did not answer; it may be from an older agent-net (Pi: run /reload in the session, OpenCode: it reloads the plugin file by itself)")
	case <-lc.closed:
		return protocol.TextPage{}, errors.New("the plugin connection closed")
	case <-ctx.Done():
		return protocol.TextPage{}, ctx.Err()
	}
}

// textResult handles a plugin's answer to a session_text push.
func (lc *localConn) textResult(raw json.RawMessage) {
	var p struct {
		Req   string            `json:"req"`
		OK    bool              `json:"ok"`
		Error string            `json:"error"`
		Page  protocol.TextPage `json:"page"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Req == "" {
		return
	}
	r := textResult{page: p.Page}
	if !p.OK {
		r.err = redact.Cut(cleanLine(p.Error), 200)
		if r.err == "" {
			r.err = "the plugin could not read the session"
		}
	}
	lc.tx.deliver(p.Req, r)
}

// ---- requests from the gateway ----

// onOperatorFrame answers a request the gateway relays from another machine's
// owner: node.usage_top and node.session_text.
func (d *Daemon) onOperatorFrame(ctx context.Context, l *link, typ, id string, raw []byte) {
	var data any
	var err error
	switch typ {
	case "node.owner_ui.request":
		var q protocol.UIRequest
		if json.Unmarshal(raw, &q) != nil {
			err = errors.New("bad owner UI request")
		} else {
			data = map[string]bool{"shown": d.showLocalOwnerEnter(q)}
		}
	case "node.machine_log":
		var q protocol.MachineLogRequest
		if json.Unmarshal(raw, &q) != nil {
			err = errors.New("bad machine log request")
		} else {
			data, err = d.readMachineLog(ctx, q)
		}
	case "node.message_body":
		var q protocol.QueryMessage
		if json.Unmarshal(raw, &q) != nil || q.MessageID == "" {
			err = errors.New("not found")
			break
		}
		var body json.RawMessage
		body, err = d.call(ctx, "query.message", q)
		if err != nil {
			err = errors.New("not found")
			break
		}
		var result protocol.MessageResult
		if json.Unmarshal(body, &result) != nil || result.Message.ID != q.MessageID || len(result.Message.Body) > protocol.MaxBodyBytes {
			err = errors.New("not found")
			break
		}
		data = result
	case "node.machine_processes":
		var q protocol.MachineGetRequest
		if err = json.Unmarshal(raw, &q); err == nil {
			var rows []protocol.MachineProcessRow
			var roster *protocol.ClaudeRosterStatus
			rows, roster, err = d.machineProcesses(ctx, q.Sort, readProcTable)
			data = protocol.MachineProcessReply{Items: rows, Roster: roster}
		}
	case "node.machine_usage":
		data = d.machineUsageNow(ctx)
	case "node.usage_top":
		var p protocol.QueryUsageTop
		if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
			err = errors.New("bad node.usage_top")
		} else {
			data, err = d.localUsageTop(ctx, p.SessionID)
		}
	case "node.session_enter":
		var q protocol.EnterRequest
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			err = errors.New("bad node.session_enter")
		} else if handled, plan, ctoErr := d.ctoEnterQuery(ctx, q, true); handled {
			data, err = plan, ctoErr
		} else {
			q.HerdrSocket, q.HerdrPaneID = "", "" // the asker's terminal means nothing here
			data, err = d.localEnter(ctx, q, true)
		}
	case protocol.FrameSignInPrepare:
		// The gateway sends this only after the owner clicked the ask. It names
		// a provider key; the argv comes from the allowlist.
		data, err = d.signInPrepare(raw)
	case protocol.FrameSignInImport:
		var p protocol.SignInImport
		if json.Unmarshal(raw, &p) != nil || p.Provider != "alibaba" || p.Token == "" {
			err = errors.New("bad sign-in import")
		} else {
			err = d.importSignIn(ctx, p.Token)
			if err == nil {
				d.goBackground(func() { d.recheckSignIn(context.Background(), p.Provider) })
			}
		}
	case "node.session_text":
		var q protocol.TextQuery
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			err = errors.New("bad node.session_text")
		} else if handled, page, ctoErr := d.ctoTextQuery(ctx, q); handled {
			data, err = page, ctoErr
		} else {
			data, err = d.localText(ctx, q)
		}
	case protocol.FrameAskIntakeTurn:
		// The gateway asks only after it checked that the caller is the live
		// chief. This machine reads the turn and masks it.
		var q protocol.AskIntakeTurnRequest
		if json.Unmarshal(raw, &q) != nil || q.SessionID == "" {
			err = errors.New("bad node.session_last_turn")
		} else {
			data = d.localLastTurn(ctx, q.SessionID)
		}
	}
	if frame, e := protocol.EncodeResult(id, data, err); e == nil {
		l.write(ctx, frame)
	}
}
