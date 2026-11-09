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

const (
	// FrameWorkersWatch asks the gateway to watch the workers panel rows of
	// some owner sessions and push FrameWorkersChanged when they change. The
	// set replaces the one asked for before on the same connection.
	FrameWorkersWatch   = "workers.watch"
	FrameWorkersChanged = "workers.changed"
)

// WorkersWatch is the FrameWorkersWatch request: every owner session whose
// workers the daemon still serves from a kept answer.
type WorkersWatch struct {
	Owners []string `json:"owners"`
}

// WorkersWatchResult answers FrameWorkersWatch with the current revision of
// each owner. A revision only goes up on one connection, and it changes
// exactly when the rows a workers query for that owner reads change.
type WorkersWatchResult struct {
	Revs map[string]uint64 `json:"revs"`
}

// WorkersChanged is the FrameWorkersChanged push: the owner's rows moved to
// this revision.
type WorkersChanged struct {
	Owner string `json:"owner"`
	Rev   uint64 `json:"rev"`
}
