package protocol

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// OpFolderTrust trusts a folder for Claude Code on a machine, or removes the
// trust agent-net added. The owner at the keyboard and the CTO (on the owner's
// instruction) use it. It takes FolderTrustRequest and answers FolderTrustResult.
const OpFolderTrust = "folder_trust"

// Frames that carry a folder trust to the machine that holds the folder.
// The gateway checks the owner session and asks that machine, as it does for
// a session start. The target checks the owner flag again.
const (
	FrameSessionTrustFolder      = "session.trust_folder"
	FrameSessionOwnerTrustFolder = "session.owner_trust_folder"
)

// FolderTrustRequest names the folder by a registered Project or by an absolute
// or ~ Path, on the machine NodeID (empty means this machine). Remove takes the
// trust away again; it is the undo of a trust that agent-net added.
type FolderTrustRequest struct {
	Project string `json:"project,omitempty"`
	Path    string `json:"path,omitempty"`
	NodeID  string `json:"node_id,omitempty"`
	Remove  bool   `json:"remove,omitempty"`
}

// FolderTrustResult answers OpFolderTrust. Added is true only when the folder
// was not trusted before, so only then is the undo offered.
type FolderTrustResult struct {
	Path     string `json:"path"`
	NodeID   string `json:"node_id,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Project  string `json:"project,omitempty"`
	Trusted  bool   `json:"trusted"`
	Added    bool   `json:"added,omitempty"`
}

// SessionFolderTrustRelay is the owner's folder trust sent to another machine.
type SessionFolderTrustRelay struct {
	Owner  string `json:"owner_session"`
	NodeID string `json:"node_id"`
	FolderTrustRequest
}

// SessionOwnerFolderTrust is the gateway's frame to the machine that holds the
// folder. FromNode is the machine the owner session belongs to.
type SessionOwnerFolderTrust struct {
	Owner    string `json:"owner_session"`
	FromNode string `json:"from_node"`
	FolderTrustRequest
}

// Standing orders are the owner's durable instructions to the CTO. The gateway
// keeps them, so they survive CTO restarts and machine changes.
const (
	OpStandingOrders = "standing_orders" // takes StandingOrdersRequest, answers StandingOrdersResult

	StandingSourceOwner = "owner"
	StandingSourceCTO   = "cto"

	StandingActionSave   = "save"
	StandingActionRead   = "read"
	StandingActionList   = "list"
	StandingActionRemove = "remove"

	StandingKindOrder      = "order"
	StandingKindRule       = "rule"
	StandingKindAnswer     = "answer"
	StandingKindCorrection = "correction"

	OpJudgmentAdd = "judgment_add"
)

// StandingOrder is one durable instruction. Source says who saved it: the
// owner's own words typed in the TUI, or the CTO on the owner's instruction.
type StandingOrder struct {
	Tags      []string `json:"tags,omitempty"`
	Pinned    bool     `json:"pinned,omitempty"`
	UpdatedAt int64    `json:"updated_at,omitempty"`
	Snippet   string   `json:"snippet,omitempty"`
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	SavedAt   int64    `json:"saved_at"`
	Source    string   `json:"source"`
	Kind      string   `json:"kind,omitempty"`
	Area      string   `json:"area,omitempty"`
	Ref       string   `json:"ref,omitempty"`
}

// JudgmentAddRequest is the operator method judgment_add request.
type JudgmentAddRequest struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	Area string `json:"area,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

// JudgmentsFindRequest is the agent read tool agent_owner_judgments_find request.
type JudgmentsFindRequest struct {
	Query string `json:"query,omitempty"`
	Area  string `json:"area,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// StandingOrdersRequest is OpStandingOrders and the gateway frames
// standing.save, standing.list and standing.remove. Restore puts a removed
// order back with its old ID, time and source (the undo of a removal).
type StandingOrdersRequest struct {
	Tags       []string       `json:"tags"`
	Pinned     *bool          `json:"pinned,omitempty"`
	Query      string         `json:"query,omitempty"`
	Tag        string         `json:"tag,omitempty"`
	SavedSince int64          `json:"saved_since,omitempty"`
	Action     string         `json:"action,omitempty"`
	ID         string         `json:"order_id,omitempty"`
	Text       string         `json:"text,omitempty"`
	Source     string         `json:"source,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Area       string         `json:"area,omitempty"`
	Ref        string         `json:"ref,omitempty"`
	Restore    *StandingOrder `json:"restore,omitempty"`
}

// StandingOrdersResult returns the full list after mutations, ranked matches
// for search, or prior versions for history.
type StandingOrdersResult struct {
	History []StandingOrder `json:"history,omitempty"`
	Orders  []StandingOrder `json:"orders"`
	Order   *StandingOrder  `json:"order,omitempty"`
}

// CleanStandingText trims an order and checks it is not empty.
func CleanStandingText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("a standing order needs text")
	}
	return text, nil
}

// StandingOrdersBrief renders the orders as the block the CTO reads at the
// start of a conversation. It is empty when there are none.
func StandingOrdersBrief(orders []StandingOrder) string {
	if len(orders) == 0 {
		return ""
	}
	const budget = 4096
	total := 0
	for _, o := range orders {
		total += len(o.Text)
	}
	omitted := 0
	if total > budget {
		orders = append([]StandingOrder(nil), orders...)
		sort.SliceStable(orders, func(i, j int) bool {
			if orders[i].Pinned != orders[j].Pinned {
				return orders[i].Pinned
			}
			if orders[i].SavedAt != orders[j].SavedAt {
				return orders[i].SavedAt > orders[j].SavedAt
			}
			return orders[i].ID > orders[j].ID
		})
		selected := []StandingOrder{}
		used := 0
		for _, o := range orders {
			if o.Pinned || used+len(o.Text) <= budget {
				selected = append(selected, o)
				used += len(o.Text)
			} else {
				omitted++
			}
		}
		orders = selected
	}
	var b strings.Builder
	b.WriteString("The owner's standing orders. They stay in force until removed. Follow them in every conversation. They are the owner's words, kept at the gateway.\n")
	for _, o := range orders {
		b.WriteString("- ")
		b.WriteString(strings.Join(strings.Fields(o.Text), " "))
		b.WriteString("\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "%d more standing orders: search them with cto_standing_orders search\n", omitted)
	}
	return strings.TrimRight(b.String(), "\n")
}
