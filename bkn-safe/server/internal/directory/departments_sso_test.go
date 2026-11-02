package directory

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestDeleteDepartmentCleansSSOMappingAtomically(t *testing.T) {
	for _, failCleanup := range []bool{false, true} {
		name := "successful deletion"
		if failCleanup {
			name = "failed cleanup rolls back deletion"
		}
		t.Run(name, func(t *testing.T) {
			s, db := newSvc(t)
			if err := db.Create(&model.Department{ID: "sso-dept", Name: "SSO department"}).Error; err != nil {
				t.Fatal(err)
			}
			mapping := model.SSODepartment{ID: "mapping", ProviderID: "casdoor", ExternalID: "org/dept", DeptID: "sso-dept"}
			if err := db.Create(&mapping).Error; err != nil {
				t.Fatal(err)
			}
			if failCleanup {
				if err := db.Exec("CREATE TRIGGER reject_sso_dept_delete BEFORE DELETE ON sso_departments BEGIN SELECT RAISE(ABORT, 'injected cleanup failure'); END").Error; err != nil {
					t.Fatal(err)
				}
			}
			err := s.DeleteDepartment(t.Context(), "sso-dept")
			if (err != nil) != failCleanup {
				t.Fatalf("DeleteDepartment() error=%v, cleanup failure=%v", err, failCleanup)
			}
			want := int64(0)
			if failCleanup {
				want = 1
			}
			for _, row := range []any{&model.Department{}, &model.SSODepartment{}} {
				var count int64
				if err := db.Model(row).Count(&count).Error; err != nil || count != want {
					t.Fatalf("unexpected rows for %T: count=%d want=%d err=%v", row, count, want, err)
				}
			}
		})
	}
}
