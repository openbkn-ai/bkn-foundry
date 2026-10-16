// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func queryReadsObjects(query *Query) bool {
	for _, stage := range query.Stages {
		if queryReadsObjects(stage.Query) {
			return true
		}
	}
	if len(query.Pattern.Nodes) > 0 {
		return true
	}
	for _, branch := range query.Union {
		if queryReadsObjects(branch) {
			return true
		}
	}
	return false
}

func queryExceedsLimit(query *Query) bool {
	for _, stage := range query.Stages {
		if queryExceedsLimit(stage.Query) {
			return true
		}
	}
	if query.Limit != nil && *query.Limit > interfaces.CYPHER_MAX_LIMIT {
		return true
	}
	for _, branch := range query.Union {
		if queryExceedsLimit(branch) {
			return true
		}
	}
	return false
}

func compileUnion(query *Query, schema *Schema, options CompileOptions) (*Plan, error) {
	if err := validateObjectProperties(options); err != nil {
		return nil, err
	}
	plan := &Plan{NetworkID: schema.KNID, Branch: schema.Branch, UnionAll: query.UnionAll}
	knownVariables := map[string]bool{}
	for _, branch := range query.Union {
		for _, node := range queryPatternNodes(branch) {
			knownVariables[node.Variable] = true
		}
	}
	for name := range options.ObjectProperties {
		if !knownVariables[name] {
			return nil, planErrorf(Position{}, "object_properties names unknown node variable %q", name)
		}
	}
	var columns map[string]bool
	for _, branch := range query.Union {
		copyQuery := *branch
		copyQuery.ValueEvaluation = true
		branchOptions := options
		branchOptions.ObjectProperties = map[string][]string{}
		for _, node := range queryPatternNodes(branch) {
			if properties, ok := options.ObjectProperties[node.Variable]; ok {
				branchOptions.ObjectProperties[node.Variable] = properties
			}
		}
		child, err := Compile(&copyQuery, schema, branchOptions)
		if err != nil {
			return nil, err
		}
		names := map[string]bool{}
		for _, projection := range child.Result.Projections {
			names[projection.Alias] = true
		}
		if columns != nil && !reflect.DeepEqual(columns, names) {
			return nil, planErrorf(Position{}, "UNION branches must return the same column names")
		}
		columns = names
		// A default cap on individual inputs would change UNION deduplication.
		// Explicit branch SKIP/LIMIT remains a logical part of that branch.
		if !child.Result.LimitExplicit {
			child.Result.Limit = graphMatchBudget
		}
		plan.Union = append(plan.Union, child)
		plan.Properties = append(plan.Properties, child.Properties...)
	}
	return plan, nil
}

func unionFailure(ctx context.Context, message string) error {
	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Cypher_LimitExceeded).WithErrorDetails(message)
}

func (s *cypherQueryService) runUnion(ctx context.Context, query interfaces.CypherQuery, compiled *compiledQuery) (*interfaces.CypherQueryResult, error) {
	result := &interfaces.CypherQueryResult{
		Entries: []map[string]any{}, ResultFormat: interfaces.CYPHER_GRAPH_V1,
		ValueTypes:      map[string]*interfaces.CypherValueType{},
		Execution:       &interfaces.CypherExecution{MatchComplete: true, EvidenceComplete: true},
		TraceDescriptor: compiled.traceDescriptor,
	}
	seen := map[string]bool{}
	objects := map[string]string{}
	observed, candidatesTotal, payloadBytes := 0, 0, 0
	hasCandidates := false
	for index, branch := range compiled.branches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		child, err := s.runCompiled(ctx, query, branch)
		if err != nil {
			return nil, err
		}
		candidates := max(child.Execution.MatchRows, child.Execution.CandidateRows, child.EvaluationRows)
		if branch.plan.Result.Where != nil || len(branch.plan.Result.Unwinds) > 0 || len(branch.plan.Stages) > 0 {
			hasCandidates = true
			candidatesTotal += child.Execution.CandidateRows
		} else {
			candidatesTotal += child.Execution.MatchRows
		}
		observed += candidates
		if observed > graphMatchBudget {
			return nil, unionFailure(ctx, "UNION exceeded its shared match budget; no complete result is available")
		}
		result.Execution.MatchRows += child.Execution.MatchRows
		result.Execution.ConclusionsLimited = result.Execution.ConclusionsLimited || child.Execution.ConclusionsLimited
		if index == 0 {
			result.Columns = append([]interfaces.RawQueryColumn(nil), child.Columns...)
			for name, typ := range child.ValueTypes {
				result.ValueTypes[name] = typ
			}
		} else {
			for name, typ := range child.ValueTypes {
				if !reflect.DeepEqual(result.ValueTypes[name], typ) {
					result.ValueTypes[name] = &interfaces.CypherValueType{Kind: "any"}
				}
			}
		}
		for _, entry := range child.Entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Check before DISTINCT: a duplicate row must not hide inconsistent
			// snapshots of a graph identity between separate observations.
			if err := checkGraphObservation(entry, objects); err != nil {
				return nil, rest.NewHTTPError(ctx, http.StatusBadGateway, berrors.BknBackend_Cypher_QueryFailed).WithErrorDetails("UNION observations contain inconsistent graph snapshots")
			}
			encoded, err := json.Marshal(entry)
			if err != nil {
				return nil, err
			}
			payloadBytes += len(encoded) + 64
			if payloadBytes > graphPayloadBudget {
				return nil, unionFailure(ctx, "UNION exceeded its shared payload budget; no complete result is available")
			}
			if !compiled.plan.UnionAll {
				key, err := expressionKey(entry)
				if err != nil {
					return nil, err
				}
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			result.Entries = append(result.Entries, entry)
		}
	}
	if hasCandidates {
		result.Execution.CandidateRows = candidatesTotal
	}
	for index := range result.Columns {
		result.Columns[index].Type = result.ValueTypes[result.Columns[index].Name].Kind
	}
	// The endpoint's default cap applies once, after complete set evaluation.
	if len(result.Entries) > interfaces.CYPHER_DEFAULT_LIMIT {
		result.Execution.ConclusionsLimited = true
		result.Entries = result.Entries[:interfaces.CYPHER_DEFAULT_LIMIT]
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(encoded) > graphPayloadBudget {
		return nil, unionFailure(ctx, "UNION response exceeds its payload budget")
	}
	return result, nil
}
