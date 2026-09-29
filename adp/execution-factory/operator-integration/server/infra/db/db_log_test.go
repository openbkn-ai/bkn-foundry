package db

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/db/sqlx"
)

// The connection-failure log used to print the whole connection info and DB config, password included.
func TestDescribeDBConnOmitsCredentials(t *testing.T) {
	got := describeDBConn(&sqlx.DBConfig{
		User: "svc-user", Password: "s3cr3t-pa55", Host: "mariadb", Port: 3306, Database: "dip_data_operator_hub",
		CustomDriver: "rds-trace",
	})
	if strings.Contains(got, "s3cr3t-pa55") || strings.Contains(got, "svc-user") {
		t.Fatalf("describeDBConn leaked credentials: %s", got)
	}
	for _, want := range []string{"mariadb", "3306", "dip_data_operator_hub", "rds-trace"} {
		if !strings.Contains(got, want) {
			t.Fatalf("describeDBConn = %q, missing %q", got, want)
		}
	}
}
