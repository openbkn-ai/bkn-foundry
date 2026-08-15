// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// declareCatalogHierarchy registers the shipped catalog/resource shape: a data
// table hangs under a data catalog, its write verbs map to resource_manage, its
// read verbs map by the same name, and authorize maps to nothing.
func declareCatalogHierarchy(t *testing.T, db *gorm.DB) {
	t.Helper()
	types := []model.ResourceType{
		{ID: "catalog", Name: "数据目录"},
		{ID: "resource", Name: "数据资源", ParentTypeID: "catalog"},
	}
	if err := db.Create(&types).Error; err != nil {
		t.Fatalf("seed resource types: %v", err)
	}
	ops := []model.Operation{
		{ResourceTypeID: "catalog", ID: "view_detail"},
		{ResourceTypeID: "catalog", ID: "modify"},
		{ResourceTypeID: "catalog", ID: "authorize"},
		{ResourceTypeID: "catalog", ID: "resource_manage"},
		{ResourceTypeID: "resource", ID: "view_detail", ParentOperationID: "view_detail"},
		{ResourceTypeID: "resource", ID: "modify", ParentOperationID: "resource_manage"},
		{ResourceTypeID: "resource", ID: "delete", ParentOperationID: "resource_manage"},
		{ResourceTypeID: "resource", ID: "authorize"}, // deliberately no mapping
	}
	if err := db.Create(&ops).Error; err != nil {
		t.Fatalf("seed operations: %v", err)
	}
}

func ownedBy(t *testing.T, db *gorm.DB, resourceID, catalogID string) {
	t.Helper()
	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "resource", ResourceID: resourceID,
		ParentTypeID: "catalog", ParentID: catalogID,
	}).Error; err != nil {
		t.Fatalf("record ownership: %v", err)
	}
}

// TestCheckInheritsThroughTheCatalog is the point of #800: a grant on the
// catalog reaches the tables inside it, and only those — a table with no
// recorded owner is judged exactly as it was before.
func TestCheckInheritsThroughTheCatalog(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-in", "cat-1")

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))

	ok, err := e.Check(user, "resource", "res-in", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a catalog resource_manage grant did not reach a table inside that catalog")
	}

	// No ownership row: nothing to climb, so the pre-#800 answer stands.
	ok, err = e.Check(user, "resource", "res-orphan", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a table with no recorded catalog inherited anyway")
	}
}

// TestInheritanceRefusesSameNameFallback is the escalation this design exists to
// prevent: "modify" on a catalog means rename the catalog. If it fell back by
// name, whoever may rename a catalog could rewrite every table in it.
func TestInheritanceRefusesSameNameFallback(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "modify"))

	ok, err := e.Check(user, "resource", "res-1", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("catalog/modify (rename the catalog) was inherited as resource/modify (rewrite the table)")
	}
}

// TestUnmappedOperationDoesNotInherit: authorize has no mapping on purpose, so
// holding it on a catalog must not confer the right to re-grant the tables in it.
func TestUnmappedOperationDoesNotInherit(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "authorize"))

	ok, err := e.Check(user, "resource", "res-1", "authorize")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("catalog/authorize leaked downwards as second-hand granting on the table")
	}
}

// TestNoHierarchyDataMeansNoBehaviourChange pins the upgrade promise: with the
// ownership table empty, every decision is the one the previous release made.
func TestNoHierarchyDataMeansNoBehaviourChange(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "view_detail"))

	for _, op := range []string{"modify", "view_detail", "delete"} {
		ok, err := e.Check(user, "resource", "res-1", op)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Errorf("resource/%s was allowed with no ownership row recorded", op)
		}
	}
}

// TestAllowedOpsMergesInheritedOps: the detail page's operation set must contain
// both what was granted on the table and what it inherits from its catalog.
func TestAllowedOpsMergesInheritedOps(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-1", "view_detail"))
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "authorize"))

	got, err := e.AllowedOps(user, "resource", "res-1", []string{"view_detail", "modify", "delete", "authorize"})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"delete", "modify", "view_detail"}
	if len(got) != len(want) {
		t.Fatalf("AllowedOps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllowedOps = %v, want %v", got, want)
		}
	}
}

