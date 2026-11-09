package operator

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// TopicSubscription coalesces pushes by topic, so a slow screen cannot block
// the daemon's socket reader or lose the latest version of any topic.
type TopicSubscription struct {
	conn    net.Conn
	mu      sync.Mutex
	pending map[string]uint64
	wake    chan struct{}
	done    chan struct{}
}

func (s *TopicSubscription) add(topic string, version uint64) {
	s.mu.Lock()
	if old, ok := s.pending[topic]; !ok || version > old {
		s.pending[topic] = version
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Wait returns the most recent versions since the preceding call. A closed
// connection returns false, prompting the caller to fall back and resubscribe.
func (s *TopicSubscription) Wait() (map[string]uint64, bool) {
	select {
	case <-s.done:
		return nil, false
	case <-s.wake:
		s.mu.Lock()
		pending := s.pending
		s.pending = make(map[string]uint64)
		s.mu.Unlock()
		return pending, true
	}
}

func (s *TopicSubscription) Close() { s.conn.Close() }

type topicResponse struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// SubscribeTopics uses a separate local connection so its lifetime follows the
// gateway subscription independently of ordinary calls.
func (s *Client) SubscribeTopics(ctx context.Context) (*TopicSubscription, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", s.path)
	if err != nil {
		return nil, err
	}
	sub := &TopicSubscription{conn: conn, pending: map[string]uint64{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	fail := func(err error) (*TopicSubscription, error) { conn.Close(); return nil, err }
	deadline := time.Now().Add(Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	enc, dec := json.NewEncoder(conn), json.NewDecoder(bufio.NewReader(conn))
	for _, req := range []struct {
		id, method string
		params     any
	}{
		{"1", "hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol}},
		{"2", protocol.OpTopicsSubscribe, nil},
	} {
		if err := enc.Encode(map[string]any{"id": req.id, "method": req.method, "params": req.params}); err != nil {
			return fail(err)
		}
		for {
			var r topicResponse
			if err := dec.Decode(&r); err != nil {
				return fail(err)
			}
			if r.Method == protocol.FrameTopicChanged {
				var ev protocol.TopicEvent
				if json.Unmarshal(r.Params, &ev) == nil {
					sub.add(ev.Topic, ev.Version)
				}
				continue
			}
			if r.ID != req.id {
				return fail(errors.New("unexpected subscription response"))
			}
			if r.Error != "" {
				return fail(errors.New(r.Error))
			}
			if req.id == "2" {
				var versions map[string]uint64
				if err := json.Unmarshal(r.Result, &versions); err != nil {
					return fail(err)
				}
				for _, topic := range protocol.Topics {
					v, ok := versions[topic]
					if !ok {
						return fail(fmt.Errorf("subscription missing topic %s", topic))
					}
					sub.add(topic, v)
				}
			}
			break
		}
	}
	conn.SetDeadline(time.Time{})
	s.mu.Lock()
	if s.topicSubs == nil {
		s.topicSubs = make(map[*TopicSubscription]bool)
	}
	s.topicSubs[sub] = true
	s.mu.Unlock()
	go func() {
		defer close(sub.done)
		defer conn.Close()
		defer func() { s.mu.Lock(); delete(s.topicSubs, sub); s.mu.Unlock() }()
		for {
			var r topicResponse
			if dec.Decode(&r) != nil {
				return
			}
			if r.Method == protocol.FrameTopicChanged {
				var ev protocol.TopicEvent
				if json.Unmarshal(r.Params, &ev) == nil && ev.Topic != "" {
					sub.add(ev.Topic, ev.Version)
				}
			}
		}
	}()
	return sub, nil
}
