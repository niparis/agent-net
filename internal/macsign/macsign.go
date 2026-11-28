// Package macsign signs agent-net binaries on macOS with a code-signing
// identity made once per Mac.
//
// Go links darwin binaries with an ad-hoc signature. macOS ties a privacy
// permission (for example "access data from other apps") to the code's
// designated requirement, and for an ad-hoc signature that requirement is the
// hash of the exact build. Each update is then a new program and macOS asks
// again. A binary signed with a certificate has the requirement "this
// identifier and this certificate", which stays the same across builds, so one
// Allow lasts.
//
// The identity is a self-signed certificate and its key in a keychain file of
// agent-net's own, never the login or system keychain (decision 0199). The
// package depends on nothing else in agent-net.
package macsign

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// Identifier is the fixed code identifier of every agent-net binary. It is
// part of the designated requirement, so it must never change.
const Identifier = "dev.agent-net.agent-net"

// commonName names the certificate in Keychain Access.
const commonName = "agent-net code signing"

// validFor is long because an expired certificate stops signing, and a new
// certificate would mean a new Allow.
const validFor = 30 * 365 * 24 * time.Hour

// Dir is the folder that holds the keychain, its password and the identity's
// hash, under the daemon state folder.
func Dir(stateDir string) string { return filepath.Join(stateDir, "codesign") }

func keychainPath(dir string) string { return filepath.Join(dir, "agent-net.keychain-db") }
func passwordPath(dir string) string { return filepath.Join(dir, "keychain-password") }
func identityPath(dir string) string { return filepath.Join(dir, "identity") }
func statusPath(dir string) string   { return filepath.Join(dir, "last-sign.txt") }

// Sign signs the binary at path with this Mac's identity, making the identity
// first if there is none. path is replaced by a rename, so a process that
// runs from it keeps running. On any other system it does nothing. A failure
// leaves path as it was; callers go on with the unsigned binary.
func Sign(ctx context.Context, stateDir, path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	dir := Dir(stateDir)
	err := sign(ctx, dir, path)
	recordStatus(dir, path, err)
	return err
}

// Remove deletes the keychain (which also takes it off the user's keychain
// search list) and the folder. It reports whether there was anything to
// remove.
func Remove(ctx context.Context, stateDir string) (bool, error) {
	return remove(ctx, Dir(stateDir), runtime.GOOS == "darwin")
}

