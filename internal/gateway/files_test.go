package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

const filesTestSum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// A message with files keeps its manifest in the store, so the receiving
// daemon gets it with every delivery and a resend gets the same manifest.
func TestMessageFilesStoredAndReturned(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	files := []protocol.FileEntry{
		{ID: "f1", Name: "report.html", Size: 90, SHA256: filesTestSum},
		{ID: "f2", Name: "data file.csv", Size: 0, SHA256: filesTestSum},
	}
	m, created, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "two files", IdempotencyKey: "k1", Files: files})
	if err != nil || !created {
		t.Fatalf("store: created=%v err=%v", created, err)
	}
	got, ok, err := s.Message(m.ID)
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if len(got.Files) != 2 || got.Files[0] != files[0] || got.Files[1] != files[1] {
		t.Fatalf("files = %+v, want %+v", got.Files, files)
	}
	again, created, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "two files", IdempotencyKey: "k1", Files: files})
	if err != nil || created || len(again.Files) != 2 {
		t.Fatalf("resend: created=%v err=%v files=%d", created, err, len(again.Files))
	}
	plain, _, err := send(s, a, b, "k2", "")
	if err != nil {
		t.Fatal(err)
	}
	if back, _, _ := s.Message(plain.ID); len(back.Files) != 0 {
		t.Fatalf("a message without files reads files: %+v", back.Files)
	}
	pending, err := s.Pending(nodeOfSession(t, s, b), true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range pending {
		if p.ID == m.ID {
			found = len(p.Files) == 2
		}
	}
	if !found {
		t.Fatal("the pending delivery lost the manifest")
	}
}

func nodeOfSession(t *testing.T, s *Store, session string) string {
	t.Helper()
	n, err := s.SessionNode(session)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The gateway checks the manifest again: a daemon cannot store a message that
// breaks the limits of decision 0054.
func TestMessageFilesRefusedOverLimits(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	big := []protocol.FileEntry{{ID: "f1", Name: "big.bin", Size: protocol.MaxFileBytes + 1, SHA256: filesTestSum}}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "x", IdempotencyKey: "k1", Files: big}); err == nil {
		t.Fatal("a file over 100 MB was stored")
	}
	path := []protocol.FileEntry{{ID: "f1", Name: "../x", Size: 1, SHA256: filesTestSum}}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "x", IdempotencyKey: "k2", Files: path}); err == nil {
		t.Fatal("a file name with a path was stored")
	}
	many := make([]protocol.FileEntry, protocol.MaxFilesPerMessage+1)
	for i := range many {
		many[i] = protocol.FileEntry{ID: "f" + string(rune('a'+i)), Name: "n" + string(rune('a'+i)), SHA256: filesTestSum}
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "x", IdempotencyKey: "k3", Files: many}); err == nil {
		t.Fatal("eleven files were stored")
	}
}

// A daemon reports its Tailscale address in hello; the node list shows it so
// a sending daemon can push files over Taildrop.
func TestHelloRecordsTailscaleIP(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	peer := enrollTransferPeer(t, g, url, "mac")
	if _, err := transferCall(t, peer.ws, "hello", protocol.Hello{Version: "dev", Hostname: "mac", TailscaleIP: "100.101.102.103"}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if ip := nodeTailscaleIP(t, g, peer.id); ip != "100.101.102.103" {
		t.Fatalf("tailscale ip = %q", ip)
	}
	// A hello without an address (Tailscale stopped) clears the old one, so
	// no sender tries a Taildrop to a machine that left the tailnet.
	if _, err := transferCall(t, peer.ws, "hello", protocol.Hello{Version: "dev", Hostname: "mac"}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if ip := nodeTailscaleIP(t, g, peer.id); ip != "" {
		t.Fatalf("tailscale ip after a hello without one = %q", ip)
	}
	if _, err := transferCall(t, peer.ws, "hello", protocol.Hello{Version: "dev", Hostname: "mac", TailscaleIP: "not an address"}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if ip := nodeTailscaleIP(t, g, peer.id); ip != "" {
		t.Fatalf("a malformed address was stored: %q", ip)
	}
}

func nodeTailscaleIP(t *testing.T, g *Gateway, id string) string {
	t.Helper()
	nodes, err := g.store.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.NodeID == id {
			return n.TailscaleIP
		}
	}
	t.Fatalf("node %s not listed", id)
	return ""
}

// xfer.find lets the receiver find a finished upload by the file's unique
// name, so a sender that falls back from Taildrop needs no second message.
func TestTransferFindByName(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	sender := enrollTransferPeer(t, g, url, "sender")
	receiver := enrollTransferPeer(t, g, url, "receiver")
	other := enrollTransferPeer(t, g, url, "other")
	data := transferData(protocol.TransferPartSize + 7)
	name := protocol.TaildropName("m_00aa", protocol.FileEntry{ID: "f1", Name: "report.html"})

	st := beginTransfer(t, sender.ws, receiver.id, name, data)
	if got := findUploads(t, receiver, name); len(got) != 0 {
		t.Fatalf("an unfinished upload was found: %+v", got)
	}
	uploadAllParts(t, sender.ws, st, data)
	if err := finishTransfer(t, sender.ws, st.UploadID); err != nil {
		t.Fatal(err)
	}
	got := findUploads(t, receiver, name, "agent-net-m_00aa-f2-missing.txt")
	if len(got) != 1 || got[0].UploadID != st.UploadID || got[0].Name != name || got[0].SHA256 != st.SHA256 || got[0].Size != st.Size || !got[0].Finished {
		t.Fatalf("found %+v, want upload %s", got, st.UploadID)
	}
	if got := findUploads(t, sender, name); len(got) != 0 {
		t.Fatal("the sender found an upload addressed to another machine")
	}
	if got := findUploads(t, other, name); len(got) != 0 {
		t.Fatal("another machine found the upload")
	}
	names := make([]string, protocol.MaxFilesPerMessage+1)
	for i := range names {
		names[i] = name
	}
	if _, err := transferCall(t, receiver.ws, "xfer.find", protocol.TransferFind{Names: names}); err == nil || !strings.Contains(err.Error(), "names") {
		t.Fatalf("eleven names: err=%v", err)
	}
	if _, err := transferCall(t, receiver.ws, "xfer.confirm", protocol.TransferConfirm{UploadID: st.UploadID}); err != nil {
		t.Fatal(err)
	}
	if got := findUploads(t, receiver, name); len(got) != 0 {
		t.Fatal("a confirmed upload was still found")
	}
}

func findUploads(t *testing.T, p *transferPeer, names ...string) []protocol.TransferState {
	t.Helper()
	raw, err := transferCall(t, p.ws, "xfer.find", protocol.TransferFind{Names: names})
	if err != nil {
		t.Fatalf("xfer.find: %v", err)
	}
	var out protocol.TransferFound
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.Uploads
}
