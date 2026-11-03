package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// testClock is the gateway's clock in the transfer tests, so an upload can
// expire without waiting seven days.
type testClock struct{ ns atomic.Int64 }

func newTestClock() *testClock {
	c := &testClock{}
	c.ns.Store(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC).UnixNano())
	return c
}

func (c *testClock) now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *testClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

type transferPeer struct {
	id    string
	token string
	ws    *websocket.Conn
	url   string
}

func newTransferGateway(t *testing.T, clock *testClock) (*Gateway, string) {
	t.Helper()
	opts := Options{DataDir: seededDataDir(t), OfflineGrace: 50 * time.Millisecond}
	if clock != nil {
		opts.Now = clock.now
	}
	g, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return g, "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/node"
}

func enrollTransferPeer(t *testing.T, g *Gateway, url, hostname string) *transferPeer {
	t.Helper()
	id, token, err := g.store.CreateNode(hostname, "linux")
	if err != nil {
		t.Fatal(err)
	}
	p := &transferPeer{id: id, token: token, url: url}
	p.ws = dialTransferPeer(t, url, token)
	return p
}

func dialTransferPeer(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ws.SetReadLimit(4 << 20)
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

var transferCallID atomic.Int64

// transferCall sends one frame and waits for its result. A refusal comes back
// as an error with the gateway's words.
func transferCall(t *testing.T, ws *websocket.Conn, typ string, params any) (json.RawMessage, error) {
	t.Helper()
	id := "c" + strconv.FormatInt(transferCallID.Add(1), 10)
	frame, err := protocol.EncodeRequest(typ, id, params)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	for {
		_, raw, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("%s: read: %v", typ, err)
		}
		var h protocol.Header
		if json.Unmarshal(raw, &h) != nil || h.Type != "result" || h.ID != id {
			continue
		}
		if !h.OK {
			return nil, errors.New(h.Error)
		}
		return h.Data, nil
	}
}

func beginTransfer(t *testing.T, ws *websocket.Conn, receiver, name string, data []byte) protocol.TransferState {
	t.Helper()
	sum := sha256.Sum256(data)
	raw, err := transferCall(t, ws, "xfer.begin", protocol.TransferBegin{
		Receiver: receiver,
		Name:     name,
		Size:     int64(len(data)),
		SHA256:   hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("xfer.begin: %v", err)
	}
	var st protocol.TransferState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func transferStatus(t *testing.T, ws *websocket.Conn, id string) (protocol.TransferState, error) {
	t.Helper()
	raw, err := transferCall(t, ws, "xfer.status", protocol.TransferStatus{UploadID: id})
	if err != nil {
		return protocol.TransferState{}, err
	}
	var st protocol.TransferState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	return st, nil
}

func partBytes(data []byte, index, size int) []byte {
	start := index * size
	end := start + size
	if end > len(data) {
		end = len(data)
	}
	return data[start:end]
}

func uploadPart(t *testing.T, ws *websocket.Conn, id string, data []byte, index, size int) error {
	t.Helper()
	b := partBytes(data, index, size)
	sum := sha256.Sum256(b)
	_, err := transferCall(t, ws, "xfer.part", protocol.TransferPart{
		UploadID: id,
		Index:    index,
		SHA256:   hex.EncodeToString(sum[:]),
		Data:     b,
	})
	return err
}

func uploadAllParts(t *testing.T, ws *websocket.Conn, st protocol.TransferState, data []byte) {
	t.Helper()
	for i := 0; i < st.Parts; i++ {
		if err := uploadPart(t, ws, st.UploadID, data, i, st.PartSize); err != nil {
			t.Fatalf("xfer.part %d: %v", i, err)
		}
	}
}

func finishTransfer(t *testing.T, ws *websocket.Conn, id string) error {
	t.Helper()
	_, err := transferCall(t, ws, "xfer.finish", protocol.TransferFinish{UploadID: id})
	return err
}

func downloadTransfer(t *testing.T, ws *websocket.Conn, id string) []byte {
	t.Helper()
	var out bytes.Buffer
	var parts int
	var size int64
	var total string
	for i := 0; ; i++ {
		raw, err := transferCall(t, ws, "xfer.get", protocol.TransferGet{UploadID: id, Index: i})
		if err != nil {
			t.Fatalf("xfer.get %d: %v", i, err)
		}
		var c protocol.TransferChunk
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		if c.Index != i {
			t.Fatalf("got part %d, want %d", c.Index, i)
		}
		parts, size, total = c.Parts, c.Size, c.TotalSHA256
		if sum := sha256.Sum256(c.Data); hex.EncodeToString(sum[:]) != c.SHA256 {
			t.Fatalf("part %d checksum mismatch", i)
		}
		out.Write(c.Data)
		if i+1 == parts {
			break
		}
	}
	if int64(out.Len()) != size {
		t.Fatalf("downloaded %d bytes, want %d", out.Len(), size)
	}
	sum := sha256.Sum256(out.Bytes())
	if hex.EncodeToString(sum[:]) != total {
		t.Fatalf("whole checksum mismatch")
	}
	return out.Bytes()
}

func transferData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*37 + 11)
	}
	return b
}

