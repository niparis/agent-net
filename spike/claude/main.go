// Spike: discover Claude Code sessions and post into their inbox socket
// from a foreign process. Throwaway code for step 1 (see docs/review.md).
//
// Frame format learned from OCS src/claude-inject.ts
// (Copyright (c) leeguooooo, MIT): one JSON line per frame, a "user" frame
// whose content is wrapped in <cross-session-message ...>.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type session struct {
	PID                 int    `json:"pid"`
	SessionID           string `json:"sessionId"`
	Name                string `json:"name"`
	Cwd                 string `json:"cwd"`
	Status              string `json:"status"`
	Kind                string `json:"kind"`
	Version             string `json:"version"`
	PeerProtocol        int    `json:"peerProtocol"`
	MessagingSocketPath string `json:"messagingSocketPath"`
}

var pidFile = regexp.MustCompile(`^(\d+)\.json$`)

func sessions() ([]session, error) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".claude", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []session
	for _, e := range entries {
		m := pidFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s session
		if json.Unmarshal(raw, &s) != nil || strconv.Itoa(s.PID) != m[1] {
			continue
		}
		if syscall.Kill(s.PID, 0) != nil && syscall.Kill(s.PID, 0) != syscall.EPERM {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func wrap(from, fromName, body string) string {
	var attrs []string
	if from != "" {
		attrs = append(attrs, fmt.Sprintf(`from="%s"`, from))
	}
	attrs = append(attrs, fmt.Sprintf(`from-name="%s"`, fromName), `from-mode="prompting"`)
	return fmt.Sprintf("<cross-session-message %s>\n%s\n</cross-session-message>", strings.Join(attrs, " "), body)
}

func send(sock, from, fromName, body, priority, msgID string) error {
	frame := map[string]any{
		"type":     "user",
		"msgV":     1,
		"msg_id":   msgID,
		"priority": priority,
		"message":  map[string]any{"role": "user", "content": wrap(from, fromName, body)},
	}
	if from != "" {
		frame["from"] = from
	}
	line, _ := json.Marshal(frame)
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write(append(line, '\n'))
	return err
}

// listen binds a reply socket and prints every line that arrives.
func listen(path string, wait time.Duration) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	os.Chmod(path, 0o600)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				sc := bufio.NewScanner(c)
				sc.Buffer(make([]byte, 1<<20), 1<<20)
				for sc.Scan() {
					fmt.Printf("%s RECV %s\n", time.Now().Format("15:04:05.000"), sc.Text())
				}
			}(c)
		}
	}()
	time.Sleep(wait)
	ln.Close()
	os.Remove(path)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: claude-spike list | send -to NAME -body TEXT [-reply SOCK -wait 120s]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "list":
		ss, err := sessions()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, s := range ss {
			fmt.Printf("%-8d %-22s %-6s %-12s v%s proto=%d %s\n", s.PID, s.Name, s.Status, s.Kind, s.Version, s.PeerProtocol, s.Cwd)
		}
	case "send":
		fs := flag.NewFlagSet("send", flag.ExitOnError)
		to := fs.String("to", "", "session name")
		body := fs.String("body", "", "message body")
		reply := fs.String("reply", "", "reply socket path to bind")
		wait := fs.Duration("wait", 0, "how long to listen on the reply socket")
		priority := fs.String("priority", "next", "now|next|later")
		fs.Parse(os.Args[2:])
		ss, _ := sessions()
		var target *session
		for i := range ss {
			if ss[i].Name == *to {
				target = &ss[i]
			}
		}
		if target == nil {
			fmt.Fprintln(os.Stderr, "no live session named", *to)
			os.Exit(1)
		}
		from := ""
		done := make(chan struct{})
		if *reply != "" {
			from = "uds:" + *reply
			go func() { listen(*reply, *wait); close(done) }()
			time.Sleep(200 * time.Millisecond)
		} else {
			close(done)
		}
		id := uuid()
		start := time.Now()
		if err := send(target.MessagingSocketPath, from, "agent-net-spike", *body, *priority, id); err != nil {
			fmt.Fprintln(os.Stderr, "send:", err)
			os.Exit(1)
		}
		fmt.Printf("%s SENT msg_id=%s to=%s status_before=%s write=%s\n", time.Now().Format("15:04:05.000"), id, target.Name, target.Status, time.Since(start))
		<-done
	}
}
