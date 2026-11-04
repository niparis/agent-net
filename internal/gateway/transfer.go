package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const (
	// MaxTransferBytes is the largest upload one message may carry. A larger
	// file is refused at xfer.begin, before any part is stored.
	MaxTransferBytes int64 = 100 << 20
	// MaxTransferStoragePerNode caps the bytes one sending machine may keep
	// staged on the gateway. It is one maximum message (10 files of 100 MB),
	// so the cap never refuses a message the per-file limits allow, and one
	// machine cannot fill the gateway disk. A sender waits for a confirmation
	// or an expiry before staging the next maximum message.
	MaxTransferStoragePerNode int64 = 1 << 30
	// TransferRetention is how long stored parts live without a confirmation
	// from the receiving machine. After it the gateway deletes the bytes.
	TransferRetention = 7 * 24 * time.Hour
	// transferFolder holds the part files under the gateway data folder.
	transferFolder = "transfers"
)

// transferRow is one row of the transfers table.
type transferRow struct {
	id         string
	sender     string
	receiver   string
	name       string
	size       int64
	sha256     string
	partSize   int
	parts      int
	createdAt  int64
	expiresAt  int64
	finishedAt int64
}

const transferColumns = `id, sender, receiver, name, size, sha256, part_size, parts, created_at, expires_at, finished_at`

func scanTransfer(row interface{ Scan(...any) error }) (transferRow, error) {
	var t transferRow
	err := row.Scan(&t.id, &t.sender, &t.receiver, &t.name, &t.size, &t.sha256,
		&t.partSize, &t.parts, &t.createdAt, &t.expiresAt, &t.finishedAt)
	return t, err
}

// transferByID reads one upload. A missing row is an error a caller may
// report as "unknown or expired".
func (s *Store) transferByID(id string) (transferRow, error) {
	return scanTransfer(s.db.QueryRow(`SELECT `+transferColumns+` FROM transfers WHERE id = ?`, id))
}

// beginTransfer validates one xfer.begin and reserves its space. It refuses a
// receiver that is not enrolled and a sender over its storage cap.
func (s *Store) beginTransfer(sender string, p protocol.TransferBegin, now time.Time) (transferRow, error) {
	if p.Size <= 0 {
		return transferRow{}, reject("the upload size must be positive")
	}
	if p.Size > MaxTransferBytes {
		return transferRow{}, reject("the file is larger than the %d MB limit", MaxTransferBytes>>20)
	}
	sum := strings.ToLower(p.SHA256)
	if !validTransferSHA(sum) {
		return transferRow{}, reject("the whole sha256 must be 64 hexadecimal characters")
	}
	name := strings.TrimSpace(p.Name)
	if len(name) > 255 {
		return transferRow{}, reject("the file name is longer than 255 bytes")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return transferRow{}, reject("the file name holds a control character")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return transferRow{}, err
	}
	defer tx.Rollback()
	var enrolled int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM nodes WHERE id = ? AND removed = 0`, p.Receiver).Scan(&enrolled); err != nil {
		return transferRow{}, err
	}
	if enrolled == 0 {
		return transferRow{}, reject("the receiver is not an enrolled machine")
	}
	var stored int64
	if err := tx.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM transfers WHERE sender = ?`, sender).Scan(&stored); err != nil {
		return transferRow{}, err
	}
	if stored+p.Size > MaxTransferStoragePerNode {
		return transferRow{}, reject("this machine already has %d MB staged; the gateway storage limit is %d MB per machine, so wait for a receiver to confirm or for the parts to expire",
			stored>>20, MaxTransferStoragePerNode>>20)
	}
	row := transferRow{
		id:        protocol.NewID("t"),
		sender:    sender,
		receiver:  p.Receiver,
		name:      name,
		size:      p.Size,
		sha256:    sum,
		partSize:  protocol.TransferPartSize,
		parts:     int((p.Size + int64(protocol.TransferPartSize) - 1) / int64(protocol.TransferPartSize)),
		createdAt: now.UnixNano(),
		expiresAt: now.Add(TransferRetention).UnixNano(),
	}
	if _, err := tx.Exec(`INSERT INTO transfers
		(id, sender, receiver, name, size, sha256, part_size, parts, created_at, expires_at, finished_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,0)`,
		row.id, row.sender, row.receiver, row.name, row.size, row.sha256, row.partSize, row.parts, row.createdAt, row.expiresAt); err != nil {
		return transferRow{}, err
	}
	return row, tx.Commit()
}

