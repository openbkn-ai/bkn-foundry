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

func TestQueryDataPEPDefaultsToDisabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv(queryDataPEPEnabledEnv, "")

	service := NewQueryAuthorizationService(&common.AppSetting{})
	if _, ok := service.(*noopQueryAuthorizationService); !ok {
		t.Fatalf("query authorization service = %T, want no-op", service)
	}

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
		t.Fatalf("disabled query_data PEP returned error: %v", err)
	}
	if !reflect.DeepEqual(*query, want) {
		t.Fatalf("disabled query_data PEP mutated query: got %#v, want %#v", *query, want)
	}
}

func TestQueryDataPEPEnabledUsesAuthorizationService(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv(queryDataPEPEnabledEnv, "1")

	service := NewQueryAuthorizationService(&common.AppSetting{})
	if _, ok := service.(*queryAuthorizationService); !ok {
		t.Fatalf("query authorization service = %T, want enforcing service", service)
	}
}

func TestQueryDataPEPIsDisabledWhenAuthenticationIsDisabled(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv(queryDataPEPEnabledEnv, "true")

	service := NewQueryAuthorizationService(&common.AppSetting{})
	if _, ok := service.(*noopQueryAuthorizationService); !ok {
		t.Fatalf("query authorization service = %T, want no-op", service)
	}
}
