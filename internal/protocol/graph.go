package protocol

import (
	"fmt"
	"strings"
	"unicode"
)

// CharterValueMaxBytes is the maximum UTF-8 byte length of a charter value.
const CharterValueMaxBytes = 2048

// Charter contains the fixed, independently versioned attributes of a node.
type Charter struct {
	Purpose         string `json:"purpose,omitempty"`
	Maturity        string `json:"maturity,omitempty"`
	Accepts         string `json:"accepts,omitempty"`
	Refuses         string `json:"refuses,omitempty"`
	FeatureRequests string `json:"feature_requests,omitempty"`
	Consumers       string `json:"consumers,omitempty"`
	Escalate        string `json:"escalate,omitempty"`
	Needs           string `json:"needs,omitempty"`
	Notes           string `json:"notes,omitempty"`
}

// Node is a component or an architect. Components can be implicit projects.
type Node struct {
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	CreatedAt int64   `json:"created_at,omitempty"`
	Charter   Charter `json:"charter"`
	// Settings are the node's overrides of its maturity defaults (decision
	// 0022). They sit beside the charter and only the owner changes them.
	Settings map[string]string `json:"settings,omitempty"`
}

// CharterChange is an immutable charter history entry.
type CharterChange struct {
	ID       int64  `json:"id"`
	Node     string `json:"node"`
	Attr     string `json:"attr"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
	At       int64  `json:"at"`
	By       string `json:"by"`
}

// Edge is a directed dependency or governance relationship.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	Mode string `json:"mode,omitempty"`
	Rule string `json:"rule"`
}

// Graph is a snapshot of all known nodes and edges.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// ParseMaturity parses a maturity level with an optional explanation.
func ParseMaturity(value string) (level, text string, err error) {
	level, text, hasText := strings.Cut(value, " - ")
	switch level {
	case "early", "growing", "mature", "critical":
	default:
		return "", "", fmt.Errorf("invalid maturity level")
	}
	if hasText && strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("empty maturity explanation")
	}
	return level, text, nil
}

// CleanCharterValue removes control characters other than newlines, then validates the value.
func CleanCharterValue(attr, value string) (string, error) {
	switch attr {
	case "purpose", "maturity", "accepts", "refuses", "feature_requests", "consumers", "escalate", "needs", "notes":
	default:
		return "", fmt.Errorf("unknown charter attribute")
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, value)
	if len(value) > CharterValueMaxBytes {
		return "", fmt.Errorf("charter value exceeds %d bytes", CharterValueMaxBytes)
	}
	if attr == "maturity" && value != "" {
		if _, _, err := ParseMaturity(value); err != nil {
			return "", err
		}
	}
	return value, nil
}

// CharterSetRequest sets an attribute; an empty value clears it.
type CharterSetRequest struct {
	Node  string `json:"node"`
	Attr  string `json:"attr"`
	Value string `json:"value"`
}

// GraphHistoryRequest selects history by node, newest first.
type GraphHistoryRequest struct {
	Node  string `json:"node"`
	Limit int    `json:"limit,omitempty"`
}

// GraphNodeRequest identifies an architect.
type GraphNodeRequest struct {
	Name string `json:"name"`
}

// GraphUndoRequest identifies a history row; zero means the latest row.
type GraphUndoRequest struct {
	CTOChat bool  `json:"cto_chat,omitempty"`
	ID      int64 `json:"id,omitempty"`
}

// GraphUndoFrame avoids the frame's own id field.
type GraphUndoFrame struct {
	HistoryID int64 `json:"history_id,omitempty"`
}

// EdgeChange records a change to an edge; nil means it did not exist.
type EdgeChange struct {
	ID  int64  `json:"id"`
	Old *Edge  `json:"old,omitempty"`
	New *Edge  `json:"new,omitempty"`
	At  int64  `json:"at"`
	By  string `json:"by"`
}
