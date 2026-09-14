// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"

	"bkn-backend/interfaces"
	bmock "bkn-backend/interfaces/mock"
)

// pathModel is network kn1 as the relation type path search reads it. relationTypes are the main
// branch's; strayRows are rows the neighbor query also returns that the main branch does not
// define, as another branch's relation type of the same id would be.
type pathModel struct {
	relationTypes []*interfaces.RelationType
	strayRows     []*interfaces.RelationType
	groups        map[string][]string
}

func pathRelation(id, source, target string) *interfaces.RelationType {
	return &interfaces.RelationType{RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
		RTID: id, RTName: id, SourceObjectTypeID: source, TargetObjectTypeID: target}}
}

// fullPathModel is the network the tests search. With partialGrants the caller sees:
//
//	order   --rel_order_user-->     user      visible
//	user    --rel_user_address-->   address   visible
//	address --rel_address_region--> region    hidden: nothing on region
//	order   --rel_order_item-->     item      hidden: nothing on item
//	item    --rel_item_supplier-->  supplier  hidden: nothing on item, though supplier is visible
//	order   --rel_order_payment-->  payment   hidden: nothing on the relation type
//	order   --rel_order_supplier--> supplier  hidden: query_data alone on the relation type
//	user    --rel_user_invoice-->   invoice   hidden: the relation type is readable, but invoice is
//	                                          granted query_data alone and a node carries its definition
func fullPathModel() pathModel {
	return pathModel{
		relationTypes: []*interfaces.RelationType{
			pathRelation("rel_order_user", "order", "user"),
			pathRelation("rel_user_address", "user", "address"),
			pathRelation("rel_address_region", "address", "region"),
			pathRelation("rel_order_item", "order", "item"),
			pathRelation("rel_item_supplier", "item", "supplier"),
			pathRelation("rel_order_payment", "order", "payment"),
			pathRelation("rel_order_supplier", "order", "supplier"),
			pathRelation("rel_user_invoice", "user", "invoice"),
		},
		groups: map[string][]string{
			"cg_sales":  {"order", "user"},
			"cg_hidden": {"order", "user", "address"},
		},
	}
}

// visiblePathModel is fullPathModel with everything partialGrants hides taken out.
func visiblePathModel() pathModel {
	model := fullPathModel()
	model.relationTypes = model.relationTypes[:2]
	return model
}

var partialGrants = map[string]map[string][]string{
	interfaces.RESOURCE_TYPE_OBJECT_TYPE: {
		"order":    {interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_QUERY_DATA},
		"user":     {interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_QUERY_DATA},
		"address":  {interfaces.OPERATION_TYPE_VIEW_DETAIL},
		"payment":  {interfaces.OPERATION_TYPE_VIEW_DETAIL},
		"supplier": {interfaces.OPERATION_TYPE_VIEW_DETAIL},
		"invoice":  {interfaces.OPERATION_TYPE_QUERY_DATA},
	},
	interfaces.RESOURCE_TYPE_RELATION_TYPE: {
		"rel_order_user":     {interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_QUERY_DATA},
		"rel_user_address":   {interfaces.OPERATION_TYPE_VIEW_DETAIL},
		"rel_address_region": {interfaces.OPERATION_TYPE_VIEW_DETAIL},
		"rel_order_item":     {interfaces.OPERATION_TYPE_VIEW_DETAIL},
		"rel_item_supplier":  {interfaces.OPERATION_TYPE_VIEW_DETAIL},
		"rel_order_supplier": {interfaces.OPERATION_TYPE_QUERY_DATA},
		"rel_user_invoice":   {interfaces.OPERATION_TYPE_VIEW_DETAIL},
	},
	interfaces.RESOURCE_TYPE_CONCEPT_GROUP: {
		"cg_sales": {interfaces.OPERATION_TYPE_VIEW_DETAIL},
	},
}

