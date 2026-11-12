package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// readMessageFile calls message_file as the given session of node n.
func (n *filesNet) readMessageFile(node *node, session string, req protocol.MessageFileRequest) (protocol.MessageFileResult, error) {
	n.t.Helper()
	lc := &localConn{d: node.d, client: "mcp", ls: node.d.sessionByID(session)}
	raw, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(10*time.Second))
	defer cancel()
	res, err := lc.dispatch(ctx, "message_file", raw)
	if err != nil {
		return protocol.MessageFileResult{}, err
	}
	out, ok := res.(protocol.MessageFileResult)
	if !ok {
		n.t.Fatalf("message_file answered %T", res)
	}
	return out, nil
}

// receivedFile sends one file to bob and waits until it is in his inbox.
func (n *filesNet) receivedFile(name string, data []byte) string {
	n.t.Helper()
	sent, err := n.send(writeTestFile(n.t, filepath.Join(n.project, name), data))
	if err != nil {
		n.t.Fatal(err)
	}
	n.waitDelivered(sent.Message.ID)
	return sent.Message.ID
}

func TestMessageFilePagesJoinToTheExactBytes(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{})
	// Multi-byte text, so a page edge can fall inside a character.
	want := []byte(strings.Repeat("héllo wörld, línea\n", 40))
	id := n.receivedFile("notes.md", want)
	var got []byte
	var offset int64
	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatal("paging never ended")
		}
		page, err := n.readMessageFile(n.b, n.bob, protocol.MessageFileRequest{MessageID: id, File: "notes.md", Offset: offset, MaxBytes: protocol.MessageFileMinBytes})
		if err != nil {
			t.Fatal(err)
		}
		if page.Type != "text" || page.Name != "notes.md" || page.Size != int64(len(want)) {
			t.Fatalf("page: %+v", page)
		}
		got = append(got, page.Text...)
		if page.NextOffset == 0 {
			break
		}
		offset = page.NextOffset
	}
	if string(got) != string(want) {
		t.Fatalf("joined pages differ: got %d bytes, want %d", len(got), len(want))
	}
}

func TestMessageFileBinaryHasNoContent(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{})
	id := n.receivedFile("shot.png", []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3})
	page, err := n.readMessageFile(n.b, n.bob, protocol.MessageFileRequest{MessageID: id, File: "shot.png"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Type != "binary" || page.Size != 8 || page.Name != "shot.png" || page.Text != "" || page.NextOffset != 0 {
		t.Fatalf("binary page: %+v", page)
	}
}

func TestMessageFileRefusals(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{})
	id := n.receivedFile("notes.md", []byte("hello\n"))
	outside := writeTestFile(t, filepath.Join(t.TempDir(), "secret.txt"), []byte("not for you"))
	// A second file the manifest lists, replaced by a link that leaves the inbox.
	sent, err := n.send(writeTestFile(t, filepath.Join(n.project, "link.txt"), []byte("real")))
	if err != nil {
		t.Fatal(err)
	}
	linkID := sent.Message.ID
	n.waitDelivered(linkID)
	linkPath := filepath.Join(n.inbox(linkID), "link.txt")
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		node    *node
		session string
		req     protocol.MessageFileRequest
		want    string
	}{
		{"sender asks for the receiver's copy", n.a, n.alice, protocol.MessageFileRequest{MessageID: id, File: "notes.md"}, "not the receiver"},
		{"file not in the manifest", n.b, n.bob, protocol.MessageFileRequest{MessageID: id, File: "other.md"}, "no file named"},
		{"path out of the folder", n.b, n.bob, protocol.MessageFileRequest{MessageID: id, File: "../" + id + "/notes.md"}, "no file named"},
		{"absolute path", n.b, n.bob, protocol.MessageFileRequest{MessageID: id, File: outside}, "no file named"},
		{"symlink that leaves the inbox", n.b, n.bob, protocol.MessageFileRequest{MessageID: linkID, File: "link.txt"}, "regular file"},
		{"offset past the end", n.b, n.bob, protocol.MessageFileRequest{MessageID: id, File: "notes.md", Offset: 99}, "offset"},
		{"unknown message", n.b, n.bob, protocol.MessageFileRequest{MessageID: "m_0000000000000000", File: "notes.md"}, "unknown message"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := n.readMessageFile(tc.node, tc.session, tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// The CTO has no file tools: its agent_message_get reads attachments from its
// own inbox, in pages that join to the exact bytes.
func TestCTOMessageGetReadsAttachmentPages(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{})
	// The CTO is the session that received the message.
	if _, err := n.b.d.st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,?)`, n.bob); err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("the review design, línea\n", 50)
	id := n.receivedFile("review.md", []byte(want))
	var got strings.Builder
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatal("paging never ended")
		}
		args, _ := json.Marshal(map[string]any{"message": id, "file": "review.md", "file_cursor": cursor, "max_bytes": 256})
		res, err := n.b.d.readCTOTool(context.Background(), "agent_message_get", args)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(res)
		var out struct {
			Files []struct {
				Name string `json:"name"`
				Size int64  `json:"size"`
			} `json:"files"`
			File struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Cursor string `json:"next_file_cursor"`
			} `json:"file"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Files) != 1 || out.Files[0].Name != "review.md" || out.File.Type != "text" {
			t.Fatalf("result: %s", raw)
		}
		got.WriteString(out.File.Text)
		if cursor = out.File.Cursor; cursor == "" {
			break
		}
	}
	if got.String() != want {
		t.Fatalf("joined pages differ: got %d bytes, want %d", got.Len(), len(want))
	}
}

