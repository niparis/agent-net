package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
	"github.com/niparis/agent-net/internal/settings"
)

// The chief of staff is a standard agent session registered in gateway_state.
// Agents that need the owner send requests to it with agent_message_send
// (decision 0196).

// chiefSessionKey is the gateway_state row that names the chief session.
const chiefSessionKey = "chief_session"

// chiefClearedKey is the gateway_state row that records a deliberate clear:
// when it happened and the chief.model line then. The clear stands while the
// line is the same, so a start from the list does not undo it (decision 0171).
const chiefClearedKey = "chief_cleared"

type chiefCleared struct {
	At    int64  `json:"at"`
	Model string `json:"model"`
}

// chiefModelSetting is the stored chief.model line, or the default.
func (s *Store) chiefModelSetting() (string, error) {
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return "", err
	}
	line := snap.String(protocol.ChiefModelKey, settings.Where{})
	if line == "" {
		line = protocol.DefaultChiefModel
	}
	return line, nil
}

// chiefSession is the registered chief session id, or "" when there is none.
func chiefSession(q askDB) (string, error) {
	var id string
	err := q.QueryRow(`SELECT value FROM gateway_state WHERE key=?`, chiefSessionKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// liveChief is the chief session when it is registered, online and not
// archived. Otherwise it is "".
func liveChief(q askDB) (string, error) {
	id, err := chiefSession(q)
	if err != nil || id == "" {
		return "", err
	}
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=? AND online=1 AND archived_at=0`, id).Scan(&n); err != nil || n == 0 {
		return "", err
	}
	return id, nil
}

// SetChief changes the chief of staff, and only what the request names: the
// session, an explicit clear, or the chief's model. Only the owner or the CTO
// may do it. A clear removes the role. The model is checked before anything is
// written, so a refused model changes nothing.
func (s *Store) SetChief(q protocol.ChiefSetRequest, actor string) error {
	if !actorAllowed(actor, "rule") {
		return reject("only the owner or the CTO may set the chief of staff")
	}
	if err := q.Validate(); err != nil {
		return reject("%v", err)
	}
	var line string
	var stored json.RawMessage
	if q.HasModel() {
		var err error
		if line, stored, err = s.chiefModelLine(q); err != nil {
			return err
		}
	}
	if err := s.setChiefSession(q); err != nil {
		return err
	}
	if line == "" {
		return nil
	}
	_, err := s.WriteSettings([]SettingsChange{{Key: protocol.ChiefModelKey, Level: "network", Scope: "network",
		Value: json.RawMessage(strconv.Quote(line)), Expect: stored}}, actor, "chief_set")
	return err
}

// chiefModelLine resolves the model list of a request against the models the
// network's machines report, the way a worker start does, and returns the
// chief.model value, with the value now stored for the settings write to
// expect (nil when the setting was never written). The single fields change the
// first entry; Models replaces the whole ordered list. A model no machine lists
// is refused.
func (s *Store) chiefModelLine(q protocol.ChiefSetRequest) (string, json.RawMessage, error) {
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return "", nil, err
	}
	var stored json.RawMessage
	for _, v := range snap.Values {
		if v.Key == protocol.ChiefModelKey && v.Level == "network" && v.Scope == "network" && v.Item == "" {
			stored = v.Value
		}
	}
	current := snap.String(protocol.ChiefModelKey, settings.Where{})
	if current == "" {
		current = protocol.DefaultChiefModel
	}
	list, err := protocol.ParseRoleModels(current)
	if err != nil {
		return "", nil, reject("%s: %v", protocol.ChiefModelKey, err)
	}
	nodes, err := s.Nodes()
	if err != nil {
		return "", nil, err
	}
	if len(q.Models) > 0 {
		list = append([]protocol.RoleModel(nil), q.Models...)
	} else {
		first := protocol.RoleModel{}
		if len(list) > 0 {
			first = list[0]
		}
		if q.Harness != "" {
			first.Harness = q.Harness
		}
		if q.Model != "" || q.Harness != "" || q.Provider != "" {
			// A new model or harness drops the old provider, which belonged to it.
			first.Provider = q.Provider
		}
		if q.Model != "" {
			first.Model = q.Model
		}
		if q.Effort != "" {
			first.Effort = q.Effort
		}
		resolved, err := resolveRoleModel(nodes, first)
		if err != nil {
			return "", nil, err
		}
		if len(list) == 0 {
			list = []protocol.RoleModel{resolved}
		} else {
			list[0] = resolved
		}
	}
	for i := range list {
		resolved, err := resolveRoleModel(nodes, list[i])
		if err != nil {
			return "", nil, err
		}
		list[i] = resolved
	}
	return protocol.FormatRoleModels(list), stored, nil
}

// resolveRoleModel checks one role entry against the machines' model lists.
func resolveRoleModel(nodes []protocol.NodeView, m protocol.RoleModel) (protocol.RoleModel, error) {
	if m.Harness == "" {
		return m, reject("a chief model entry needs a harness")
	}
	if !slices.Contains(harnesses.Names, m.Harness) {
		return m, reject("unknown harness %q; known: %s", m.Harness, strings.Join(harnesses.Names, ", "))
	}
	if m.Effort == "default" {
		m.Effort = ""
	}
	if m.Effort != "" {
		if _, level := protocol.SplitModelLevel("m:" + m.Effort); level == "" {
			return m, reject("unknown effort %q", m.Effort)
		}
	}
	if m.Model == "" || m.Model == "default" {
		return m, reject("chief_set needs a model for harness %q", m.Harness)
	}
	var models, providers []string
	for _, n := range nodes {
		for _, h := range n.Harnesses {
			if h.Name == m.Harness {
				models = append(models, h.Models...)
				providers = append(providers, h.ModelProviders...)
			}
		}
	}
	if len(models) == 0 {
		return m, reject("no machine reports a %s model list, so model %q cannot be checked", m.Harness, m.Model)
	}
	sel := protocol.ParseModelSelection(m.Harness, m.Provider, m.Model, m.Effort)
	res, err := protocol.ResolveWorkerModelRankedQuota(m.Harness, sel.ModelID(), models, nil, providers, nil, nil, nil)
	if err != nil {
		return m, reject("model %q is not available on this network for %s: %v", m.Model, m.Harness, err)
	}
	sel = protocol.ParseModelSelection(m.Harness, "", res.Model, sel.Effort)
	if sel.Provider == "" {
		sel.Provider = m.Provider
	}
	if sel.Harness == "agy" {
		if _, err := sel.Arguments(models); err != nil {
			return m, reject("%v", err)
		}
	}
	return protocol.RoleModel{Harness: sel.Harness, Provider: sel.Provider, Model: sel.Model, Effort: sel.Effort}, nil
}

// setChiefSession applies the session part of a request: a named session
// becomes the chief, an explicit clear removes it, and a model-only request
// leaves the chief as it is.
func (s *Store) setChiefSession(q protocol.ChiefSetRequest) error {
	if q.Clear {
		line, err := s.chiefModelSetting()
		if err != nil {
			return err
		}
		mark, _ := json.Marshal(chiefCleared{At: s.now().UnixNano(), Model: line})
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`DELETE FROM gateway_state WHERE key=?`, chiefSessionKey); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO gateway_state(key,value,updated_at) VALUES(?,?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, chiefClearedKey, string(mark), s.now().UnixNano()); err != nil {
			return err
		}
		return tx.Commit()
	}
	if q.SessionID == "" {
		return nil
	}
	var harness, worker string
	var archived int64
	err := s.db.QueryRow(`SELECT harness,worker,archived_at FROM sessions WHERE id=?`, q.SessionID).Scan(&harness, &worker, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return reject("unknown session %q", q.SessionID)
	}
	if err != nil {
		return err
	}
	if protocol.IsOwner(harness) || harness == "cto" || harness == "bot" || worker != "" || archived != 0 {
		return reject("the chief of staff must be a live agent session, not a worker, the owner, the CTO or a bot")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixNano()
	var previous string
	if err = tx.QueryRow(`SELECT value FROM gateway_state WHERE key=?`, chiefSessionKey).Scan(&previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO gateway_state(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, chiefSessionKey, q.SessionID, now); err != nil {
		return err
	}
	// The session the owner moves away from is replaced by the new chief, so a
	// message still sent to its ID reaches the current chief (decision 0171). The
	// new chief is not replaced by anything.
	if previous != "" && previous != q.SessionID {
		if _, err = tx.Exec(`UPDATE sessions SET replaced_by=? WHERE id=?`, q.SessionID, previous); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE sessions SET replaced_by='' WHERE id=?`, q.SessionID); err != nil {
		return err
	}
	// Naming a chief ends a clear.
	if _, err = tx.Exec(`DELETE FROM gateway_state WHERE key=?`, chiefClearedKey); err != nil {
		return err
	}
	return tx.Commit()
}

// ChiefStatus reads the registered chief and the model it is set to run on.
func (s *Store) ChiefStatus() (protocol.ChiefStatus, error) {
	out := protocol.ChiefStatus{}
	id, err := chiefSession(s.db)
	if err != nil {
		return out, err
	}
	out.SessionID = id
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return out, err
	}
	out.Model = snap.String(protocol.ChiefModelKey, settings.Where{})
	if out.Model == "" {
		out.Model = protocol.DefaultChiefModel
	}
	// The status names the choice the chief would start on, which is the
	// first entry of its ordered list.
	if list, err := protocol.ParseRoleModels(out.Model); err == nil && len(list) > 0 {
		out.Model = list[0].Display()
	}
	if id == "" {
		out.ClearedAt, err = s.chiefClearedAt()
		return out, err
	}
	err = s.db.QueryRow(`SELECT COALESCE(NULLIF(s.name,''),s.title,''), s.online=1 AND s.archived_at=0
		FROM sessions s WHERE s.id=?`, id).Scan(&out.Name, &out.Online)
	if errors.Is(err, sql.ErrNoRows) {
		out.Down = "session offline"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if !out.Online {
		out.Down = "session offline"
	}
	return out, nil
}

// chiefClearedAt is when a standing clear happened, or 0. A clear ends when the
// chief.model line differs from the one it was made under.
func (s *Store) chiefClearedAt() (int64, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM gateway_state WHERE key=?`, chiefClearedKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var c chiefCleared
	if json.Unmarshal([]byte(raw), &c) != nil {
		return 0, nil
	}
	line, err := s.chiefModelSetting()
	if err != nil || line != c.Model {
		return 0, err
	}
	return c.At, nil
}

// maskCharterDraft masks a draft before it leaves the gateway: it can quote a
// line from AGENTS.md that holds a secret value.
func maskCharterDraft(d protocol.CharterDraft) protocol.CharterDraft {
	d.Scope = redact.SecretValues(d.Scope)
	d.Name = redact.SecretValues(d.Name)
	d.Attr = redact.SecretValues(d.Attr)
	d.Value = redact.SecretValues(d.Value)
	d.Source = redact.SecretValues(d.Source)
	d.DraftedBy = redact.SecretValues(d.DraftedBy)
	return d
}
