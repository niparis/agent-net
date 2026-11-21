package gateway

import (
	"database/sql"
	"errors"
)

// A role address is the name a session is reached by when its role is what
// matters: "chief" and "cto" (decision 0171, phase 2). The name resolves to the
// session pinned for the role when the message is stored, so a sender never
// needs the session ID of the current holder.
var roleAddressKeys = map[string]struct {
	key, missing string
}{
	"chief": {chiefSessionKey, "no chief of staff is set, so the message to \"chief\" has no session: the owner sets one with chief_set"},
	"cto":   {ctoSessionKey, "no CTO session is registered, so the message to \"cto\" has no session"},
}

// ctoSessionKey is the gateway_state row that names the CTO's network session.
const ctoSessionKey = "cto_session"

// resolveRoleAddress returns the session pinned for a role address. isRole is
// false when recipient is an ordinary session ID, and nothing is read then.
func resolveRoleAddress(q queryer, recipient string) (id string, isRole bool, err error) {
	role, ok := roleAddressKeys[recipient]
	if !ok {
		return "", false, nil
	}
	err = q.QueryRow(`SELECT value FROM gateway_state WHERE key=?`, role.key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return "", true, err
	}
	if id == "" {
		return "", true, reject("%s", role.missing)
	}
	return id, true, nil
}
