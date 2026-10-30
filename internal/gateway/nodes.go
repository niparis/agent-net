package gateway

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// SetNodeHello records what a daemon said in its hello: host name and version.
func (s *Store) SetNodeHello(nodeID, hostname, version string) error {
	if err := s.SetNodeHostname(nodeID, hostname); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE nodes SET version = ?, seen_at = ? WHERE id = ?`, version, s.now().UnixNano(), nodeID)
	return err
}

// ErrNodeName is a display name the owner may not use.
type ErrNodeName struct{ Reason string }

func (e ErrNodeName) Error() string { return e.Reason }

// SetNodeDisplayName gives a node the owner's display name, or clears it with
// an empty name (decision 0015). It returns the name now listed for the node.
// The name is checked (protocol.CleanNodeName) and must not be the name of
// another node, in any letter case. The reported host name stays stored.
func (s *Store) SetNodeDisplayName(nodeID, name string) (protocol.NodeRenameResult, error) {
	var res protocol.NodeRenameResult
	clean, err := protocol.CleanNodeName(name)
	if err != nil {
		return res, ErrNodeName{err.Error()}
	}
	var reported string
	if err := s.db.QueryRow(`SELECT reported_hostname FROM nodes WHERE id = ?`, nodeID).Scan(&reported); err != nil {
		return res, ErrNodeName{"unknown machine " + nodeID}
	}
	if clean != "" {
		var other string
		err := s.db.QueryRow(`SELECT id FROM nodes WHERE id != ? AND lower(hostname) = lower(?) LIMIT 1`, nodeID, clean).Scan(&other)
		if err == nil {
			return res, ErrNodeName{"another machine is already called " + clean}
		}
	}
	shown := clean
	if shown == "" {
		shown = reported
	}
	if _, err := s.db.Exec(`UPDATE nodes SET display_name = ?, hostname = ? WHERE id = ?`, clean, shown, nodeID); err != nil {
		return res, err
	}
	s.Event("node.renamed", nodeID, "", "", shown)
	s.logf("node.renamed", "node", nodeID, "name", clean, "reported", reported)
	return protocol.NodeRenameResult{NodeID: nodeID, Hostname: shown, DisplayName: clean, Reported: reported}, nil
}

// SetNodeLaunch records whether a node accepts workers. Nil is "not reported"
// and changes nothing.
func (s *Store) SetNodeLaunch(nodeID string, launch *bool) error {
	if launch == nil {
		return nil
	}
	v := 0
	if *launch {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE nodes SET launch = ? WHERE id = ?`, v, nodeID)
	return err
}

// NodeLaunch returns whether a node accepts workers: nil when it never said.
func (s *Store) NodeLaunch(nodeID string) (*bool, error) {
	var v int
	if err := s.db.QueryRow(`SELECT launch FROM nodes WHERE id = ?`, nodeID).Scan(&v); err != nil {
		return nil, err
	}
	return launchValue(v), nil
}

func launchValue(v int) *bool {
	if v < 0 {
		return nil
	}
	b := v == 1
	return &b
}

// NodeVersion returns the version a node last reported.
func (s *Store) NodeVersion(nodeID string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT version FROM nodes WHERE id = ?`, nodeID).Scan(&v)
	return v, err
}

// Nodes lists every node that has connected at least once. Online is left
// false; the gateway fills it from its live connections.
func (s *Store) Nodes() ([]protocol.NodeView, error) {
	rows, err := s.db.Query(`SELECT id, hostname, os, version, seen_at, harnesses, launch, display_name, reported_hostname FROM nodes WHERE seen_at > 0 ORDER BY hostname, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.NodeView
	for rows.Next() {
		var n protocol.NodeView
		var seen int64
		var hs string
		var launch int
		if err := rows.Scan(&n.NodeID, &n.Hostname, &n.OS, &n.Version, &seen, &hs, &launch, &n.DisplayName, &n.Reported); err != nil {
			return nil, err
		}
		n.Launch = launchValue(launch)
		n.Harnesses = decodeHarnesses(hs)
		n.LastSeen = seen / 1e9
		out = append(out, n)
	}
	return out, rows.Err()
}

