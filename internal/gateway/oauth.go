package gateway

import (
	"net/http"
	"strings"
)

func (g *Gateway) registerOAuth(mux *http.ServeMux) {}

func protectedResourceMetadataURL(base string) string {
	return strings.TrimRight(base, "/") + "/.well-known/oauth-protected-resource"
}
