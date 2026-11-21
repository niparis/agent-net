package gateway

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestEnrollmentWithFreshCodeSucceedsOnce(t *testing.T) {
	s, _ := newTestStore(t)
	code, err := s.CreateEnrollCode(10 * time.Minute)
	if err != nil {
		t.Fatalf("CreateEnrollCode: %v", err)
	}
	valid, err := s.EnrollCodeValid(code)
	if err != nil || !valid {
		t.Fatalf("EnrollCodeValid = %v, %v; want true, nil", valid, err)
	}
	ok, err := s.ConsumeEnrollCode(code)
	if err != nil || !ok {
		t.Fatalf("ConsumeEnrollCode = %v, %v; want true, nil", ok, err)
	}
}

func TestEnrollmentSecondUseRefused(t *testing.T) {
	s, _ := newTestStore(t)
	code, err := s.CreateEnrollCode(10 * time.Minute)
	if err != nil {
		t.Fatalf("CreateEnrollCode: %v", err)
	}
	if ok, _ := s.ConsumeEnrollCode(code); !ok {
		t.Fatal("first use failed")
	}
	if ok, _ := s.ConsumeEnrollCode(code); ok {
		t.Fatal("second use succeeded; want refused")
	}
	if valid, _ := s.EnrollCodeValid(code); valid {
		t.Fatal("consumed code still valid; want false")
	}
}

func TestEnrollmentExpiredCodeRefused(t *testing.T) {
	s, c := newTestStore(t)
	code, err := s.CreateEnrollCode(10 * time.Minute)
	if err != nil {
		t.Fatalf("CreateEnrollCode: %v", err)
	}
	c.advance(10*time.Minute + time.Second)
	if valid, _ := s.EnrollCodeValid(code); valid {
		t.Fatal("expired code valid; want false")
	}
	if ok, _ := s.ConsumeEnrollCode(code); ok {
		t.Fatal("expired code consumed; want refused")
	}
}

func TestInvitedRowInvisibleToMachineListsAndReads(t *testing.T) {
	g, _ := newTransferGateway(t, nil)
	s := g.store

	// Enrolled machine
	nodeID, _, err := s.CreateNode("worker-alpha", "linux")
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if err := s.SetNodeHello(nodeID, "worker-alpha", "0.5.120"); err != nil {
		t.Fatalf("SetNodeHello: %v", err)
	}

	// Create an invite code
	code, err := s.CreateEnrollCode(10 * time.Minute)
	if err != nil {
		t.Fatalf("CreateEnrollCode: %v", err)
	}

	// Verify an invite row exists in nodes
	var inviteID, inviteState, tokenHash string
	err = s.db.QueryRow(`SELECT id, state, token_hash FROM nodes WHERE code_hash = ?`, hashHex(code)).Scan(&inviteID, &inviteState, &tokenHash)
	if err != nil {
		t.Fatalf("query invited row: %v", err)
	}
	if inviteState != "invited" {
		t.Fatalf("inviteState = %q; want invited", inviteState)
	}

	// Check Store.Nodes(): must only return worker-alpha
	nodes, err := s.Nodes()
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != nodeID {
		t.Fatalf("Nodes = %+v; want only %s", nodes, nodeID)
	}

	// Check Gateway.FindMachines: must not contain invited row
	page, err := g.FindMachines(protocol.MachinesFindRequest{})
	if err != nil {
		t.Fatalf("FindMachines: %v", err)
	}
	for _, m := range page.Items {
		if m.ID == inviteID {
			t.Fatalf("FindMachines returned invited machine %s", inviteID)
		}
	}

	// Check Gateway.MachineGet: must fail for invited row
	_, err = g.MachineGet(context.Background(), protocol.MachineGetRequest{Machine: inviteID})
	if err == nil {
		t.Fatalf("MachineGet(%q) succeeded; want unknown machine", inviteID)
	}

	// Check NodeByToken: invited token_hash cannot authenticate
	_, ok, _ := s.NodeByToken(tokenHash)
	if ok {
		t.Fatal("NodeByToken authenticated invited token_hash; want false")
	}
}

