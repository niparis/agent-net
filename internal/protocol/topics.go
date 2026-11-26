package protocol

const (
	FrameTopicsSubscribe = "topics.subscribe"
	FrameTopicChanged    = "topic.changed"
	OpTopicsSubscribe    = "topics_subscribe"
	TopicAgents          = "agents"
	TopicNodes           = "nodes"
	TopicWorkers         = "workers"
	TopicGraph           = "graph"
	TopicInbox           = "inbox"
	TopicChanges         = "changes"
	TopicProjects        = "projects"
	TopicLearned         = "learned"
	TopicUsage           = "usage"
)

var Topics = []string{TopicAgents, TopicNodes, TopicWorkers, TopicGraph, TopicInbox, TopicChanges, TopicProjects, TopicLearned, TopicUsage}

type TopicEvent struct {
	Topic   string `json:"topic"`
	Version uint64 `json:"version"`
}
