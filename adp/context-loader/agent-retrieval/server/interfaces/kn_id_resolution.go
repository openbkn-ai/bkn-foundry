// Copyright openbkn.ai
//
// Licensed under the OpenBKN License.
// See the LICENSE file in the project root for details.

package interfaces

import "strings"

// ResolveKnID picks the knowledge network a call addresses. The explicit
// argument wins and the X-Kn-ID header is the fallback, so a client can
// configure one network per connection and still address another one per call.
//
// The order matters beyond convenience. The managed guard, the REST lifecycle
// middleware and the kn tool routes all read the argument first, and the guard
// is what derives the business refs a receipt records. When retrieval read the
// header first instead, a call that carried both - a host with a configured
// header, a model passing kn_id - was recorded against one network and executed
// against the other.
func ResolveKnID(argument, header string) string {
	if value := strings.TrimSpace(argument); value != "" {
		return value
	}
	return strings.TrimSpace(header)
}

// ResolvedKnID is the network this search_instance call addresses.
func (r *SearchInstanceReq) ResolvedKnID() string {
	if r == nil {
		return ""
	}
	return ResolveKnID(r.KnID, r.XKnID)
}

// ResolvedKnID is the network this search_schema call addresses.
func (r *SearchSchemaReq) ResolvedKnID() string {
	if r == nil {
		return ""
	}
	return ResolveKnID(r.KnID, r.XKnID)
}
