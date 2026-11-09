package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// fakeTailscaleScript stands in for the tailscale CLI. Both test daemons run
// in this process, so the script serves the sender (status, file cp) and the
// receiver (file get). Its folder holds:
//
//	status.json    what `status --json` prints
//	peer-<ip>      the folder a `file cp` to <ip> writes into
//	waiting/       where a Linux receiver's incoming files wait for `file get`
//	fail-cp        when present, every `file cp` fails (receiver offline)
//	corrupt        when present, `file cp` changes the bytes on the way
//	hold-<name>    when present, a file whose name ends in <name> goes to held/
//	calls          one line per call
const fakeTailscaleScript = `#!/bin/sh
d="$FAKE_TAILSCALE_DIR"
echo "$*" >> "$d/calls"
case "$1 $2" in
"status --json")
	cat "$d/status.json"
	;;
"file cp")
	src="$3"
	ip="${4%:}"
	if [ -f "$d/fail-cp" ]; then
		echo "peer is offline" >&2
		exit 1
	fi
	dest=$(cat "$d/peer-$ip" 2>/dev/null) || { echo "unknown target" >&2; exit 1; }
	name=$(basename "$src")
	for h in "$d"/hold-*; do
		[ -e "$h" ] || continue
		case "$name" in *"${h##*/hold-}") dest="$d/held" ;; esac
	done
	mkdir -p "$dest"
	cp "$src" "$dest/$name"
	if [ -f "$d/corrupt" ]; then
		printf 'x' >> "$dest/$name"
	fi
	;;
"file get")
	if [ -f "$d/refuse-get" ]; then
		echo "file access denied" >&2
		exit 1
	fi
	for f in "$d"/waiting/*; do
		[ -e "$f" ] && mv "$f" "$3/"
	done
	;;
*)
	echo "unexpected: $*" >&2
	exit 2
	;;
esac
`

const (
	filesSenderIP   = "100.64.0.1"
	filesReceiverIP = "100.64.0.2"
)

type filesNet struct {
	t          *testing.T
	g          *testGateway
	a, b       *node
	fa, fb     *fakeAdapter
	alice      string // session on a
	bob        string // session on b
	project    string // alice's project folder
	senderHome string // a's isolated home folder
	ts         string // the fake tailscale folder
	downloads  string // b's Downloads folder
}

type filesNetOpts struct {
	layout  string // "darwin" or "linux"
	tailnet bool   // b reports a Tailscale address that a sees online
	wait    time.Duration
}

