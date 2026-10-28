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
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

type Client struct {
	c    net.Conn
	wmu  sync.Mutex
	mu   sync.Mutex
	next int
	wait map[string]chan response
	dead chan struct{}
}

func Dial(path string) (*Client, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	cl := &Client{c: c, wait: map[string]chan response{}, dead: make(chan struct{})}
	go cl.read()
	return cl, nil
}

func (c *Client) Close() error { return c.c.Close() }

func (c *Client) read() {
	defer close(c.dead)
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		var r response
		if json.Unmarshal(sc.Bytes(), &r) != nil || len(r.ID) == 0 {
			continue // pushes are for plugins; this client ignores them
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
			return nil, errors.New(r.Error)
		}
		return r.Result, nil
	case <-c.dead:
		return nil, errors.New("daemon connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
