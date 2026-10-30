// Package phone serves the embedded owner interface.
package phone

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

//go:embed templates/* static/*
var files embed.FS
var shell = template.Must(template.ParseFS(files, "templates/*.html"))

type source interface {
	Snapshot(context.Context) (operator.Snapshot, error)
	SessionText(context.Context, protocol.TextQuery) (protocol.TextPage, error)
	AnswerHeld(context.Context, protocol.HeldAnswer) error
	ResolveOutcome(context.Context, string, string) error
	SendAsOwner(context.Context, string, string) (protocol.MessageSent, error)
	Call(context.Context, string, any) (json.RawMessage, error)
}
type app struct{ src source }
type page struct {
	Snapshot operator.Snapshot
	Inbox    []protocol.InboxItem
	Agent    *protocol.AgentView
	Text     protocol.TextPage
	Error    string
}

func New(src source) http.Handler {
	a := &app{src: src}
	mux := http.NewServeMux()
	static, _ := fs.Sub(files, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		http.ServeFileFS(w, r, files, "static/manifest.webmanifest")
	})
	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /session/{id}", a.session)
	mux.HandleFunc("GET /events", a.stream)
	mux.HandleFunc("POST /action", a.action)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}
func (a *app) data(ctx context.Context) page {
	var p page
	if a.src == nil {
		return p
	}
	var err error
	p.Snapshot, err = a.src.Snapshot(ctx)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	raw, err := a.src.Call(ctx, "inbox_list", nil)
	if err == nil {
		var v struct {
			Items []protocol.InboxItem `json:"items"`
		}
		if json.Unmarshal(raw, &v) == nil {
			for _, it := range v.Items {
				if it.Status == "open" {
					p.Inbox = append(p.Inbox, it)
				}
			}
		}
	}
	return p
}
func render(w http.ResponseWriter, name string, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := shell.ExecuteTemplate(w, name, p); err != nil {
		return
	}
}
func (a *app) home(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), operator.Timeout)
	defer cancel()
	render(w, "home", a.data(ctx))
}
func (a *app) session(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), operator.Timeout)
	defer cancel()
	p := a.data(ctx)
	for _, v := range p.Snapshot.Agents {
		if v.SessionID == r.PathValue("id") {
			agent := v
			p.Agent = &agent
			break
		}
	}
	if p.Agent == nil {
		http.NotFound(w, r)
		return
	}
	var err error
	p.Text, err = a.src.SessionText(ctx, protocol.TextQuery{SessionID: p.Agent.SessionID})
	if err != nil {
		p.Error = err.Error()
	}
	render(w, "detail", p)
}
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
	id, act := r.PostForm.Get("id"), r.PostForm.Get("action")
	if id == "" || act == "" {
		http.Error(w, "missing action", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), operator.Timeout)
	defer cancel()
	switch r.PostForm.Get("kind") {
	case "held":
		if !strings.Contains("|a|A|r|R|", "|"+act+"|") {
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
	case "send":
		_, err = a.src.SendAsOwner(ctx, id, act)
	default:
		err = fmt.Errorf("invalid action")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := "/"
	if r.PostForm.Get("kind") == "send" {
		target = "/session/" + url.PathEscape(id)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
