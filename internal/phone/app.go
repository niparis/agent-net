// Package phone serves the embedded owner web console. The same pages serve
// the desktop browser and the phone (decision 0119).
package phone

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

//go:embed templates/* static/*
var files embed.FS
var shell = template.Must(template.ParseFS(files, "templates/*.html"))

type source interface {
	Snapshot(context.Context) (operator.Snapshot, error)
	AnswerHeld(context.Context, protocol.HeldAnswer) error
	ResolveOutcome(context.Context, string, string) error
	SendAsOwner(context.Context, string, string) (protocol.MessageSent, error)
	Call(context.Context, string, any) (json.RawMessage, error)
}
type app struct {
	src      source
	interval time.Duration
}

// New returns the console handler. Every route here sits behind the gateway's
// phone pairing (phoneRoute in internal/gateway/phone_auth.go). The console
// reads and answers: it has no route that starts, stops or changes anything.
func New(src source) http.Handler {
	a := &app{src: src}
	mux := http.NewServeMux()
	static, _ := fs.Sub(files, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		http.ServeFileFS(w, r, files, "static/manifest.webmanifest")
	})
	mux.HandleFunc("GET /{$}", a.screen("desk"))
	for _, s := range screens[1:] {
		mux.HandleFunc("GET "+s.Path, a.screen(s.ID))
	}
	// The phone app of the first version linked to /session/{id}.
	mux.HandleFunc("GET /session/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/sessions?s="+url.QueryEscape(r.PathValue("id")), http.StatusSeeOther)
	})
	mux.HandleFunc("GET /events", a.stream)
	mux.HandleFunc("POST /action", a.action)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

// screen serves one full page.
func (a *app) screen(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), operator.Timeout)
		defer cancel()
		p := a.load(ctx, id, query{Selected: r.URL.Query().Get("s"), Filter: r.URL.Query().Get("f")})
		var b bytes.Buffer
		if err := shell.ExecuteTemplate(&b, "page", p); err != nil {
			http.Error(w, "page error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b.Bytes())
	}
}

// action answers one ask or writes one reply. Only these kinds exist: an answer
// to something that waits for the owner, or the owner's own words in a thread.
// Never a start, a stop, a step of a change or a change of settings.
func (a *app) action(w http.ResponseWriter, r *http.Request) {
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host != r.Host || (origin.Scheme != "https" && origin.Scheme != "http") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, protocol.MaxBodyBytes+4096)
	if err = r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if a.src == nil {
		http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
		return
	}
	id, act := r.PostForm.Get("id"), strings.TrimSpace(r.PostForm.Get("action"))
	if id == "" || act == "" {
		http.Error(w, "missing action", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), operator.Timeout)
	defer cancel()
	back := "/"
	switch r.PostForm.Get("kind") {
	case "held":
		if act != "a" && act != "A" && act != "r" && act != "R" {
			err = fmt.Errorf("invalid action")
		} else {
			err = a.src.AnswerHeld(ctx, protocol.HeldAnswer{MessageID: id, Action: act})
		}
	case "outcome":
		if act != "let_stand" && act != "overrule" {
			err = fmt.Errorf("invalid action")
		} else {
			err = a.src.ResolveOutcome(ctx, id, act)
		}
	case "inbox":
		_, err = a.src.Call(ctx, "inbox_answer", map[string]string{"id": id, "answer": act})
		// The gateway closed the ask meanwhile: it is gone, which is what the
		// owner wanted.
		if err != nil && err.Error() == "inbox item not found" {
			err = nil
		}
	case "mark":
		if act != protocol.MarkDidntNeedMe {
			err = fmt.Errorf("invalid action")
		} else {
			_, err = a.src.Call(ctx, protocol.OpAskMark, protocol.AskMarkRequest{Ask: id, Mark: act})
		}
	case "send":
		_, err = a.src.SendAsOwner(ctx, id, act)
	case "reply":
		// A written reply of the owner into a conversation of the Messages
		// screen (conversation id, recipient session, text).
		var conversation string
		conversation, err = a.reply(ctx, id, r.PostForm.Get("to"), act)
		back = "/messages?s=" + url.QueryEscape(conversation)
	default:
		err = fmt.Errorf("invalid action")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
