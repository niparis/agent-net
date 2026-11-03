package settings

import (
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

// Views resolves settings and attaches their complete history for a reader.
func Views(snapshot Snapshot, history []protocol.SettingsHistory, q protocol.SettingsReadRequest, owner bool) ([]protocol.SettingView, error) {
	snap := snapshot
	out := []protocol.SettingView{}
	for _, def := range Standard().Definitions {
		if (q.Key != "" && q.Key != def.Key) || (!owner && def.Visibility == "owner") {
			continue
		}
		e, err := snap.Resolve(def.Key, Where{Repo: q.Repo})
		if err != nil {
			return nil, err
		}
		row := protocol.SettingView{Key: def.Key, Description: def.Description, Kind: def.Kind, Type: def.Type, Levels: def.Levels, Values: def.Values, Live: def.Live, Value: e.Value, Source: e.Source, Revision: e.Revision, History: []protocol.SettingsHistory{}}
		level, scope := "network", "network"
		if q.Repo != "" {
			for _, l := range def.Levels {
				if l == "repo" {
					level, scope = "repo", q.Repo
					break
				}
			}
		}
		for _, v := range snap.Values {
			if v.Key == def.Key && v.Level == level && v.Scope == scope && v.Item == "" {
				row.Explicit = v.Value
				break
			}
		}
		for _, h := range history {
			if h.Key != def.Key {
				continue
			}
			row.History = append(row.History, h)
			if row.Actor == "" && ((e.Source == "network" && h.Level == "network") || (e.Source == "repo "+q.Repo && h.Level == "repo" && h.Scope == q.Repo)) {
				row.Actor = h.Actor
			}
		}
		out = append(out, row)
	}
	if q.Key != "" && len(out) == 0 {
		return nil, fmt.Errorf("unknown setting %s", q.Key)
	}
	return out, nil
}
