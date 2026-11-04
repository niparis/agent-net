// Command release-key creates a release key or signs a release manifest.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/niparis/agent-net/internal/upgrade"
)

const namespace = "agent-net"

func main() {
	var err error
	switch {
	case len(os.Args) == 3 && os.Args[1] == "generate":
		err = generate(os.Args[2], os.Stdout)
	case (len(os.Args) == 3 || len(os.Args) == 4) && os.Args[1] == "sign":
		installerPath := "install.sh"
		if len(os.Args) == 4 {
			installerPath = os.Args[3]
		}
		var installer []byte
		installer, err = os.ReadFile(installerPath)
		if err == nil {
			err = signFile([]byte(os.Getenv("RELEASE_SIGNING_KEY")), os.Args[2], upgrade.ReleasePublicKeyBase64, installer)
		}
	default:
		err = errors.New("usage: release-key generate PRIVATE_KEY_PATH | sign CHECKSUMS_PATH [INSTALLER_PATH]")
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
	line := "  RELEASE_SSH_PUBLIC_KEY=\"" + sshPublicKey(pub) + "\""
	if base64.StdEncoding.EncodeToString(pub) != expectedGoPublic || !strings.Contains("\n"+string(installer)+"\n", "\n"+line+"\n") {
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
