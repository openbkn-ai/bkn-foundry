// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestDepartmentMovesRejectInvalidParentsAtomically(t *testing.T) {
	s, db := newSvc(t)
	ctx := context.Background()
	for _, dept := range []model.Department{
		{ID: "root", Name: "Root"},
		{ID: "child", Name: "Child", ParentID: "root"},
		{ID: "alt", Name: "Alt", ParentID: "child"},
		{ID: "leaf", Name: "Leaf", ParentID: "alt"},
		{ID: "other", Name: "Other"},
		{ID: "cycle-a", Name: "Cycle A", ParentID: "cycle-b"},
		{ID: "cycle-b", Name: "Cycle B", ParentID: "cycle-a"},
		{ID: "orphan", Name: "Orphan", ParentID: "missing"},
	} {
		if err := db.Create(&dept).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		parent string
		want   error
	}{
		{"child", ErrDepartmentCycle},
		{"alt", ErrDepartmentCycle},
		{"leaf", ErrDepartmentCycle},
		{"cycle-a", ErrDepartmentCycle},
		{"missing", ErrUnknownDepartment},
		{"orphan", ErrUnknownDepartment},
	} {
		t.Run(tc.parent, func(t *testing.T) {
			err := s.UpdateDepartment(ctx, "child", map[string]any{"parent_id": tc.parent, "name": "Changed"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("update = %v, want %v", err, tc.want)
			}
			dept, err := s.GetDepartment(ctx, "child")
			if err != nil || dept.ParentID != "root" || dept.Name != "Child" {
				t.Fatalf("rejected update changed department: %+v, %v", dept, err)
			}
		})
	}
	if err := s.UpdateDepartment(ctx, "child", map[string]any{"parent_id": "other"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDepartment(ctx, "child", map[string]any{"parent_id": "other"}); err != nil {
		t.Fatalf("unchanged parent: %v", err)
	}
	if err := s.UpdateDepartment(ctx, "child", map[string]any{"parent_id": ""}); err != nil {
		t.Fatalf("move to root: %v", err)
	}
	if err := s.UpdateDepartment(ctx, "cycle-a", map[string]any{"parent_id": "root"}); err != nil {
		t.Fatalf("break existing cycle: %v", err)
	}
	if err := s.UpdateDepartment(ctx, "missing", map[string]any{"parent_id": "root"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing department: %v", err)
	}
}

func TestDepartmentCreateRejectsInvalidParents(t *testing.T) {
	s, db := newSvc(t)
	ctx := context.Background()
	for _, parent := range []string{"new", "missing"} {
		if err := s.CreateDepartment(ctx, &model.Department{ID: "new", Name: "New", ParentID: parent}); err == nil {
			t.Fatalf("accepted parent %q", parent)
		}
	}
	var count int64
	if err := db.Model(&model.Department{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid create persisted: count=%d, err=%v", count, err)
	}
	if err := s.CreateDepartment(ctx, &model.Department{ID: "root", Name: "Root"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDepartment(ctx, &model.Department{ID: "new", Name: "New", ParentID: "root"}); err != nil {
		t.Fatal(err)
	}
}

func TestDepartmentCountsTerminateForLegacyCycles(t *testing.T) {
	for _, size := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("cycle-%d", size), func(t *testing.T) {
			s, db := newSvc(t)
			for i := range size {
				dept := model.Department{ID: fmt.Sprintf("d%d", i), Name: fmt.Sprintf("Department %d", i), ParentID: fmt.Sprintf("d%d", (i+1)%size)}
				if err := db.Create(&dept).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&model.Department{ID: "healthy", Name: "Healthy"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.User{ID: "user", Account: "member", Name: "Member", Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			for i := range size {
				if err := db.Create(&model.UserDepartment{UserID: "user", DepartmentID: fmt.Sprintf("d%d", i)}).Error; err != nil {
					t.Fatal(err)
				}
			}
			items, total, err := s.ListAllDepartments(t.Context(), "", 0, 100)
			if err != nil || total != int64(size+1) || len(items) != size+1 {
				t.Fatalf("list: items=%v, total=%d, err=%v", items, total, err)
			}
			for _, item := range items {
				want := int64(1)
				if item.ID == "healthy" {
					want = 0
				}
				if item.SubtreeMemberCount != want {
					t.Fatalf("%s count=%d, want %d", item.ID, item.SubtreeMemberCount, want)
				}
			}
			children, err := s.ListDepartmentsWithCounts(t.Context(), "d0")
			if err != nil || len(children) != 1 || children[0].SubtreeMemberCount != 1 {
				t.Fatalf("children=%v, err=%v", children, err)
			}
		})
	}
}

func TestDepartmentCountsDeepTreeAcrossPages(t *testing.T) {
	s, db := newSvc(t)
	const depth = 2000
	deps := make([]model.Department, depth)
	for i := range depth {
		deps[i] = model.Department{ID: fmt.Sprintf("d%04d", i), Name: fmt.Sprintf("Department %04d", i)}
		if i > 0 {
			deps[i].ParentID = deps[i-1].ID
		}
	}
	if err := db.CreateInBatches(&deps, 50).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "user", Account: "member", Name: "Member", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{deps[0].ID, deps[depth-1].ID} {
		if err := db.Create(&model.UserDepartment{UserID: "user", DepartmentID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := s.ListAllDepartments(t.Context(), "", 0, 1)
	if err != nil || total != depth || len(items) != 1 || items[0].ID != deps[0].ID || items[0].SubtreeMemberCount != 1 {
		t.Fatalf("first page: %v, total=%d, err=%v", items, total, err)
	}
	if err := s.UpdateDepartment(t.Context(), deps[0].ID, map[string]any{"parent_id": deps[depth-1].ID}); !errors.Is(err, ErrDepartmentCycle) {
		t.Fatalf("deep descendant parent: %v", err)
	}
}

func TestConcurrentDepartmentMovesAcrossConnections(t *testing.T) {
	// Independent pools model independent replicas. A local mutex cannot make
	// this safe; validation and writing must share the database lock.
	dsn := filepath.Join(t.TempDir(), "departments.db") + "?_pragma=busy_timeout(5000)"
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return db
	}
	db1, db2 := open(), open()
	if err := db1.AutoMigrate(&model.Department{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := db1.Create(&model.Department{ID: id, Name: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for range 10 {
		if err := db1.Model(&model.Department{}).Where("1 = 1").Update("parent_id", "").Error; err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			results <- New(db1).UpdateDepartment(t.Context(), "a", map[string]any{"parent_id": "b"})
		}()
		go func() {
			<-start
			results <- New(db2).UpdateDepartment(t.Context(), "b", map[string]any{"parent_id": "a"})
		}()
		close(start)
		first, second := <-results, <-results
		if first != nil {
			first, second = second, first
		}
		if first != nil || !errors.Is(second, ErrDepartmentCycle) {
			t.Fatalf("concurrent moves = %v / %v; want one success and one cycle rejection", first, second)
		}
		var deps []model.Department
		if err := db1.Find(&deps).Error; err != nil {
			t.Fatal(err)
		}
		if deps[0].ParentID != "" && deps[1].ParentID != "" {
			t.Fatalf("concurrent moves created a cycle: %+v", deps)
		}
	}
}

func TestDepartmentCountsFollowValidMove(t *testing.T) {
	s, db := newSvc(t)
	for _, dept := range []model.Department{
		{ID: "root", Name: "Root"},
		{ID: "child", Name: "Child", ParentID: "root"},
		{ID: "alt", Name: "Alt", ParentID: "child"},
		{ID: "target", Name: "Target"},
	} {
		if err := s.CreateDepartment(t.Context(), &dept); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"Alice", "Bob"} {
		if err := db.Create(&model.User{ID: id, Name: id, Account: id, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, membership := range []model.UserDepartment{
		{UserID: "Alice", DepartmentID: "root"},
		{UserID: "Alice", DepartmentID: "alt"},
		{UserID: "Bob", DepartmentID: "alt"},
	} {
		if err := db.Create(&membership).Error; err != nil {
			t.Fatal(err)
		}
	}
	check := func(root, target int64) {
		t.Helper()
		for name, want := range map[string]int64{"Root": root, "Target": target, "Child": 2} {
			items, total, err := s.ListAllDepartments(t.Context(), name, 0, 1)
			if err != nil || total != 1 || len(items) != 1 || items[0].SubtreeMemberCount != want {
				t.Fatalf("%s page: items=%v, total=%d, err=%v, want count=%d", name, items, total, err, want)
			}
		}
	}
	check(2, 0)
	if err := s.UpdateDepartment(t.Context(), "child", map[string]any{"parent_id": "target"}); err != nil {
		t.Fatal(err)
	}
	check(1, 2)
}