// TestFilterResourceOpsSeesInheritance is the reason the list page had to be
// taught the same walk: a table visible only through its catalog holds no policy
// row of its own, so without inheritance it does not merely lose operations — it
// disappears from the page.
func TestFilterResourceOpsSeesInheritance(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")
	ownedBy(t, db, "res-2", "cat-2")

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "view_detail"))
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))

	got, err := e.FilterResourceOps(user,
		[]ResourceRef{{Type: "resource", ID: "res-1"}, {Type: "resource", ID: "res-2"}},
		[]string{"view_detail"},
		[]string{"view_detail", "modify", "authorize"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "res-1" {
		t.Fatalf("filter = %v, want only res-1 (res-2 sits in an ungranted catalog)", got)
	}
	sort.Strings(got[0].Operations)
	if len(got[0].Operations) != 2 || got[0].Operations[0] != "modify" || got[0].Operations[1] != "view_detail" {
		t.Errorf("operations = %v, want [modify view_detail]", got[0].Operations)
	}
}

// TestFilterResourceOpsMatchesCheckUnderInheritance keeps the batch path and the
// single-decision path from drifting: the whole risk of two implementations is
// that a list page and a detail page answer differently for the same grant.
func TestFilterResourceOpsMatchesCheckUnderInheritance(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")
	ownedBy(t, db, "res-2", "cat-1")

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-2", "view_detail"))

	ops := []string{"view_detail", "modify", "delete", "authorize"}
	refs := []ResourceRef{{Type: "resource", ID: "res-1"}, {Type: "resource", ID: "res-2"}}
	filtered, err := e.FilterResourceOps(user, refs, nil, ops)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]bool{}
	for _, f := range filtered {
		set := map[string]bool{}
		for _, op := range f.Operations {
			set[op] = true
		}
		byID[f.ID] = set
	}
	for _, r := range refs {
		for _, op := range ops {
			want, err := e.Check(user, r.Type, r.ID, op)
			if err != nil {
				t.Fatal(err)
			}
			if byID[r.ID][op] != want {
				t.Errorf("%s/%s: filter=%v check=%v", r.ID, op, byID[r.ID][op], want)
			}
		}
	}
}

// TestInheritanceComposesAcrossLevels: the operation is translated at EVERY hop,
// not once. A three-level chain is what #515 turns the knowledge-network line
// into, so the walk must not assume the child's spelling survives the climb.
func TestInheritanceComposesAcrossLevels(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	types := []model.ResourceType{
		{ID: "top"},
		{ID: "mid", ParentTypeID: "top"},
		{ID: "leaf", ParentTypeID: "mid"},
	}
	if err := db.Create(&types).Error; err != nil {
		t.Fatal(err)
	}
	ops := []model.Operation{
		{ResourceTypeID: "top", ID: "manage_all"},
		{ResourceTypeID: "mid", ID: "manage_children", ParentOperationID: "manage_all"},
		{ResourceTypeID: "leaf", ID: "modify", ParentOperationID: "manage_children"},
	}
	if err := db.Create(&ops).Error; err != nil {
		t.Fatal(err)
	}
	rows := []model.ResourceParent{
		{ResourceTypeID: "leaf", ResourceID: "l-1", ParentTypeID: "mid", ParentID: "m-1"},
		{ResourceTypeID: "mid", ResourceID: "m-1", ParentTypeID: "top", ParentID: "t-1"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "top", "t-1", "manage_all"))

	ok, err := e.Check(user, "leaf", "l-1", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a grant two levels up did not reach the leaf")
	}

	// The intermediate spelling must not be accepted at the top: manage_children
	// is a mid-level verb, and holding it on the top node proves nothing.
	const other = "u-2"
	mustNoErr(t, e.GrantObjectPermission(other, "top", "t-1", "manage_children"))
	ok, err = e.Check(other, "leaf", "l-1", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("the climb accepted an untranslated operation at the top level")
	}
}

