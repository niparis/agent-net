// Spike: send one agent-net message to a plugin socket (Pi extension or
// OpenCode plugin), print its ack, and listen for a reply. Throwaway code.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

func id() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func stamp() string { return time.Now().Format("15:04:05.000") }

func main() {
	sock := flag.String("sock", "", "plugin socket path")
	body := flag.String("body", "", "message body")
	session := flag.String("session", "", "target session ID (OpenCode)")
	reply := flag.String("reply", "/run/user/1000/ans-ext.sock", "reply socket to bind")
	wait := flag.Duration("wait", 60*time.Second, "how long to wait for a reply")
	flag.Parse()

	os.Remove(*reply)
	ln, err := net.Listen("unix", *reply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	defer os.Remove(*reply)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			sc := bufio.NewScanner(c)
			for sc.Scan() {
				fmt.Printf("%s REPLY %s\n", stamp(), sc.Text())
			}
			c.Close()
		}
	}()

	msg := map[string]string{"msg_id": id(), "from_name": "agent-net-spike", "reply_to": *reply, "body": *body}
	if *session != "" {
		msg["session_id"] = *session
	}
	line, _ := json.Marshal(msg)
	conn, err := net.DialTimeout("unix", *sock, 2*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	conn.Write(append(line, '\n'))
	fmt.Printf("%s SENT %s\n", stamp(), line)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if ack, err := bufio.NewReader(conn).ReadString('\n'); err == nil {
		fmt.Printf("%s ACK %s", stamp(), ack)
	} else {
		fmt.Printf("%s NO-ACK %v\n", stamp(), err)
	}
	conn.Close()
	time.Sleep(*wait)
}
