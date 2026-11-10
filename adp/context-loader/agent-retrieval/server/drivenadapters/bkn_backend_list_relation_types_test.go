// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"
)

func TestListRelationTypesBranchReadsOneOrderedWindow(t *testing.T) {
	client, mockHTTP, ctrl := newKNDetailTestClient(t)
	defer ctrl.Finish()

	var gotTarget string
	var gotQuery url.Values
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, target string, query url.Values, _ map[string]string) (int, []byte, error) {
			gotTarget, gotQuery = target, query
			return 200, []byte(`{"entries":[{"id":"rt_b","name":"beta","source_object_type_id":"ot_1","target_object_type_id":"ot_2"}],"total_count":7}`), nil
		}).Times(1)

	page, err := client.ListRelationTypesBranch(context.Background(), "kn1", "release", []string{"ot_1", "ot_2"}, 5, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(gotTarget, "/in/v1/knowledge-networks/kn1/relation-types") {
		t.Fatalf("target = %q, want the relation type list", gotTarget)
	}
	for key, want := range map[string]string{"offset": "5", "limit": "1", "sort": "name", "direction": "asc", "branch": "release"} {
		if got := gotQuery.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	// bound_object_type_id: a relation type that starts or ends at one of them.
	if got := gotQuery["bound_object_type_id"]; !reflect.DeepEqual(got, []string{"ot_1", "ot_2"}) {
		t.Fatalf("bound_object_type_id = %v, want both object types", got)
	}
	if page.TotalCount != 7 || page.Scanned != 1 || page.Entries[0].SourceObjectTypeID != "ot_1" {
		t.Fatalf("page = %+v", page)
	}
}

// main is the backend's default, so neither read names it -- the by-id read on main
// sends exactly the request it always sent.
func TestRelationTypeReadsLeaveMainUnnamed(t *testing.T) {
	client, mockHTTP, ctrl := newKNDetailTestClient(t)
	defer ctrl.Finish()

	var queries []url.Values
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, query url.Values, _ map[string]string) (int, []byte, error) {
			queries = append(queries, query)
			return 200, []byte(`{"entries":[]}`), nil
		}).Times(3)

	if _, err := client.ListRelationTypesBranch(context.Background(), "kn1", "main", nil, 0, 50); err != nil {
		t.Fatal(err)
	}
	_, _ = client.GetRelationTypeDetail(context.Background(), "kn1", []string{"rt_a"}, true)
	_, _ = client.GetRelationTypeDetailBranch(context.Background(), "kn1", "release", []string{"rt_a"}, true)

	if queries[0].Has("branch") || queries[0].Has("bound_object_type_id") {
		t.Fatalf("a main listing of every relation type names neither, got %v", queries[0])
	}
	if queries[1].Has("branch") || queries[1].Get("include_detail") != "true" {
		t.Fatalf("the main by-id read changed: %v", queries[1])
	}
	if queries[2].Get("branch") != "release" {
		t.Fatalf("a branch read must name its branch, got %v", queries[2])
	}
}