// neighbors answers the neighbor query the way the SQL does: one-hop paths from each node in the
// given direction, limited to relation types with both ends in the concept groups when any are
// given.
func (m pathModel) neighbors(_ context.Context, otIDs []string,
	query interfaces.RelationTypePathsBaseOnSource) (map[string][]interfaces.RelationTypePath, error) {

	inBatch := map[string]bool{}
	for _, id := range otIDs {
		inBatch[id] = true
	}
	members := map[string]bool{}
	for _, group := range query.ConceptGroups {
		for _, id := range m.groups[group] {
			members[id] = true
		}
	}
	rows := append(append([]*interfaces.RelationType{}, m.relationTypes...), m.strayRows...)
	result := map[string][]interfaces.RelationTypePath{}
	add := func(direction, from, to string, relationType *interfaces.RelationType) {
		if !inBatch[from] || (len(query.ConceptGroups) > 0 &&
			(!members[relationType.SourceObjectTypeID] || !members[relationType.TargetObjectTypeID])) {
			return
		}
		result[from] = append(result[from], interfaces.RelationTypePath{
			ObjectTypes: []interfaces.ObjectTypeWithKeyField{{OTID: from}, {OTID: to, OTName: to}},
			TypeEdges: []interfaces.TypeEdge{{
				RelationTypeId:      relationType.RTID,
				RelationType:        relationType.RelationTypeWithKeyField,
				SourceObjectTypeId:  from,
				Target_ObjectTypeId: to,
				Direction:           direction,
			}},
			Length: 1,
		})
	}
	if query.Direction != interfaces.DIRECTION_BACKWARD {
		for _, relationType := range rows {
			add(interfaces.DIRECTION_FORWARD, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID, relationType)
		}
	}
	if query.Direction != interfaces.DIRECTION_FORWARD {
		for _, relationType := range rows {
			add(interfaces.DIRECTION_BACKWARD, relationType.TargetObjectTypeID, relationType.SourceObjectTypeID, relationType)
		}
	}
	return result, nil
}

type pathFixture struct {
	service *knowledgeNetworkService
	model   pathModel
	kna     *bmock.MockKNAccess
	rta     *bmock.MockRelationTypeAccess
	ots     *bmock.MockObjectTypeService
	ps      *bmock.MockPermissionService
}

func newPathFixture(t *testing.T, model pathModel) pathFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := pathFixture{
		model: model,
		kna:   bmock.NewMockKNAccess(ctrl),
		rta:   bmock.NewMockRelationTypeAccess(ctrl),
		ots:   bmock.NewMockObjectTypeService(ctrl),
		ps:    bmock.NewMockPermissionService(ctrl),
	}
	f.service = &knowledgeNetworkService{kna: f.kna, rta: f.rta, ots: f.ots, ps: f.ps}
	return f
}

// expectTopologyReads lets the search read the model. Tests that must not reach it leave it out.
func (f pathFixture) expectTopologyReads() {
	f.kna.EXPECT().GetNeighborPathsBatch(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(f.model.neighbors).AnyTimes()
	f.ots.EXPECT().GetObjectTypeByID(gomock.Any(), gomock.Any(), "kn1", interfaces.MAIN_BRANCH, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, _, _, id string) (*interfaces.ObjectType, error) {
			return &interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id, OTName: id}}, nil
		}).AnyTimes()
}

func (f pathFixture) expectRelationTypeList() {
	f.rta.EXPECT().ListRelationTypes(gomock.Any(), interfaces.RelationTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      "kn1",
		Branch:                    interfaces.MAIN_BRANCH,
	}).Return(f.model.relationTypes, nil).AnyTimes()
}

func (f pathFixture) asFullCaller() {
	f.ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN, ID: "kn1",
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(nil)
}

// asPartialCaller refuses the network check and answers child checks from grants. A failure,
// when set, fails the failure.call-th check of failure.resourceType instead.
func (f pathFixture) asPartialCaller(grants map[string]map[string][]string, failure authzFailure) {
	f.ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN, ID: "kn1",
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).
		Return(rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden))
	calls := map[string]int{}
	f.ps.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, resourceType string, ids, visibility []string, _ bool,
			candidates []string) (map[string]interfaces.PermissionResourceOps, error) {
			calls[resourceType]++
			if resourceType == failure.resourceType && calls[resourceType] == failure.call {
				return nil, failure.err
			}
			matched := map[string]interfaces.PermissionResourceOps{}
			for _, id := range ids {
				granted := grants[resourceType][strings.TrimPrefix(id, "kn1/")]
				if !holdsAll(granted, visibility) {
					continue
				}
				if ops := intersect(granted, candidates); len(ops) > 0 {
					matched[id] = interfaces.PermissionResourceOps{ResourceID: id, Operations: ops}
				}
			}
			return matched, nil
		}).AnyTimes()
}

type authzFailure struct {
	resourceType string
	call         int
	err          error
}

func holdsAll(granted, required []string) bool {
	for _, op := range required {
		if !slices.Contains(granted, op) {
			return false
		}
	}
	return true
}