// TestInheritanceTerminatesOnACycle: ownership rows are pushed by another
// service, so a loop is a data state the enforcer must survive rather than a
// case it may assume away. A hang here would take down authorization itself.
func TestInheritanceTerminatesOnACycle(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	if err := db.Create(&model.ResourceType{ID: "folder", ParentTypeID: "folder"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Operation{
		ResourceTypeID: "folder", ID: "view_detail", ParentOperationID: "view_detail",
	}).Error; err != nil {
		t.Fatal(err)
	}
	rows := []model.ResourceParent{
		{ResourceTypeID: "folder", ResourceID: "f-1", ParentTypeID: "folder", ParentID: "f-2"},
		{ResourceTypeID: "folder", ResourceID: "f-2", ParentTypeID: "folder", ParentID: "f-1"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	done := make(chan bool, 1)
	go func() {
		ok, err := e.Check("u-1", "folder", "f-1", "view_detail")
		if err != nil {
			t.Error(err)
		}
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Error("a cycle produced an allow out of nowhere")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Check did not terminate on a cyclic hierarchy")
	}
}

// --- enumeration side (GET /authz/resources) -------------------------------

// TestAccessibleResourcesIncludesInherited: the enumeration read must agree with
// Check, or a list page loses rows the detail page allows. It walks DOWN from
// the granted catalogs, the inverse of Check's climb.
func TestAccessibleResourcesIncludesInherited(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")
	ownedBy(t, db, "res-2", "cat-1")
	ownedBy(t, db, "res-3", "cat-2")

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-9", "modify"))

	got, err := e.AccessibleResources(user, "resource", "modify")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"res-1", "res-2", "res-9"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ids = %v, want %v (res-3 sits in an ungranted catalog)", got, want)
	}
}

// TestAccessibleResourcesTypeWideParentGrant covers the case the recursion
// cannot answer on its own: AccessibleResources reports concrete instances only,
// so a "every catalog" grant would enumerate no parents at all and silently
// return nothing.
func TestAccessibleResourcesTypeWideParentGrant(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")
	ownedBy(t, db, "res-2", "cat-2")

	const user, role = "u-1", "role-1"
	mustNoErr(t, e.GrantRolePermission(role, "catalog", "*", "resource_manage"))
	mustNoErr(t, e.AssignRole(user, role))

	got, err := e.AccessibleResources(user, "resource", "modify")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "res-1,res-2" {
		t.Errorf("ids = %v, want every table that has a catalog", got)
	}
}

// TestAccessibleResourcesUnmappedOpDoesNotInherit: authorize maps to nothing, so
// enumeration must not leak the catalog's tables into it either.
func TestAccessibleResourcesUnmappedOpDoesNotInherit(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-1")

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "authorize"))

	got, err := e.AccessibleResources(user, "resource", "authorize")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("ids = %v, want none", got)
	}
}

// TestAccessibleResourcesUnchangedWithoutHierarchy is the upgrade guarantee for
// this read: with no ownership rows, it returns exactly what it returned before.
func TestAccessibleResourcesUnchangedWithoutHierarchy(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-9", "modify"))

	got, err := e.AccessibleResources(user, "resource", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "res-9" {
		t.Errorf("ids = %v, want only the directly granted res-9", got)
	}
}

// TestAccessibleResourcesAgreesWithCheck pins the two reads together across the
// whole population: anything enumerated must pass Check, and anything Check
// allows must be enumerated. Drift here is the failure this slice exists to
// prevent — a row present on the detail page and missing from the list.
func TestAccessibleResourcesAgreesWithCheck(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	all := []string{"res-1", "res-2", "res-3", "res-4"}
	ownedBy(t, db, "res-1", "cat-1")
	ownedBy(t, db, "res-2", "cat-1")
	ownedBy(t, db, "res-3", "cat-2")
	// res-4 has no catalog at all.

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-4", "modify"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-3", "view_detail"))

	for _, op := range []string{"modify", "view_detail", "delete", "authorize"} {
		ids, err := e.AccessibleResources(user, "resource", op)
		if err != nil {
			t.Fatal(err)
		}
		listed := map[string]bool{}
		for _, id := range ids {
			listed[id] = true
		}
		for _, id := range all {
			ok, err := e.Check(user, "resource", id, op)
			if err != nil {
				t.Fatal(err)
			}
			if ok != listed[id] {
				t.Errorf("%s/%s: check=%v enumerated=%v", id, op, ok, listed[id])
			}
		}
	}
}

