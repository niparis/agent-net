package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/upgrade"
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

func TestReleasePublicKeysMatch(t *testing.T) {
	installer, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPublicKeys(upgrade.ReleasePublicKeyBase64, installer); err != nil {
		t.Fatal(err)
	}
}

func TestReleasePublicKeyChecksRejectPlaceholdersAndMismatches(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	goKey := base64.StdEncoding.EncodeToString(pub)
	line := func(sshKey string) []byte { return []byte("  RELEASE_SSH_PUBLIC_KEY=\"" + sshKey + "\"\n") }
	valid := line(sshPublicKey(pub))
	if err := checkPublicKeys(goKey, valid); err != nil {
		t.Fatalf("valid test key: %v", err)
	}
	for _, tc := range []struct {
		name, goKey string
		installer   []byte
	}{
		{"Go placeholder", "REPLACE_WITH_RELEASE_PUBLIC_KEY", valid},
		{"SSH placeholder", goKey, line("ssh-ed25519 REPLACE_WITH_RELEASE_SSH_PUBLIC_KEY")},
		{"wrong key", goKey, line(sshPublicKey(other))},
		{"missing SSH declaration", goKey, nil},
		{"duplicate SSH declaration", goKey, append(append([]byte(nil), valid...), valid...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkPublicKeys(tc.goKey, tc.installer); err == nil {
				t.Fatal("accepted invalid release public keys")
			}
		})
	}
}

func TestSigningRejectsDuplicateInstallerPublicKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.pem")
	if err := generate(path, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(keyPEM)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub := parsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey)
	line := []byte("  RELEASE_SSH_PUBLIC_KEY=\"" + sshPublicKey(pub) + "\"\n")
	installer := append(append([]byte(nil), line...), line...)
	sums := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(sums, []byte("checksums\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := signFile(keyPEM, sums, base64.StdEncoding.EncodeToString(pub), installer); err == nil {
		t.Fatal("signed with ambiguous installer keys")
	}
	if _, err := os.Stat(sums + ".sig"); !os.IsNotExist(err) {
		t.Fatal("signature written on failure")
	}
}

func TestVerifyReleaseSignatures(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen unavailable")
	}
	path := filepath.Join(t.TempDir(), "private.pem")
	if err := generate(path, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(keyPEM)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub := parsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey)
	goKey := base64.StdEncoding.EncodeToString(pub)
	installer := []byte("  RELEASE_SSH_PUBLIC_KEY=\"" + sshPublicKey(pub) + "\"\n")
	otherPath := filepath.Join(t.TempDir(), "other.pem")
	if err := generate(otherPath, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	otherKey, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	otherBlock, _ := pem.Decode(otherKey)
	otherParsed, err := x509.ParsePKCS8PrivateKey(otherBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	otherPub := otherParsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey)
	otherSums := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(otherSums, []byte("checksums\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherInstaller := []byte("  RELEASE_SSH_PUBLIC_KEY=\"" + sshPublicKey(otherPub) + "\"\n")
	if err := signFile(otherKey, otherSums, base64.StdEncoding.EncodeToString(otherPub), otherInstaller); err != nil {
		t.Fatal(err)
	}
	wrongSSHSignature, err := os.ReadFile(otherSums + ".sshsig")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		alter   func(*testing.T, string) []byte
		wantErr bool
	}{
		{"valid", nil, false},
		{"missing raw", func(t *testing.T, p string) []byte {
			if err := os.Remove(p + ".sig"); err != nil {
				t.Fatal(err)
			}
			return installer
		}, true},
		{"bad raw", func(t *testing.T, p string) []byte {
			if err := os.WriteFile(p+".sig", []byte("bad"), 0o600); err != nil {
				t.Fatal(err)
			}
			return installer
		}, true},
		{"missing ssh", func(t *testing.T, p string) []byte {
			if err := os.Remove(p + ".sshsig"); err != nil {
				t.Fatal(err)
			}
			return installer
		}, true},
		{"bad ssh", func(t *testing.T, p string) []byte {
			if err := os.WriteFile(p+".sshsig", []byte("bad"), 0o600); err != nil {
				t.Fatal(err)
			}
			return installer
		}, true},
		{"ssh signed by wrong key", func(t *testing.T, p string) []byte {
			if err := os.WriteFile(p+".sshsig", wrongSSHSignature, 0o600); err != nil {
				t.Fatal(err)
			}
			return installer
		}, true},
		{"tampered checksums", func(t *testing.T, p string) []byte {
			if err := os.WriteFile(p, []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
			return installer
		}, true},
		{"wrong installer key", func(_ *testing.T, _ string) []byte { return []byte("  RELEASE_SSH_PUBLIC_KEY=\"ssh-ed25519 wrong\"\n") }, true},
		{"missing verifier", func(t *testing.T, _ string) []byte { t.Setenv("PATH", t.TempDir()); return installer }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sums := filepath.Join(t.TempDir(), "checksums.txt")
			if err := os.WriteFile(sums, []byte("checksums\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := signFile(keyPEM, sums, goKey, installer); err != nil {
				t.Fatal(err)
			}
			currentInstaller := installer
			if tc.alter != nil {
				currentInstaller = tc.alter(t, sums)
			}
			err := verifyFile(sums, goKey, currentInstaller)
			if (err != nil) != tc.wantErr {
				t.Fatalf("verifyFile error = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}
