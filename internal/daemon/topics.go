package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/niparis/agent-net/internal/protocol"
)

type topicSubscriber struct {
	wake    chan struct{}
	pending map[string]uint64
}

type topicHub struct {
	setup    sync.Mutex // one subscription request per gateway link
	mu       sync.Mutex
	link     *link
	versions map[string]uint64
	subs     map[*localConn]*topicSubscriber
}

func (h *topicHub) subscribe(ctx context.Context, l *link, lc *localConn, request func(context.Context) (map[string]uint64, error)) (map[string]uint64, error) {
	h.setup.Lock()
	defer h.setup.Unlock()
	h.mu.Lock()
	if h.subs == nil {
		h.subs = make(map[*localConn]*topicSubscriber)
	}
	if _, exists := h.subs[lc]; exists {
		h.mu.Unlock()
		return nil, errors.New("connection already subscribed to topics")
	}
	if h.link != l {
		h.link, h.versions = l, nil
	}
	state := &topicSubscriber{wake: make(chan struct{}, 1), pending: map[string]uint64{}}
	h.subs[lc] = state
	need := h.versions == nil
	h.mu.Unlock()
	if need {
		if err := h.fill(ctx, l, request); err != nil {
			h.remove(lc)
			return nil, err
		}
	}
	h.mu.Lock()
	versions := make(map[string]uint64, len(h.versions))
	for topic, v := range h.versions {
		versions[topic] = v
	}
	h.mu.Unlock()
	return versions, nil
}

// fill asks the gateway for the current versions and keeps the newest of what
// it answers and what was pushed meanwhile. It does not take setup.
func (h *topicHub) fill(ctx context.Context, l *link, request func(context.Context) (map[string]uint64, error)) error {
	versions, err := request(ctx)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Pushes can precede the result. Preserve the newest version.
	if h.link != l {
		return errors.New("gateway link changed")
	}
	if h.versions == nil {
		h.versions = make(map[string]uint64)
	}
	for topic, v := range versions {
		if v > h.versions[topic] {
			h.versions[topic] = v
		} else if _, ok := h.versions[topic]; !ok {
			h.versions[topic] = v
		}
	}
	return nil
}

// ensure makes the daemon itself follow the topics of l, with no local
// client, so versions answers. The gateway pushes every change after the first
// subscription on the link, so a later call costs nothing.
func (h *topicHub) ensure(ctx context.Context, l *link, request func(context.Context) (map[string]uint64, error)) error {
	h.setup.Lock()
	defer h.setup.Unlock()
	h.mu.Lock()
	if h.link != l {
		h.link, h.versions = l, nil
	}
	need := h.versions == nil
	h.mu.Unlock()
	if !need {
		return nil
	}
	return h.fill(ctx, l, request)
}

// versionsOf returns the versions of the topics as the gateway last told this
// daemon on l, and false when it has not (no subscription, another link).
func (h *topicHub) versionsOf(l *link, topics ...string) ([]uint64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.link != l || h.versions == nil {
		return nil, false
	}
	out := make([]uint64, len(topics))
	for i, t := range topics {
		v, ok := h.versions[t]
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

func (h *topicHub) publish(l *link, event protocol.TopicEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.link != l || event.Topic == "" {
		return
	}
	if h.versions == nil {
		h.versions = make(map[string]uint64)
	}
	if old, ok := h.versions[event.Topic]; ok && event.Version <= old {
		return
	}
	h.versions[event.Topic] = event.Version
	for _, sub := range h.subs {
		sub.pending[event.Topic] = event.Version
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (h *topicHub) take(lc *localConn) map[string]uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if sub := h.subs[lc]; sub != nil {
		pending := sub.pending
		sub.pending = make(map[string]uint64)
		return pending
	}
	return nil
}

func (h *topicHub) remove(lc *localConn) { h.mu.Lock(); delete(h.subs, lc); h.mu.Unlock() }

func (h *topicHub) disconnected(l *link) {
	h.mu.Lock()
	if h.link != l {
		h.mu.Unlock()
		return
	}
	h.link, h.versions = nil, nil
	var clients []*localConn
	for lc := range h.subs {
		clients = append(clients, lc)
		delete(h.subs, lc)
	}
	h.mu.Unlock()
	for _, lc := range clients {
		lc.close()
	}
}

func (lc *localConn) subscribeTopics(ctx context.Context) (any, error) {
	if err := lc.operatorGate(protocol.OpTopicsSubscribe); err != nil {
		return nil, err
	}
	lc.d.mu.Lock()
	l := lc.d.lk
	lc.d.mu.Unlock()
	if l == nil {
		return nil, errNotConnected
	}
	h := &lc.d.topics
	versions, err := h.subscribe(ctx, l, lc, requestTopicVersions(l))
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	sub := h.subs[lc]
	h.mu.Unlock()
	if sub == nil {
		return nil, errNotConnected
	}
	go func() {
		for {
			select {
			case <-lc.closed:
				return
			case <-sub.wake:
				for topic, v := range h.take(lc) {
					if lc.writeJSON(map[string]any{"method": protocol.FrameTopicChanged, "params": protocol.TopicEvent{Topic: topic, Version: v}}) != nil {
						lc.close()
						return
					}
				}
			}
		}
	}()
	return versions, nil
}

// requestTopicVersions subscribes the link to the gateway's topic pushes and
// returns the current versions.
func requestTopicVersions(l *link) func(context.Context) (map[string]uint64, error) {
	return func(ctx context.Context) (map[string]uint64, error) {
		raw, err := l.call(ctx, protocol.FrameTopicsSubscribe, nil)
		if err != nil {
			return nil, err
		}
		var v map[string]uint64
		err = json.Unmarshal(raw, &v)
		if err == nil && len(v) != len(protocol.Topics) {
			err = errors.New("incomplete topic subscription")
		}
		return v, err
	}
}