// decodeHarnesses reads the stored inventory. "null" (never reported) gives nil.
func decodeHarnesses(raw string) []protocol.HarnessInfo {
	var hs []protocol.HarnessInfo
	if json.Unmarshal([]byte(raw), &hs) != nil {
		return nil
	}
	return hs
}

// SetNodeHarnesses stores the inventory a daemon reported. It writes an event
// and a log line when the inventory differs from the stored one, and returns
// the change in words ("" when nothing changed). A nil list is "not reported"
// and changes nothing.
func (s *Store) SetNodeHarnesses(nodeID string, hs []protocol.HarnessInfo) (string, error) {
	if hs == nil {
		return "", nil
	}
	var old string
	var oldAt int64
	if err := s.db.QueryRow(`SELECT harnesses, harnesses_at FROM nodes WHERE id = ?`, nodeID).Scan(&old, &oldAt); err != nil {
		return "", err
	}
	raw, err := json.Marshal(hs)
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`UPDATE nodes SET harnesses = ?, harnesses_at = ? WHERE id = ?`, string(raw), s.now().UnixNano(), nodeID); err != nil {
		return "", err
	}
	var prev []protocol.HarnessInfo
	if oldAt > 0 {
		prev = decodeHarnesses(old)
	}
	change := DescribeHarnessChange(prev, hs)
	if change == "" && oldAt > 0 {
		return "", nil
	}
	if change == "" {
		change = "none installed"
	}
	s.Event("node.harnesses", nodeID, "", "", change)
	s.logf("node.harnesses", "node", nodeID, "host", s.hostname(nodeID), "change", change, "installed", HarnessNames(hs))
	return change, nil
}

// HarnessNames lists the harness names, comma separated.
func HarnessNames(hs []protocol.HarnessInfo) string {
	var n []string
	for _, h := range hs {
		n = append(n, h.Name)
	}
	return strings.Join(n, ",")
}

// DescribeHarnessChange says what changed between two inventories, like
// "+codex,-pi,opencode:v1->v2,claude:path,opencode:models 100->120". It is ""
// when they are the same.
func DescribeHarnessChange(old, cur []protocol.HarnessInfo) string {
	byName := func(hs []protocol.HarnessInfo) map[string]protocol.HarnessInfo {
		m := map[string]protocol.HarnessInfo{}
		for _, h := range hs {
			m[h.Name] = h
		}
		return m
	}
	o, c := byName(old), byName(cur)
	var names []string
	for n := range o {
		names = append(names, n)
	}
	for n := range c {
		if _, ok := o[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var parts []string
	for _, n := range names {
		oh, hadOld := o[n]
		ch, hasNew := c[n]
		switch {
		case !hadOld:
			parts = append(parts, "+"+n)
		case !hasNew:
			parts = append(parts, "-"+n)
		default:
			if oh.Version != ch.Version {
				parts = append(parts, n+":"+orDash(oh.Version)+"->"+orDash(ch.Version))
			}
			if oh.Path != ch.Path {
				parts = append(parts, n+":path")
			}
			if len(oh.Others) != len(ch.Others) {
				parts = append(parts, n+":installs "+strconv.Itoa(1+len(oh.Others))+"->"+strconv.Itoa(1+len(ch.Others)))
			}
			if len(oh.Models) != len(ch.Models) {
				parts = append(parts, n+":models "+strconv.Itoa(len(oh.Models))+"->"+strconv.Itoa(len(ch.Models)))
			} else if strings.Join(oh.Models, "\n") != strings.Join(ch.Models, "\n") {
				parts = append(parts, n+":models changed")
			}
		}
	}
	return strings.Join(parts, ",")
}

func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