func intersect(granted, candidates []string) []string {
	ops := []string{}
	for _, op := range candidates {
		if slices.Contains(granted, op) {
			ops = append(ops, op)
		}
	}
	return ops
}

func pathQuery(source, direction string, length int, groups ...string) interfaces.RelationTypePathsBaseOnSource {
	return interfaces.RelationTypePathsBaseOnSource{
		KNID:              "kn1",
		Branch:            interfaces.MAIN_BRANCH,
		SourceObjecTypeId: source,
		Direction:         direction,
		PathLength:        length,
		ConceptGroups:     groups,
	}
}

// signatures renders paths as "order -rel_order_user-> user", a backward hop as
// "user <rel_order_user- order".
func signatures(paths []interfaces.RelationTypePath) []string {
	rendered := make([]string, 0, len(paths))
	for _, path := range paths {
		var b strings.Builder
		b.WriteString(path.ObjectTypes[0].OTID)
		for i, edge := range path.TypeEdges {
			if edge.Direction == interfaces.DIRECTION_BACKWARD {
				fmt.Fprintf(&b, " <%s- %s", edge.RelationTypeId, path.ObjectTypes[i+1].OTID)
			} else {
				fmt.Fprintf(&b, " -%s-> %s", edge.RelationTypeId, path.ObjectTypes[i+1].OTID)
			}
		}
		rendered = append(rendered, b.String())
	}
	return rendered
}

// namedIDs is every relation and object type id the paths carry, however deep.
func namedIDs(paths []interfaces.RelationTypePath) map[string]struct{} {
	ids := map[string]struct{}{}
	for _, path := range paths {
		for _, objectType := range path.ObjectTypes {
			ids[objectType.OTID] = struct{}{}
		}
		for _, edge := range path.TypeEdges {
			for _, id := range []string{edge.RelationTypeId, edge.SourceObjectTypeId, edge.Target_ObjectTypeId,
				edge.RelationType.RTID, edge.RelationType.SourceObjectTypeID, edge.RelationType.TargetObjectTypeID} {
				ids[id] = struct{}{}
			}
		}
	}
	return ids
}

func assertForbidden(t *testing.T, paths []interfaces.RelationTypePath, err error) {
	t.Helper()
	var httpErr *rest.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusForbidden ||
		httpErr.BaseError.ErrorCode != rest.PublicError_Forbidden {
		t.Fatalf("GetRelationTypePaths() error = %v, want 403 %s", err, rest.PublicError_Forbidden)
	}
	if paths != nil {
		t.Fatalf("GetRelationTypePaths() = %v alongside a refusal", signatures(paths))
	}
}

// TestRelationTypePaths_PartialCallerGetsOnlyFullyVisiblePaths is #1553: a caller granted object
// and relation types but not the network explores from a source it holds view_detail on instead of
// being refused, and nothing outside its scope leaves the service.
func TestRelationTypePaths_PartialCallerGetsOnlyFullyVisiblePaths(t *testing.T) {
	f := newPathFixture(t, fullPathModel())
	f.asPartialCaller(partialGrants, authzFailure{})
	f.expectTopologyReads()
	f.expectRelationTypeList()

	paths, err := f.service.GetRelationTypePaths(context.Background(), pathQuery("order", interfaces.DIRECTION_FORWARD, 3))

	if err != nil {
		t.Fatalf("GetRelationTypePaths() error = %v", err)
	}
	if got, want := signatures(paths), []string{"order -rel_order_user-> user -rel_user_address-> address"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GetRelationTypePaths() = %v, want %v", got, want)
	}
	for _, hidden := range []string{"region", "item", "invoice", "rel_address_region", "rel_order_item",
		"rel_item_supplier", "rel_order_payment", "rel_order_supplier", "rel_user_invoice"} {
		if _, ok := namedIDs(paths)[hidden]; ok {
			t.Fatalf("GetRelationTypePaths() names hidden %s", hidden)
		}
	}
}

// TestRelationTypePaths_HiddenRelationTypeDropsThePath: a visible object type reached only through
// a relation type the caller may not read is not reached -- whether the caller holds nothing on
// the relation type or only query_data, which does not make its definition readable.
func TestRelationTypePaths_HiddenRelationTypeDropsThePath(t *testing.T) {
	f := newPathFixture(t, fullPathModel())
	f.asPartialCaller(partialGrants, authzFailure{})
	f.expectTopologyReads()
	f.expectRelationTypeList()

	paths, err := f.service.GetRelationTypePaths(context.Background(), pathQuery("order", interfaces.DIRECTION_FORWARD, 1))

	if err != nil {
		t.Fatalf("GetRelationTypePaths() error = %v", err)
	}
	if got, want := signatures(paths), []string{"order -rel_order_user-> user"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GetRelationTypePaths() = %v, want %v: payment and supplier sit behind unreadable relation types", got, want)
	}
}

