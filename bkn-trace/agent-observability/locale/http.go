// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package locale

import (
	"context"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

// LanguageMiddleware resolves Accept-Language once and stores the effective
// locale in the request context.
func LanguageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		language := i18n.ResolveLanguage(r.Header.Get(i18n.AcceptLanguageHeader))
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
	w.Header().Set(i18n.ContentLanguageHeader, string(i18n.GetLanguageByCtx(ctx)))
	addVaryHeader(w.Header(), i18n.AcceptLanguageHeader)
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
