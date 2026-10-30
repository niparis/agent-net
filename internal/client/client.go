// Package client talks to the daemon's local socket (JSON lines).
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

type response struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Params json.RawMessage `json:"params"`
	Error  string          `json:"error"`
}

// RemoteError is an error the daemon answered with. Any other error from Call
// is a problem with the connection itself.
type RemoteError string

func (e RemoteError) Error() string { return string(e) }

// IsConnError reports whether err means the connection failed (as opposed to
// the daemon refusing the request or the caller's context ending).
func IsConnError(err error) bool {
	var re RemoteError
	return err != nil && !errors.As(err, &re) &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

type Client struct {
	c    net.Conn
	wmu  sync.Mutex
	mu   sync.Mutex
	next int
	wait map[string]chan response
	dead chan struct{}
	// onPush gets the daemon's pushes (frames without an id).
	onPush func(method string, params json.RawMessage)
}

func Dial(path string) (*Client, error) { return DialPush(path, nil) }

// DialPush is Dial with a handler for pushes such as "mcp_changed".
func DialPush(path string, onPush func(method string)) (*Client, error) {
	return DialPushData(path, func(method string, _ json.RawMessage) {
		if onPush != nil {
			onPush(method)
		}
	})
}

// DialPushData also supplies the JSON parameters of a push.
func DialPushData(path string, onPush func(string, json.RawMessage)) (*Client, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	cl := &Client{c: c, wait: map[string]chan response{}, dead: make(chan struct{}), onPush: onPush}
	go cl.read()
	return cl, nil
}

func (c *Client) Close() error { return c.c.Close() }

// Dead reports whether the connection has closed.
func (c *Client) Dead() bool {
	select {
	case <-c.dead:
		return true
	default:
		return false
	}
}

func (c *Client) read() {
	defer close(c.dead)
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		var r response
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if len(r.ID) == 0 {
			if r.Method != "" && c.onPush != nil {
				c.onPush(r.Method, r.Params)
			}
			continue
		}
		var id string
		json.Unmarshal(r.ID, &id)
		c.mu.Lock()
		ch := c.wait[id]
		c.mu.Unlock()
		if ch != nil {
			ch <- r
		}
	}
}

// Call sends one request and waits for its response.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := strconv.Itoa(c.next)
	ch := make(chan response, 1)
	c.wait[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.wait, id)
		c.mu.Unlock()
	}()
	line, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	c.wmu.Lock()
	_, err = c.c.Write(append(line, '\n'))
	c.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Error != "" {
			return nil, RemoteError(r.Error)
		}
		return r.Result, nil
	case <-c.dead:
		return nil, errors.New("daemon connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