// TestAccessibleResourcesAcrossLevels: a grant two levels up must enumerate the
// leaves, which only works if the recursion carries the translated operation.
func TestAccessibleResourcesAcrossLevels(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	types := []model.ResourceType{
		{ID: "top"},
		{ID: "mid", ParentTypeID: "top"},
		{ID: "leaf", ParentTypeID: "mid"},
	}
	if err := db.Create(&types).Error; err != nil {
		t.Fatal(err)
	}
	ops := []model.Operation{
		{ResourceTypeID: "top", ID: "manage_all"},
		{ResourceTypeID: "mid", ID: "manage_children", ParentOperationID: "manage_all"},
		{ResourceTypeID: "leaf", ID: "modify", ParentOperationID: "manage_children"},
	}
	if err := db.Create(&ops).Error; err != nil {
		t.Fatal(err)
	}
	rows := []model.ResourceParent{
		{ResourceTypeID: "leaf", ResourceID: "l-1", ParentTypeID: "mid", ParentID: "m-1"},
		{ResourceTypeID: "leaf", ResourceID: "l-2", ParentTypeID: "mid", ParentID: "m-2"},
		{ResourceTypeID: "mid", ResourceID: "m-1", ParentTypeID: "top", ParentID: "t-1"},
		{ResourceTypeID: "mid", ResourceID: "m-2", ParentTypeID: "top", ParentID: "t-2"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "top", "t-1", "manage_all"))

	got, err := e.AccessibleResources(user, "leaf", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "l-1" {
		t.Errorf("ids = %v, want only l-1 (l-2 hangs under an ungranted top)", got)
	}
}

// TestAccessibleResourcesSeesPubliclyGrantedAncestor closes the gap a review
// found: Check's climb goes through Enforce, whose matcher honours the
// "granted to everyone" subject, while the enumeration walks
// GetImplicitPermissionsForUser, which does not. A catalog granted to everyone
// would then allow its tables on the detail page and hide them from the list.
func TestAccessibleResourcesSeesPubliclyGrantedAncestor(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-public")
	ownedBy(t, db, "res-2", "cat-private")

	mustNoErr(t, e.GrantObjectPermission(PublicAccessorID, "catalog", "cat-public", "resource_manage"))

	const user = "u-nobody" // holds nothing of its own
	ok, err := e.Check(user, "resource", "res-1", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Check did not honour the public grant on the catalog")
	}
	ids, err := e.AccessibleResources(user, "resource", "modify")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "res-1" {
		t.Errorf("ids = %v, want [res-1] — enumeration must agree with Check", ids)
	}
}

// --- ownership preview (dry run) -------------------------------------------

// TestPreviewOwnershipReportsTheWidening is the confirmation step the design
// promised in place of a feature flag: before any ownership row is written, say
// exactly who starts reaching what.
func TestPreviewOwnershipReportsTheWidening(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)

	const alice, bob = "u-alice", "u-bob"
	mustNoErr(t, e.GrantObjectPermission(alice, "catalog", "cat-1", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(bob, "catalog", "cat-2", "view_detail"))

	links := map[string]string{"res-1": "cat-1", "res-2": "cat-1", "res-3": "cat-2"}
	flips, total, err := e.PreviewOwnership("resource", "catalog", links, 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range flips {
		got[f.AccessorID+"/"+f.ResourceID+"/"+f.Operation] = true
	}
	// alice's resource_manage maps to both modify and delete on the two tables
	// in cat-1; bob's view_detail maps to view_detail on the single table in cat-2.
	want := []string{
		alice + "/res-1/modify", alice + "/res-1/delete",
		alice + "/res-2/modify", alice + "/res-2/delete",
		bob + "/res-3/view_detail",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("preview missed %s (got %v)", w, got)
		}
	}
	if total != len(want) {
		t.Errorf("total = %d, want %d (%v)", total, len(want), got)
	}
	// Nothing may have been written: the preview is the step that happens before
	// the decision to write.
	var n int64
	db.Model(&model.ResourceParent{}).Count(&n)
	if n != 0 {
		t.Errorf("dry run wrote %d ownership rows", n)
	}
}

// TestPreviewOwnershipSkipsWhatIsAlreadyHeld: a table the subject can already
// touch is not a change, and reporting it would drown the real widening.
func TestPreviewOwnershipSkipsWhatIsAlreadyHeld(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-1", "modify"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-1", "delete"))

	_, total, err := e.PreviewOwnership("resource", "catalog", map[string]string{"res-1": "cat-1"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0 — the subject already held both operations", total)
	}
}

