// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_authorization

import (
	"context"
	"reflect"
	"testing"

	"ontology-query/common"
	"ontology-query/interfaces"
)

func TestQueryAuthorizationIsAlwaysEnabledWithAuthentication(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")

	service := NewQueryAuthorizationService(&common.AppSetting{})
	if _, ok := service.(*queryAuthorizationService); !ok {
		t.Fatalf("query authorization service = %T, want enforcing service", service)
	}
}

func TestQueryAuthorizationBypassesOnlyWhenAuthenticationIsDisabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")

	service := NewQueryAuthorizationService(&common.AppSetting{})
	query := &interfaces.SubGraphQueryBaseOnTypePath{
		KNID:   "kn-a",
		Branch: "historical-branch",
		Paths: interfaces.QueryRelationTypePaths{
			TypePaths: []interfaces.QueryRelationTypePath{{}},
		},
	}
	want := *query
	want.Paths.TypePaths = append([]interfaces.QueryRelationTypePath(nil), query.Paths.TypePaths...)
	if err := service.AuthorizeSubgraphByTypePath(context.Background(), query); err != nil {
		t.Fatalf("authentication-disabled query authorization returned error: %v", err)
	}
	if !reflect.DeepEqual(*query, want) {
		t.Fatalf("authentication-disabled authorization mutated query: got %#v, want %#v", *query, want)
	}
}
