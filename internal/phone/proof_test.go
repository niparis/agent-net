package phone

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// proofPage inlines the console's own CSS and scripts so a headless browser
// renders the page without the gateway.
func proofPage(t *testing.T, body string) string {
	t.Helper()
	css, err := os.ReadFile("static/console.css")
	if err != nil {
		t.Fatal(err)
	}
	body = strings.Replace(body, `<link rel="stylesheet" href="/static/console.css">`, "<style>"+string(css)+"</style>", 1)
	for _, name := range []string{"htmx.min.js", "sse.js", "console.js"} {
		body = strings.Replace(body, `<script src="/static/`+name+`" defer></script>`, "", 1)
	}
	return body
}

// proofScreenshot captures one page at a fixed window size and returns the
// browser's log, which is the errors the capture saw.
func proofScreenshot(t *testing.T, chrome, page string, width, height int, shot string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(file, []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(chrome, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--disable-background-networking", "--host-resolver-rules=MAP * ~NOTFOUND", "--user-data-dir="+filepath.Join(t.TempDir(), "chrome"), "--lang=en-US", "--force-device-scale-factor=1", "--window-size="+strconv.Itoa(width)+","+strconv.Itoa(height), "--screenshot="+shot, "file://"+file)
	chromeHome := t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+chromeHome, "XDG_CONFIG_HOME="+filepath.Join(chromeHome, "config"), "XDG_CACHE_HOME="+filepath.Join(chromeHome, "cache"), "XDG_DATA_HOME="+filepath.Join(chromeHome, "data"))
	log, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("capture %dx%d: %v\n%s", width, height, err, log)
	}
	if _, err := os.Stat(shot); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(log))
}

// TestDeskWebProofScenario captures the Desk at phone and desktop width. It
// proves the page lists Needs you and the Log only.
func TestDeskWebProofScenario(t *testing.T) {
	out := os.Getenv("AGENT_NET_PROOF_DIR")
	if out == "" {
		t.Skip("set AGENT_NET_PROOF_DIR to capture the web Desk")
	}
	chrome := os.Getenv("AGENT_NET_PROOF_CHROME")
	if chrome == "" {
		t.Fatal("set AGENT_NET_PROOF_CHROME to a Chromium executable")
	}
	body := page1(t, newConsoleOperator(), "/")
	mustContain(t, "desk proof", body, "Needs you", "Log")
	mustNotContain(t, "desk proof", body, "In flight", "Handled for you")
	page := proofPage(t, body)
	if err := os.WriteFile(filepath.Join(out, "desk.html"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name, step, action string
		width, height      int
	}{
		{"phone", "open", "open the web Desk at phone width", 390, 844},
		{"desktop", "resize", "widen the same page to desktop width", 1280, 900},
	} {
		shot := filepath.Join(out, "desk-"+size.name+".png")
		log := proofScreenshot(t, chrome, page, size.width, size.height, shot)
		meta := fmt.Sprintf("build: %s\nsize: %dx%d\nstep: %s\naction: %s\nexpected: Needs you and Log only, no In flight and no Handled for you\nerrors: %s\n", os.Getenv("AGENT_NET_PROOF_BUILD"), size.width, size.height, size.step, size.action, log)
		if err := os.WriteFile(strings.TrimSuffix(shot, ".png")+".txt", []byte(meta), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
