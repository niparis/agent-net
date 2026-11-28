package macsign

import (
	"context"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// fakeSecurity stands in for macOS security(1). It logs each call, creates
// and deletes the keychain file, and keeps a search list in a file.
// FAKE_SECURITY_FAIL names one subcommand that fails.
const fakeSecurity = `#!/bin/sh
echo "security $*" >> "$FAKE_LOG"
[ "$1" = "${FAKE_SECURITY_FAIL:-}" ] && { echo "security: $1 failed" >&2; exit 1; }
case "$1" in
  create-keychain) eval "kc=\${$#}"; [ -e "$kc" ] && { echo "already exists" >&2; exit 48; }; : > "$kc" ;;
  delete-keychain) eval "kc=\${$#}"; rm -f "$kc" ;;
  import) cp "$2" "$FAKE_P12" ;;
  list-keychains)
    if [ "${4:-}" = "-s" ]; then shift 4; : > "$FAKE_SEARCH"; for k in "$@"; do echo "$k" >> "$FAKE_SEARCH"; done
    else [ -f "$FAKE_SEARCH" ] && while read -r k; do echo "    \"$k\""; done < "$FAKE_SEARCH"; fi ;;
esac
exit 0
`

// fakeCodesign stands in for codesign(1): it appends a marker to the file it
// signs, or fails when FAKE_CODESIGN_FAIL is set.
const fakeCodesign = `#!/bin/sh
echo "codesign $*" >> "$FAKE_LOG"
[ -n "${FAKE_CODESIGN_FAIL:-}" ] && { echo "errSecInternalComponent" >&2; exit 1; }
eval "f=\${$#}"
echo "SIGNED" >> "$f"
`

type fakes struct {
	log, search string
}

// setupFakes puts fake security and codesign first on PATH, so no test ever
// reaches a real keychain.
func setupFakes(t *testing.T) fakes {
	t.Helper()
	bin := t.TempDir()
	for name, body := range map[string]string{"security": fakeSecurity, "codesign": fakeCodesign} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := fakes{log: filepath.Join(bin, "calls.log"), search: filepath.Join(bin, "search.list")}
	os.WriteFile(f.search, []byte("/Users/someone/Library/Keychains/login.keychain-db\n"), 0o600)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_LOG", f.log)
	t.Setenv("FAKE_SEARCH", f.search)
	t.Setenv("FAKE_P12", filepath.Join(bin, "imported.p12"))
	return f
}

func (f fakes) calls(t *testing.T) []string {
	t.Helper()
	b, _ := os.ReadFile(f.log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (f fakes) count(t *testing.T, prefix string) int {
	n := 0
	for _, c := range f.calls(t) {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func writeBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agent-net")
	if err := os.WriteFile(p, []byte("BINARY\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSignCreatesIdentityOnceAndSignsWithFixedIdentifier(t *testing.T) {
	f := setupFakes(t)
	dir := filepath.Join(t.TempDir(), "codesign")
	ctx := context.Background()

	bin := writeBinary(t)
	if err := sign(ctx, dir, bin); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "BINARY\nSIGNED\n" {
		t.Fatalf("binary after sign = %q", b)
	}
	if st, _ := os.Stat(bin); st.Mode().Perm() != 0o755 {
		t.Fatalf("binary mode = %v, want 0755", st.Mode().Perm())
	}
	kc := filepath.Join(dir, "agent-net.keychain-db")
	if f.count(t, "security create-keychain") != 1 || f.count(t, "security import") != 1 || f.count(t, "security set-key-partition-list") != 1 {
		t.Fatalf("identity not created once:\n%s", strings.Join(f.calls(t), "\n"))
	}
	if st, err := os.Stat(filepath.Join(dir, "keychain-password")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("password file: %v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "identity.p12")); !os.IsNotExist(err) {
		t.Fatalf("the key file was left outside the keychain: %v", err)
	}
	checkImportedIdentity(t, f, identityOf(t, dir))
	search, _ := os.ReadFile(f.search)
	if !strings.Contains(string(search), kc) || !strings.Contains(string(search), "login.keychain-db") {
		t.Fatalf("search list = %q, want our keychain added and login kept", search)
	}
	cs := regexp.MustCompile(`^codesign --force --sign [0-9A-F]{40} --keychain ` + regexp.QuoteMeta(kc) + ` --identifier dev\.agent-net\.agent-net --timestamp=none `)
	if !cs.MatchString(f.calls(t)[len(f.calls(t))-1]) {
		t.Fatalf("codesign call = %q", f.calls(t)[len(f.calls(t))-1])
	}

	// A second upgrade reuses the identity: no new keychain, no new import.
	bin2 := writeBinary(t)
	if err := sign(ctx, dir, bin2); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "security create-keychain") != 1 || f.count(t, "security import") != 1 {
		t.Fatalf("second sign created a new identity:\n%s", strings.Join(f.calls(t), "\n"))
	}
	if f.count(t, "codesign") != 2 {
		t.Fatalf("want two codesign calls:\n%s", strings.Join(f.calls(t), "\n"))
	}
	for _, c := range f.calls(t) {
		if strings.HasPrefix(c, "codesign") && !strings.Contains(c, "--sign "+identityOf(t, dir)) {
			t.Fatalf("signed with another identity: %q", c)
		}
	}
}

// checkImportedIdentity decodes the file given to security import with the
// password given with it: a code signing certificate, its key, and the SHA-1
// that codesign is given.
func checkImportedIdentity(t *testing.T, f fakes, id string) {
	t.Helper()
	var pw string
	for _, c := range f.calls(t) {
		if m := regexp.MustCompile(` -P (\S+) `).FindStringSubmatch(c); m != nil && strings.HasPrefix(c, "security import") {
			pw = m[1]
		}
	}
	p12, err := os.ReadFile(os.Getenv("FAKE_P12"))
	if err != nil {
		t.Fatal(err)
	}
	key, cert, err := pkcs12.Decode(p12, pw)
	if err != nil {
		t.Fatalf("imported file does not decode: %v", err)
	}
	if cert.Subject.CommonName != "agent-net code signing" || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageCodeSigning || cert.IsCA {
		t.Fatalf("certificate: %+v", cert)
	}
	if !key.(*rsa.PrivateKey).PublicKey.Equal(cert.PublicKey) {
		t.Fatal("key does not match the certificate")
	}
	if cert.NotAfter.Before(time.Now().Add(29 * 365 * 24 * time.Hour)) {
		t.Fatalf("certificate ends %v", cert.NotAfter)
	}
	sum := sha1.Sum(cert.Raw)
	if strings.ToUpper(hex.EncodeToString(sum[:])) != id {
		t.Fatalf("identity %s is not the certificate's SHA-1", id)
	}
}

func identityOf(t *testing.T, dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "identity"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestSignFailureLeavesBinaryUnchanged(t *testing.T) {
	for _, tc := range []struct{ name, env, val, want string }{
		{"identity", "FAKE_SECURITY_FAIL", "import", "cannot create the signing identity"},
		{"codesign", "FAKE_CODESIGN_FAIL", "1", "errSecInternalComponent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupFakes(t)
			t.Setenv(tc.env, tc.val)
			bin := writeBinary(t)
			err := sign(context.Background(), filepath.Join(t.TempDir(), "codesign"), bin)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to say %q", err, tc.want)
			}
			if b, _ := os.ReadFile(bin); string(b) != "BINARY\n" {
				t.Fatalf("binary changed after a failed sign: %q", b)
			}
			if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(bin), ".agent-net.sign-*")); len(leftovers) > 0 {
				t.Fatalf("temp files left: %v", leftovers)
			}
		})
	}
}

func TestSignRecreatesHalfMadeIdentity(t *testing.T) {
	f := setupFakes(t)
	dir := filepath.Join(t.TempDir(), "codesign")
	t.Setenv("FAKE_SECURITY_FAIL", "import")
	if err := sign(context.Background(), dir, writeBinary(t)); err == nil {
		t.Fatal("want an error")
	}
	t.Setenv("FAKE_SECURITY_FAIL", "")
	if err := sign(context.Background(), dir, writeBinary(t)); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "security delete-keychain") != 1 || f.count(t, "security create-keychain") != 2 {
		t.Fatalf("half-made keychain not replaced:\n%s", strings.Join(f.calls(t), "\n"))
	}
}

func TestErrorsNeverShowThePassword(t *testing.T) {
	setupFakes(t)
	t.Setenv("FAKE_SECURITY_FAIL", "unlock-keychain")
	dir := filepath.Join(t.TempDir(), "codesign")
	err := sign(context.Background(), dir, writeBinary(t))
	if err == nil {
		t.Fatal("want an error")
	}
	pw, _ := os.ReadFile(filepath.Join(dir, "keychain-password"))
	if len(pw) == 0 || strings.Contains(err.Error(), strings.TrimSpace(string(pw))) {
		t.Fatalf("error shows the keychain password: %v", err)
	}
}

func TestRemoveDeletesKeychainAndFolder(t *testing.T) {
	f := setupFakes(t)
	state := t.TempDir()
	dir := Dir(state)
	if err := sign(context.Background(), dir, writeBinary(t)); err != nil {
		t.Fatal(err)
	}
	removed, err := remove(context.Background(), dir, true)
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if f.count(t, "security delete-keychain "+filepath.Join(dir, "agent-net.keychain-db")) != 1 {
		t.Fatalf("keychain not deleted with security:\n%s", strings.Join(f.calls(t), "\n"))
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
	if removed, err := remove(context.Background(), dir, true); err != nil || removed {
		t.Fatalf("second remove = %v, %v, want nothing to do", removed, err)
	}
}

func TestStatusIsRecordedForTheDaemonLog(t *testing.T) {
	state := t.TempDir()
	if got := LastStatus(state); got != "" {
		t.Fatalf("status before any sign = %q", got)
	}
	recordStatus(Dir(state), "/x/agent-net", errFake("codesign: errSecInternalComponent"))
	if got := LastStatus(state); !strings.Contains(got, "/x/agent-net not signed") || !strings.Contains(got, "errSecInternalComponent") {
		t.Fatalf("status = %q", got)
	}
	recordStatus(Dir(state), "/x/agent-net", nil)
	if got := LastStatus(state); !strings.Contains(got, "/x/agent-net signed as dev.agent-net.agent-net") {
		t.Fatalf("status = %q", got)
	}
}

func TestSignDoesNothingOffMacOS(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("checks the other systems")
	}
	f := setupFakes(t)
	bin := writeBinary(t)
	if err := Sign(context.Background(), t.TempDir(), bin); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "BINARY\n" {
		t.Fatalf("binary changed: %q", b)
	}
	if _, err := os.Stat(f.log); !os.IsNotExist(err) {
		t.Fatalf("security or codesign ran: %v", f.calls(t))
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
