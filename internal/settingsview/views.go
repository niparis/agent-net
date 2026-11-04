package settingsview

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Views resolves settings and attaches their complete history for a reader.
func Views(snapshot settings.Snapshot, history []protocol.SettingsHistory, q protocol.SettingsReadRequest, owner bool) ([]protocol.SettingView, error) {
	return ViewsWithWhere(snapshot, history, q, owner, settings.Where{})
}

// ViewsWithWhere also resolves host-dependent defaults for the local machine.
func ViewsWithWhere(snapshot settings.Snapshot, history []protocol.SettingsHistory, q protocol.SettingsReadRequest, owner bool, where settings.Where) ([]protocol.SettingView, error) {
	snap := snapshot
	where.Repo = q.Repo
	out := []protocol.SettingView{}
	for _, def := range settings.Standard().Definitions {
		if (q.Key != "" && q.Key != def.Key) || (!owner && def.Visibility == "owner") {
			continue
		}
		e, err := snap.Resolve(def.Key, where)
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
			valueKey := def.Key
			if snap.DeliveryPipelineCutover && settings.DeliveryCanonicalKey(def.Key) != "" {
				valueKey = settings.DeliveryCanonicalKey(def.Key)
			}
			if v.Key == valueKey && v.Level == level && v.Scope == scope && v.Item == "" {
				row.Explicit = v.Value
				if valueKey != def.Key {
					if shorthand, err := settings.DeliveryShorthandValue(def.Key, v.Value); err == nil {
						if string(shorthand) != `"custom"` {
							row.Explicit = shorthand
						}
					}
				}
				break
			}
		}
		for _, h := range history {
			if snap.DeliveryPipelineCutover && h.Key == settings.DeliveryCanonicalKey(def.Key) {
				h.Key = def.Key
				if h.Old != nil {
					if v, err := settings.DeliveryShorthandValue(def.Key, h.Old); err == nil {
						h.Old = v
					}
				}
				if h.New != nil {
					if v, err := settings.DeliveryShorthandValue(def.Key, h.New); err == nil {
						h.New = v
					}
				}
			}
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
	if q.Key == "" || q.Key == "delivery.pipeline" {
		p, err := protocol.PipelineFromSettings(snap, q.Repo)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		var sources []string
		keys := []string{"delivery.pipeline.merge.requires", "delivery.pipeline.merge.approver", "delivery.pipeline.deploy.approver"}
		if !snap.DeliveryPipelineCutover {
			keys = []string{"delivery.tests_before_merge", "delivery.merge_authority", "delivery.deploy_rule"}
		}
		for _, key := range keys {
			e, err := snap.Resolve(key, where)
			if err == nil && !containsSource(sources, e.Source) {
				sources = append(sources, e.Source)
			}
		}
		for _, a := range snap.DeliveryAssignments {
			if a.Project == q.Repo {
				sources = append(sources, "graph assignments")
				break
			}
		}
		out = append(out, protocol.SettingView{Key: "delivery.pipeline", Description: "Resolved pipeline. Change its ordered rules or graph-backed owner rows.", Kind: "rule", Type: "pipeline_view", Levels: []string{"network", "repo"}, Live: true, Value: value, Source: strings.Join(sources, ", "), Revision: snap.Revision, History: []protocol.SettingsHistory{}})
	}
	if q.Key != "" && len(out) == 0 {
		return nil, fmt.Errorf("unknown setting %s", q.Key)
	}
	return out, nil
}

func containsSource(sources []string, source string) bool {
	for _, s := range sources {
		if s == source {
			return true
		}
	}
	return false
}