// LastStatus is the result of the last signing on this machine, for the
// daemon log. It is empty when nothing was ever signed here.
func LastStatus(stateDir string) string {
	b, err := os.ReadFile(statusPath(Dir(stateDir)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func recordStatus(dir, path string, err error) {
	line := fmt.Sprintf("%s %s signed as %s", time.Now().UTC().Format(time.RFC3339), path, Identifier)
	if err != nil {
		line = fmt.Sprintf("%s %s not signed, macOS may ask again for access: %v", time.Now().UTC().Format(time.RFC3339), path, err)
	}
	if os.MkdirAll(dir, 0o700) == nil {
		os.WriteFile(statusPath(dir), []byte(line+"\n"), 0o600)
	}
}

func sign(ctx context.Context, dir, path string) error {
	id, pw, err := ensureIdentity(ctx, dir)
	if err != nil {
		return fmt.Errorf("cannot create the signing identity: %w", err)
	}
	kc := keychainPath(dir)
	if err := ensureSearchList(ctx, kc); err != nil {
		return err
	}
	if err := security(ctx, "unlock-keychain", "-p", pw, kc); err != nil {
		return err
	}
	tmp, err := copyBeside(path)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	// --timestamp=none: a self-signed signature gains nothing from Apple's
	// timestamp server, and signing must work offline.
	if out, err := exec.CommandContext(ctx, "codesign", "--force", "--sign", id, "--keychain", kc,
		"--identifier", Identifier, "--timestamp=none", tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("codesign: %v: %s", err, lastLine(out))
	}
	return os.Rename(tmp, path)
}

// ensureIdentity returns the identity's SHA-1 and the keychain password,
// making both when they are missing. The identity file is written last, so
// its presence means the keychain is complete.
func ensureIdentity(ctx context.Context, dir string) (id, pw string, err error) {
	kc := keychainPath(dir)
	idb, idErr := os.ReadFile(identityPath(dir))
	pwb, pwErr := os.ReadFile(passwordPath(dir))
	_, kcErr := os.Stat(kc)
	if idErr == nil && pwErr == nil && kcErr == nil {
		return strings.TrimSpace(string(idb)), strings.TrimSpace(string(pwb)), nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	if kcErr == nil {
		// A half-made keychain from an interrupted first run.
		security(ctx, "delete-keychain", kc)
		os.Remove(kc)
	}
	os.Remove(identityPath(dir))
	pw = randomHex(32)
	if err := os.WriteFile(passwordPath(dir), []byte(pw+"\n"), 0o600); err != nil {
		return "", "", err
	}
	key, cert, err := newCertificate()
	if err != nil {
		return "", "", err
	}
	p12pw := randomHex(16)
	// Legacy encryption: older macOS releases cannot read the modern one.
	p12, err := pkcs12.LegacyDES.Encode(key, cert, nil, p12pw)
	if err != nil {
		return "", "", err
	}
	p12Path := filepath.Join(dir, "identity.p12")
	if err := os.WriteFile(p12Path, p12, 0o600); err != nil {
		return "", "", err
	}
	defer os.Remove(p12Path)
	steps := [][]string{
		{"create-keychain", "-p", pw, kc},
		{"unlock-keychain", "-p", pw, kc},
		// -T lets codesign use the key without a dialog.
		{"import", p12Path, "-k", kc, "-P", p12pw, "-f", "pkcs12", "-T", "/usr/bin/codesign"},
		// Since macOS 10.12 the key also needs a partition list that names
		// Apple's tools, or codesign stops with errSecInternalComponent.
		{"set-key-partition-list", "-S", "apple-tool:,apple:,codesign:", "-s", "-k", pw, kc},
	}
	for _, s := range steps {
		if err := security(ctx, s...); err != nil {
			return "", "", err
		}
	}
	sum := sha1.Sum(cert.Raw)
	id = strings.ToUpper(hex.EncodeToString(sum[:]))
	if err := os.WriteFile(identityPath(dir), []byte(id+"\n"), 0o600); err != nil {
		return "", "", err
	}
	return id, pw, nil
}

func newCertificate() (*rsa.PrivateKey, *x509.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validFor),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return key, cert, err
}

// ensureSearchList puts the keychain on the user's keychain search list when
// it is not there: codesign finds the private key only through that list.
func ensureSearchList(ctx context.Context, kc string) error {
	out, err := exec.CommandContext(ctx, "security", "list-keychains", "-d", "user").Output()
	if err != nil {
		return fmt.Errorf("security list-keychains: %v", err)
	}
	list := []string{kc}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.Trim(strings.TrimSpace(l), `"`)
		if l == kc {
			return nil
		}
		if l != "" {
			list = append(list, l)
		}
	}
	return security(ctx, append([]string{"list-keychains", "-d", "user", "-s"}, list...)...)
}

func remove(ctx context.Context, dir string, useSecurity bool) (bool, error) {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	var errs []error
	if _, err := os.Stat(keychainPath(dir)); err == nil && useSecurity {
		errs = append(errs, security(ctx, "delete-keychain", keychainPath(dir)))
	}
	errs = append(errs, os.RemoveAll(dir))
	return true, errors.Join(errs...)
}

// security runs one security(1) subcommand. The error names only the
// subcommand, never its arguments: some of them are passwords.
func security(ctx context.Context, args ...string) error {
	if out, err := exec.CommandContext(ctx, "security", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("security %s: %v: %s", args[0], err, lastLine(out))
	}
	return nil
}

// copyBeside copies path to a new file in the same folder with the same mode.
func copyBeside(path string) (string, error) {
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return "", err
	}
	out, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".sign-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(st.Mode().Perm())
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
