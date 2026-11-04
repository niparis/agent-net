package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveryBackgroundLifecycle(t *testing.T) {
	exit := `{"type":"user","message":{"content":"<command-message>exit</command-message>\n<command-name>/exit</command-name>\n<command-args></command-args>"}}` + "\n"
	for _, tc := range []struct {
		name, status, transcript, want string
		spare                          bool
	}{
		{name: "warm spare", status: "idle", spare: true},
		{name: "shell with live command", status: "shell", want: "shell"},
		{name: "exit with surviving process", status: "shell", transcript: exit, want: "exited"},
		{name: "exit then local output", status: "shell", transcript: exit + `{"type":"user","message":{"content":"<local-command-stdout>Bye</local-command-stdout>"}}` + "\n", want: "exited"},
		{name: "resumed conversation", status: "idle", transcript: exit + `{"type":"user","message":{"content":"continue"}}` + "\n", want: "idle"},
		{name: "quoted exit", status: "shell", transcript: `{"type":"user","message":{"content":"Explain <command-name>/exit</command-name>"}}` + "\n", want: "shell"},
		{name: "sidechain exit", status: "shell", transcript: `{"type":"user","isSidechain":true,"message":{"content":"/exit"}}` + "\n", want: "shell"},
		{name: "explicit ended status", status: "exited", want: "exited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sessions := filepath.Join(dir, "sessions")
			projects := filepath.Join(dir, "projects")
			if err := os.MkdirAll(sessions, 0700); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(projects, transcriptDir("/work/test"))
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(project, "fixture.jsonl")
			if err := os.WriteFile(path, []byte(tc.transcript), 0600); err != nil {
				t.Fatal(err)
			}
			sf := map[string]any{"pid": os.Getpid(), "sessionId": "fixture", "cwd": "/work/test", "status": tc.status, "kind": "bg", "jobId": "abcd1234", "spare": tc.spare, "messagingSocketPath": "/unused.sock"}
			raw, _ := json.Marshal(sf)
			if err := os.WriteFile(filepath.Join(sessions, fmt.Sprintf("%d.json", os.Getpid())), raw, 0600); err != nil {
				t.Fatal(err)
			}
			a := New(Config{SessionsDir: sessions, ProjectsDir: projects, ReplyDir: filepath.Join(dir, "r")})
			h := &fakeHost{}
			a.host = h
			a.poll()
			got := h.current()
			if tc.spare {
				if len(got) != 0 {
					t.Fatalf("spare listed: %+v", got)
				}
				if len(a.byNative) != 0 || len(a.bySock) != 0 {
					t.Fatal("spare routable")
				}
				return
			}
			if len(got) != 1 || got[0].Status != tc.want {
				t.Fatalf("sessions %+v, want status %s", got, tc.want)
			}
			if tc.want == "exited" {
				if a.byNative["fixture"].sock != "" || len(a.bySock) != 0 {
					t.Fatal("exited session still has inbox route")
				}
				// A new turn in the same transcript must clear the cached exit marker.
				if err := os.WriteFile(path, []byte(tc.transcript+`{"type":"user","message":{"content":"continue"}}`+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if tc.status != "exited" {
					a.poll()
					if got := h.current(); len(got) != 1 || got[0].Status != tc.status {
						t.Fatalf("resumed: %+v", got)
					}
				}
			}
		})
	}
}