// TestPreviewOwnershipEmptyWhenNobodyHoldsTheCatalog is the case that matters
// most in practice: on an installation with no per-object catalog grants, the
// diff is empty and the push is a zero-behaviour-change operation.
func TestPreviewOwnershipEmptyWhenNobodyHoldsTheCatalog(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)

	_, total, err := e.PreviewOwnership("resource", "catalog",
		map[string]string{"res-1": "cat-1", "res-2": "cat-2"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
}

// TestPreviewOwnershipCountsBeyondTheLimit: the sample is capped but the total
// is exact, so a reviewer is never shown a short list that reads as complete.
func TestPreviewOwnershipCountsBeyondTheLimit(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	mustNoErr(t, e.GrantObjectPermission("u-1", "catalog", "cat-1", "resource_manage"))

	links := map[string]string{}
	for i := 0; i < 20; i++ {
		links["res-"+strconv.Itoa(i)] = "cat-1"
	}
	flips, total, err := e.PreviewOwnership("resource", "catalog", links, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(flips) != 5 {
		t.Errorf("sample = %d, want 5", len(flips))
	}
	if total != 40 { // 20 tables x {modify, delete}
		t.Errorf("total = %d, want 40", total)
	}
}

// TestPreviewOwnershipSeesRoleAndPublicGrants: the report has to name the grant
// that causes the widening, and a catalog held through a role or granted to
// everyone widens exactly as a direct grant does.
func TestPreviewOwnershipSeesRoleAndPublicGrants(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)

	const role = "role-builder"
	mustNoErr(t, e.GrantRolePermission(role, "catalog", "cat-1", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(PublicAccessorID, "catalog", "cat-2", "view_detail"))

	flips, total, err := e.PreviewOwnership("resource", "catalog",
		map[string]string{"res-1": "cat-1", "res-2": "cat-2"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	subjects := map[string]bool{}
	for _, f := range flips {
		subjects[f.AccessorID] = true
	}
	if !subjects[role] {
		t.Error("preview did not name the role whose grant causes the widening")
	}
	if !subjects[PublicAccessorID] {
		t.Error("preview missed the grant-to-everyone subject")
	}
	if total == 0 {
		t.Error("total = 0")
	}
}

// TestPreviewOwnershipSeesGrandparentGrants: the enforce-time climb has no depth
// limit, so a grant two levels above the proposed parent reaches the child. A
// preview that only read policy rows on the immediate parent would miss it, and
// a safety preview that under-reports is worse than none at all.
func TestPreviewOwnershipSeesGrandparentGrants(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	types := []model.ResourceType{
		{ID: "top"},
		{ID: "mid", ParentTypeID: "top"},
		{ID: "leaf", ParentTypeID: "mid"},
	}
	if err := db.Create(&types).Error; err != nil {
		t.Fatal(err)
	}
	ops := []model.Operation{
		{ResourceTypeID: "top", ID: "manage_all"},
		{ResourceTypeID: "mid", ID: "manage_children", ParentOperationID: "manage_all"},
		{ResourceTypeID: "leaf", ID: "modify", ParentOperationID: "manage_children"},
	}
	if err := db.Create(&ops).Error; err != nil {
		t.Fatal(err)
	}
	// mid already hangs under top; the proposal is to put a leaf under mid.
	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "mid", ResourceID: "m-1", ParentTypeID: "top", ParentID: "t-1",
	}).Error; err != nil {
		t.Fatal(err)
	}
	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "top", "t-1", "manage_all"))

	flips, total, err := e.PreviewOwnership("leaf", "mid", map[string]string{"l-1": "m-1"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(flips) != 1 {
		t.Fatalf("flips = %v (total %d), want the one grandparent-derived grant", flips, total)
	}
	if flips[0].AccessorID != user || flips[0].Operation != "modify" || flips[0].Direction != FlipGrant {
		t.Errorf("flip = %+v, want u-1 gains modify on l-1", flips[0])
	}
}

// TestPreviewOwnershipReportsRevokesOnReparent: moving a resource to another
// parent takes it away from the old parent's holders. Counting only widening
// would hand a synchroniser total=0 for a push that silently removes access.
func TestPreviewOwnershipReportsRevokesOnReparent(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-old")

	const loser, keeper = "u-loser", "u-keeper"
	mustNoErr(t, e.GrantObjectPermission(loser, "catalog", "cat-old", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(keeper, "catalog", "cat-new", "resource_manage"))

	flips, total, err := e.PreviewOwnership("resource", "catalog", map[string]string{"res-1": "cat-new"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	byDir := map[string][]string{}
	for _, f := range flips {
		byDir[f.Direction] = append(byDir[f.Direction], f.AccessorID+"/"+f.Operation)
	}
	sort.Strings(byDir[FlipGrant])
	sort.Strings(byDir[FlipRevoke])
	if strings.Join(byDir[FlipGrant], ",") != keeper+"/delete,"+keeper+"/modify" {
		t.Errorf("grants = %v, want the new catalog's holder gaining modify+delete", byDir[FlipGrant])
	}
	if strings.Join(byDir[FlipRevoke], ",") != loser+"/delete,"+loser+"/modify" {
		t.Errorf("revokes = %v, want the old catalog's holder losing modify+delete", byDir[FlipRevoke])
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
}

// TestPreviewOwnershipKeepsDirectGrantsOnReparent: a grant written on the
// resource itself survives a move, so it must not be reported as a loss.
func TestPreviewOwnershipKeepsDirectGrantsOnReparent(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)
	ownedBy(t, db, "res-1", "cat-old")

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-old", "resource_manage"))
	mustNoErr(t, e.GrantObjectPermission(user, "resource", "res-1", "modify"))

	flips, _, err := e.PreviewOwnership("resource", "catalog", map[string]string{"res-1": "cat-new"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range flips {
		if f.Operation == "modify" && f.Direction == FlipRevoke {
			t.Errorf("reported a loss of modify, but it is granted on the resource itself: %+v", f)
		}
	}
}

// TestPreviewOwnershipFirstRegistrationCannotRevoke: a resource that had no
// parent cannot lose anything, so the report must be grants only.
func TestPreviewOwnershipFirstRegistrationCannotRevoke(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	declareCatalogHierarchy(t, db)

	const user = "u-1"
	mustNoErr(t, e.GrantObjectPermission(user, "catalog", "cat-1", "resource_manage"))

	flips, _, err := e.PreviewOwnership("resource", "catalog", map[string]string{"res-1": "cat-1"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range flips {
		if f.Direction != FlipGrant {
			t.Errorf("first registration reported %+v", f)
		}
	}
}
