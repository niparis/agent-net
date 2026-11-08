package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The read tools of the tool redesign. Filtering and paging happen here, in
// the process that holds the data; the local daemon only forwards the request
// with the verified caller.

const (
	readDefaultLimit = 25
	readMaxLimit     = 100
)

// readCursor is the opaque page marker. It is bound to the caller and to a
// hash of the filters and sort order, so it cannot be reused with another
// query.
type readCursor struct {
	V      int    `json:"v"`
	Caller string `json:"c"`
	Query  string `json:"q"`
	Offset int    `json:"o"`
}

func encodeReadCursor(c readCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeReadCursor(s string) (readCursor, error) {
	var c readCursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(raw, &c) != nil || c.V != 1 || c.Offset < 0 {
		return c, fmt.Errorf("that cursor is not valid; start again without cursor")
	}
	return c, nil
}

// readQueryHash is the fingerprint of the filters and sort order.
func readQueryHash(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

// readPaging is one list request after validation.
type readPaging struct {
	Caller string
	Query  string
	Limit  int
	Offset int
}

func newReadPaging(caller, cursor, query string, limit int) (readPaging, error) {
	limit = normalizeLimit(limit)
	// The limit is part of the fingerprint too: an offset cursor with another
	// page size would leave a gap or repeat rows.
	query += "|limit=" + strconv.Itoa(limit)
	p := readPaging{Caller: caller, Query: query, Limit: limit}
	if cursor == "" {
		return p, nil
	}
	c, err := decodeReadCursor(cursor)
	if err != nil {
		return p, err
	}
	if c.Caller != caller || c.Query != query {
		return p, fmt.Errorf("that cursor belongs to another query; start again without cursor")
	}
	p.Offset = c.Offset
	return p, nil
}

// page returns the slice bounds and the next cursor, if any.
func (p readPaging) page(total int) (start, end int, next string) {
	start = p.Offset
	if start > total {
		start = total
	}
	end = start + p.Limit
	if end > total {
		end = total
	}
	if end < total {
		next = encodeReadCursor(readCursor{V: 1, Caller: p.Caller, Query: p.Query, Offset: end})
	}
	return start, end, next
}

func normalizeLimit(n int) int {
	if n <= 0 {
		return readDefaultLimit
	}
	if n > readMaxLimit {
		return readMaxLimit
	}
	return n
}

func pageNote(shown, total int, next string) string {
	if next == "" {
		return ""
	}
	return fmt.Sprintf("Showing %d of %d. Pass next_cursor for the next page, or narrow the filters.", shown, total)
}

func checkDetail(detail string) (string, error) {
	switch detail {
	case "":
		return "concise", nil
	case "concise", "full":
		return detail, nil
	}
	return "", fmt.Errorf("detail must be concise or full. Example: {\"detail\":\"full\"}")
}

func checkOnline(online, def string) (string, error) {
	switch online {
	case "":
		return def, nil
	case "online", "offline", "any":
		return online, nil
	}
	return "", fmt.Errorf("online must be online, offline or any. Example: {\"online\":\"any\"}")
}

func checkKinds(kinds []string) (map[string]bool, error) {
	if len(kinds) == 0 {
		kinds = []string{"agent", "worker", "merger", "cto"}
	}
	out := map[string]bool{}
	for _, kind := range kinds {
		switch kind {
		case "agent", "worker", "owner", "bot", "merger", "cto":
			out[kind] = true
		default:
			return nil, fmt.Errorf("kind must be agent, worker, merger, cto, owner or bot. Example: {\"kind\":[\"agent\"]}")
		}
	}
	return out, nil
}

func equalFoldAny(s string, values ...string) bool {
	for _, v := range values {
		if v != "" && strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

func rfc3339(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func agoText(unix int64, now time.Time) string {
	if unix <= 0 {
		return ""
	}
	d := now.Sub(time.Unix(unix, 0))
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm ago", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh ago", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func durationText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func sessionKind(a protocol.AgentView) string {
	return protocol.SessionType(a.Harness, a.Worker, a.Type)
}

func sessionProject(a protocol.AgentView) string {
	// Keep the legacy announced project in the read-tool display while the
	// resolved repo agrees with the reported repo. If the context differs,
	// the resolved context is the only current repo.
	if a.Profile.Project != "" && (a.RepoContext == "" || a.RepoContext == a.Repo.Project) {
		return a.Profile.Project
	}
	return projectOfSession(a)
}

func delegationText(v *protocol.DelegationView) string {
	if v == nil {
		return ""
	}
	var parts []string
	if v.Delegate != "" {
		parts = append(parts, "delegate "+v.Delegate)
	}
	if coder := v.Coder(); coder != "" {
		parts = append(parts, "coder "+coder)
	}
	if main := v.Main(); main != "" {
		parts = append(parts, "main "+main)
	}
	if v.Workers != "" {
		parts = append(parts, "workers "+v.Workers)
	}
	if v.Scope != "" {
		parts = append(parts, "scope "+v.Scope)
	}
	return strings.Join(parts, ", ")
}

func sessionRow(a protocol.AgentView, full bool, now time.Time, name func(string) string) protocol.SessionRow {
	row := protocol.SessionRow{
		AgentID: a.AgentID, AgentName: a.AgentName, RepoLead: a.RepoLead, RepoLeadExplicit: a.RepoLeadExplicit, TeamLead: a.TeamLead,
		RepoMaxAgents: a.RepoMaxAgents, RepoAgentCount: a.RepoAgentCount, RepoOverMax: a.RepoOverMax,
		ID:       a.SessionID,
		Name:     a.Profile.Name,
		Kind:     sessionKind(a),
		Type:     sessionKind(a),
		Machine:  a.Hostname,
		Project:  sessionProject(a),
		Harness:  a.Harness,
		Model:    a.Model,
		Online:   a.Online,
		Status:   a.Status,
		Task:     a.Profile.Task,
		LastSeen: rfc3339(a.LastSeen),
	}
	if a.Harness == "cto" && a.Online {
		row.Availability = "wakes on message"
	}
	if a.ToolsDisconnected() {
		row.Tools = protocol.ToolsDisconnected
	}
	if a.TaskStarted > 0 {
		started := rfc3339(a.TaskStarted)
		row.TaskStarted = &started
	}
	if w := a.Waiting; w != nil && a.Online && a.Worker == nil && !protocol.IsOwner(a.Harness) {
		row.Status = protocol.StatusWaitingForUser
		row.Waiting = &protocol.WaitingRow{Kind: w.Kind, Since: rfc3339(w.Since)}
	}
	if !full {
		return row
	}
	row.LastSeenAgo = agoText(a.LastSeen, now)
	row.Folder = a.Cwd
	row.Branch = a.Repo.Branch
	row.Remote = a.Repo.Remote
	row.Function = a.Profile.Function
	row.Expertise = a.Profile.Expertise
	row.Capabilities = a.Profile.Capabilities
	row.Purpose = a.Charter.Purpose
	row.Charter = &protocol.ReadCharter{
		Purpose: a.Charter.Purpose, PurposeFallback: a.Charter.PurposeFallback,
		Accepts: charterList(a.Charter.Accepts), Refuses: charterList(a.Charter.Refuses),
		FeatureRequests: a.Charter.FeatureRequests,
	}
	if c := a.Charter.Effective; c != nil {
		row.Charter.Maturity = c.Maturity
		row.Charter.MaturityNote = c.MaturityNote
		row.Charter.Rules = c.Rules
		row.Charter.Escalate = c.Escalate
		row.Charter.Sources = c.Sources
	}
	if len(a.Charter.Rules) > 0 {
		row.Rules = map[string]string{}
		for _, rule := range a.Charter.Rules {
			row.Rules[rule.Name] = rule.Value
		}
	}
	row.Delegation = delegationText(a.Delegation)
	row.Loop = a.Loop
	if a.Worker != nil {
		state := "ended"
		if a.Online {
			state = "running"
		}
		difficulty := a.Worker.DifficultyUsed
		if difficulty == "" {
			difficulty = a.Worker.DifficultyAsked
		}
		runTime := ""
		if a.Worker.StartedAt > 0 {
			runTime = durationText(now.Sub(time.Unix(a.Worker.StartedAt, 0)))
		}
		row.Worker = &protocol.SessionWorker{
			ID:         a.Worker.ID,
			State:      state,
			Model:      a.Model,
			Difficulty: difficulty,
			Caller:     protocol.SessionRefRow{ID: a.Worker.Owner, Name: name(a.Worker.Owner)},
			RunTime:    runTime,
		}
	}
	return row
}

func charterList(value string) []string {
	return protocol.SplitCharterList(value)
}

// FindSessions answers agent_sessions_find.
func (s *Store) FindSessions(p protocol.SessionsFindRequest) (protocol.SessionsPage, error) {
	kinds, err := checkKinds(p.Kind)
	if err != nil {
		return protocol.SessionsPage{}, err
	}
	var canonicalKinds []string
	for kind := range kinds {
		canonicalKinds = append(canonicalKinds, kind)
	}
	sort.Strings(canonicalKinds)
	detail, err := checkDetail(p.Detail)
	if err != nil {
		return protocol.SessionsPage{}, err
	}
	online, err := checkOnline(p.Online, "online")
	if err != nil {
		return protocol.SessionsPage{}, err
	}
	if p.MatchAny != nil && len(p.MatchAny) == 0 {
		return protocol.SessionsPage{}, fmt.Errorf("match_any needs at least one alternative. Example: {\"match_any\":[{\"harness\":\"opencode\"}]}")
	}
	for _, m := range p.MatchAny {
		if m.Harness == "" && m.Model == "" {
			return protocol.SessionsPage{}, fmt.Errorf("each match_any alternative needs harness or model. Example: {\"match_any\":[{\"model\":\"terra\"}]}")
		}
	}
	query := readQueryHash(struct {
		Machine, Project, Harness, Model, Online, Text, Detail string
		Kind                                                   []string
		MatchAny                                               []protocol.SessionMatch
		Sort                                                   string
	}{p.Machine, p.Project, p.Harness, p.Model, online, p.Text, detail, canonicalKinds, p.MatchAny, "last_seen_desc_id"})
	paging, err := newReadPaging(p.Caller, p.Cursor, query, p.Limit)
	if err != nil {
		return protocol.SessionsPage{}, err
	}

	agents, err := s.Agents(protocol.QueryAgents{IncludeOwners: true})
	if err != nil {
		return protocol.SessionsPage{}, err
	}
	names := map[string]string{}
	for _, a := range agents {
		names[a.SessionID] = a.Profile.Name
	}
	name := func(id string) string { return names[id] }
	text := strings.ToLower(p.Text)
	var matched []protocol.AgentView
	for _, a := range agents {
		if !kinds[sessionKind(a)] {
			continue
		}
		if online != "any" && a.Online != (online == "online") {
			continue
		}
		if p.Machine != "" && !equalFoldAny(a.Hostname, p.Machine) && !equalFoldAny(a.NodeID, p.Machine) {
			continue
		}
		if p.Project != "" && !equalFoldAny(sessionProject(a), p.Project) {
			continue
		}
		if p.Harness != "" && !strings.EqualFold(a.Harness, p.Harness) {
			continue
		}
		if p.Model != "" && !strings.Contains(strings.ToLower(a.Model), strings.ToLower(p.Model)) {
			continue
		}
		if text != "" {
			hay := strings.ToLower(strings.Join(append([]string{a.SessionID, a.Profile.Name, a.Profile.Function,
				sessionProject(a), a.Profile.Task, a.Profile.Expertise, a.Cwd, a.Hostname,
				a.Repo.Branch, a.Repo.Remote}, a.Profile.Capabilities...), " "))
			if !strings.Contains(hay, text) {
				continue
			}
		}
		if len(p.MatchAny) > 0 && !matchesAny(a, p.MatchAny) {
			continue
		}
		matched = append(matched, a)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].LastSeen != matched[j].LastSeen {
			return matched[i].LastSeen > matched[j].LastSeen
		}
		return matched[i].SessionID < matched[j].SessionID
	})

	total := len(matched)
	start, end, next := paging.page(total)
	page := protocol.SessionsPage{Items: []protocol.SessionRow{}, Total: total, NextCursor: next}
	now := s.now()
	for _, a := range matched[start:end] {
		page.Items = append(page.Items, sessionRow(a, detail == "full", now, name))
	}
	page.Note = pageNote(len(page.Items), total, next)
	return page, nil
}

func matchesAny(a protocol.AgentView, alts []protocol.SessionMatch) bool {
	for _, alt := range alts {
		if alt.Harness != "" && !strings.EqualFold(a.Harness, alt.Harness) {
			continue
		}
		if alt.Model != "" && !strings.Contains(strings.ToLower(a.Model), strings.ToLower(alt.Model)) {
			continue
		}
		return true
	}
	return false
}

// GetSession answers agent_session_get by ID or exact name.
func (s *Store) GetSession(p protocol.SessionGetRequest) (protocol.SessionGetResult, error) {
	id := strings.TrimSpace(p.Session)
	if id == "" {
		return protocol.SessionGetResult{}, fmt.Errorf("session is required. Example: {\"session\":\"s01\"}")
	}
	a, ok, err := s.Agent(id)
	if err != nil {
		return protocol.SessionGetResult{}, err
	}
	if !ok {
		agents, err := s.Agents(protocol.QueryAgents{IncludeOwners: true})
		if err != nil {
			return protocol.SessionGetResult{}, err
		}
		var matches []protocol.AgentView
		for _, candidate := range agents {
			if strings.EqualFold(candidate.Profile.Name, id) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) > 1 {
			return protocol.SessionGetResult{}, fmt.Errorf("more than one session is named %q; use its session ID", p.Session)
		}
		if len(matches) == 1 {
			a, ok = matches[0], true
		}
	}
	if !ok {
		return protocol.SessionGetResult{}, fmt.Errorf("unknown session %q; use agent_sessions_find to find its ID or name", p.Session)
	}
	names := map[string]string{a.SessionID: a.Profile.Name}
	if a.Worker != nil && a.Worker.Owner != "" && a.Worker.Owner != a.SessionID {
		if owner, found, err := s.Agent(a.Worker.Owner); err == nil && found {
			names[a.Worker.Owner] = owner.Profile.Name
		}
	}
	return protocol.SessionGetResult{Session: sessionRow(a, true, s.now(), func(name string) string { return names[name] })}, nil
}

// sessionCountsByNode counts online sessions and running workers per machine.
func (s *Store) sessionCountsByNode() (map[string][2]int, error) {
	rows, err := s.db.Query(`SELECT node_id,
		COALESCE(SUM(CASE WHEN online = 1 AND harness != 'owner' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN online = 1 AND worker != '' THEN 1 ELSE 0 END), 0)
		FROM sessions WHERE archived_at=0 GROUP BY node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]int{}
	for rows.Next() {
		var node string
		var sessions, workers int
		if err := rows.Scan(&node, &sessions, &workers); err != nil {
			return nil, err
		}
		out[node] = [2]int{sessions, workers}
	}
	return out, rows.Err()
}

func (g *Gateway) nodeOnline(nodeID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.conns[nodeID]
	return ok
}

func machineRow(n protocol.NodeView, online bool, counts [2]int, full bool) protocol.MachineRow {
	row := protocol.MachineRow{
		ID:             n.NodeID,
		Name:           n.Hostname,
		Online:         online,
		Version:        n.Version,
		Harnesses:      []protocol.HarnessRow{},
		SessionsOnline: counts[0],
		WorkersRunning: counts[1],
	}
	for _, h := range n.Harnesses {
		row.Harnesses = append(row.Harnesses, protocol.HarnessRow{Name: h.Name, Version: h.Version})
	}
	if full {
		row.OS = n.OS
		row.LastSeen = rfc3339(n.LastSeen)
	}
	return row
}

// FindMachines answers agent_machines_find.
func (g *Gateway) FindMachines(p protocol.MachinesFindRequest) (protocol.MachinesPage, error) {
	online, err := checkOnline(p.Online, "any")
	if err != nil {
		return protocol.MachinesPage{}, err
	}
	detail, err := checkDetail(p.Detail)
	if err != nil {
		return protocol.MachinesPage{}, err
	}
	query := readQueryHash(struct {
		Online, Detail, Sort string
	}{online, detail, "name_id"})
	paging, err := newReadPaging(p.Caller, p.Cursor, query, p.Limit)
	if err != nil {
		return protocol.MachinesPage{}, err
	}
	nodes, err := g.store.Nodes()
	if err != nil {
		return protocol.MachinesPage{}, err
	}
	counts, err := g.store.sessionCountsByNode()
	if err != nil {
		return protocol.MachinesPage{}, err
	}
	var rows []protocol.MachineRow
	for _, n := range nodes {
		up := g.nodeOnline(n.NodeID)
		if online != "any" && up != (online == "online") {
			continue
		}
		rows = append(rows, machineRow(n, up, counts[n.NodeID], detail == "full"))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !strings.EqualFold(rows[i].Name, rows[j].Name) {
			return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
		}
		return rows[i].ID < rows[j].ID
	})
	total := len(rows)
	start, end, next := paging.page(total)
	page := protocol.MachinesPage{Items: []protocol.MachineRow{}, Total: total, NextCursor: next}
	page.Items = append(page.Items, rows[start:end]...)
	page.Note = pageNote(len(page.Items), total, next)
	return page, nil
}

// MachineGet answers agent_machine_get with one category.
func (g *Gateway) MachineGet(p protocol.MachineGetRequest) (protocol.MachineGetResult, error) {
	if len(p.Include) > 1 {
		return protocol.MachineGetResult{}, fmt.Errorf("agent_machine_get takes at most one include category, because the cursor pages that category. Example: {\"machine\":\"research\",\"include\":[\"models\"]}")
	}
	category := ""
	if len(p.Include) == 1 {
		category = p.Include[0]
		switch category {
		case "models", "quotas", "track_record", "playbook":
		default:
			return protocol.MachineGetResult{}, fmt.Errorf("include must be models, quotas, track_record or playbook. Example: {\"include\":[\"track_record\"]}")
		}
	}
	nodes, err := g.store.Nodes()
	if err != nil {
		return protocol.MachineGetResult{}, err
	}
	var node protocol.NodeView
	found := false
	for _, n := range nodes {
		if equalFoldAny(n.Hostname, p.Machine) || equalFoldAny(n.NodeID, p.Machine) {
			node, found = n, true
			break
		}
	}
	if !found {
		return protocol.MachineGetResult{}, fmt.Errorf("unknown machine %q; use agent_machines_find to find its name", p.Machine)
	}
	counts, err := g.store.sessionCountsByNode()
	if err != nil {
		return protocol.MachineGetResult{}, err
	}
	online := g.nodeOnline(node.NodeID)
	health, err := g.store.nodeHealth(node.NodeID, online)
	if err != nil {
		return protocol.MachineGetResult{}, err
	}
	out := protocol.MachineGetResult{Machine: machineRow(node, online, counts[node.NodeID], true), Health: health}
	if category == "" {
		return out, nil
	}
	query := readQueryHash(struct {
		Machine, Category, Harness, ModelFilter string
	}{node.NodeID, category, p.Harness, p.ModelFilter})
	paging, err := newReadPaging(p.Caller, p.Cursor, query, p.Limit)
	if err != nil {
		return protocol.MachineGetResult{}, err
	}
	switch category {
	case "models":
		scores, err := g.store.scores()
		if err != nil {
			return protocol.MachineGetResult{}, err
		}
		var rows []protocol.MachineModelRow
		for _, h := range node.Harnesses {
			if p.Harness != "" && !strings.EqualFold(h.Name, p.Harness) {
				continue
			}
			for _, id := range h.Models {
				if p.ModelFilter != "" && !strings.Contains(strings.ToLower(id), strings.ToLower(p.ModelFilter)) {
					continue
				}
				rows = append(rows, protocol.MachineModelRow{Harness: h.Name, ID: id, Scores: protocol.ModelScoresFor(id, scores)})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Harness != rows[j].Harness {
				return rows[i].Harness < rows[j].Harness
			}
			return rows[i].ID < rows[j].ID
		})
		total := len(rows)
		start, end, next := paging.page(total)
		page := &protocol.MachineModelPage{Items: []protocol.MachineModelRow{}, Total: total, NextCursor: next}
		page.Items = append(page.Items, rows[start:end]...)
		page.Note = pageNote(len(page.Items), total, next)
		out.Models = page

	case "quotas":
		var rows []protocol.MachineQuotaRow
		omitted := 0
		if q := g.nodeQuotas(node.NodeID); q != nil {
			for _, provider := range q.Providers {
				if provider.Error != "" {
					omitted++
					continue
				}
				for _, w := range provider.Windows {
					window := w.Label
					if window == "" {
						window = w.ID
					}
					rows = append(rows, protocol.MachineQuotaRow{
						Provider: provider.Provider, Window: window,
						PercentUsed: w.PercentUsed, ResetsAt: rfc3339(w.ResetsAt),
					})
				}
			}
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Provider != rows[j].Provider {
				return rows[i].Provider < rows[j].Provider
			}
			return rows[i].Window < rows[j].Window
		})
		total := len(rows)
		start, end, next := paging.page(total)
		page := &protocol.MachineQuotaPage{Items: []protocol.MachineQuotaRow{}, Total: total, NextCursor: next}
		page.Items = append(page.Items, rows[start:end]...)
		page.Note = pageNote(len(page.Items), total, next)
		if omitted > 0 {
			page.Note = strings.TrimSpace(fmt.Sprintf("%s %d quota rows were omitted because their credential is missing or invalid.", page.Note, omitted))
		}
		out.Quotas = page

	case "track_record":
		tracks, err := g.store.TrackRecords(node.NodeID, protocol.TrackRecordDays*24*time.Hour)
		if err != nil {
			return protocol.MachineGetResult{}, err
		}
		var rows []protocol.MachineTrackRow
		for _, t := range tracks {
			if p.Harness != "" && !strings.EqualFold(t.Harness, p.Harness) {
				continue
			}
			if p.ModelFilter != "" && !strings.Contains(strings.ToLower(t.Model), strings.ToLower(p.ModelFilter)) {
				continue
			}
			row := protocol.MachineTrackRow{Harness: t.Harness, Model: t.Model, Runs: t.Runs,
				Complete: t.Complete, Partial: t.Partial, Failed: t.Failed, EarlyQuits: t.EarlyQuits}
			if t.MedianRunSeconds > 0 {
				row.MedianMinutes = float64(t.MedianRunSeconds) / 60
			}
			rows = append(rows, row)
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Harness != rows[j].Harness {
				return rows[i].Harness < rows[j].Harness
			}
			return rows[i].Model < rows[j].Model
		})
		total := len(rows)
		start, end, next := paging.page(total)
		page := &protocol.MachineTrackPage{Items: []protocol.MachineTrackRow{}, Total: total, NextCursor: next}
		page.Items = append(page.Items, rows[start:end]...)
		page.Note = pageNote(len(page.Items), total, next)
		out.TrackRecord = page

	case "playbook":
		notes, err := g.store.PlaybookNotes()
		if err != nil {
			return protocol.MachineGetResult{}, err
		}
		models := map[string]bool{}
		harnesses := map[string]bool{}
		for _, h := range node.Harnesses {
			harnesses[h.Name] = true
			for _, id := range h.Models {
				models[id] = true
			}
		}
		var rows []protocol.MachinePlaybookRow
		for _, n := range notes {
			if !playbookApplies(n, node, harnesses, models, p.Harness, p.ModelFilter) {
				continue
			}
			rows = append(rows, protocol.MachinePlaybookRow{Subject: n.Subject, Note: n.Note})
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Subject != rows[j].Subject {
				return rows[i].Subject < rows[j].Subject
			}
			return rows[i].Note < rows[j].Note
		})
		total := len(rows)
		start, end, next := paging.page(total)
		page := &protocol.MachinePlaybookPage{Items: []protocol.MachinePlaybookRow{}, Total: total, NextCursor: next}
		page.Items = append(page.Items, rows[start:end]...)
		page.Note = pageNote(len(page.Items), total, next)
		out.Playbook = page
	}
	return out, nil
}

func playbookApplies(n protocol.PlaybookNote, node protocol.NodeView, harnesses, models map[string]bool, harness, modelFilter string) bool {
	if harness != "" && n.Harness != "" && !strings.EqualFold(n.Harness, harness) {
		return false
	}
	if modelFilter != "" && !strings.Contains(strings.ToLower(n.Subject), strings.ToLower(modelFilter)) {
		return false
	}
	switch n.SubjectKind {
	case protocol.SubjectMachine:
		return equalFoldAny(n.Subject, node.NodeID, node.Hostname)
	case protocol.SubjectModel:
		return models[n.Subject]
	case protocol.SubjectModelHarness:
		return models[n.Subject] && harnesses[n.Harness]
	case protocol.SubjectHarness:
		return harnesses[n.Subject]
	}
	return false
}

// FindProjects answers agent_projects_find.
func (s *Store) FindProjects(p protocol.ProjectsFindRequest) (protocol.ProjectsPage, error) {
	detail, err := checkDetail(p.Detail)
	if err != nil {
		return protocol.ProjectsPage{}, err
	}
	query := readQueryHash(struct {
		Name, Machine, Text, Detail, Sort string
	}{p.Name, p.Machine, p.Text, detail, "name_machine_folder"})
	paging, err := newReadPaging(p.Caller, p.Cursor, query, p.Limit)
	if err != nil {
		return protocol.ProjectsPage{}, err
	}
	projects, err := s.Projects(p.Name)
	if err != nil {
		return protocol.ProjectsPage{}, err
	}
	text := strings.ToLower(p.Text)
	var matched []protocol.ProjectView
	for _, project := range projects {
		if p.Machine != "" && !equalFoldAny(project.Hostname, p.Machine) && !equalFoldAny(project.NodeID, p.Machine) {
			continue
		}
		if text != "" {
			hay := strings.ToLower(strings.Join([]string{project.Name, project.DisplayName, project.Path,
				project.Remote, project.Hostname}, " "))
			if !strings.Contains(hay, text) {
				continue
			}
		}
		matched = append(matched, project)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if !strings.EqualFold(matched[i].Name, matched[j].Name) {
			return strings.ToLower(matched[i].Name) < strings.ToLower(matched[j].Name)
		}
		if !strings.EqualFold(matched[i].Hostname, matched[j].Hostname) {
			return strings.ToLower(matched[i].Hostname) < strings.ToLower(matched[j].Hostname)
		}
		return matched[i].Path < matched[j].Path
	})
	total := len(matched)
	start, end, next := paging.page(total)
	page := protocol.ProjectsPage{Items: []protocol.ProjectRow{}, Total: total, NextCursor: next}
	for _, project := range matched[start:end] {
		source := project.Source
		if source == "" {
			source = protocol.SourceSeen
		}
		row := protocol.ProjectRow{
			Name:           project.Name,
			DisplayName:    project.DisplayName,
			Machine:        project.Hostname,
			Folder:         project.Path,
			SessionsOnline: project.Online,
			SessionsTotal:  project.Sessions,
			Source:         source,
		}
		if detail == "full" {
			row.Remote = project.Remote
			row.DefaultBranch = project.DefaultBranch
		}
		page.Items = append(page.Items, row)
	}
	page.Note = pageNote(len(page.Items), total, next)
	return page, nil
}

// handleToolRead serves the read tools of the tool redesign. The second value
// says whether this hook handled the frame type.
func (g *Gateway) handleToolRead(ctx context.Context, nc *nodeConn, typ string, raw []byte) (data any, ok bool, err error) {
	switch typ {
	case "query.sessions_find":
		p, err := decode[protocol.SessionsFindRequest](raw)
		if err != nil {
			return nil, true, reject("bad query.sessions_find")
		}
		out, err := g.store.FindSessions(p)
		return out, true, err
	case "query.session_get":
		p, err := decode[protocol.SessionGetRequest](raw)
		if err != nil || strings.TrimSpace(p.Session) == "" {
			return nil, true, reject("bad query.session_get")
		}
		out, err := g.store.GetSession(p)
		return out, true, err
	case "query.machines_find":
		p, err := decode[protocol.MachinesFindRequest](raw)
		if err != nil {
			return nil, true, reject("bad query.machines_find")
		}
		out, err := g.FindMachines(p)
		return out, true, err
	case "query.machine_get":
		p, err := decode[protocol.MachineGetRequest](raw)
		if err != nil || strings.TrimSpace(p.Machine) == "" {
			return nil, true, reject("bad query.machine_get")
		}
		out, err := g.MachineGet(p)
		return out, true, err
	case "query.projects_find":
		p, err := decode[protocol.ProjectsFindRequest](raw)
		if err != nil {
			return nil, true, reject("bad query.projects_find")
		}
		out, err := g.store.FindProjects(p)
		return out, true, err
	case "query.projects_layout":
		p, err := decode[protocol.ProjectsLayoutRequest](raw)
		if err != nil {
			return nil, true, reject("bad query.projects_layout")
		}
		out, err := g.ProjectsLayout(ctx, p)
		return out, true, err
	}
	return nil, false, nil
}
