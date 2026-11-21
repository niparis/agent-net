package gateway

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's setup integrations are one setting for the whole network
// (decision 0021). A machine reads it with its node token, which only its user
// has, and the owner's daemon only answers the owner's command line or TUI for
// the operator methods.

const integrationsKey = "owner.integrations"

// Integrations reads the owner's answers. ok is false when nobody has answered
// yet.
func (s *Store) Integrations() (protocol.Integrations, bool, error) {
	var in protocol.Integrations
	ok, err := ownerJSON(s.db, integrationsKey, &in)
	return in, ok, err
}

// SetIntegrations stores the owner's answers for every machine.
func (s *Store) SetIntegrations(in protocol.Integrations) error {
	return s.setOwner(integrationsKey, in)
}

// nodeAuthed returns the node ID of the caller of an HTTP route that uses a
// node token.
func (g *Gateway) nodeAuthed(r *http.Request) (string, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	nodeID, ok, err := g.store.NodeByToken(token)
	return nodeID, err == nil && ok && token != ""
}

func (g *Gateway) handleIntegrationsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.nodeAuthed(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	in, set, err := g.store.Integrations()
	writeJSON(w, protocol.IntegrationsResult{Set: set, Integrations: in}, err)
}

func (g *Gateway) handleIntegrationsSet(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in protocol.Integrations
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := g.store.SetIntegrations(in); err != nil {
		writeJSON(w, nil, err)
		return
	}
	g.store.Event("integrations.set", nodeID, "", "", "on="+strconv.Itoa(in.CountOn()))
	g.logLine("integrations.set", "node", nodeID, "on", in.CountOn())
	writeJSON(w, protocol.IntegrationsResult{Set: true, Integrations: in}, nil)
}
