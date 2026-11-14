package daemon

// The CTO op tier: rare operations run through cto_op, discovered with
// cto_ops_list. The op table and its argument schemas live in mcpserver so the
// server can reject a bad call before it reaches the daemon; the daemon checks
// again and computes the owner gate from the one class table in cto_classes.go.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/authority"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

// ctoOpGate returns the owner-gate class of one op: direct, proposal or
// irreversible.
func ctoOpGate(op mcpserver.CTOOp) string {
	if op.Method == "" {
		if class, ok := ctoActionClasses[op.Tool][op.Action]; ok && class == ctoProposal {
			return "proposal"
		}
		if authority.Irreversible(op.Action) {
			return "irreversible"
		}
	}
	return "direct"
}

type ctoOpRow struct {
	Name    string `json:"name"`
	Gate    string `json:"gate"`
	Purpose string `json:"purpose"`
	Example string `json:"example"`
}

type ctoOpGroup struct {
	Name string     `json:"name"`
	Ops  []ctoOpRow `json:"ops"`
}

// ctoOpsCatalog builds the grouped list. It is pure, so the contract can be
// checked without a running daemon.
func ctoOpsCatalog(filter string) []ctoOpGroup {
	filter = strings.ToLower(strings.TrimSpace(filter))
	byGroup := map[string][]ctoOpRow{}
	for _, op := range mcpserver.CTOOps() {
		if filter != "" && !strings.Contains(strings.ToLower(op.Name+" "+op.Purpose+" "+op.Group), filter) {
			continue
		}
		byGroup[op.Group] = append(byGroup[op.Group], ctoOpRow{Name: op.Name, Gate: ctoOpGate(op), Purpose: op.Purpose, Example: op.Example})
	}
	groups := make([]ctoOpGroup, 0, len(mcpserver.CTOOpGroups()))
	for _, name := range mcpserver.CTOOpGroups() {
		rows := byGroup[name]
		if len(rows) == 0 {
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		groups = append(groups, ctoOpGroup{Name: name, Ops: rows})
	}
	return groups
}

func (d *Daemon) ctoOpsList(raw json.RawMessage) (any, error) {
	var q struct {
		Filter string `json:"filter"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, fmt.Errorf("cto_ops_list takes an optional filter string")
		}
	}
	groups := ctoOpsCatalog(q.Filter)
	count := 0
	for _, group := range groups {
		count += len(group.Ops)
	}
	return map[string]any{
		"count":  count,
		"groups": groups,
		"note":   `Run one with cto_op {"method":"<name>","args":{...}}. A wrong, missing or unknown argument returns its schema and runs nothing.`,
	}, nil
}

// ctoOpCall resolves one cto_op request into the parent daemon tool and its
// full arguments. It refuses a bad call before anything runs.
func ctoOpCall(raw json.RawMessage) (mcpserver.CTOOp, string, json.RawMessage, error) {
	var q struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &q); err != nil {
			return mcpserver.CTOOp{}, "", nil, fmt.Errorf("cto_op arguments must be an object with method and optional args")
		}
	}
	q.Method = strings.TrimSpace(q.Method)
	if q.Method == "" {
		return mcpserver.CTOOp{}, "", nil, fmt.Errorf("cto_op needs method; call cto_ops_list for the names")
	}
	op, ok := mcpserver.CTOOpByName(q.Method)
	if !ok {
		return mcpserver.CTOOp{}, "", nil, fmt.Errorf("unknown cto_op %q; call cto_ops_list for the names", q.Method)
	}
	if err := mcpserver.ValidateCTOOpArgs(op, q.Args); err != nil {
		return mcpserver.CTOOp{}, "", nil, err
	}
	tool, args, err := op.CallArgs(q.Args)
	if err != nil {
		return mcpserver.CTOOp{}, "", nil, err
	}
	return op, tool, args, nil
}

// ctoOpsGroupNames keeps the group order used by a test that checks the list.
func ctoOpsGroupNames() []string {
	return mcpserver.CTOOpGroups()
}

// ctoOwnerOpClass classifies an owner operation method. Only reads and changes
// are allowed, and both are direct.
func ctoOwnerOpClass(method string) (ctoToolClass, bool) {
	read, ok := protocol.CTOOperatorMethods()[method]
	if !ok {
		return "", false
	}
	if read {
		return ctoRead, true
	}
	return ctoMinor, true
}
