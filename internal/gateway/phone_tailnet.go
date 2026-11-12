package gateway

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/funnel"
	"github.com/niparis/agent-net/internal/protocol"
)

// Only the TCP socket's kernel owner authenticates the local proxy. Headers,
// including forwarded addresses, cannot establish that the caller is root.
type phonePeer interface {
	UID(remote, local string) (uint32, error)
}
type phoneOwner interface {
	Login(context.Context) (string, error)
}
type phoneTailnet struct {
	peer  phonePeer
	owner phoneOwner
}

type procPhonePeer struct{ dir string }

func (p procPhonePeer) UID(remote, local string) (uint32, error) {
	if runtime.GOOS != "linux" {
		return 0, errors.New("tailnet pairing requires Linux")
	}
	peer, err := netip.ParseAddrPort(remote)
	if err != nil || !peer.Addr().IsLoopback() {
		return 0, errors.New("not a loopback peer")
	}
	listener, err := netip.ParseAddrPort(local)
	if err != nil || !listener.Addr().IsLoopback() {
		return 0, errors.New("not a loopback listener")
	}
	dir := p.dir
	if dir == "" {
		dir = "/proc/net"
	}
	var uid uint32
	found := false
	for _, file := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return 0, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "01" || fields[9] == "0" {
				continue
			}
			src, err := procPhoneAddr(fields[1])
			if err != nil || src != netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port()) {
				continue
			}
			dst, err := procPhoneAddr(fields[2])
			if err != nil || dst != netip.AddrPortFrom(listener.Addr().Unmap(), listener.Port()) {
				continue
			}
			n, err := strconv.ParseUint(fields[7], 10, 32)
			if err != nil {
				return 0, err
			}
			if found {
				return 0, errors.New("ambiguous peer socket")
			}
			found = true
			uid = uint32(n)
		}
	}
	if !found {
		return 0, errors.New("peer socket not found")
	}
	return uid, nil
}

// proc prints addresses as native-endian 32-bit words, including IPv6.
func procPhoneAddr(raw string) (netip.AddrPort, error) {
	host, port, ok := strings.Cut(raw, ":")
	if !ok || (len(host) != 8 && len(host) != 32) {
		return netip.AddrPort{}, errors.New("invalid proc address")
	}
	n, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return netip.AddrPort{}, err
	}
	b := make([]byte, len(host)/2)
	for i := 0; i < len(host); i += 8 {
		word, err := strconv.ParseUint(host[i:i+8], 16, 32)
		if err != nil {
			return netip.AddrPort{}, err
		}
		binary.NativeEndian.PutUint32(b[i/2:i/2+4], uint32(word))
	}
	addr, ok := netip.AddrFromSlice(b)
	if !ok {
		return netip.AddrPort{}, errors.New("invalid proc address")
	}
	return netip.AddrPortFrom(addr.Unmap(), uint16(n)), nil
}

type tailscalePhoneOwner struct {
	mu      sync.Mutex
	runner  funnel.Runner
	now     func() time.Time
	expires time.Time
	login   string
	err     error
}

func (o *tailscalePhoneOwner) Login(ctx context.Context) (string, error) {
	// Concurrent refreshes fall back to pairing rather than queueing requests.
	if !o.mu.TryLock() {
		return "", errors.New("Tailscale identity refresh in progress")
	}
	defer o.mu.Unlock()
	now := time.Now()
	if o.now != nil {
		now = o.now()
	}
	if now.Before(o.expires) {
		return o.login, o.err
	}
	o.login = ""
	o.err = errors.New("Tailscale owner unavailable")
	o.expires = now.Add(5 * time.Second)
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	runner := o.runner
	if runner == nil {
		runner = funnel.ExecRunner{}
	}
	raw, _, err := runner.Run(ctx, "status", "--json")
	if err != nil {
		o.err = err
		return "", err
	}
	var status struct {
		Self *struct{ UserID uint64 }
		User map[string]struct{ LoginName string }
	}
	if err = json.Unmarshal([]byte(raw), &status); err != nil {
		o.err = err
		return "", err
	}
	if status.Self == nil || status.Self.UserID == 0 {
		return "", o.err
	}
	login := status.User[strconv.FormatUint(status.Self.UserID, 10)].LoginName
	if login == "" {
		return "", o.err
	}
	o.login = login
	o.err = nil
	o.expires = now.Add(30 * time.Second)
	return login, nil
}

func (t *phoneTailnet) login(r *http.Request) string {
	if len(r.Header.Values("Tailscale-User-Login")) != 1 || r.Header.Get("Tailscale-Funnel-Request") != "" {
		return ""
	}
	login, err := new(mime.WordDecoder).DecodeHeader(r.Header.Get("Tailscale-User-Login"))
	if err != nil || login == "" {
		return ""
	}
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return ""
	}
	uid, err := t.peer.UID(r.RemoteAddr, local.String())
	if err != nil || uid != 0 {
		return ""
	}
	owner, err := t.owner.Login(r.Context())
	if err != nil || owner == "" || login != owner {
		return ""
	}
	return owner
}

// Device, token and information notice are committed together. No pending
// code or app-consent approval is created by this path.
func (s *Store) pairPhoneTailnet(login, ua string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := s.now()
	id := protocol.NewID("app")
	base := "Tailscale sign-in: " + login + ", " + phoneClientName(ua)
	name := base
	// Browsers with the same short user agent still get separate revocable apps.
	for i := 2; ; i++ {
		var count int
		if err = tx.QueryRow(`SELECT count(*) FROM apps WHERE name=? AND revoked_at IS NULL`, name).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			break
		}
		name = fmt.Sprintf("%s (%d)", base, i)
	}
	if _, err = tx.Exec(`INSERT INTO apps (id,name,kind,client_id,client_name,redirect_uris,created_at,last_used_at,revoked_at,grants) VALUES (?,?,'owner_app','phone',?,'[]',?,?,NULL,'[]')`, id, name, phoneClientName(ua), now.UnixNano(), now.UnixNano()); err != nil {
		return "", err
	}
	family, err := oauthRandom(32)
	if err != nil {
		return "", err
	}
	token, err := issueFamilyToken(tx, id, family, "access", "phone", now, phoneTokenTTL)
	if err != nil {
		return "", err
	}
	// A pairing is a digest line for the CTO (decision 0152), not an owner
	// item. The device itself shows in Connected apps, where it can be revoked.
	_, err = s.recordIntake(tx, intakeEvent{Kind: protocol.IntakePhonePaired, Subject: id,
		Text: "Paired " + name + " by Tailscale sign-in", Detail: "Revoke this device in Connected apps if the owner did not pair it."}, now)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}
