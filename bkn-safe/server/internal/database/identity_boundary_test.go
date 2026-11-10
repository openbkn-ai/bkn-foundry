package database

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestCommunityDoesNotOwnIdentityExtensionTables(t *testing.T) {
	for _, existing := range []bool{false, true} {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		if existing {
			if err := db.Exec("CREATE TABLE sso_identities (id TEXT PRIMARY KEY); INSERT INTO sso_identities(id) VALUES ('preserved')").Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := Migrate(db); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"sso_providers", "sso_departments", "sso_login_states"} {
			if db.Migrator().HasTable(table) {
				t.Fatalf("community created private table %s", table)
			}
		}
		if !existing && db.Migrator().HasTable("sso_identities") {
			t.Fatal("community created private identity table")
		}
		if existing {
			var n int64
			if err := db.Table("sso_identities").Count(&n).Error; err != nil || n != 1 {
				t.Fatal("community migration changed extension data")
			}
		}
	}
}
