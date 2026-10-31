package seed

import (
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

func legacyProvenanceOwner() model.User {
	return model.User{ID: retiredProvenanceOwnerID, Account: retiredProvenanceOwnerAccount, Name: retiredProvenanceOwnerName, Enabled: true, Source: model.SourceLocal, AccountType: model.AccountTypeApp}
}

func TestApplyNeverCreatesProvenanceAccount(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Apply(db, e); err != nil {
			t.Fatal(err)
		}
		if err := ReconcileRetiredBusinessProvenanceOwner(db, e); err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Model(&model.User{}).Where("id = ? OR account = ?", retiredProvenanceOwnerID, retiredProvenanceOwnerAccount).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("created %d retired accounts", count)
		}
	}
}

func TestRetireProvenanceAccountRemovesOnlyItsLiveIdentity(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	old := legacyProvenanceOwner()
	other := model.User{ID: "real-user", Account: "real-user", Name: "Real User", Enabled: true, AccountType: model.AccountTypeOther}
	for _, u := range []model.User{old, other} {
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{old.ID, other.ID} {
		if err := e.AssignRole(id, "test-role"); err != nil {
			t.Fatal(err)
		}
		if err := e.GrantObjectPermission(id, "catalog", "catalog-1", "view_detail"); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.APIKey{ID: id, KeyID: id, OwnerUserID: id, Name: "key", SecretHash: "hash"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.UserDepartment{UserID: id, DepartmentID: "dept"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.GroupMember{GroupID: "group", MemberID: id, MemberType: "user"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	audit := model.AuditLog{ID: "historical-audit", ActorID: old.ID, ActorNameSnapshot: old.Name, TargetID: old.ID, Detail: "historical snapshot"}
	if err := db.Create(&audit).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ReconcileRetiredBusinessProvenanceOwner(db, e); err != nil {
			t.Fatal(err)
		}
	}
	var got model.User
	if err := db.First(&got, "id = ?", old.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("retired user: %v", err)
	}
	if err := db.First(&got, "id = ?", other.ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ table, column string }{
		{"api_keys", "owner_user_id"}, {"user_departments", "user_id"}, {"group_members", "member_id"}, {"casbin_rule", "v0"}, {"authorization_grant", "accessor_id"},
	} {
		var n int64
		if err := db.Table(check.table).Where(check.column+" = ?", old.ID).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("orphaned %s: %d", check.table, n)
		}
		if err := db.Table(check.table).Where(check.column+" = ?", other.ID).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatalf("removed real user's %s", check.table)
		}
	}
	var preserved model.AuditLog
	if err := db.First(&preserved, "id = ?", audit.ID).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.ActorID != audit.ActorID || preserved.ActorNameSnapshot != audit.ActorNameSnapshot || preserved.Detail != audit.Detail {
		t.Fatal("rewrote audit history")
	}
	if allowed, err := e.Check(other.ID, "catalog", "catalog-1", "view_detail"); err != nil || !allowed {
		t.Fatalf("other user lost permission: %v %v", allowed, err)
	}
	if allowed, err := e.Check(old.ID, "catalog", "catalog-1", "view_detail"); err != nil || allowed {
		t.Fatalf("retired permission: %v %v", allowed, err)
	}
}

func TestRetireProvenanceAccountPreservesCollisions(t *testing.T) {
	for name, change := range map[string]func(*model.User){
		"different id":      func(u *model.User) { u.ID = "customer-id" },
		"different account": func(u *model.User) { u.Account = "customer" },
		"different name":    func(u *model.User) { u.Name = "Customer" },
		"password":          func(u *model.User) { u.PasswordHash = "hash" },
		"user type":         func(u *model.User) { u.AccountType = model.AccountTypeOther },
		"federated":         func(u *model.User) { u.Source = model.SourceLDAP },
		"email":             func(u *model.User) { u.Email = "customer@example.com" },
	} {
		t.Run(name, func(t *testing.T) {
			db := newDB(t)
			e, err := authz.New(db)
			if err != nil {
				t.Fatal(err)
			}
			u := legacyProvenanceOwner()
			change(&u)
			if err := db.Create(&u).Error; err != nil {
				t.Fatal(err)
			}
			if err := ReconcileRetiredBusinessProvenanceOwner(db, e); err != nil {
				t.Fatal(err)
			}
			var got model.User
			if err := db.First(&got, "id = ?", u.ID).Error; err != nil {
				t.Fatal(err)
			}
			if got.Account != u.Account || got.PasswordHash != u.PasswordHash || got.Name != u.Name {
				t.Fatal("modified customer account")
			}
		})
	}
}

func TestRetireProvenanceAccountRollsBackOnDeleteFailure(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	u := legacyProvenanceOwner()
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.GrantObjectPermission(u.ID, "catalog", "catalog-1", "view_detail"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.APIKey{ID: "key", KeyID: "key", OwnerUserID: u.ID, SecretHash: "hash"},
		&model.UserDepartment{UserID: u.ID, DepartmentID: "dept"},
		&model.GroupMember{GroupID: "group", MemberID: u.ID, MemberType: "user"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("CREATE TRIGGER reject_retired_delete BEFORE DELETE ON users BEGIN SELECT RAISE(ABORT, 'injected'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := ReconcileRetiredBusinessProvenanceOwner(db, e); err == nil {
		t.Fatal("expected failure")
	}
	var got model.User
	if err := db.First(&got, "id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ table, column string }{
		{"api_keys", "owner_user_id"}, {"user_departments", "user_id"}, {"group_members", "member_id"},
	} {
		var count int64
		if err := db.Table(check.table).Where(check.column+" = ?", u.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("rollback lost %s", check.table)
		}
	}
	if allowed, err := e.Check(u.ID, "catalog", "catalog-1", "view_detail"); err != nil || !allowed {
		t.Fatalf("rollback lost permission: %v %v", allowed, err)
	}
}

func TestRetireProvenanceAccountPreservesManagedProxy(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	u := legacyProvenanceOwner()
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ManagedProxyAccount{ProxyAccountID: u.ID, ManagedBy: "bkn", ManagedResourceType: "knowledge_network", ManagedResourceID: "kn"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ReconcileRetiredBusinessProvenanceOwner(db, e); err != nil {
		t.Fatal(err)
	}
	var got model.User
	if err := db.First(&got, "id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
}