func transferDir(g *Gateway, id string) string {
	return filepath.Join(g.opts.DataDir, "transfers", id)
}

func TestTransferUploadAndDownload(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	sender := enrollTransferPeer(t, g, url, "sender")
	receiver := enrollTransferPeer(t, g, url, "receiver")
	other := enrollTransferPeer(t, g, url, "other")
	data := transferData(3*protocol.TransferPartSize + 12345)

	st := beginTransfer(t, sender.ws, receiver.id, "report.html", data)
	if st.Parts != 4 || st.PartSize != protocol.TransferPartSize {
		t.Fatalf("parts %d, part size %d", st.Parts, st.PartSize)
	}
	uploadAllParts(t, sender.ws, st, data)
	if err := finishTransfer(t, sender.ws, st.UploadID); err != nil {
		t.Fatalf("xfer.finish: %v", err)
	}

	got := downloadTransfer(t, receiver.ws, st.UploadID)
	if !bytes.Equal(got, data) {
		t.Fatal("downloaded bytes differ from the upload")
	}

	// The sender and any other machine must not read the bytes, and only the
	// receiver may confirm.
	if _, err := transferCall(t, sender.ws, "xfer.get", protocol.TransferGet{UploadID: st.UploadID, Index: 0}); err == nil {
		t.Fatal("the sender downloaded its own upload")
	}
	if _, err := transferCall(t, other.ws, "xfer.get", protocol.TransferGet{UploadID: st.UploadID, Index: 0}); err == nil {
		t.Fatal("another machine downloaded the upload")
	}
	if _, err := transferCall(t, sender.ws, "xfer.confirm", protocol.TransferConfirm{UploadID: st.UploadID}); err == nil {
		t.Fatal("the sender confirmed its own upload")
	}

	if _, err := transferCall(t, receiver.ws, "xfer.confirm", protocol.TransferConfirm{UploadID: st.UploadID}); err != nil {
		t.Fatalf("xfer.confirm: %v", err)
	}
	if _, err := os.Stat(transferDir(g, st.UploadID)); !os.IsNotExist(err) {
		t.Fatalf("the parts folder still exists: %v", err)
	}
	if _, err := transferCall(t, receiver.ws, "xfer.get", protocol.TransferGet{UploadID: st.UploadID, Index: 0}); err == nil {
		t.Fatal("a confirmed upload was served again")
	}
}

func TestTransferResumeAfterDroppedConnection(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	sender := enrollTransferPeer(t, g, url, "sender")
	receiver := enrollTransferPeer(t, g, url, "receiver")
	data := transferData(3*protocol.TransferPartSize + 99)

	st := beginTransfer(t, sender.ws, receiver.id, "report.html", data)
	if err := uploadPart(t, sender.ws, st.UploadID, data, 0, st.PartSize); err != nil {
		t.Fatalf("part 0: %v", err)
	}
	if err := uploadPart(t, sender.ws, st.UploadID, data, 2, st.PartSize); err != nil {
		t.Fatalf("part 2: %v", err)
	}
	// The connection drops. On the new one the sender asks what the gateway
	// holds and sends only the missing parts.
	sender.ws.CloseNow()
	sender.ws = dialTransferPeer(t, url, sender.token)

	resumed, err := transferStatus(t, sender.ws, st.UploadID)
	if err != nil {
		t.Fatalf("xfer.status: %v", err)
	}
	if len(resumed.Have) != 2 || resumed.Have[0] != 0 || resumed.Have[1] != 2 {
		t.Fatalf("gateway holds parts %v, want [0 2]", resumed.Have)
	}
	if resumed.Received != int64(2*protocol.TransferPartSize) {
		t.Fatalf("received %d bytes", resumed.Received)
	}
	for i := 0; i < resumed.Parts; i++ {
		if i == 0 || i == 2 {
			continue
		}
		if err := uploadPart(t, sender.ws, st.UploadID, data, i, resumed.PartSize); err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
	}
	if err := finishTransfer(t, sender.ws, st.UploadID); err != nil {
		t.Fatalf("xfer.finish: %v", err)
	}
	if got := downloadTransfer(t, receiver.ws, st.UploadID); !bytes.Equal(got, data) {
		t.Fatal("downloaded bytes differ after a resume")
	}
}

