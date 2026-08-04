package httpapi

import (
	"net/http"
	"strings"
)

const currentUserCacheControl = "no-store"

func currentUserResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", currentUserCacheControl)
		appendVary(w.Header(), "Authorization")
		next.ServeHTTP(w, r)
	})
}

func appendVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, token := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
