package auth

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestDeleteUserRollsBackWhenSSOIdentityCleanupFails(t *testing.T) {
	s := newStore(t)
	u := model.User{ID: "sso-user", Account: "sso-user", Enabled: true, Source: model.SourceSSO}
	if err := s.db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&model.UserDepartment{UserID: u.ID, DepartmentID: "dept"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&model.SSOIdentity{ID: "identity", ProviderID: "casdoor", Subject: "subject", UserID: u.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Exec("CREATE TRIGGER reject_identity_delete BEFORE DELETE ON sso_identities BEGIN SELECT RAISE(ABORT, 'injected cleanup failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(t.Context(), u.ID); err == nil {
		t.Fatal("expected SSO identity cleanup failure")
	}
	if err := s.db.First(&model.User{}, "id = ?", u.ID).Error; err != nil {
		t.Fatalf("user deletion was not rolled back: %v", err)
	}
	if err := s.db.First(&model.UserDepartment{}, "user_id = ?", u.ID).Error; err != nil {
		t.Fatalf("membership deletion was not rolled back: %v", err)
	}
	if err := s.db.First(&model.SSOIdentity{}, "user_id = ?", u.ID).Error; err != nil {
		t.Fatalf("identity changed after failed cleanup: %v", err)
	}
}
