package gateway

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Topic dependencies include the tables of the cached reads and the other
// reads used by the screen. Column keys keep session activity from moving graph.
var topicTables = map[string][]string{
	protocol.TopicAgents:   agentsTables,
	protocol.TopicNodes:    tableUnion(nodesTables, pickerTables, []string{"quota_blocks", "nodes.health_report", "nodes.health_reported_at", "playbook", "setting_values"}),
	protocol.TopicWorkers:  tableUnion(workerLogTables, pickerTables, []string{"worker_starts", "quota_blocks", "setting_values"}),
	protocol.TopicGraph:    tableUnion(graphTables, []string{"charter_drafts"}),
	protocol.TopicChanges:  []string{"delivery_changes", "delivery_revisions", "delivery_evidence", "delivery_step_tasks", "events", "messages"},
	protocol.TopicProjects: tableUnion([]string{"projects", "repos", "sessions", "nodes.id", "nodes.hostname"}, graphTables),
	protocol.TopicUsage:    tableUnion(nodesTables, []string{"sessions", "quota_blocks"}),
}

// No screen topic follows gateway_state, even if a cached read declares it by
// table or by column. A value there that a screen does show moves its topics
// through bumpStateTopics instead, because most keys of gateway_state are
// markers that no screen shows.
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
	s.stateTopicBumps.Range(func(topic, n any) bool {
		versions[topic.(string)] += n.(*atomic.Uint64).Load()
		return true
	})
	return versions
}

// bumpStateTopics moves the topics of the screens that show a gateway_state
// value the caller just wrote. Call it after the write. A caller inside a
// transaction may bump before its commit: a rolled back transaction then moves
// a topic for nothing, which costs a refresh, never a missed change. The count
// is in memory: the gateway that writes the value also samples the topics, and
// a restart makes every subscriber subscribe again.
func (s *Store) bumpStateTopics(topics ...string) {
	for _, topic := range topics {
		n, _ := s.stateTopicBumps.LoadOrStore(topic, new(atomic.Uint64))
		n.(*atomic.Uint64).Add(1)
	}
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
