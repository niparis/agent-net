// Package phone serves the embedded owner interface.
package phone

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/* static/*
var files embed.FS

var shell = template.Must(template.ParseFS(files, "templates/*.html"))

func New(_ any) http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(files, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		http.ServeFileFS(w, r, files, "static/manifest.webmanifest")
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { shell.ExecuteTemplate(w, "shell", nil) })
	return mux
}
