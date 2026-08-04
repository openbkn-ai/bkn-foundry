// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package connector_type

import (
	"strings"

	extconn "github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/extension/connector"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
)

// extensionConnectorType returns the catalogue record for a connector the
// enterprise code line contributed, or nil when there is none the licence in
// force covers.
//
// Nil is also the answer for a paid connector this build carries but is not
// licensed for. The caller turns that into the same 404 an unknown type gets,
// because an under-licensed enterprise image has to be indistinguishable from a
// community one — see the extension/connector package comment.
func extensionConnectorType(tp string) *interfaces.ConnectorType {
	if !extconn.Allowed(tp) {
		return nil
	}
	for _, ct := range extconn.LicensedTypes() {
		if ct.Type == tp {
			return ct
		}
	}
	return nil
}

// licensedExtensionTypes returns the licensed extension records that match the
// same filters the database query was given.
//
// The filters are re-applied here rather than shared with the access layer
// because that layer speaks SQL and these records never touch the database.
// Keeping the two in step matters: a caller filtering by category should not
// get a paid connector of a different category simply because it arrived by a
// different route.
func licensedExtensionTypes(params interfaces.ConnectorTypesQueryParams) []*interfaces.ConnectorType {
	all := extconn.LicensedTypes()
	out := make([]*interfaces.ConnectorType, 0, len(all))
	for _, ct := range all {
		if matchesConnectorTypeFilters(ct, params) {
			out = append(out, ct)
		}
	}
	return out
}

// matchesConnectorTypeFilters mirrors the WHERE clause the access layer builds.
func matchesConnectorTypeFilters(ct *interfaces.ConnectorType, params interfaces.ConnectorTypesQueryParams) bool {
	if params.Mode != "" && ct.Mode != params.Mode {
		return false
	}
	if params.Category != "" && ct.Category != params.Category {
		return false
	}
	if params.Name != "" && !strings.Contains(strings.ToLower(ct.Name), strings.ToLower(params.Name)) {
		return false
	}
	if params.Enabled != nil && ct.Enabled != *params.Enabled {
		return false
	}
	if params.Tag != "" {
		found := false
		for _, tag := range ct.Tags {
			if tag == params.Tag {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
