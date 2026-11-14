package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// clientError takes a report from the TUI or the web console: one of its
// requests failed or timed out on its side. The daemon never saw a timeout
// that the client ended, so the client says so. It writes one log line and
// tells the gateway, which counts the failure per method and origin
// (protocol.ClientError). The report is answered at once: the log line is
// written before it returns and the gateway frame is sent in the background.
func (lc *localConn) clientError(ctx context.Context, client string, raw json.RawMessage) (any, error) {
	if client != "cli" {
		return nil, errors.New("client_error needs a cli client")
	}
	var e protocol.ClientError
	if json.Unmarshal(raw, &e) != nil {
		return nil, errors.New("bad client_error params")
	}
	d := lc.d
	e.Method = clientErrorText(e.Method)
	e.Screen = clientErrorText(e.Screen)
	e.Err = clientErrorText(e.Err)
	e.Ms, e.TimeoutMs = max(e.Ms, 0), max(e.TimeoutMs, 0)
	if d.log != nil {
		d.log.Printf("event=client_error method=%s ms=%d timeout=%d screen=%s err=%q",
			logToken(e.Method), e.Ms, e.TimeoutMs, logToken(e.Screen), e.Err)
	}
	via := lc.via()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		cctx, cancel := context.WithTimeout(withVia(context.WithoutCancel(ctx), via), 5*time.Second)
		defer cancel()
		// A daemon that is not connected, or a gateway older than this frame,
		// cannot count it. The log line is already written.
		_, _ = d.call(cctx, protocol.FrameClientError, e)
	}()
	return map[string]bool{"ok": true}, nil
}

// clientErrorText makes a client's text safe for one log line: no control
// characters, no runs of blanks, at most ClientErrorMaxText bytes.
func clientErrorText(s string) string {
	return logText(s, protocol.ClientErrorMaxText)
}

// logText makes a client's text safe for one log line: control characters
// become blanks, runs of blanks collapse, and the text is cut to max bytes
// without splitting a UTF-8 sequence.
func logText(s string, max int) string {
	s = cleanLine(s)
	if len(s) > max {
		s = s[:max]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// logToken writes s as it is when it has only plain characters, so ordinary
// names are easy to scan, and quoted otherwise, so no text can end the field.
func logToken(s string) string {
	if s == "" {
		return `""`
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' || c == '/' || c == ':') {
			return strconv.Quote(s)
		}
	}
	return s
}
