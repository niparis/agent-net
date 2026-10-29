package gateway

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newInstallGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	data, dist := t.TempDir(), t.TempDir()
	gw, err := New(Options{DataDir: data, DistDir: dist})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gw.Close() })
	return gw, dist
}

func TestServerKeyStableAcrossRestarts(t *testing.T) {
	data := t.TempDir()
	g1, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	pin1 := g1.Pin()
	g1.Close()
	g2, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()
	if pin1 != g2.Pin() {
		t.Fatalf("pin changed across restart: %s vs %s", pin1, g2.Pin())
	}
	if p, err := ServerPin(data); err != nil || p != pin1 {
		t.Fatalf("ServerPin = %q, %v", p, err)
	}
	st, err := os.Stat(filepath.Join(data, "server-key.pem"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("server-key.pem: %v %v", st, err)
	}
}

func TestPinMatchesCertificate(t *testing.T) {
	gw, _ := newInstallGateway(t)
	cert, err := x509.ParseCertificate(gw.TLSConfig().Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	want := "sha256//" + base64.StdEncoding.EncodeToString(sum[:])
	if gw.Pin() != want {
		t.Fatalf("pin %s, want %s", gw.Pin(), want)
	}
}

func TestCurlAcceptsPinRejectsWrongPin(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("no curl")
	}
	gw, _ := newInstallGateway(t)
	srv := httptest.NewUnstartedServer(gw.Handler())
	srv.TLS = gw.TLSConfig()
	srv.StartTLS()
	defer srv.Close()
	run := func(pin string) error {
		return exec.Command(curl, "-fsS", "-k", "--pinnedpubkey", pin, "-o", os.DevNull, srv.URL+"/v1/ca").Run()
	}
	if err := run(gw.Pin()); err != nil {
		t.Fatalf("curl rejected the right pin: %v", err)
	}
	wrong := "sha256//" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := run(wrong); err == nil {
		t.Fatal("curl accepted a wrong pin")
	}
}

func TestInstallScript(t *testing.T) {
	gw, _ := newInstallGateway(t)
	code, _ := gw.Store().CreateEnrollCode(time.Minute)
	h := gw.Handler()
	get := func(path, host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	w := get("/install/"+code, "10.0.0.5:7420")
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/x-shellscript" {
		t.Fatalf("got %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	for _, want := range []string{"URL='https://10.0.0.5:7420'", "CODE='" + code + "'", "CA='" + gw.Fingerprint() + "'", "PIN='" + gw.Pin() + "'"} {
		if !strings.Contains(body, want) {
			t.Errorf("script lacks %s", want)
		}
	}
	// Fetching the script does not consume the code.
	if w := get("/install/"+code, "10.0.0.5:7420"); w.Code != 200 {
		t.Fatalf("second fetch: %d", w.Code)
	}
	if ok, _ := gw.Store().ConsumeEnrollCode(code); !ok {
		t.Fatal("code was consumed by the install fetch")
	}
	if w := get("/install/"+code, "10.0.0.5:7420"); w.Code != 404 {
		t.Fatalf("used code: %d", w.Code)
	}
	if w := get("/install/ABCDEFGH", "10.0.0.5:7420"); w.Code != 404 {
		t.Fatalf("unknown code: %d", w.Code)
	}
	code2, _ := gw.Store().CreateEnrollCode(time.Minute)
	if w := get("/install/"+code2, "evil';rm -rf ~;'x:7420"); w.Code != 400 {
		t.Fatalf("bad host: %d", w.Code)
	}
}

func TestValidHost(t *testing.T) {
	for _, h := range []string{"192.168.1.5:7420", "research.local:7420", "localhost:1", "[::1]:7420", "[fd7a:115c::1]:7420"} {
		if !ValidHost(h) {
			t.Errorf("%q should be valid", h)
		}
	}
	for _, h := range []string{"", "host", "host:", "host:abc", "a b:7420", "x';id;':7420", "$(id):7420", "-bad:7420", "a`b`:1", "[fe80::1%eth0]:7420", "host:7420/x"} {
		if ValidHost(h) {
			t.Errorf("%q should be invalid", h)
		}
	}
}

func TestInstallBin(t *testing.T) {
	gw, dist := newInstallGateway(t)
	os.WriteFile(filepath.Join(dist, "agent-net-linux-amd64"), []byte("BINARY"), 0o755)
	os.WriteFile(filepath.Join(dist, "agent-net-plan9-amd64"), []byte("NO"), 0o755)
	h := gw.Handler()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	if w := get("/install/bin/linux/amd64"); w.Code != 200 || w.Body.String() != "BINARY" {
		t.Fatalf("linux/amd64: %d %q", w.Code, w.Body.String())
	}
	for _, p := range []string{"/install/bin/linux/arm64", "/install/bin/plan9/amd64", "/install/bin/linux/386", "/install/bin/..%2f/amd64", "/install/bin/linux/amd64%2f..%2f.."} {
		if w := get(p); w.Code != 404 && w.Code != 400 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	for _, c := range [][2]string{{"linux", "amd64"}, {"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "arm64"}} {
		if !ValidPlatform(c[0], c[1]) {
			t.Errorf("%v should be valid", c)
		}
	}
	for _, c := range [][2]string{{"windows", "amd64"}, {"linux", "386"}, {"", ""}, {"../x", "amd64"}} {
		if ValidPlatform(c[0], c[1]) {
			t.Errorf("%v should be invalid", c)
		}
	}
}

func TestInstallScriptSyntax(t *testing.T) {
	script, err := RenderInstallScript("https://10.0.0.5:7420", "ABCD2345", strings.Repeat("a", 64), "sha256//abc+/=")
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range []string{"sh", "dash", "bash"} {
		path, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, "-n")
		cmd.Stdin = strings.NewReader(string(script))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s -n: %v\n%s", sh, err, out)
		}
	}
	if _, err := RenderInstallScript("https://x:1", "A'B", "c", "p"); err == nil {
		t.Error("quote in code was accepted")
	}
	if strings.Contains(string(script), "\xe2\x80\x94") {
		t.Error("em dash in script")
	}
}
