package upgrade

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func sshTestKey(t *testing.T) (string, string) {
	t.Helper()
	key := filepath.Join(t.TempDir(), "key")
	cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(string(pub))
	if len(parts) < 2 {
		t.Fatal("invalid public key")
	}
	return key, parts[1]
}

func sshSign(t *testing.T, key string, sums []byte) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(path, sums, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ssh-keygen", "-Y", "sign", "-f", key, "-n", "agent-net", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen sign: %v: %s", err, out)
	}
	sig, err := os.ReadFile(path + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// testInstaller replaces the key declaration in a private copy of install.sh.
func testInstaller(t *testing.T, pub string) []byte {
	t.Helper()
	installer, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	keyLine := regexp.MustCompile(`(?m)^[ \t]*RELEASE_SSH_PUBLIC_KEY="[^"\n]*"[ \t]*$`)
	if len(keyLine.FindAllIndex(installer, -1)) != 1 {
		t.Fatal("install.sh must contain exactly one release public key declaration")
	}
	return keyLine.ReplaceAll(installer, []byte(`  RELEASE_SSH_PUBLIC_KEY="ssh-ed25519 `+pub+`"`))
}

func TestInstallerSignedRelease(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	key, pub := sshTestKey(t)
	other, _ := sshTestKey(t)
	installer := testInstaller(t, pub)
	script := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(script, installer, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := fakeBinary("v1.2.3")
	sums := []byte(fmt.Sprintf("%s  %s\n", sum(bin), ThisAsset()))
	validSig := sshSign(t, key, sums)
	wrongSig := sshSign(t, other, sums)
	for _, tc := range []struct {
		name           string
		sums, sig, bin []byte
		want           string
	}{
		{"valid", sums, validSig, bin, ""},
		{"missing signature", sums, nil, bin, "signature"},
		{"bad signature", sums, []byte("bad"), bin, "signature"},
		{"missing verifier", sums, validSig, bin, "signature"},
		{"wrong key", sums, wrongSig, bin, "signature"},
		{"tampered checksum", append(append([]byte(nil), sums...), 'x'), validSig, bin, "signature"},
		{"tampered binary", sums, validSig, []byte("tampered"), "checksum mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch strings.TrimPrefix(r.URL.Path, "/download/v1.2.3") {
				case "/checksums.txt":
					w.Write(tc.sums)
				case "/checksums.txt.sshsig":
					if tc.sig == nil {
						http.NotFound(w, r)
					} else {
						w.Write(tc.sig)
					}
				case "/" + ThisAsset():
					hits++
					w.Write(tc.bin)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			home := t.TempDir()
			cmd := exec.Command("sh", script, "--binary-only", "--version", "v1.2.3")
			cmd.Env = append(os.Environ(), "HOME="+home, "AGENT_NET_BASE_URL="+srv.URL, "TMPDIR="+t.TempDir())
			if tc.name == "missing verifier" {
				path := t.TempDir()
				curl, err := exec.LookPath("curl")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(curl, filepath.Join(path, "curl")); err != nil {
					t.Fatal(err)
				}
				cmd.Env = append(cmd.Env, "PATH="+path)
			}
			out, err := cmd.CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("valid release: %v: %s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("want %s error, got %v: %s", tc.want, err, out)
			}
			if tc.want == "signature" && hits != 0 {
				t.Fatal("binary downloaded before signature verified")
			}
			if _, err := os.Stat(filepath.Join(home, ".local/bin/agent-net")); !os.IsNotExist(err) {
				t.Fatal("binary installed on failure")
			}
		})
	}
}

func TestInstallerGatewayCarriesSignedManifest(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	key, pub := sshTestKey(t)
	script := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(script, testInstaller(t, pub), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := fakeBinary("v1.2.3")
	var b strings.Builder
	for _, name := range AllAssets() {
		fmt.Fprintf(&b, "%s  %s\n", sum(bin), name)
	}
	sums := []byte(b.String())
	sshSig := sshSign(t, key, sums)
	rawSig := []byte("raw signature for nodes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/download/v1.2.3") {
		case "/checksums.txt":
			w.Write(sums)
		case "/checksums.txt.sshsig":
			w.Write(sshSig)
		case "/checksums.txt.sig":
			w.Write(rawSig)
		default:
			w.Write(bin)
		}
	}))
	defer srv.Close()
	home := t.TempDir()
	cmd := exec.Command("sh", script, "--gateway", "--version", "v1.2.3")
	cmd.Env = append(os.Environ(), "HOME="+home, "AGENT_NET_BASE_URL="+srv.URL, "TMPDIR="+t.TempDir(), "AGENT_NET_NO_SERVICE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gateway install: %v: %s", err, out)
	}
	for name, want := range map[string][]byte{"checksums.txt": sums, "checksums.txt.sig": rawSig} {
		got, err := os.ReadFile(filepath.Join(home, ".local/share/agent-net/dist", name))
		if err != nil || string(got) != string(want) {
			t.Errorf("dist %s: %q, %v", name, got, err)
		}
	}
}

