package gateway

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

var (
	codeRe     = regexp.MustCompile(`^[A-Z2-7]{8}$`)
	hostNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	portRe     = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// ValidHost checks a Host header before it goes into the install script. It
// must be a host name or IP address with a port, and nothing else.
func ValidHost(h string) bool {
	host, port, err := net.SplitHostPort(h)
	if err != nil || !portRe.MatchString(port) {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		// A zone ("fe80::1%eth0") is not parsed as an IP, so it fails here.
		return true
	}
	return hostNameRe.MatchString(host)
}

// ValidPlatform accepts only the os and arch values the dist dir can hold.
func ValidPlatform(osName, arch string) bool {
	return (osName == "linux" || osName == "darwin") && (arch == "amd64" || arch == "arm64")
}

func (g *Gateway) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(r.PathValue("code"))
	valid := false
	if codeRe.MatchString(code) {
		var err error
		if valid, err = g.store.EnrollCodeValid(code); err != nil {
			g.logLine("error", "where", "install", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	if !valid {
		http.Error(w, "agent-net: this install link is unknown, expired or already used. Run `agent-net invite` again.", http.StatusNotFound)
		return
	}
	if !ValidHost(r.Host) {
		http.Error(w, "bad Host header", http.StatusBadRequest)
		return
	}
	script, err := RenderInstallScript("https://"+r.Host, code, g.fp, g.pin)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript")
	w.Write(script)
}

func (g *Gateway) handleInstallBin(w http.ResponseWriter, r *http.Request) {
	osName, arch := r.PathValue("os"), r.PathValue("arch")
	if !ValidPlatform(osName, arch) {
		http.Error(w, "unsupported platform", http.StatusNotFound)
		return
	}
	f, err := os.Open(filepath.Join(g.opts.DistDir, fmt.Sprintf("agent-net-%s-%s", osName, arch)))
	if err != nil {
		http.Error(w, "no binary for this platform on the gateway (run scripts/dist.sh)", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.Error(w, "no binary for this platform", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "agent-net", st.ModTime(), f)
}

var installTmpl = template.Must(template.New("install").Parse(`#!/bin/sh
# agent-net installer. Downloads the binary from the gateway and joins this machine.
set -eu

URL='{{.URL}}'
CODE='{{.Code}}'
CA='{{.CA}}'
PIN='{{.Pin}}'

die() { echo "agent-net install: $*" >&2; exit 1; }

PEER_FLAG=""
for arg in "$@"; do
  case "$arg" in
    --accept-peer-messages|--no-accept-peer-messages) PEER_FLAG="$arg" ;;
    *) die "unknown option: $arg (usage: install [--accept-peer-messages | --no-accept-peer-messages])" ;;
  esac
done

command -v curl >/dev/null 2>&1 || die "curl is required"
[ -n "${HOME:-}" ] || die "HOME is not set"

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "unsupported operating system: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "unsupported CPU: $(uname -m)" ;;
esac

BIN_DIR="$HOME/.local/bin"
mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"
TMP="$(mktemp "$BIN_DIR/.agent-net.XXXXXX")" || die "cannot write in $BIN_DIR"
trap 'rm -f "$TMP"' EXIT

echo "downloading agent-net for $OS/$ARCH"
curl -fsSL -k --pinnedpubkey "$PIN" -o "$TMP" "$URL/install/bin/$OS/$ARCH" || die "download failed (is the gateway reachable, and does it have a $OS/$ARCH binary?)"
chmod 755 "$TMP"
mv -f "$TMP" "$BIN_DIR/agent-net" || die "cannot install $BIN_DIR/agent-net"
trap - EXIT

"$BIN_DIR/agent-net" setup --join "$URL#$CODE#$CA" ${PEER_FLAG:+"$PEER_FLAG"}
`))

// RenderInstallScript fills the install script template. The values end up
// inside shell single quotes, so anything unusual is refused.
func RenderInstallScript(url, code, caSHA, pin string) ([]byte, error) {
	for _, v := range []string{url, code, caSHA, pin} {
		if v == "" || strings.ContainsAny(v, "'\\\n\r \t$`\"") {
			return nil, fmt.Errorf("unsafe value %q for install script", v)
		}
	}
	var b bytes.Buffer
	err := installTmpl.Execute(&b, map[string]string{"URL": url, "Code": code, "CA": caSHA, "Pin": pin})
	return b.Bytes(), err
}
