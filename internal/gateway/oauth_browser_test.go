package gateway

import "net/http"

func oauthWithCookie(h http.Handler, cookie *http.Cookie) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie != nil {
			r.AddCookie(cookie)
		}
		h.ServeHTTP(w, r)
	})
}