// The latest tag must be resolved once, before any release assets are fetched.
func TestInstallerPinsLatestAndChecksVersion(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen unavailable")
	}
	key, pub := sshTestKey(t)
	script := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(script, testInstaller(t, pub), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, binVersion string
		wantError        bool
	}{{"matching", "v1.2.3", false}, {"wrong version", "v1.2.4", true}} {
		t.Run(tc.name, func(t *testing.T) {
			bin := fakeBinary(tc.binVersion)
			sums := []byte(fmt.Sprintf("%s  %s\n", sum(bin), ThisAsset()))
			sig := sshSign(t, key, sums)
			var latestHits atomic.Int32
			wrongPath := make(chan string, 10)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/latest/") {
					count := latestHits.Add(1)
					tag := "v1.2.3"
					if count > 1 {
						tag = "v1.2.4"
					}
					http.Redirect(w, r, "/download/"+tag+"/checksums.txt", http.StatusFound)
					return
				}
				switch r.URL.Path {
				case "/download/v1.2.3/checksums.txt":
					w.Write(sums)
				case "/download/v1.2.3/checksums.txt.sshsig":
					w.Write(sig)
				case "/download/v1.2.3/" + ThisAsset():
					w.Write(bin)
				default:
					wrongPath <- r.URL.Path
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			home := t.TempDir()
			cmd := exec.Command("sh", script, "--binary-only")
			cmd.Env = append(os.Environ(), "HOME="+home, "AGENT_NET_BASE_URL="+srv.URL, "TMPDIR="+t.TempDir())
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantError {
				t.Fatalf("install error %v: %s", err, out)
			}
			if latestHits.Load() != 1 {
				t.Errorf("latest requested %d times", latestHits.Load())
			}
			select {
			case path := <-wrongPath:
				t.Errorf("unexpected path %s", path)
			default:
			}
			if tc.wantError {
				if _, err := os.Stat(filepath.Join(home, ".local/bin/agent-net")); !os.IsNotExist(err) {
					t.Fatal("mismatched binary installed")
				}
			}
		})
	}
}

func TestInstallerBadSSHSignatureLeavesGatewayDistAlone(t *testing.T) {
	key, pub := sshTestKey(t)
	_ = key
	script := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(script, testInstaller(t, pub), 0755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			w.Write([]byte("bad manifest"))
		} else {
			w.Write([]byte("bad signature"))
		}
	}))
	defer srv.Close()
	home := t.TempDir()
	dist := filepath.Join(home, ".local/share/agent-net/dist")
	if err := os.MkdirAll(dist, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"checksums.txt", "checksums.txt.sig"} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte("previous"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", script, "--gateway", "--version", "v1.2.3")
	cmd.Env = append(os.Environ(), "HOME="+home, "AGENT_NET_BASE_URL="+srv.URL, "TMPDIR="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "signature") {
		t.Fatalf("bad SSH signature: %v %s", err, out)
	}
	for _, name := range []string{"checksums.txt", "checksums.txt.sig"} {
		b, err := os.ReadFile(filepath.Join(dist, name))
		if err != nil || string(b) != "previous" {
			t.Fatalf("dist %s changed: %q %v", name, b, err)
		}
	}
}