// TestRelationTypePaths_HiddenObjectTypeDropsThePath: a path through an object type the caller
// holds nothing on is dropped as a whole -- no prefix up to it, nothing behind it -- and a path
// whose next hop is hidden ends where the visible model ends.
func TestRelationTypePaths_HiddenObjectTypeDropsThePath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model pathModel
		query interfaces.RelationTypePathsBaseOnSource
		want  []string
	}{
		{
			name:  "hidden intermediate object type",
			model: fullPathModel(),
			query: pathQuery("order", interfaces.DIRECTION_FORWARD, 2),
			want:  []string{"order -rel_order_user-> user -rel_user_address-> address"},
		},
		{
			name:  "hidden object type after the last visible hop",
			model: fullPathModel(),
			query: pathQuery("user", interfaces.DIRECTION_FORWARD, 3),
			want:  []string{"user -rel_user_address-> address"},
		},
		{
			name: "row of a readable relation type id pointing at a hidden object type",
			model: func() pathModel {
				model := fullPathModel()
				model.strayRows = []*interfaces.RelationType{pathRelation("rel_order_user", "order", "item")}
				return model
			}(),
			query: pathQuery("order", interfaces.DIRECTION_FORWARD, 1),
			want:  []string{"order -rel_order_user-> user"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPathFixture(t, tc.model)
			f.asPartialCaller(partialGrants, authzFailure{})
			f.expectTopologyReads()
			f.expectRelationTypeList()

			paths, err := f.service.GetRelationTypePaths(context.Background(), tc.query)

			if err != nil {
				t.Fatalf("GetRelationTypePaths() error = %v", err)
			}
			if got := signatures(paths); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("GetRelationTypePaths() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRelationTypePaths_PartialCallerSearchesOnlyTheVisibleModel pins the rule as a whole: for
// every source, direction, length and concept group limit, a caller without view_detail on the
// network gets exactly what a caller with it gets from a network holding only the first's scope --
// relation types it may read between object types it holds view_detail on. Nothing outside shows
// through, not even as a path cut short or missing.
func TestRelationTypePaths_PartialCallerSearchesOnlyTheVisibleModel(t *testing.T) {
	for _, source := range []string{"order", "user", "address", "payment", "supplier"} {
		for _, direction := range []string{interfaces.DIRECTION_FORWARD, interfaces.DIRECTION_BACKWARD,
			interfaces.DIRECTION_BIDIRECTIONAL} {
			for length := 1; length <= 3; length++ {
				for _, groups := range [][]string{nil, {"cg_sales"}} {
					query := pathQuery(source, direction, length, groups...)
					t.Run(fmt.Sprintf("%s/%s/%d/%v", source, direction, length, groups), func(t *testing.T) {
						partial := newPathFixture(t, fullPathModel())
						partial.asPartialCaller(partialGrants, authzFailure{})
						partial.expectTopologyReads()
						partial.expectRelationTypeList()
						full := newPathFixture(t, visiblePathModel())
						full.asFullCaller()
						full.expectTopologyReads()

						got, err := partial.service.GetRelationTypePaths(context.Background(), query)
						if err != nil {
							t.Fatalf("partial caller error = %v", err)
						}
						want, err := full.service.GetRelationTypePaths(context.Background(), query)
						if err != nil {
							t.Fatalf("full caller error = %v", err)
						}
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("partial caller = %v, want %v", signatures(got), signatures(want))
						}
					})
				}
			}
		}
	}
}

// TestRelationTypePaths_PartialCallerKeepsDirection spells out backward and bidirectional searches
// over the visible model.
func TestRelationTypePaths_PartialCallerKeepsDirection(t *testing.T) {
	for _, tc := range []struct {
		query interfaces.RelationTypePathsBaseOnSource
		want  []string
	}{
		{pathQuery("address", interfaces.DIRECTION_BACKWARD, 2),
			[]string{"address <rel_user_address- user <rel_order_user- order"}},
		{pathQuery("user", interfaces.DIRECTION_BIDIRECTIONAL, 1),
			[]string{"user -rel_user_address-> address", "user <rel_order_user- order"}},
		// Both relation types into supplier are hidden, so the search ends at the source.
		{pathQuery("supplier", interfaces.DIRECTION_BACKWARD, 1), []string{"supplier"}},
	} {
		t.Run(fmt.Sprintf("%s/%s", tc.query.SourceObjecTypeId, tc.query.Direction), func(t *testing.T) {
			f := newPathFixture(t, fullPathModel())
			f.asPartialCaller(partialGrants, authzFailure{})
			f.expectTopologyReads()
			f.expectRelationTypeList()

			paths, err := f.service.GetRelationTypePaths(context.Background(), tc.query)

			if err != nil {
				t.Fatalf("GetRelationTypePaths() error = %v", err)
			}
			if got := signatures(paths); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("GetRelationTypePaths() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRelationTypePaths_EndpointWithoutViewDetailDropsItsRelationType: a relation type the caller
// may read -- invoice passes the reference rule on query_data alone -- still leads nowhere, since
// a path node carries invoice's definition and that needs view_detail.
func TestRelationTypePaths_EndpointWithoutViewDetailDropsItsRelationType(t *testing.T) {
	f := newPathFixture(t, fullPathModel())
	f.asPartialCaller(partialGrants, authzFailure{})
	f.expectTopologyReads()
	f.expectRelationTypeList()

	paths, err := f.service.GetRelationTypePaths(context.Background(), pathQuery("user", interfaces.DIRECTION_FORWARD, 1))

	if err != nil {
		t.Fatalf("GetRelationTypePaths() error = %v", err)
	}
	if got, want := signatures(paths), []string{"user -rel_user_address-> address"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GetRelationTypePaths() = %v, want %v", got, want)
	}
	for _, hidden := range []string{"invoice", "rel_user_invoice"} {
		if _, ok := namedIDs(paths)[hidden]; ok {
			t.Fatalf("GetRelationTypePaths() names %s", hidden)
		}
	}
}

// TestRelationTypePaths_SourceNeedsViewDetail: the source is a path node like any other. Without
// view_detail on it -- query_data alone or nothing -- the caller keeps the old refusal and no path
// is searched.
func TestRelationTypePaths_SourceNeedsViewDetail(t *testing.T) {
	t.Run("view_detail on the source", func(t *testing.T) {
		f := newPathFixture(t, fullPathModel())
		f.asPartialCaller(partialGrants, authzFailure{})
		f.expectTopologyReads()
		f.expectRelationTypeList()

		paths, err := f.service.GetRelationTypePaths(context.Background(), pathQuery("address", interfaces.DIRECTION_FORWARD, 1))

		if err != nil {
			t.Fatalf("GetRelationTypePaths() error = %v", err)
		}
		// region, address's only neighbor, is hidden.
		if got, want := signatures(paths), []string{"address"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("GetRelationTypePaths() = %v, want %v", got, want)
		}
	})
	for _, tc := range []struct{ name, source string }{
		{"query_data alone on the source", "invoice"},
		{"nothing on the source", "item"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPathFixture(t, fullPathModel())
			f.asPartialCaller(partialGrants, authzFailure{})
			f.expectRelationTypeList()

			paths, err := f.service.GetRelationTypePaths(context.Background(), pathQuery(tc.source, interfaces.DIRECTION_FORWARD, 1))

			assertForbidden(t, paths, err)
		})
	}
}

// TestRelationTypePaths_NoVisibleChildIsRefused keeps the refusal for a caller who holds nothing
// in the network: no relation type is readable, the source lacks view_detail, and no path is
// searched.
func TestRelationTypePaths_NoVisibleChildIsRefused(t *testing.T) {
	f := newPathFixture(t, fullPathModel())
	f.ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden))
	f.expectRelationTypeList()
	f.ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_RELATION_TYPE, gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{}, nil).Times(1)
	f.ps.EXPECT().FilterResources(gomock.Any(), interfaces.RESOURCE_TYPE_OBJECT_TYPE, []string{"kn1/order"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, gomock.Any(), gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{}, nil).Times(1)

	paths, err := f.service.GetRelationTypePaths(context.Background(), pathQuery("order", interfaces.DIRECTION_FORWARD, 1))

	assertForbidden(t, paths, err)
}

// TestRelationTypePaths_ConceptGroupsMustBeReadable: a readable group limits the search as before;
// limiting by a group the caller cannot read would reveal its members, so it is refused.
func TestRelationTypePaths_ConceptGroupsMustBeReadable(t *testing.T) {
	t.Run("readable group", func(t *testing.T) {
		f := newPathFixture(t, fullPathModel())
		f.asPartialCaller(partialGrants, authzFailure{})
		f.expectTopologyReads()
		f.expectRelationTypeList()

		paths, err := f.service.GetRelationTypePaths(context.Background(),
			pathQuery("order", interfaces.DIRECTION_FORWARD, 3, "cg_sales"))

		if err != nil {
			t.Fatalf("GetRelationTypePaths() error = %v", err)
		}
		// address is outside cg_sales.
		if got, want := signatures(paths), []string{"order -rel_order_user-> user"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("GetRelationTypePaths() = %v, want %v", got, want)
		}
	})
	for _, groups := range [][]string{{"cg_hidden"}, {"cg_sales", "cg_hidden"}, {"cg_missing"}} {
		t.Run(fmt.Sprintf("unreadable %v", groups), func(t *testing.T) {
			f := newPathFixture(t, fullPathModel())
			f.asPartialCaller(partialGrants, authzFailure{})

			paths, err := f.service.GetRelationTypePaths(context.Background(),
				pathQuery("order", interfaces.DIRECTION_FORWARD, 1, groups...))

			assertForbidden(t, paths, err)
		})
	}
}

// TestRelationTypePaths_FullCallerIsUnchanged guards the caller with view_detail on the network:
// the whole model is searched and no child check or extra read is made.
func TestRelationTypePaths_FullCallerIsUnchanged(t *testing.T) {
	f := newPathFixture(t, fullPathModel())
	f.asFullCaller()
	f.expectTopologyReads()

	paths, err := f.service.GetRelationTypePaths(context.Background(), pathQuery("order", interfaces.DIRECTION_FORWARD, 2))

	if err != nil {
		t.Fatalf("GetRelationTypePaths() error = %v", err)
	}
	want := []string{
		"order -rel_order_payment-> payment",
		"order -rel_order_supplier-> supplier",
		"order -rel_order_user-> user -rel_user_address-> address",
		"order -rel_order_user-> user -rel_user_invoice-> invoice",
		"order -rel_order_item-> item -rel_item_supplier-> supplier",
	}
	if got := signatures(paths); !reflect.DeepEqual(got, want) {
		t.Fatalf("GetRelationTypePaths() = %v, want %v", got, want)
	}
}

// TestRelationTypePaths_AuthorizationFailureIsAnError refuses to read an authorization outage as
// "nothing is visible" at every check, and never falls back when the network check fails.
func TestRelationTypePaths_AuthorizationFailureIsAnError(t *testing.T) {
	t.Run("network check", func(t *testing.T) {
		f := newPathFixture(t, fullPathModel())
		outage := rest.NewHTTPError(context.Background(), http.StatusInternalServerError, rest.PublicError_InternalServerError)
		f.ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(outage)

		if _, err := f.service.GetRelationTypePaths(context.Background(),
			pathQuery("order", interfaces.DIRECTION_FORWARD, 1)); !errors.Is(err, outage) {
			t.Fatalf("GetRelationTypePaths() error = %v, want %v", err, outage)
		}
	})
	unavailable := errors.New("authorization unavailable")
	for _, tc := range []struct {
		name    string
		failure authzFailure
		groups  []string
	}{
		{"concept group check", authzFailure{interfaces.RESOURCE_TYPE_CONCEPT_GROUP, 1, unavailable}, []string{"cg_sales"}},
		{"relation type check", authzFailure{interfaces.RESOURCE_TYPE_RELATION_TYPE, 1, unavailable}, nil},
		{"endpoint reference check", authzFailure{interfaces.RESOURCE_TYPE_OBJECT_TYPE, 1, unavailable}, nil},
		{"path node check", authzFailure{interfaces.RESOURCE_TYPE_OBJECT_TYPE, 2, unavailable}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPathFixture(t, fullPathModel())
			f.asPartialCaller(partialGrants, tc.failure)
			f.expectRelationTypeList()

			paths, err := f.service.GetRelationTypePaths(context.Background(),
				pathQuery("order", interfaces.DIRECTION_FORWARD, 1, tc.groups...))

			if !errors.Is(err, unavailable) || paths != nil {
				t.Fatalf("GetRelationTypePaths() = %v, %v; want the authorization error", signatures(paths), err)
			}
		})
	}
}
