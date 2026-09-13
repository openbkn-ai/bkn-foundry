// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_authorization

import (
	"testing"

	"ontology-query/common"
)

func TestQueryAuthorizationIsAlwaysEnabledWithAuthentication(t *testing.T) {
	service := NewQueryAuthorizationService(&common.AppSetting{})
	if _, ok := service.(*queryAuthorizationService); !ok {
		t.Fatalf("query authorization service = %T, want enforcing service", service)
	}
}
