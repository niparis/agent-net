// Command release-key creates a release key or signs a release manifest.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"

	"github.com/niparis/agent-net/internal/upgrade"
)

const namespace = "agent-net"

func main() {
	var err error
	switch {
	case len(os.Args) == 3 && os.Args[1] == "generate":
		err = generate(os.Args[2], os.Stdout)
	case (len(os.Args) == 3 || len(os.Args) == 4) && (os.Args[1] == "sign" || os.Args[1] == "verify"):
		installerPath := "install.sh"
		if len(os.Args) == 4 {
			installerPath = os.Args[3]
		}
		var installer []byte
		installer, err = os.ReadFile(installerPath)
		if err == nil {
			if os.Args[1] == "sign" {
				err = signFile([]byte(os.Getenv("RELEASE_SIGNING_KEY")), os.Args[2], upgrade.ReleasePublicKeyBase64, installer)
			} else {
				err = verifyFile(os.Args[2], upgrade.ReleasePublicKeyBase64, installer)
			}
		}
	default:
		err = errors.New("usage: release-key generate PRIVATE_KEY_PATH | sign|verify CHECKSUMS_PATH [INSTALLER_PATH]")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release-key:", err)
		os.Exit(1)
	}
}

func generate(path string, out io.Writer) error {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	_, err = fmt.Fprintf(out, "Go public key (ReleasePublicKeyBase64): %s\nSSH public key (RELEASE_SSH_PUBLIC_KEY): %s\n", base64.StdEncoding.EncodeToString(pub), sshPublicKey(pub))
	return err
}

// SSH strings are uint32-length-prefixed byte strings, per PROTOCOL.sshsig.
func sshString(data []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

func sshPublicBlob(pub ed25519.PublicKey) []byte {
	return append(sshString([]byte("ssh-ed25519")), sshString(pub)...)
}

func sshPublicKey(pub ed25519.PublicKey) string {
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(sshPublicBlob(pub))
}

// checkPublicKeys checks the committed Go key and the one declaration in the
// installer. A placeholder, malformed key, or mismatch must fail CI.
func checkPublicKeys(goKey string, installer []byte) error {
	pub, err := base64.StdEncoding.Strict().DecodeString(goKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("Go release public key is invalid or still a placeholder")
	}
	keyLine := regexp.MustCompile(`(?m)^[ \t]*RELEASE_SSH_PUBLIC_KEY="([^"\n]*)"[ \t]*$`)
	matches := keyLine.FindAllSubmatch(installer, -1)
	if len(matches) != 1 {
		return errors.New("installer must have exactly one release public key declaration")
	}
	if string(matches[0][1]) != sshPublicKey(ed25519.PublicKey(pub)) {
		return errors.New("installer release public key is invalid, a placeholder, or differs from the Go key")
	}
	return nil
}

func signFile(keyPEM []byte, path, expectedGoPublic string, installer []byte) error {
	block, rest := pem.Decode(keyPEM)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("RELEASE_SIGNING_KEY must contain a PKCS#8 Ed25519 private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return errors.New("RELEASE_SIGNING_KEY is not a valid PKCS#8 private key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return errors.New("RELEASE_SIGNING_KEY is not an Ed25519 private key")
	}
	pub := key.Public().(ed25519.PublicKey)
	if err := checkPublicKeys(expectedGoPublic, installer); err != nil {
		return err
	}
	if base64.StdEncoding.EncodeToString(pub) != expectedGoPublic {
		return errors.New("signing key does not match both embedded release public keys")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	raw := ed25519.Sign(key, data)
	digest := sha512.Sum512(data)
	payload := []byte("SSHSIG")
	for _, field := range [][]byte{[]byte(namespace), nil, []byte("sha512"), digest[:]} {
		payload = append(payload, sshString(field)...)
	}
	sig := ed25519.Sign(key, payload)
	sigBlob := append(sshString([]byte("ssh-ed25519")), sshString(sig)...)
	body := []byte("SSHSIG\x00\x00\x00\x01")
	for _, field := range [][]byte{sshPublicBlob(pub), []byte(namespace), nil, []byte("sha512"), sigBlob} {
		body = append(body, sshString(field)...)
	}
	armored := pem.EncodeToMemory(&pem.Block{Type: "SSH SIGNATURE", Bytes: body})
	if err := os.WriteFile(path+".sig", raw, 0o644); err != nil {
		return err
	}
	return os.WriteFile(path+".sshsig", armored, 0o644)
}

// verifyFile independently checks both release signature formats before upload.
func verifyFile(path, expectedGoPublic string, installer []byte) error {
	if err := checkPublicKeys(expectedGoPublic, installer); err != nil {
		return err
	}
	pub, _ := base64.StdEncoding.Strict().DecodeString(expectedGoPublic)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read checksums: %w", err)
	}
	raw, err := os.ReadFile(path + ".sig")
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), data, raw) {
		return errors.New("raw Ed25519 release signature is missing or invalid")
	}
	allowed, err := os.CreateTemp("", "agent-net-allowed-signers.")
	if err != nil {
		return err
	}
	defer os.Remove(allowed.Name())
	if _, err := fmt.Fprintf(allowed, "agent-net %s\n", sshPublicKey(ed25519.PublicKey(pub))); err != nil {
		allowed.Close()
		return err
	}
	if err := allowed.Close(); err != nil {
		return err
	}
	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed.Name(), "-I", "agent-net", "-n", namespace, "-s", path+".sshsig")
	cmd.Stdin = bytes.NewReader(data)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SSH release signature is missing or invalid, or ssh-keygen cannot verify it: %w", err)
	}
	sums, err := upgrade.ParseChecksums(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("cannot parse checksums: %w", err)
	}
	want, ok := sums["install.sh"]
	if !ok {
		return errors.New("install.sh is not listed in checksums.txt")
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(installer)); got != want {
		return errors.New("install.sh checksum mismatch")
	}
	return nil
}
