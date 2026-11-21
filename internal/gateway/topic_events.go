package gateway

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Topic dependencies include the tables of the cached reads and the other
// reads used by the screen. Column keys keep session activity from moving graph.
var topicTables = map[string][]string{
	protocol.TopicAgents:   agentsTables,
	protocol.TopicNodes:    tableUnion(nodesTables, calibrationTables, []string{"quota_blocks", "nodes.health_report", "nodes.health_reported_at", "playbook", "setting_values"}),
	protocol.TopicWorkers:  tableUnion(workerLogTables, calibrationTables, []string{"worker_starts", "quota_blocks", "setting_values"}),
	protocol.TopicGraph:    graphTables,
	protocol.TopicInbox:    tableUnion(inboxTables, []string{"messages", "ask_sorts", "desk_log", "charter_drafts", "setting_values"}),
	protocol.TopicChanges:  []string{"delivery_changes", "delivery_revisions", "delivery_evidence", "delivery_step_tasks", "delivery_step_inbox", "events", "messages"},
	protocol.TopicProjects: tableUnion([]string{"projects", "repos", "sessions", "nodes.id", "nodes.hostname"}, graphTables),
	protocol.TopicLearned:  learnedListTables,
	protocol.TopicUsage:    tableUnion(nodesTables, []string{"sessions", "quota_blocks"}),
}

// No screen topic follows gateway_state, even if a cached read declares it by
// table or by column (calibration reads gateway_state.key and .value for the
// worker_runs_version counter, which moves in the same transaction as the
// worker_runs write that the topic already follows).
func init() {
	for topic, tables := range topicTables {
		topicTables[topic] = slices.DeleteFunc(slices.Clone(tables), func(t string) bool {
			return t == "gateway_state" || strings.HasPrefix(t, "gateway_state.")
		})
	}
}

func (s *Store) topicVersions() map[string]uint64 {
	versions := make(map[string]uint64, len(topicTables))
	for topic, tables := range topicTables {
		versions[topic] = s.readStamp(tables)
	}
	return versions
}

func (g *Gateway) topicVersions() map[string]uint64 {
	v := g.store.topicVersions()
	v[protocol.TopicUsage] += g.usage.version.Load()
	v[protocol.TopicNodes] += g.usage.quotaVersion.Load()
	// Nodes, agent views, worker profile availability and project layouts use
	// live connections. MCP and inventory readers also watch the nodes topic.
	connections := g.connVersion.Load()
	for _, topic := range []string{protocol.TopicNodes, protocol.TopicAgents, protocol.TopicWorkers, protocol.TopicProjects} {
		v[topic] += connections
	}
	return v
}

// One sampler per subscribed node connection sends only the latest version of
// each changed topic. The counters themselves are shared by all subscribers.
func (g *Gateway) subscribeTopics(ctx context.Context, nc *nodeConn) map[string]uint64 {
	v := g.topicVersions()
	if nc.topicSubscribed.CompareAndSwap(false, true) {
		go func(v map[string]uint64) {
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-nc.dead:
					return
				case <-ticker.C:
					next := g.topicVersions()
					for _, topic := range protocol.Topics {
						if next[topic] == v[topic] {
							continue
						}
						frame, _ := json.Marshal(struct {
							Type string `json:"type"`
							protocol.TopicEvent
						}{protocol.FrameTopicChanged, protocol.TopicEvent{Topic: topic, Version: next[topic]}})
						if nc.write(ctx, frame) != nil {
							return
						}
					}
					v = next
				}
			}
		}(v)
	}
	return v
}