func TestTransferBadPartChecksumRefused(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	sender := enrollTransferPeer(t, g, url, "sender")
	receiver := enrollTransferPeer(t, g, url, "receiver")
	data := transferData(protocol.TransferPartSize + 17)

	st := beginTransfer(t, sender.ws, receiver.id, "report.html", data)
	part := partBytes(data, 1, st.PartSize)
	sum := sha256.Sum256([]byte("not the part"))
	_, err := transferCall(t, sender.ws, "xfer.part", protocol.TransferPart{
		UploadID: st.UploadID,
		Index:    1,
		SHA256:   hex.EncodeToString(sum[:]),
		Data:     part,
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("bad part checksum: %v", err)
	}
	state, err := transferStatus(t, sender.ws, st.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Have) != 0 {
		t.Fatalf("a refused part was stored: %v", state.Have)
	}
	if err := uploadPart(t, sender.ws, st.UploadID, data, 1, st.PartSize); err != nil {
		t.Fatalf("good part after a bad one: %v", err)
	}
}

func TestTransferWrongFinalHashRefused(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	sender := enrollTransferPeer(t, g, url, "sender")
	receiver := enrollTransferPeer(t, g, url, "receiver")
	data := transferData(2*protocol.TransferPartSize + 5)

	wrong := sha256.Sum256([]byte("different bytes"))
	raw, err := transferCall(t, sender.ws, "xfer.begin", protocol.TransferBegin{
		Receiver: receiver.id,
		Name:     "report.html",
		Size:     int64(len(data)),
		SHA256:   hex.EncodeToString(wrong[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	var st protocol.TransferState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	uploadAllParts(t, sender.ws, st, data)
	err = finishTransfer(t, sender.ws, st.UploadID)
	if err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("wrong final hash: %v", err)
	}
	if _, err := os.Stat(transferDir(g, st.UploadID)); !os.IsNotExist(err) {
		t.Fatalf("the parts of a refused upload still exist: %v", err)
	}
	if _, err := transferStatus(t, sender.ws, st.UploadID); err == nil {
		t.Fatal("a refused upload can still be resumed")
	}
}

func TestTransferLimitsRefused(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	sender := enrollTransferPeer(t, g, url, "sender")
	receiver := enrollTransferPeer(t, g, url, "receiver")

	big := sha256.Sum256([]byte("big"))
	raw, err := transferCall(t, sender.ws, "xfer.begin", protocol.TransferBegin{
		Receiver: receiver.id, Size: MaxTransferBytes + 1, SHA256: hex.EncodeToString(big[:]),
	})
	if err == nil || !strings.Contains(err.Error(), "100 MB") {
		t.Fatalf("over the per-transfer limit: %v (data %s)", err, raw)
	}
	raw, err = transferCall(t, sender.ws, "xfer.begin", protocol.TransferBegin{
		Receiver: "n_nobody", Size: 10, SHA256: hex.EncodeToString(big[:]),
	})
	if err == nil || !strings.Contains(err.Error(), "machine") {
		t.Fatalf("unknown receiver: %v (data %s)", err, raw)
	}
	raw, err = transferCall(t, sender.ws, "xfer.begin", protocol.TransferBegin{
		Receiver: receiver.id, Size: 10, SHA256: "nothex",
	})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("bad whole hash: %v (data %s)", err, raw)
	}

	// A sender may stage one maximum message (10 files of 100 MB) but not
	// more until the receiver confirms or the parts expire.
	sum := sha256.Sum256([]byte("file"))
	for i := 0; i < 10; i++ {
		if _, err := transferCall(t, sender.ws, "xfer.begin", protocol.TransferBegin{
			Receiver: receiver.id, Size: MaxTransferBytes, SHA256: hex.EncodeToString(sum[:]),
		}); err != nil {
			t.Fatalf("begin %d under the per-sender cap: %v", i, err)
		}
	}
	if _, err := transferCall(t, sender.ws, "xfer.begin", protocol.TransferBegin{
		Receiver: receiver.id, Size: MaxTransferBytes, SHA256: hex.EncodeToString(sum[:]),
	}); err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("over the per-sender cap: %v", err)
	}
}

func TestTransferExpiryDeletesParts(t *testing.T) {
	clock := newTestClock()
	g, url := newTransferGateway(t, clock)
	sender := enrollTransferPeer(t, g, url, "sender")
	receiver := enrollTransferPeer(t, g, url, "receiver")
	data := transferData(protocol.TransferPartSize + 1)

	st := beginTransfer(t, sender.ws, receiver.id, "report.html", data)
	if err := uploadPart(t, sender.ws, st.UploadID, data, 0, st.PartSize); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(transferDir(g, st.UploadID)); err != nil {
		t.Fatalf("parts folder before expiry: %v", err)
	}
	clock.advance(TransferRetention + time.Minute)
	if _, err := transferStatus(t, sender.ws, st.UploadID); err == nil {
		t.Fatal("an expired upload is still listed")
	}
	if _, err := os.Stat(transferDir(g, st.UploadID)); !os.IsNotExist(err) {
		t.Fatalf("expired parts still exist: %v", err)
	}
	var n int
	if err := g.store.db.QueryRow(`SELECT COUNT(*) FROM transfers WHERE id = ?`, st.UploadID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expired transfer row remains")
	}
}
