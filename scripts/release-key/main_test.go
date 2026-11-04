package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyAndReleaseSignatures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.pem")
	var output bytes.Buffer
	if err := generate(path, &output); err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "PRIVATE KEY") {
		t.Fatal("printed private key")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode: %v", info.Mode())
	}
	block, _ := pem.Decode(keyPEM)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key := parsed.(ed25519.PrivateKey)
	pub := key.Public().(ed25519.PublicKey)
	if !strings.Contains(output.String(), base64.StdEncoding.EncodeToString(pub)) || !strings.Contains(output.String(), sshPublicKey(pub)) {
		t.Fatal("public keys not printed")
	}
	if err := generate(path, &output); err == nil {
		t.Fatal("overwrote private key")
	}
	sumsPath := filepath.Join(t.TempDir(), "checksums.txt")
	sums := []byte("example checksums\n")
	if err := os.WriteFile(sumsPath, sums, 0o644); err != nil {
		t.Fatal(err)
	}
	installer := []byte("  RELEASE_SSH_PUBLIC_KEY=\"" + sshPublicKey(pub) + "\"\n")
	if err := signFile(keyPEM, sumsPath, base64.StdEncoding.EncodeToString(pub), installer); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(sumsPath + ".sig")
	if err != nil || !ed25519.Verify(pub, sums, raw) {
		t.Fatalf("raw signature: %v", err)
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen unavailable")
	}
	allowed := filepath.Join(t.TempDir(), "allowed")
	if err := os.WriteFile(allowed, []byte("agent-net "+sshPublicKey(pub)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "agent-net", "-n", "agent-net", "-s", sumsPath+".sshsig")
	cmd.Stdin = bytes.NewReader(sums)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("SSH signature: %v: %s", err, out)
	}
}

func TestSigningRejectsMissingOrMismatchedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.pem")
	var output bytes.Buffer
	if err := generate(path, &output); err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(path)
	block, _ := pem.Decode(key)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	public := base64.StdEncoding.EncodeToString(parsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey))
	sums := filepath.Join(t.TempDir(), "checksums.txt")
	os.WriteFile(sums, []byte("sums"), 0o600)
	for _, tc := range []struct {
		key       []byte
		goPub     string
		installer []byte
	}{
		{nil, "", nil}, {key, "wrong", nil}, {key, public, []byte("wrong installer key")},
	} {
		if err := signFile(tc.key, sums, tc.goPub, tc.installer); err == nil {
			t.Fatal("accepted bad signing configuration")
		}
		if _, err := os.Stat(sums + ".sig"); !os.IsNotExist(err) {
			t.Fatal("signature written on failure")
		}
	}
}
