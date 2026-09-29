// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// publicHostPattern accepts a bare host name, IPv4 or bracketed IPv6 address
// with an optional port. Anything else (a path, credentials, whitespace) is
// refused, so a forged header cannot smuggle extra URL parts into a link.
var publicHostPattern = regexp.MustCompile(`^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+)(:[0-9]{1,5})?$`)

// PublicOriginFromRequest returns the scheme://host a caller used to reach
// this service, or "" when the request did not come through a proxy.
//
// Only a request that carries X-Forwarded-Host is trusted to have a browsable
// host: the ingress sets it, while an in-cluster caller (bkn-agent, the
// sandbox calling back) dials the service name, which a user's browser cannot
// open. X-Forwarded-Proto alone is not enough, since a sidecar or internal
// gateway may add it in front of a service-name Host. Such callers get
// relative links instead.
func PublicOriginFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	forwardedHost := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	forwardedProto := strings.ToLower(firstHeaderValue(r.Header.Get("X-Forwarded-Proto")))
	if forwardedHost == "" {
		return ""
	}
	host := forwardedHost
	scheme := forwardedProto
	if scheme == "" {
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	if (scheme != "http" && scheme != "https") || !publicHostPattern.MatchString(host) {
		return ""
	}
	return scheme + "://" + host
}

// firstHeaderValue takes the client-most entry of a comma-separated proxy header.
func firstHeaderValue(value string) string {
	if i := strings.IndexByte(value, ','); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value)
}

// SetPublicOriginToCtx records the caller's public origin; an empty origin is not stored.
func SetPublicOriginToCtx(ctx context.Context, origin string) context.Context {
	if origin == "" {
		return ctx
	}
	return context.WithValue(ctx, interfaces.KeyPublicOrigin, origin)
}

// GetPublicOriginFromCtx returns the caller's public origin, or "" when unknown.
func GetPublicOriginFromCtx(ctx context.Context) string {
	origin, _ := ctx.Value(interfaces.KeyPublicOrigin).(string)
	return origin
}
