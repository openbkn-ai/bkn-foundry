// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestSchemaSandboxWithPythonOSDK exercises the real client against the alias
// router. Set both variables to a checked-out SDK and its Python environment.
func TestSchemaSandboxWithPythonOSDK(t *testing.T) {
	python, source := os.Getenv("BKN_OSDK_PYTHON"), os.Getenv("BKN_OSDK_SOURCE")
	if python == "" || source == "" {
		t.Skip("set BKN_OSDK_PYTHON and BKN_OSDK_SOURCE for the cross-repository HTTP check")
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	handler := &restSchemaHandler{Hydra: schemaAuth{}, Schema: schemaReadFunc(func(_ context.Context, kn, collection string, q url.Values) (int, []byte, error) {
		mu.Lock()
		seen[collection] = true
		mu.Unlock()
		if kn != "kn" || q.Get("branch") != "dev" {
			t.Error("network/branch changed")
		}
		switch collection {
		case "":
			return 200, []byte(`{"id":"kn","branch":"dev"}`), nil
		case "object-types":
			return 200, []byte(`{"entries":[{"id":"order","data_properties":null,"primary_keys":null}],"total_count":1}`), nil
		case "relation-types":
			return 200, []byte(`{"entries":[],"total_count":0}`), nil
		case "object-metrics":
			if q.Get("limit") != "500" || q.Get("status") != "published" || q.Get("offset") != "0" {
				t.Error("V1 pagination lost")
			}
			return 200, []byte(`{"entries":[{"definition":{"id":"gmv","version":2,"owner_object_type_id":"order","lifecycle":{"status":"published"},"query_capabilities":{"analysis_dimensions":null}}}],"total_count":1}`), nil
		default:
			t.Error("unexpected collection")
			return 500, nil, nil
		}
	})}
	router := gin.New()
	handler.RegisterRouter(router.Group("/api/bkn-backend/v1"))
	server := httptest.NewServer(router)
	defer server.Close()
	code := `import sys
from bkn_osdk import Context
from bkn_osdk.schema import fetch_schema
schema = fetch_schema(Context(base_url=sys.argv[1], token="token-a"), "kn", "dev")
assert schema.kn_id == "kn" and schema.branch == "dev"
assert schema.object_types[0].properties == ()
assert schema.object_types[0].primary_key == ()
assert schema.metrics == ()
print("real Python OSDK -> authenticated Go schema aliases: passed")
`
	command := exec.Command(python, "-c", code, server.URL)
	command.Env = append(os.Environ(), "PYTHONPATH="+source)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("OSDK HTTP check failed: %v\n%s", err, output)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("missing schema reads: %#v", seen)
	}
	t.Log(string(output))
}