func TestTransferResumeAfterLostPart(t *testing.T) {
	g, url := newTransferGateway(t, nil)
	sender := enrollTransferPeer(t, g, url, "sender")
	receiver := enrollTransferPeer(t, g, url, "receiver")
	data := transferData(3*protocol.TransferPartSize + 42)

	st := beginTransfer(t, sender.ws, receiver.id, "document.pdf", data)
	// Send part 0 and part 2, part 1 is lost
	if err := uploadPart(t, sender.ws, st.UploadID, data, 0, st.PartSize); err != nil {
		t.Fatalf("part 0: %v", err)
	}
	if err := uploadPart(t, sender.ws, st.UploadID, data, 2, st.PartSize); err != nil {
		t.Fatalf("part 2: %v", err)
	}

	status, err := transferStatus(t, sender.ws, st.UploadID)
	if err != nil {
		t.Fatalf("transferStatus: %v", err)
	}
	if len(status.Have) != 2 || status.Have[0] != 0 || status.Have[1] != 2 {
		t.Fatalf("status.Have = %v; want [0 2]", status.Have)
	}

	// Resume: upload missing part 1 and part 3
	for i := 0; i < status.Parts; i++ {
		if i == 0 || i == 2 {
			continue
		}
		if err := uploadPart(t, sender.ws, st.UploadID, data, i, status.PartSize); err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
	}
	if err := finishTransfer(t, sender.ws, st.UploadID); err != nil {
		t.Fatalf("finishTransfer: %v", err)
	}
	got := downloadTransfer(t, receiver.ws, st.UploadID)
	if !bytes.Equal(got, data) {
		t.Fatal("downloaded bytes differ after resume")
	}
}

func TestMachineTablesMigrationMovesRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s := migrateOnly(t, path, migrationVersion(t, machineTablesMarker)-1)
	defer s.Close()

	// Seed enroll_codes
	unusedHash := hashHex("UNUSEDCODE")
	usedHash := hashHex("USEDCODE1")
	now := time.Now().UnixNano()
	for _, q := range []string{
		fmt.Sprintf("INSERT INTO enroll_codes(code_hash, expires_at, used_at) VALUES ('%s', %d, NULL)", unusedHash, now+int64(10*time.Minute)),
		fmt.Sprintf("INSERT INTO enroll_codes(code_hash, expires_at, used_at) VALUES ('%s', %d, %d)", usedHash, now-int64(time.Hour), now-int64(2*time.Hour)),
		`INSERT INTO nodes(id, hostname, os, token_hash, created_at) VALUES ('n_s1', 'host-s1', 'linux', 'tok_s1', 1)`,
		`INSERT INTO nodes(id, hostname, os, token_hash, created_at) VALUES ('n_r1', 'host-r1', 'linux', 'tok_r1', 1)`,
		`INSERT INTO transfers(id, sender, receiver, name, size, sha256, part_size, parts, created_at, expires_at, finished_at) VALUES
			('t_mig1', 'n_s1', 'n_r1', 'file.txt', 300, '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef', 100, 3, 1, 200, 0)`,
		`INSERT INTO transfer_parts(id, idx, size, sha256, at) VALUES
			('t_mig1', 0, 100, 'sha0', 10),
			('t_mig1', 1, 200, 'sha1', 20)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}

	if err := replayMigrations(t, s); err != nil {
		t.Fatal(err)
	}

	// Verify dropped tables are gone
	for _, table := range []string{"enroll_codes", "transfer_parts"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("table %s still exists; want dropped", table)
		}
	}

	// Verify enroll_codes facts moved into nodes invite rows
	var unusedCount, usedCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE code_hash = ? AND state = 'invited' AND used_at = 0`, unusedHash).Scan(&unusedCount); err != nil || unusedCount != 1 {
		t.Fatalf("unused invite row missing in nodes: count=%d, err=%v", unusedCount, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE code_hash = ? AND state = 'invited' AND used_at > 0`, usedHash).Scan(&usedCount); err != nil || usedCount != 1 {
		t.Fatalf("used invite row missing in nodes: count=%d, err=%v", usedCount, err)
	}

	// Verify transfers facts moved into parts_received
	have, received, err := s.transferHave("t_mig1")
	if err != nil {
		t.Fatalf("transferHave(t_mig1): %v", err)
	}
	if len(have) != 2 || have[0] != 0 || have[1] != 1 || received != 300 {
		t.Fatalf("transferHave(t_mig1) = %v, %d; want [0 1], 300", have, received)
	}
}
