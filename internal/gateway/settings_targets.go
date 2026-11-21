package gateway

import (
	"database/sql"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// ResolveSettingsTarget turns one named selector into a typed target. The
// level disambiguates kinds, so repo shop and agent shop never collide. A name
// is looked up only in its own kind. An ambiguous name returns typed choices;
// an unknown name is refused and never guessed.
func (s *Store) ResolveSettingsTarget(t protocol.SettingsTarget) (protocol.SettingsTargetResolution, error) {
	level, name := strings.TrimSpace(t.Level), strings.TrimSpace(t.Name)
	out := protocol.SettingsTargetResolution{Level: level, Name: name}
	switch level {
	case "network":
		if name != "" {
			return out, reject("network target takes no name")
		}
		tx, err := s.db.Begin()
		if err != nil {
			return out, err
		}
		defer tx.Rollback()
		token, err := targetTokenTx(tx, "network", "network")
		if err != nil {
			return out, err
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		out.Target = &protocol.SettingsRef{Kind: "network", ID: "network", Name: "network", Token: token}
		return out, nil
	case "team", "repo", "agent":
		if name == "" {
			return out, reject("%s target needs a name", level)
		}
		query := `SELECT id,display_name FROM repos WHERE display_name=? ORDER BY id`
		switch level {
		case "agent":
			query = `SELECT id,display_name FROM agents WHERE display_name=? AND tombstone=0 ORDER BY id`
		case "team":
			query = `SELECT id,name FROM teams WHERE name=? AND active=1 ORDER BY id`
		}
		rows, err := s.db.Query(query, name)
		if err != nil {
			return out, err
		}
		var ids, names []string
		for rows.Next() {
			var id, display string
			if err = rows.Scan(&id, &display); err != nil {
				rows.Close()
				return out, err
			}
			ids = append(ids, id)
			names = append(names, display)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		if len(ids) == 0 {
			return out, reject("unknown %s name %q", level, name)
		}
		if len(ids) > 1 {
			for i := range ids {
				out.Choices = append(out.Choices, protocol.SettingsTargetChoice{
					Ref:  protocol.SettingsRef{Kind: level, ID: ids[i], Name: names[i]},
					Name: names[i],
				})
			}
			return out, nil
		}
		tx, err := s.db.Begin()
		if err != nil {
			return out, err
		}
		defer tx.Rollback()
		token, err := targetTokenTx(tx, level, ids[0])
		if err != nil {
			return out, err
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		out.Target = &protocol.SettingsRef{Kind: level, ID: ids[0], Name: names[0], Token: token}
		return out, nil
	}
	return out, reject("unknown settings target level %q", level)
}

// networkTokenKey is the gateway_state key of the network's token.
const networkTokenKey = "settings_network_token"

// settingsTokenTables names the table whose settings_token column holds the
// token of each target kind.
var settingsTokenTables = map[string]string{"repo": "repos", "agent": "agents", "team": "teams"}

// targetTokenTx returns the durable token for one typed target, creating it on
// first use. A rename keeps the token. A deleted row takes its token with it,
// and a tombstoned agent loses it (a trigger), so a recreation gets a fresh one.
func targetTokenTx(tx *sql.Tx, kind, id string) (token string, err error) {
	table := settingsTokenTables[kind]
	if table == "" {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO gateway_state(key,value) VALUES(?,lower(hex(randomblob(16))))`, networkTokenKey); err == nil {
			err = tx.QueryRow(`SELECT value FROM gateway_state WHERE key=?`, networkTokenKey).Scan(&token)
		}
		return token, err
	}
	if _, err = tx.Exec(`UPDATE `+table+` SET settings_token=lower(hex(randomblob(16))) WHERE id=? AND settings_token=''`, id); err == nil {
		err = tx.QueryRow(`SELECT settings_token FROM `+table+` WHERE id=?`, id).Scan(&token)
	}
	return token, err
}

// ambiguousSettingsTargetError names every candidate and never picks one.
func ambiguousSettingsTargetError(t protocol.SettingsTarget, choices []protocol.SettingsTargetChoice) error {
	parts := make([]string, 0, len(choices))
	for _, c := range choices {
		parts = append(parts, c.Ref.Kind+" "+c.Name+" (id "+c.Ref.ID+")")
	}
	return reject("%s name %q is ambiguous: %s", t.Level, t.Name, strings.Join(parts, ", "))
}
