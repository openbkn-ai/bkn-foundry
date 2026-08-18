// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"context"
	"net/http"
	"strings"

	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// LanguageMiddleware resolves Accept-Language once and stores the effective
// locale in the request context.
func LanguageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		language := sharedrest.ResolveLanguage(r.Header.Get(sharedrest.AcceptLanguageHeader))
		ctx := WithLanguage(r.Context(), language)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// PrivateNoCacheForPrefixes applies the authenticated API cache contract while
// leaving paths outside the API prefixes untouched.
func PrivateNoCacheForPrefixes(next http.Handler, prefixes ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, prefix := range prefixes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				w.Header().Set("Cache-Control", "private, no-cache")
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}

// MarkLocalizedResponse declares the language of a representation containing
// localized human-readable text.
func MarkLocalizedResponse(w http.ResponseWriter, ctx context.Context) {
	w.Header().Set(sharedrest.ContentLanguageHeader, sharedrest.GetLanguageByCtx(ctx))
	addVaryHeader(w.Header(), sharedrest.AcceptLanguageHeader)
}

func addVaryHeader(header http.Header, value string) {
	for _, headerValue := range header.Values("Vary") {
		for _, token := range strings.Split(headerValue, ",") {
			if token = strings.TrimSpace(token); token == "*" || strings.EqualFold(token, value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