// transferState builds the answer to xfer.begin and xfer.status.
func (s *Store) transferState(row transferRow) (protocol.TransferState, error) {
	have, received, err := s.transferHave(row.id)
	if err != nil {
		return protocol.TransferState{}, err
	}
	return protocol.TransferState{
		UploadID:  row.id,
		Name:      row.name,
		Receiver:  row.receiver,
		PartSize:  row.partSize,
		Parts:     row.parts,
		Size:      row.size,
		SHA256:    row.sha256,
		Have:      have,
		Received:  received,
		Finished:  row.finishedAt != 0,
		ExpiresAt: row.expiresAt / int64(time.Second),
	}, nil
}

// transferHave lists the stored part indexes and their total size.
func (s *Store) transferHave(id string) ([]int, int64, error) {
	rows, err := s.db.Query(`SELECT idx, size FROM transfer_parts WHERE id = ? ORDER BY idx`, id)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	have := []int{}
	var received int64
	for rows.Next() {
		var idx int
		var size int64
		if err := rows.Scan(&idx, &size); err != nil {
			return nil, 0, err
		}
		have = append(have, idx)
		received += size
	}
	return have, received, rows.Err()
}

// saveTransferPart records a part that is already on disk.
func (s *Store) saveTransferPart(id string, index int, size int64, sha string, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO transfer_parts (id, idx, size, sha256, at) VALUES (?,?,?,?,?)
		ON CONFLICT(id, idx) DO UPDATE SET size = excluded.size, sha256 = excluded.sha256, at = excluded.at`,
		id, index, size, sha, now.UnixNano())
	return err
}

// finishTransfer records that the whole hash was checked.
func (s *Store) finishTransfer(id string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE transfers SET finished_at = ? WHERE id = ?`, now.UnixNano(), id)
	return err
}