// A file from a session on the same machine reaches the receiver's inbox
// through the local handoff folder. The receiver reads it in pages, the same
// as a file from another machine.
func TestMessageFileSameMachinePagesJoinToTheExactBytes(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{})
	n.fa.setSessions(
		protocol.SessionInfo{Harness: "fake", NativeID: "na", PID: 1, Cwd: n.project, Name: "alice", Status: "idle"},
		protocol.SessionInfo{Harness: "fake", NativeID: "nc", PID: 1, Cwd: t.TempDir(), Name: "carol", Status: "idle"},
	)
	eventually(t, "carol has an id", func() bool { return n.fa.host.SessionID("fake", "nc") != "" })
	carol := n.fa.host.SessionID("fake", "nc")
	want := []byte(strings.Repeat("héllo wörld, línea\n", 40))
	src := writeTestFile(t, filepath.Join(n.project, "verify-attach.txt"), want)
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()
	res, err := n.a.d.SendFiles(ctx, FileSend{From: n.alice, To: carol, Body: "a local file", Paths: []string{src}})
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	if res.Files[0].Path != FilePathLocal {
		t.Fatalf("path = %q, want local", res.Files[0].Path)
	}
	n.waitDelivered(res.Message.ID)
	var got []byte
	var offset int64
	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatal("paging never ended")
		}
		page, err := n.readMessageFile(n.a, carol, protocol.MessageFileRequest{MessageID: res.Message.ID, File: "verify-attach.txt", Offset: offset, MaxBytes: protocol.MessageFileMinBytes})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page.Text...)
		if page.NextOffset == 0 {
			break
		}
		offset = page.NextOffset
	}
	if string(got) != string(want) {
		t.Fatalf("joined pages differ: got %d bytes, want %d", len(got), len(want))
	}
}

// A session in a turn holds its messages at the gateway, and they go out
// together when it is ready. A file message in that batch must still reach the
// receiver's inbox, so the receiver can read it.
func TestMessageFileHeldWhileReceiverBusyReachesInbox(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{})
	n.fa.setSessions(
		protocol.SessionInfo{Harness: "fake", NativeID: "na", PID: 1, Cwd: n.project, Name: "alice", Status: "idle"},
		protocol.SessionInfo{Harness: "fake", NativeID: "nc", PID: 1, Cwd: t.TempDir(), Name: "carol", Status: "busy"},
	)
	eventually(t, "carol has an id", func() bool { return n.fa.host.SessionID("fake", "nc") != "" })
	carol := n.fa.host.SessionID("fake", "nc")
	eventually(t, "the gateway sees carol busy", func() bool {
		a, ok, err := n.g.gw.Store().Agent(carol)
		return err == nil && ok && a.Status == "busy"
	})
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()
	sendText := func(body string) string {
		t.Helper()
		raw, err := n.a.d.send(ctx, protocol.MsgSend{Sender: n.alice, Recipient: carol, Type: protocol.TypeRequest, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		var stored struct {
			Message protocol.Message `json:"message"`
		}
		if err := json.Unmarshal(raw, &stored); err != nil || stored.Message.ID == "" {
			t.Fatalf("msg.send answered %s: %v", raw, err)
		}
		return stored.Message.ID
	}
	want := "held with the others"
	src := writeTestFile(t, filepath.Join(n.project, "verify-attach.txt"), []byte(want))
	first := sendText("first")
	res, err := n.a.d.SendFiles(ctx, FileSend{From: n.alice, To: carol, Body: "a file", Paths: []string{src}})
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	second := sendText("second")
	fileID := res.Message.ID
	eventually(t, "the messages wait at the gateway", func() bool {
		for _, id := range []string{first, fileID, second} {
			m, ok, err := n.g.gw.Store().Message(id)
			if err != nil || !ok || !m.HeldForBusy {
				return false
			}
		}
		return true
	})
	n.fa.setSessions(
		protocol.SessionInfo{Harness: "fake", NativeID: "na", PID: 1, Cwd: n.project, Name: "alice", Status: "idle"},
		protocol.SessionInfo{Harness: "fake", NativeID: "nc", PID: 1, Cwd: t.TempDir(), Name: "carol", Status: "idle"},
	)
	for _, id := range []string{first, second} {
		n.waitDelivered(id)
	}
	n.waitDelivered(fileID)
	page, err := n.readMessageFile(n.a, carol, protocol.MessageFileRequest{MessageID: fileID, File: "verify-attach.txt"})
	if err != nil {
		t.Fatalf("read the file that waited for a busy receiver: %v", err)
	}
	if page.Text != want || page.NextOffset != 0 {
		t.Fatalf("page = %+v, want the whole file %q", page, want)
	}
}
