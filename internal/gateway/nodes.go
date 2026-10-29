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

// NodeVersion returns the version a node last reported.
func (s *Store) NodeVersion(nodeID string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT version FROM nodes WHERE id = ?`, nodeID).Scan(&v)
	return v, err
}

// Nodes lists every node that has connected at least once. Online is left
// false; the gateway fills it from its live connections.
func (s *Store) Nodes() ([]protocol.NodeView, error) {
	rows, err := s.db.Query(`SELECT id, hostname, os, version, seen_at, harnesses FROM nodes WHERE seen_at > 0 ORDER BY hostname, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.NodeView
	for rows.Next() {
		var n protocol.NodeView
		var seen int64
		var hs string
		if err := rows.Scan(&n.NodeID, &n.Hostname, &n.OS, &n.Version, &seen, &hs); err != nil {
			return nil, err
		}
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