// deleteTransferRows removes the bookkeeping of one upload. The files are
// removed by the gateway around it.
func (s *Store) deleteTransferRows(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM transfer_parts WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM transfers WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// expiredTransfers lists the uploads whose retention has passed.
func (s *Store) expiredTransfers(now time.Time) ([]transferRow, error) {
	rows, err := s.db.Query(`SELECT `+transferColumns+` FROM transfers WHERE expires_at <= ? ORDER BY expires_at`, now.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []transferRow
	for rows.Next() {
		row, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// handleTransfer serves the xfer.* frames. One mutex keeps the parts of all
// links in order, so a finish cannot overtake a part and a sweep cannot run
// under a part that is being written.
func (g *Gateway) handleTransfer(nc *nodeConn, typ string, raw []byte) (any, error) {
	g.transferMu.Lock()
	defer g.transferMu.Unlock()
	if err := g.sweepTransfers(); err != nil {
		g.logLine("error", "where", "transfer sweep", "err", err)
	}
	switch typ {
	case "xfer.begin":
		p, err := decode[protocol.TransferBegin](raw)
		if err != nil {
			return nil, reject("bad xfer.begin")
		}
		row, err := g.store.beginTransfer(nc.id, p, g.store.now())
		if err != nil {
			return nil, err
		}
		g.logLine("transfer.started", "upload", row.id, "from", nc.id, "to", row.receiver, "bytes", row.size, "parts", row.parts)
		return g.store.transferState(row)

	case "xfer.status":
		p, err := decode[protocol.TransferStatus](raw)
		if err != nil || p.UploadID == "" {
			return nil, reject("bad xfer.status")
		}
		row, err := g.transferOf(nc.id, p.UploadID, true)
		if err != nil {
			return nil, err
		}
		return g.store.transferState(row)

	case "xfer.part":
		p, err := decode[protocol.TransferPart](raw)
		if err != nil {
			return nil, reject("bad xfer.part")
		}
		return g.storeTransferPart(nc.id, p)

	case "xfer.finish":
		p, err := decode[protocol.TransferFinish](raw)
		if err != nil || p.UploadID == "" {
			return nil, reject("bad xfer.finish")
		}
		return g.finishTransfer(nc.id, p.UploadID)

	case "xfer.get":
		p, err := decode[protocol.TransferGet](raw)
		if err != nil || p.UploadID == "" {
			return nil, reject("bad xfer.get")
		}
		return g.getTransferPart(nc.id, p)

	case "xfer.find":
		p, err := decode[protocol.TransferFind](raw)
		if err != nil {
			return nil, reject("bad xfer.find")
		}
		if len(p.Names) == 0 || len(p.Names) > protocol.MaxFilesPerMessage {
			return nil, reject("xfer.find takes 1 to %d names", protocol.MaxFilesPerMessage)
		}
		return g.findTransfers(nc.id, p.Names)

	case "xfer.confirm":
		p, err := decode[protocol.TransferConfirm](raw)
		if err != nil || p.UploadID == "" {
			return nil, reject("bad xfer.confirm")
		}
		row, err := g.transferOf(nc.id, p.UploadID, false)
		if err != nil {
			return nil, err
		}
		if err := g.removeTransferFiles(row.id); err != nil {
			return nil, err
		}
		if err := g.store.deleteTransferRows(row.id); err != nil {
			return nil, err
		}
		g.logLine("transfer.confirmed", "upload", row.id, "from", row.sender, "to", nc.id, "bytes", row.size)
		return protocol.TransferConfirmed{UploadID: row.id, Deleted: true}, nil
	}
	return nil, reject("unknown frame type %q", typ)
}

// findTransfers lists the finished uploads addressed to the calling machine
// under the given names. Only the receiver sees them, as with xfer.get.
func (g *Gateway) findTransfers(nodeID string, names []string) (protocol.TransferFound, error) {
	out := protocol.TransferFound{Uploads: []protocol.TransferState{}}
	args := []any{nodeID}
	marks := make([]string, 0, len(names))
	for _, n := range names {
		args = append(args, n)
		marks = append(marks, "?")
	}
	rows, err := g.store.db.Query(`SELECT `+transferColumns+` FROM transfers
		WHERE receiver = ? AND finished_at <> 0 AND name IN (`+strings.Join(marks, ",")+`) ORDER BY created_at`, args...)
	if err != nil {
		return out, err
	}
	var found []transferRow
	for rows.Next() {
		row, err := scanTransfer(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		found = append(found, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, row := range found {
		st, err := g.store.transferState(row)
		if err != nil {
			return out, err
		}
		out.Uploads = append(out.Uploads, st)
	}
	return out, nil
}

// transferOf loads one upload and checks that this machine may act on it. The
// sender may upload and resume; only the receiver may download and confirm.
func (g *Gateway) transferOf(nodeID, id string, sender bool) (transferRow, error) {
	if !validTransferID(id) {
		return transferRow{}, reject("unknown or expired upload %q", id)
	}
	row, err := g.store.transferByID(id)
	if errors.Is(err, sql.ErrNoRows) {
		return transferRow{}, reject("unknown or expired upload %q", id)
	}
	if err != nil {
		return transferRow{}, err
	}
	if sender && row.sender != nodeID {
		return transferRow{}, reject("only the sending machine may do that")
	}
	if !sender && row.receiver != nodeID {
		return transferRow{}, reject("only the receiving machine may do that")
	}
	return row, nil
}

// transferPartSize is the expected byte count of one part.
func transferPartSize(row transferRow, index int) int64 {
	if index == row.parts-1 {
		return row.size - int64(row.parts-1)*int64(row.partSize)
	}
	return int64(row.partSize)
}

func (g *Gateway) storeTransferPart(nodeID string, p protocol.TransferPart) (any, error) {
	row, err := g.transferOf(nodeID, p.UploadID, true)
	if err != nil {
		return nil, err
	}
	if row.finishedAt != 0 {
		return nil, reject("this upload is already finished")
	}
	if p.Index < 0 || p.Index >= row.parts {
		return nil, reject("part %d is outside 0 to %d", p.Index, row.parts-1)
	}
	want := transferPartSize(row, p.Index)
	if int64(len(p.Data)) != want {
		return nil, reject("part %d is %d bytes, want %d", p.Index, len(p.Data), want)
	}
	sum := sha256.Sum256(p.Data)
	got := hex.EncodeToString(sum[:])
	if got != strings.ToLower(p.SHA256) {
		return nil, reject("part %d checksum does not match", p.Index)
	}
	if err := g.writeTransferPart(row.id, p.Index, p.Data); err != nil {
		return nil, err
	}
	if err := g.store.saveTransferPart(row.id, p.Index, want, got, g.store.now()); err != nil {
		return nil, err
	}
	have, received, err := g.store.transferHave(row.id)
	if err != nil {
		return nil, err
	}
	return protocol.TransferPartResult{Have: len(have), Received: received}, nil
}

func (g *Gateway) finishTransfer(nodeID, id string) (any, error) {
	row, err := g.transferOf(nodeID, id, true)
	if err != nil {
		return nil, err
	}
	if row.finishedAt == 0 {
		have, received, err := g.store.transferHave(row.id)
		if err != nil {
			return nil, err
		}
		if len(have) != row.parts || received != row.size {
			return nil, reject("the upload is incomplete: %d of %d parts, %d of %d bytes", len(have), row.parts, received, row.size)
		}
		got, err := g.hashTransfer(row)
		if err != nil {
			return nil, err
		}
		if got != row.sha256 {
			// A part matched its own checksum, so the declared whole hash or
			// one part from another upload is wrong. Drop it all rather than
			// keep bytes nobody can use.
			if err := g.removeTransferFiles(row.id); err != nil {
				return nil, err
			}
			if err := g.store.deleteTransferRows(row.id); err != nil {
				return nil, err
			}
			return nil, reject("the whole sha256 hash does not match; the upload was dropped")
		}
		if err := g.store.finishTransfer(row.id, g.store.now()); err != nil {
			return nil, err
		}
		g.logLine("transfer.finished", "upload", row.id, "from", row.sender, "to", row.receiver, "bytes", row.size)
	}
	return protocol.TransferFinished{UploadID: row.id, SHA256: row.sha256, Size: row.size}, nil
}

func (g *Gateway) getTransferPart(nodeID string, p protocol.TransferGet) (any, error) {
	row, err := g.transferOf(nodeID, p.UploadID, false)
	if err != nil {
		return nil, err
	}
	if row.finishedAt == 0 {
		return nil, reject("this upload is not finished")
	}
	if p.Index < 0 || p.Index >= row.parts {
		return nil, reject("part %d is outside 0 to %d", p.Index, row.parts-1)
	}
	b, err := os.ReadFile(g.transferPartPath(row.id, p.Index))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("part %d of upload %s is missing", p.Index, row.id)
		}
		return nil, err
	}
	sum := sha256.Sum256(b)
	return protocol.TransferChunk{
		UploadID:    row.id,
		Index:       p.Index,
		Parts:       row.parts,
		Size:        row.size,
		SHA256:      hex.EncodeToString(sum[:]),
		TotalSHA256: row.sha256,
		Data:        b,
	}, nil
}

// hashTransfer reads the stored parts in order and hashes them.
func (g *Gateway) hashTransfer(row transferRow) (string, error) {
	h := sha256.New()
	for i := 0; i < row.parts; i++ {
		f, err := os.Open(g.transferPartPath(row.id, i))
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (g *Gateway) transferPartPath(id string, index int) string {
	return filepath.Join(g.transferFolderPath(id), strconv.Itoa(index))
}

func (g *Gateway) transferFolderPath(id string) string {
	return filepath.Join(g.opts.DataDir, transferFolder, id)
}

func (g *Gateway) writeTransferPart(id string, index int, data []byte) error {
	dir := g.transferFolderPath(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+strconv.Itoa(index)+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, strconv.Itoa(index)))
}

func (g *Gateway) removeTransferFiles(id string) error {
	if !validTransferID(id) {
		return reject("unknown or expired upload %q", id)
	}
	if err := os.RemoveAll(g.transferFolderPath(id)); err != nil {
		return err
	}
	return nil
}

// sweepTransfers deletes the uploads whose retention passed. A removal that
// fails keeps the row, so the next sweep tries again and the bytes stay
// counted against the sender's cap.
func (g *Gateway) sweepTransfers() error {
	rows, err := g.store.expiredTransfers(g.store.now())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := g.removeTransferFiles(row.id); err != nil {
			return err
		}
		if err := g.store.deleteTransferRows(row.id); err != nil {
			return err
		}
		g.logLine("transfer.expired", "upload", row.id, "from", row.sender, "to", row.receiver, "bytes", row.size)
	}
	return nil
}

// sweepTransfersNow takes the transfer lock for the maintenance loop.
func (g *Gateway) sweepTransfersNow() {
	g.transferMu.Lock()
	defer g.transferMu.Unlock()
	if err := g.sweepTransfers(); err != nil {
		g.logLine("error", "where", "transfer sweep", "err", err)
	}
}

func validTransferSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// validTransferID accepts only the shape protocol.NewID("t") makes, so an id
// from a frame can never name a path outside the transfer folder.
func validTransferID(id string) bool {
	if len(id) != 18 || !strings.HasPrefix(id, "t_") {
		return false
	}
	_, err := hex.DecodeString(id[2:])
	return err == nil
}
