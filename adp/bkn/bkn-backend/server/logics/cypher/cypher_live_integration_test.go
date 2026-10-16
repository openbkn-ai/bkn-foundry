//go:build integration

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// This adapter exercises the local compiler/assembler against real Vega reads
// using the caller already logged into openbkn. Permission behavior is covered
// separately: this fixture uses the unit-test permission adapter and never
// claims a deployed Backend/MCP/Studio authorization acceptance result.
type liveCypherVega struct {
	*recordingVega
	reads     int
	lastError error
}

func liveCypherCLI(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "openbkn", append([]string{"--json"}, args...)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read-only openbkn call failed: %w: %s", err, stderr.String())
	}
	return output, nil
}
func (v *liveCypherVega) RawQuery(ctx context.Context, request *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	raw, err := liveCypherCLI(ctx, "vega", "sql", "--data", string(body))
	if err != nil {
		v.lastError = err
		return nil, err
	}
	var result interfaces.RawQueryResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	v.reads++
	return &result, nil
}
func (v *liveCypherVega) RawQueryAs(context.Context, interfaces.AccountInfo, *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	return nil, fmt.Errorf("live integration never impersonates a proxy account")
}

func TestLiveReadOnlyCypherVega(t *testing.T) {
	knID := os.Getenv("BKN_CYPHER_LIVE_KN")
	if knID == "" {
		t.Skip("set BKN_CYPHER_LIVE_KN to an existing small read-only fixture network")
	}
	ctx := context.Background()
	raw, err := liveCypherCLI(ctx, "bkn", "object-type", "list", knID)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Entries []struct {
			ID string `json:"id"`
		}
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	source := &fakeSchemaSource{}
	for _, entry := range listed.Entries {
		raw, err := liveCypherCLI(ctx, "bkn", "object-type", "get", knID, entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		var body struct{ Entries []*interfaces.ObjectType }
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		source.objectTypes = append(source.objectTypes, body.Entries...)
	}
	raw, err = liveCypherCLI(ctx, "bkn", "relation-type", "list", knID)
	if err != nil {
		t.Fatal(err)
	}
	var relationBody struct{ Entries []*interfaces.RelationType }
	if err := json.Unmarshal(raw, &relationBody); err != nil {
		t.Fatal(err)
	}
	for _, relation := range relationBody.Entries {
		raw, err := json.Marshal(relation.MappingRules)
		if err != nil {
			t.Fatal(err)
		}
		var mappings []interfaces.Mapping
		if relation.Type == interfaces.RELATION_TYPE_DIRECT {
			if err := json.Unmarshal(raw, &mappings); err != nil {
				t.Fatal(err)
			}
			relation.MappingRules = mappings
		}
		source.relationTypes = append(source.relationTypes, relation)
	}
	if len(source.objectTypes) == 0 {
		t.Fatal("fixture network has no object types")
	}
	var first *interfaces.ObjectType
	for _, object := range source.objectTypes {
		if len(object.PrimaryKeys) > 0 {
			first = object
			break
		}
	}
	if first == nil {
		t.Fatal("fixture has no complete node identities")
	}
	if len(source.relationTypes) == 0 {
		// A local virtual self relation exercises real SQL joins without
		// creating or changing a platform model or any source data.
		relation := relationType("__live_self", "__live_self")
		relation.SourceObjectTypeID, relation.TargetObjectTypeID = first.OTID, first.OTID
		mappings := []interfaces.Mapping{}
		for _, key := range first.PrimaryKeys {
			mappings = append(mappings, interfaces.Mapping{SourceProp: interfaces.SimpleProperty{Name: key}, TargetProp: interfaces.SimpleProperty{Name: key}})
		}
		relation.MappingRules = mappings
		source.relationTypes = append(source.relationTypes, relation)
		t.Log("using a local self-relation fixture over the existing resource; no platform model writes")
	}
	identifier := func(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }
	queries := []string{
		"MATCH (n:" + identifier(first.OTID) + ") RETURN n LIMIT 3",
		"MATCH (n:" + identifier(first.OTID) + ") WITH n ORDER BY n." + identifier(first.PrimaryKeys[0]) + " LIMIT 2 RETURN n",
	}
	for _, relation := range source.relationTypes {
		if relation.Type != interfaces.RELATION_TYPE_DIRECT {
			continue
		}
		pattern := "(a:" + identifier(relation.SourceObjectTypeID) + ")-[:" + identifier(relation.RTID) + "]->(b:" + identifier(relation.TargetObjectTypeID) + ")"
		queries = append(queries, "MATCH route="+pattern+" RETURN route,length(route) AS hops LIMIT 3")
		queries = append(queries, "MATCH route=(a:"+identifier(relation.SourceObjectTypeID)+")-[:"+identifier(relation.RTID)+"*1..1]->(b) RETURN route,length(route) AS hops LIMIT 3")
		queries = append(queries, "MATCH (a:"+identifier(relation.SourceObjectTypeID)+") WHERE EXISTS { "+pattern+" } RETURN a LIMIT 3")
		break
	}
	for index, query := range queries {
		t.Run(fmt.Sprintf("query-%d", index+1), func(t *testing.T) {
			vega := &liveCypherVega{recordingVega: &recordingVega{}}
			service := &cypherQueryService{ps: &stubPermission{}, schema: source, vba: vega}
			result, err := service.Query(ctx, interfaces.CypherQuery{KNID: knID, Branch: "main", Query: query, ResultFormat: interfaces.CYPHER_GRAPH_V1})
			if err != nil {
				t.Fatalf("%v; adapter error: %v", err, vega.lastError)
			}
			if len(result.Entries) == 0 {
				t.Fatal("live fixture returned no facts; positive traversal has not been verified")
			}
			if result.Execution == nil || !result.Execution.MatchComplete || !result.Execution.EvidenceComplete {
				t.Fatal("incomplete live result")
			}
			for _, row := range result.Entries {
				if hops, exists := row["hops"]; exists && hops != int64(1) {
					t.Fatal("unexpected live path length", hops)
				}
			}
			t.Logf("live source reads=%d result_rows=%d candidate_rows=%d complete=%t", vega.reads, len(result.Entries), result.Execution.CandidateRows, result.Execution.EvidenceComplete)
		})
	}
}
