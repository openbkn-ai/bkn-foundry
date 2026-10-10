// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

// PermissionRequestScope names the kind of access a caller can ask for. The
// values are the bkn-safe permission-request proposal kinds, so a client can
// pass them to Studio or bkn-safe without translation.
type PermissionRequestScope string

const (
	// PermissionScopeGrant is a missing base operation such as query_data.
	PermissionScopeGrant PermissionRequestScope = "grant"
	// PermissionScopeRowFilter means the caller's row filter narrowed the result.
	PermissionScopeRowFilter PermissionRequestScope = "row_filter"
	// PermissionScopePropertyGrants means some properties are not readable in raw form.
	PermissionScopePropertyGrants PermissionRequestScope = "property_grants"
)

// PermissionGuidanceResource identifies the object type a shortfall is about.
// Name is set only when the caller may view the object type's definition, so
// the guidance never reveals more than the caller could already read.
type PermissionGuidanceResource struct {
	Type string `json:"type" toon:"type"`
	ID   string `json:"id" toon:"id"`
	KnID string `json:"kn_id" toon:"kn_id"`
	OtID string `json:"ot_id" toon:"ot_id"`
	Name string `json:"name,omitempty" toon:"name,omitempty"`
}

// PermissionShortfall is one access limit that affected a tool call.
//
// RequestPermissionURL is absolute when the caller reached this service
// through the ingress, and a path on the Studio host otherwise (an in-cluster
// caller knows the public address, this service does not). It is empty when
// the caller cannot act on it in Studio and has to ask an administrator.
type PermissionShortfall struct {
	Scope                PermissionRequestScope `json:"scope" toon:"scope"`
	Operations           []string               `json:"operations,omitempty" toon:"operations,omitempty"`
	Properties           []string               `json:"properties,omitempty" toon:"properties,omitempty"`
	RequestPermissionURL string                 `json:"request_permission_url,omitempty" toon:"request_permission_url,omitempty"`
}

// PermissionGuidance tells an agent which access limits shaped a tool result
// and where the user can request more access. It is attached to a refused call
// and to a successful call whose result was masked or narrowed.
type PermissionGuidance struct {
	// Message is a localized sentence for the user: what was withheld and the
	// next step (request and wait for approval, or ask an administrator).
	Message    string                     `json:"message" toon:"message"`
	Resource   PermissionGuidanceResource `json:"resource" toon:"resource"`
	Shortfalls []PermissionShortfall      `json:"shortfalls" toon:"shortfalls"`
}