func startFilesNet(t *testing.T, o filesNetOpts) *filesNet {
	t.Helper()
	ts := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tailscale"), []byte(fakeTailscaleScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_TAILSCALE_DIR", ts)
	for _, dir := range []string{"waiting", "held"} {
		if err := os.MkdirAll(filepath.Join(ts, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	status := map[string]any{
		"BackendState": "Running",
		"Self":         map[string]any{"TailscaleIPs": []string{filesSenderIP}, "Online": true},
		"Peer": map[string]any{
			"nodekey:b": map[string]any{"TailscaleIPs": []string{filesReceiverIP, "fd7a:115c:a1e0::2"}, "Online": true, "HostName": "b"},
		},
	}
	raw, _ := json.Marshal(status)
	if err := os.WriteFile(filepath.Join(ts, "status.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	downloads := filepath.Join(t.TempDir(), "Downloads")
	if err := os.MkdirAll(downloads, 0o700); err != nil {
		t.Fatal(err)
	}
	peerDir := filepath.Join(ts, "waiting")
	if o.layout == "darwin" {
		peerDir = downloads // the Mac app saves straight into Downloads
	}
	if err := os.WriteFile(filepath.Join(ts, "peer-"+filesReceiverIP), []byte(peerDir), 0o600); err != nil {
		t.Fatal(err)
	}

	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	fb := &fakeAdapter{harness: "fake"}
	senderHome := t.TempDir()
	a := startNode(t, g, nodeOpts{adapter: fa, cfg: func(c *Config) {
		c.Files.Home = senderHome
		c.Files.TailscalePATH = bin // the fake tailscale is the only one on the daemon's path
		c.Files.TailscaleIP = func() string { return filesSenderIP }
		c.Files.Poll = 20 * time.Millisecond
		c.Files.PollMax = 50 * time.Millisecond
	}})
	b := startNode(t, g, nodeOpts{adapter: fb, cfg: func(c *Config) {
		c.Files.Home = t.TempDir()
		c.Files.TailscalePATH = bin
		c.Files.Layout = o.layout
		c.Files.Downloads = downloads
		c.Files.Poll = 20 * time.Millisecond
		c.Files.PollMax = 50 * time.Millisecond
		c.Files.Wait = o.wait
		if o.tailnet {
			c.Files.TailscaleIP = func() string { return filesReceiverIP }
		}
	}})
	project := t.TempDir()
	bobProject := filepath.Join(t.TempDir(), "bobproject")
	if err := os.MkdirAll(bobProject, 0o700); err != nil {
		t.Fatal(err)
	}
	fa.setSessions(protocol.SessionInfo{Harness: "fake", NativeID: "na", PID: 1, Cwd: project, Name: "alice", Status: "idle"})
	fb.setSessions(protocol.SessionInfo{Harness: "fake", NativeID: "nb", PID: 1, Cwd: bobProject, Name: "bob", Status: "idle"})
	eventually(t, "sessions have ids", func() bool {
		return fa.host.SessionID("fake", "na") != "" && fb.host.SessionID("fake", "nb") != ""
	})
	return &filesNet{t: t, g: g, a: a, b: b, fa: fa, fb: fb, alice: fa.host.SessionID("fake", "na"), bob: fb.host.SessionID("fake", "nb"),
		project: project, senderHome: senderHome, ts: ts, downloads: downloads}
}

func (n *filesNet) flag(name string) {
	n.t.Helper()
	if err := os.WriteFile(filepath.Join(n.ts, name), nil, 0o600); err != nil {
		n.t.Fatal(err)
	}
}

func (n *filesNet) calls() string {
	b, _ := os.ReadFile(filepath.Join(n.ts, "calls"))
	return string(b)
}

func (n *filesNet) send(paths ...string) (FileSendResult, error) {
	n.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()
	return n.a.d.SendFiles(ctx, FileSend{From: n.alice, To: n.bob, Body: "here are the files", Paths: paths})
}

func (n *filesNet) inbox(msgID string) string {
	return filepath.Join(n.b.cfg.StateDir, "inbox", n.bob, msgID)
}

// message reads a message as the sending machine sees it on the gateway.
func (n *filesNet) message(id string) protocol.Message {
	n.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := n.a.d.call(ctx, "query.message", protocol.QueryMessage{MessageID: id})
	if err != nil {
		n.t.Fatalf("query.message: %v", err)
	}
	var r struct {
		Message protocol.Message `json:"message"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		n.t.Fatal(err)
	}
	return r.Message
}

func (n *filesNet) waitDelivered(id string) protocol.Message {
	n.t.Helper()
	var m protocol.Message
	eventually(n.t, "message delivered", func() bool {
		m = n.message(id)
		return m.State == protocol.StateDelivered
	})
	return m
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s is missing: %v", path, err)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s still exists: %v", path, err)
	}
}

// The Mac app saves incoming files straight into Downloads. The receiving
// daemon takes only the announced ones, by unique name and hash, and leaves
// the owner's own files there.
func TestFilesTaildropMacLayout(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true})
	holiday := writeTestFile(t, filepath.Join(n.downloads, "holiday.jpg"), []byte("owner's photo"))
	stranger := writeTestFile(t, filepath.Join(n.downloads, "agent-net-m_0000000000000000-f1-x.txt"), []byte("not announced"))
	report := []byte("<html>the report</html>")
	src := writeTestFile(t, filepath.Join(n.project, "out", "report final.html"), report)

	res, err := n.send(src)
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != FilePathTaildrop {
		t.Fatalf("result = %+v, want one file over Taildrop", res.Files)
	}
	m := n.waitDelivered(res.Message.ID)
	if !strings.Contains(m.DeliveryDetail, "Taildrop") {
		t.Fatalf("delivery detail %q does not name the path", m.DeliveryDetail)
	}
	dir := n.inbox(res.Message.ID)
	if got := mustReadFile(t, filepath.Join(dir, "report final.html")); !bytes.Equal(got, report) {
		t.Fatalf("inbox file = %q", got)
	}
	got := n.fb.delivered()
	if len(got) != 1 || !strings.Contains(got[0].Body, "here are the files") || !strings.Contains(got[0].Body, dir) || !strings.Contains(got[0].Body, "report final.html") {
		t.Fatalf("the agent's message does not say where the files are: %+v", got)
	}
	assertMissing(t, filepath.Join(n.downloads, protocol.TaildropName(res.Message.ID, res.Files[0].Entry)))
	if b := mustReadFile(t, holiday); string(b) != "owner's photo" {
		t.Fatal("the owner's own file changed")
	}
	assertExists(t, stranger)
	if strings.Contains(n.calls(), "file get") {
		t.Fatal("the Mac layout ran tailscale file get")
	}
}

// On Linux the files wait for `tailscale file get`. The daemon collects into a
// staging folder and moves every file nobody announced to Downloads, so the
// owner's own Taildrops still arrive where the owner expects them.
func TestFilesTaildropLinuxLayout(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "linux", tailnet: true})
	writeTestFile(t, filepath.Join(n.ts, "waiting", "scan.pdf"), []byte("owner's scan"))
	existing := writeTestFile(t, filepath.Join(n.downloads, "scan.pdf"), []byte("an older scan"))
	data := bytes.Repeat([]byte("csv,row\n"), 1000)
	src := writeTestFile(t, filepath.Join(n.project, "data.csv"), data)
	png := append([]byte{0x89, 'P', 'N', 'G', 0}, bytes.Repeat([]byte{1, 2, 3}, 700)...)
	src2 := writeTestFile(t, filepath.Join(n.project, "chart.png"), png)

	res, err := n.send(src, src2)
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	m := n.waitDelivered(res.Message.ID)
	if !strings.Contains(m.DeliveryDetail, "Taildrop") {
		t.Fatalf("delivery detail %q", m.DeliveryDetail)
	}
	dir := n.inbox(res.Message.ID)
	if got := mustReadFile(t, filepath.Join(dir, "data.csv")); !bytes.Equal(got, data) {
		t.Fatal("data.csv differs")
	}
	if got := mustReadFile(t, filepath.Join(dir, "chart.png")); !bytes.Equal(got, png) {
		t.Fatal("chart.png differs")
	}
	if !strings.Contains(n.calls(), "file get") {
		t.Fatal("the Linux layout did not run tailscale file get")
	}
	// The owner's file went to Downloads under a free name, next to the older one.
	if string(mustReadFile(t, existing)) != "an older scan" {
		t.Fatal("the owner's older file was overwritten")
	}
	moved := filepath.Join(n.downloads, "scan (1).pdf")
	if string(mustReadFile(t, moved)) != "owner's scan" {
		t.Fatal("the owner's Taildrop did not reach Downloads")
	}
	staged, _ := os.ReadDir(filepath.Join(n.b.cfg.StateDir, "files", "taildrop"))
	if len(staged) != 0 {
		t.Fatalf("files left in staging: %v", staged)
	}
}

// A file whose bytes do not match the manifest is refused: it never reaches
// the inbox and the message is not delivered.
func TestFilesHashMismatchRejected(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true})
	n.flag("corrupt")
	src := writeTestFile(t, filepath.Join(n.project, "report.html"), []byte("<p>exact bytes</p>"))
	res, err := n.send(src)
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	var m protocol.Message
	eventually(t, "message failed", func() bool {
		m = n.message(res.Message.ID)
		return m.State == protocol.StateFailed
	})
	if !strings.Contains(m.DeliveryDetail, "sha256") || !strings.Contains(m.DeliveryDetail, "report.html") {
		t.Fatalf("detail %q does not explain the mismatch", m.DeliveryDetail)
	}
	if len(n.fb.delivered()) != 0 {
		t.Fatal("the agent got a message whose file failed its hash")
	}
	assertMissing(t, filepath.Join(n.inbox(res.Message.ID), "report.html"))
	// On the Mac the bad file is left where Taildrop put it.
	assertExists(t, filepath.Join(n.downloads, protocol.TaildropName(res.Message.ID, res.Files[0].Entry)))
}

// When Taildrop fails (the receiver is offline), the sender falls back to the
// gateway in parts and says so; the receiver finds the upload by its unique
// name and the delivery record names the gateway.
func TestFilesGatewayFallback(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "linux", tailnet: true})
	n.flag("fail-cp")
	data := bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 6, 7}, (protocol.TransferPartSize+100)/8)
	src := writeTestFile(t, filepath.Join(n.project, "model.bin"), data)
	res, err := n.send(src)
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != FilePathGateway || !strings.Contains(res.Files[0].Note, "Taildrop") {
		t.Fatalf("result = %+v, want the gateway after a failed Taildrop", res.Files)
	}
	m := n.waitDelivered(res.Message.ID)
	if !strings.Contains(m.DeliveryDetail, "gateway") {
		t.Fatalf("delivery detail %q does not name the gateway", m.DeliveryDetail)
	}
	if got := mustReadFile(t, filepath.Join(n.inbox(res.Message.ID), "model.bin")); !bytes.Equal(got, data) {
		t.Fatal("the file from the gateway differs")
	}
	// The receiver confirmed the upload, so the gateway freed the parts.
	eventually(t, "upload confirmed", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		raw, err := n.b.d.call(ctx, "xfer.find", protocol.TransferFind{Names: []string{protocol.TaildropName(res.Message.ID, res.Files[0].Entry)}})
		var found protocol.TransferFound
		return err == nil && json.Unmarshal(raw, &found) == nil && len(found.Uploads) == 0
	})
}

// A receiver that is not on the sender's tailnet gets the files through the
// gateway, without a Taildrop attempt.
func TestFilesGatewayWithoutTailnet(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: false})
	src := writeTestFile(t, filepath.Join(n.project, "notes.md"), []byte("# notes\n"))
	res, err := n.send(src)
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	if res.Files[0].Path != FilePathGateway {
		t.Fatalf("path = %q, want the gateway", res.Files[0].Path)
	}
	m := n.waitDelivered(res.Message.ID)
	if !strings.Contains(m.DeliveryDetail, "gateway") {
		t.Fatalf("delivery detail %q", m.DeliveryDetail)
	}
	if strings.Contains(n.calls(), "file cp") {
		t.Fatal("a Taildrop was tried to a machine off the tailnet")
	}
	assertExists(t, filepath.Join(n.inbox(res.Message.ID), "notes.md"))
}

// The message is delivered only when every file is in. Until then the sender
// sees how many have arrived.
func TestFilesDeliveredOnlyWhenAllIn(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true})
	n.flag("hold-second.txt")
	first := writeTestFile(t, filepath.Join(n.project, "first.txt"), []byte("one"))
	second := writeTestFile(t, filepath.Join(n.project, "second.txt"), []byte("two"))
	res, err := n.send(first, second)
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	dir := n.inbox(res.Message.ID)
	eventually(t, "first file in the inbox", func() bool {
		_, err := os.Stat(filepath.Join(dir, "first.txt"))
		return err == nil
	})
	eventually(t, "sender sees the progress", func() bool {
		return strings.Contains(n.message(res.Message.ID).DeliveryDetail, "1 of 2")
	})
	if m := n.message(res.Message.ID); m.State == protocol.StateDelivered || len(n.fb.delivered()) != 0 {
		t.Fatalf("delivered with a file missing: state %s", m.State)
	}
	// The held file arrives late.
	name := protocol.TaildropName(res.Message.ID, res.Files[1].Entry)
	if err := os.Rename(filepath.Join(n.ts, "held", name), filepath.Join(n.downloads, name)); err != nil {
		t.Fatal(err)
	}
	n.waitDelivered(res.Message.ID)
	if string(mustReadFile(t, filepath.Join(dir, "second.txt"))) != "two" {
		t.Fatal("second.txt differs")
	}
	if len(n.fb.delivered()) != 1 {
		t.Fatalf("delivered %d times", len(n.fb.delivered()))
	}
}

// Files that never arrive fail the message with the names of the missing
// files, and nothing reaches the agent.
func TestFilesThatNeverArriveFail(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true, wait: 300 * time.Millisecond})
	n.flag("hold-lost.txt")
	src := writeTestFile(t, filepath.Join(n.project, "lost.txt"), []byte("lost"))
	res, err := n.send(src)
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	var m protocol.Message
	eventually(t, "message failed", func() bool {
		m = n.message(res.Message.ID)
		return m.State == protocol.StateFailed
	})
	if !strings.Contains(m.DeliveryDetail, "lost.txt") {
		t.Fatalf("detail %q does not name the missing file", m.DeliveryDetail)
	}
	if len(n.fb.delivered()) != 0 {
		t.Fatal("the agent got a message without its files")
	}
}

// A refused path stops the send before any message is stored.
func TestFilesSendRefusesSymlinkIntoSSH(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true})
	key := writeTestFile(t, filepath.Join(n.senderHome, ".ssh", "id_files_test"), []byte("not a real key"))
	link := filepath.Join(n.project, "key.txt")
	if err := os.Symlink(key, link); err != nil {
		t.Fatal(err)
	}
	if _, err := n.send(link); err == nil || !strings.Contains(err.Error(), "~/.ssh") {
		t.Fatalf("symlink into ~/.ssh: got %v, want credential path refusal", err)
	}
	if strings.Contains(n.calls(), "file cp") {
		t.Fatal("bytes were pushed for a refused file")
	}
	if len(n.fb.delivered()) != 0 {
		t.Fatal("a message reached the receiver")
	}
}

// Two sessions on one machine: the bytes stay on the machine.
func TestFilesSameMachine(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true})
	n.fa.setSessions(
		protocol.SessionInfo{Harness: "fake", NativeID: "na", PID: 1, Cwd: n.project, Name: "alice", Status: "idle"},
		protocol.SessionInfo{Harness: "fake", NativeID: "nc", PID: 1, Cwd: t.TempDir(), Name: "carol", Status: "idle"},
	)
	eventually(t, "carol has an id", func() bool { return n.fa.host.SessionID("fake", "nc") != "" })
	carol := n.fa.host.SessionID("fake", "nc")
	src := writeTestFile(t, filepath.Join(n.project, "local.txt"), []byte("stays here"))
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()
	res, err := n.a.d.SendFiles(ctx, FileSend{From: n.alice, To: carol, Body: "a local file", Paths: []string{src}})
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	if res.Files[0].Path != FilePathLocal {
		t.Fatalf("path = %q, want local", res.Files[0].Path)
	}
	m := n.waitDelivered(res.Message.ID)
	if !strings.Contains(m.DeliveryDetail, "this machine") {
		t.Fatalf("delivery detail %q", m.DeliveryDetail)
	}
	in := filepath.Join(n.a.cfg.StateDir, "inbox", carol, res.Message.ID, "local.txt")
	if string(mustReadFile(t, in)) != "stays here" {
		t.Fatal("the local file differs")
	}
	if strings.Contains(n.calls(), "file cp") {
		t.Fatal("a Taildrop was tried to this machine")
	}
}

// A message the owner's rule holds for approval keeps its files on the
// gateway, which deletes them if nobody approves; a Taildrop would leave them
// in the receiver's Downloads. After the approval the receiver collects them.
func TestFilesHeldMessageWaitsOnGateway(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true})
	cli := cliClient(t, n.a)
	cli.mustCall("edge_set", protocol.Edge{From: "*", To: "bobproject", Kind: "depends", Rule: "ask"})
	src := writeTestFile(t, filepath.Join(n.project, "plan.md"), []byte("# plan\n"))
	res, err := n.send(src)
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	if res.Message.State != protocol.StateHeld {
		t.Fatalf("state = %s, want held", res.Message.State)
	}
	if res.Files[0].Path != FilePathGateway || !strings.Contains(res.Files[0].Note, "approval") {
		t.Fatalf("result = %+v, want the gateway while held", res.Files)
	}
	if strings.Contains(n.calls(), "file cp") {
		t.Fatal("a held message's file went over Taildrop")
	}
	cli.mustCall("held_answer", protocol.HeldAnswer{MessageID: res.Message.ID, Action: "a"})
	m := n.waitDelivered(res.Message.ID)
	if !strings.Contains(m.DeliveryDetail, "gateway") {
		t.Fatalf("delivery detail %q", m.DeliveryDetail)
	}
	if string(mustReadFile(t, filepath.Join(n.inbox(res.Message.ID), "plan.md"))) != "# plan\n" {
		t.Fatal("plan.md differs")
	}
}

// When the receiver cannot read Downloads (macOS privacy rules can block a
// background daemon), the sender's waiting detail says so instead of waiting
// in silence.
func TestFilesUnreadableDownloadsShowsInDetail(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads any folder")
	}
	n := startFilesNet(t, filesNetOpts{layout: "darwin", tailnet: true})
	n.flag("hold-blocked.txt")
	if err := os.Chmod(n.downloads, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(n.downloads, 0o700) })
	src := writeTestFile(t, filepath.Join(n.project, "blocked.txt"), []byte("x"))
	res, err := n.send(src)
	if err != nil {
		t.Fatalf("SendFiles: %v", err)
	}
	eventually(t, "the detail names the unreadable folder", func() bool {
		d := n.message(res.Message.ID).DeliveryDetail
		return strings.Contains(d, "cannot read") && strings.Contains(d, "Downloads")
	})
}

// A successful Taildrop does not prove that the receiver can collect it.
// Linux may deny its daemon access to the Tailscale inbox. Worker notices
// must still get their files through the gateway in this case.
func TestWorkerNoticeFilesWhenTaildropCannotBeCollected(t *testing.T) {
	n := startFilesNet(t, filesNetOpts{layout: "linux", tailnet: true})
	n.flag("refuse-get")
	src := writeTestFile(t, filepath.Join(n.project, "result.txt"), []byte("result"))
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()
	res, err := n.a.d.SendFiles(ctx, FileSend{From: n.alice, To: n.bob, Body: "[agent-net worker notice]\nstatus: done\nreport", WorkerNotice: true, Paths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != FilePathGateway {
		t.Fatalf("file route = %+v, want gateway when Taildrop collection can fail", res.Files)
	}
	n.waitDelivered(res.Message.ID)
	if got := string(mustReadFile(t, filepath.Join(n.inbox(res.Message.ID), "result.txt"))); got != "result" {
		t.Fatalf("file = %q", got)
	}
}
