package protocol

const (
	FrameTopicsSubscribe = "topics.subscribe"
	FrameTopicChanged    = "topic.changed"
	OpTopicsSubscribe    = "topics_subscribe"
	TopicAgents          = "agents"
	TopicNodes           = "nodes"
	TopicWorkers         = "workers"
	TopicGraph           = "graph"
	TopicChanges         = "changes"
	TopicProjects        = "projects"
	TopicUsage           = "usage"
)

var Topics = []string{TopicAgents, TopicNodes, TopicWorkers, TopicGraph, TopicChanges, TopicProjects, TopicUsage}

type TopicEvent struct {
	Topic   string `json:"topic"`
	Version uint64 `json:"version"`
}
