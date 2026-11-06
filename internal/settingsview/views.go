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
	return ViewsWithTarget(snapshot, history, q, owner, where, nil)
}

// ViewsWithTarget reads at one resolved target. The legacy Repo field keeps its
// old meaning; a typed target sets exactly one level and never guesses a kind.
func ViewsWithTarget(snapshot settings.Snapshot, history []protocol.SettingsHistory, q protocol.SettingsReadRequest, owner bool, where settings.Where, target *protocol.SettingsRef) ([]protocol.SettingView, error) {
	snap := snapshot
	where.Repo, where.Agent = q.Repo, ""
	if target != nil {
		where.Repo, where.Agent = "", ""
		switch target.Kind {
		case "network":
		case "repo":
			where.Repo = target.ID
		case "agent":
			where.Agent = target.ID
		default:
			return nil, fmt.Errorf("unknown settings target kind %q", target.Kind)
		}
	}
	level, scope := requestedAddress(q, target)
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
		row.SourceLevel = e.SourceLevel
		row.SourceRef = sourceRef(e, target)
		row.SourceLabel = sourceLabel(e, target)
		row.Raised = e.Raised
		row.Bound = e.Bound
		row.RecipeDigest = settings.RecipeHash()
		row.ContextRevision = snap.Revision
		row.Target = target
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
		if row.Explicit == nil {
			row.ExplicitNote = "not set here"
		}
		sourceText := levelText(level, scope)
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
			if row.Actor == "" && ((e.Source == "network" && h.Level == "network") || (e.Source == sourceText && h.Level == level && h.Scope == scope)) {
				row.Actor = h.Actor
			}
		}
		out = append(out, row)
	}
	if q.Key == "" || q.Key == "delivery.pipeline" {
		p, err := protocol.PipelineFromSettings(snap, where.Repo)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		var sources []string
		keys := []string{"delivery.pipeline.review.required_from", "delivery.pipeline.review.independent", "delivery.pipeline.review.requires", "delivery.pipeline.merge.requires", "delivery.pipeline.merge.approver", "delivery.pipeline.deploy.approver"}
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
			if a.Project == where.Repo {
				sources = append(sources, "graph assignments")
				break
			}
		}
		out = append(out, protocol.SettingView{Key: "delivery.pipeline", Description: "Resolved pipeline. Change its ordered rules or graph-backed owner rows.", Kind: "rule", Type: "pipeline_view", Levels: []string{"network", "repo"}, Live: true, Value: value, Source: strings.Join(sources, ", "), Revision: snap.Revision, History: []protocol.SettingsHistory{}, RecipeDigest: settings.RecipeHash(), ContextRevision: snap.Revision, Target: target})
	}
	if q.Key != "" && len(out) == 0 {
		return nil, fmt.Errorf("unknown setting %s", q.Key)
	}
	return out, nil
}

// requestedAddress is the explicit address a read asks about. The legacy Repo
// field keeps its old repo meaning; no target means network.
func requestedAddress(q protocol.SettingsReadRequest, target *protocol.SettingsRef) (string, string) {
	if target != nil {
		switch target.Kind {
		case "repo", "agent":
			return target.Kind, target.ID
		default:
			return "network", "network"
		}
	}
	if q.Repo != "" {
		return "repo", q.Repo
	}
	return "network", "network"
}

func levelText(level, scope string) string {
	if level == "network" {
		return "network"
	}
	return level + " " + scope
}

// sourceRef keeps identity typed. It never recovers an ID by parsing Source.
func sourceRef(e settings.Effective, target *protocol.SettingsRef) *protocol.SettingsRef {
	switch e.SourceLevel {
	case "recipe":
		return &protocol.SettingsRef{Kind: "recipe", Name: "standard recipe"}
	case "network":
		return &protocol.SettingsRef{Kind: "network", ID: "network", Name: "network"}
	case "repo", "agent":
		name := e.SourceID
		if target != nil && target.Kind == e.SourceLevel && target.ID == e.SourceID && target.Name != "" {
			name = target.Name
		}
		return &protocol.SettingsRef{Kind: e.SourceLevel, ID: e.SourceID, Name: name}
	case "":
		return nil
	default:
		return &protocol.SettingsRef{Kind: e.SourceLevel, Name: e.SourceLevel}
	}
}

func sourceLabel(e settings.Effective, target *protocol.SettingsRef) string {
	ref := sourceRef(e, target)
	if ref == nil {
		return ""
	}
	if e.SourceLevel == "recipe" || e.SourceLevel == "network" {
		return ref.Name
	}
	label := ref.Kind + " " + ref.Name
	if e.SourceNote != "" {
		label += ", " + e.SourceNote
	}
	return label
}

func containsSource(sources []string, source string) bool {
	for _, s := range sources {
		if s == source {
			return true
		}
	}
	return false
}
