// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

type graphFactEdge struct {
	key, source, target string
	value               interfaces.CypherRelationValue
}
type graphFactModel struct {
	nodes              map[string]interfaces.CypherObjectValue
	types              map[string][]string
	outgoing, incoming map[string][]graphFactEdge
	count              int
}
type graphPathSegment struct {
	nodes         []interfaces.CypherObjectValue
	relationships []interfaces.CypherRelationValue
}

func (s *cypherQueryService) readPatternFacts(ctx context.Context, query interfaces.CypherQuery, compiled *compiledQuery, budget *pipelineBudget, seen map[string]string) (*graphFactModel, error) {
	model := &graphFactModel{nodes: map[string]interfaces.CypherObjectValue{}, types: map[string][]string{}, outgoing: map[string][]graphFactEdge{}, incoming: map[string][]graphFactEdge{}}
	edges := []interfaces.CypherRelationValue{}
	for _, observation := range compiled.observations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := s.execute(ctx, query, observation.plan, interfaces.RawQueryRequest{Query: observation.statement, QueryFormat: interfaces.VEGA_QUERY_FORMAT_SQL, InputDialect: interfaces.VEGA_DIALECT_MYSQL, Paging: interfaces.ResourceDataPagingRequest{Mode: interfaces.VEGA_PAGING_MODE_SINGLE, Limit: observation.rowLimit}, QueryTimeoutSec: interfaces.CYPHER_DEFAULT_TIMEOUT_SEC})
		if err != nil {
			return nil, executionError(ctx, err)
		}
		if response == nil {
			return nil, errors.New("missing graph observation")
		}
		model.count += len(response.Entries)
		if budget.rows+model.count > graphMatchBudget || response.TotalCount > int64(len(response.Entries)) || (response.Paging != nil && response.Paging.NextCursor != nil) {
			return nil, errGraphBudget
		}
		for _, row := range response.Entries {
			encoded, err := json.Marshal(row)
			if err != nil {
				return nil, err
			}
			budget.bytes += len(encoded) + 64
			if budget.bytes > graphPayloadBudget {
				return nil, errGraphBudget
			}
			value, err := evaluateGraph(observation.plan, observation.plan.Result.Projections[0].Value, row)
			if err != nil {
				return nil, err
			}
			if err := checkGraphObservation(value, seen); err != nil {
				return nil, err
			}
			switch fact := value.(type) {
			case interfaces.CypherObjectValue:
				key, err := expressionKey(fact)
				if err != nil {
					return nil, err
				}
				if _, exists := model.nodes[key]; !exists {
					model.nodes[key] = fact
					model.types[fact.ObjectTypeID] = append(model.types[fact.ObjectTypeID], key)
				}
			case interfaces.CypherRelationValue:
				edges = append(edges, fact)
			default:
				return nil, expressionTypeError()
			}
		}
	}
	unique := map[string]bool{}
	for _, relation := range edges {
		source, err := expressionKey(relation.Source)
		if err != nil {
			return nil, err
		}
		target, err := expressionKey(relation.Target)
		if err != nil {
			return nil, err
		}
		src, sourceOK := model.nodes[source]
		dst, targetOK := model.nodes[target]
		if !sourceOK || !targetOK {
			return nil, errors.New("graph observations disagree on visible endpoints")
		}
		relation.Source, relation.Target = src, dst
		key, err := expressionKey(relation)
		if err != nil {
			return nil, err
		}
		if unique[key] {
			continue
		}
		unique[key] = true
		edge := graphFactEdge{key: key, source: source, target: target, value: relation}
		model.outgoing[source] = append(model.outgoing[source], edge)
		model.incoming[target] = append(model.incoming[target], edge)
	}
	return model, nil
}

func copyPatternBindings(row map[string]any) map[string]any {
	copy := make(map[string]any, len(row)+1)
	for name, value := range row {
		copy[name] = value
	}
	return copy
}

func (s *cypherQueryService) runGraphPattern(ctx context.Context, query interfaces.CypherQuery, compiled *compiledQuery, stage PlanStage, incoming []map[string]any, budget *pipelineBudget, seen map[string]string) ([]map[string]any, int, error) {
	state := requestEvaluationState(ctx)
	model := state.models[compiled.plan]
	candidates := 0
	if model == nil {
		var err error
		model, err = s.readPatternFacts(ctx, query, compiled, budget, seen)
		if err != nil {
			return nil, 0, pipelineError(ctx, err)
		}
		state.models[compiled.plan] = model
		candidates = model.count
	}
	graph := compiled.plan.Pattern
	output := []map[string]any{}
	tick := func() error {
		budget.comparisons++
		if budget.comparisons > graphMatchBudget*8 {
			return errGraphBudget
		}
		return ctx.Err()
	}
	for _, source := range incoming {
		nodes := make([]*interfaces.CypherObjectValue, len(graph.Pattern.Nodes))
		bound := copyPatternBindings(source)
		compatible := true
		for i, node := range graph.Pattern.Nodes {
			if node.Variable == "" || node.Anonymous {
				continue
			}
			if previous, exists := source["unwind:"+node.Variable]; exists {
				value, ok := previous.(interfaces.CypherObjectValue)
				if !ok {
					compatible = false
					break
				}
				if _, ok := graph.NodeChoices[i][value.ObjectTypeID]; !ok {
					compatible = false
					break
				}
				key, err := expressionKey(value)
				if err != nil {
					return nil, 0, pipelineError(ctx, err)
				}
				fresh, exists := model.nodes[key]
				if !exists {
					compatible = false
					break
				}
				nodes[i] = &fresh
			}
		}
		segments := make([]graphPathSegment, len(graph.Pattern.Edges))
		used := map[string]bool{}
		matched := false
		var finish func(int) error
		finish = func(index int) error {
			if index < len(nodes) {
				if nodes[index] != nil {
					return finish(index + 1)
				}
				for _, id := range sortedGraphKeys(graph.NodeChoices[index]) {
					for _, key := range model.types[id] {
						if err := tick(); err != nil {
							return err
						}
						value := model.nodes[key]
						nodes[index] = &value
						if err := finish(index + 1); err != nil {
							return err
						}
					}
				}
				nodes[index] = nil
				return nil
			}
			row := copyPatternBindings(bound)
			for i, node := range graph.Pattern.Nodes {
				if node.Variable == "" || node.Anonymous {
					continue
				}
				value, err := patternNodeValue(*nodes[i], graph.NodeChoices[i][nodes[i].ObjectTypeID])
				if err != nil {
					return err
				}
				if _, exists := row["unwind:"+node.Variable]; !exists {
					row["unwind:"+node.Variable] = value
				}
				if err := checkGraphObservation(value, seen); err != nil {
					return err
				}
			}
			for _, path := range graph.Pattern.Paths {
				value := interfaces.CypherPathValue{Kind: "path", Nodes: []interfaces.CypherObjectValue{}, Relationships: []interfaces.CypherRelationValue{}}
				if len(path.Edges) == 0 {
					value.Nodes = append(value.Nodes, *nodes[path.Nodes[0]])
				}
				for _, index := range path.Edges {
					segment := segments[index]
					if len(value.Nodes) == 0 {
						value.Nodes = append(value.Nodes, segment.nodes...)
					} else {
						value.Nodes = append(value.Nodes, segment.nodes[1:]...)
					}
					value.Relationships = append(value.Relationships, segment.relationships...)
				}
				row["unwind:"+path.Variable] = value
			}
			accepted, err := evaluateCondition(compiled.plan, compiled.plan.Result.Where, row)
			if err != nil {
				return err
			}
			if !accepted {
				return nil
			}
			output = append(output, row)
			matched = true
			if len(output) > graphMatchBudget {
				return errGraphBudget
			}
			return nil
		}
		var matchEdge func(int, int) error
		matchEdge = func(index int, totalHops int) error {
			if index == len(graph.Pattern.Edges) {
				return finish(0)
			}
			edge := graph.Pattern.Edges[index]
			starts := []string{}
			if nodes[edge.Left] != nil {
				key, err := expressionKey(*nodes[edge.Left])
				if err != nil {
					return err
				}
				starts = append(starts, key)
			} else {
				for _, id := range sortedGraphKeys(graph.NodeChoices[edge.Left]) {
					starts = append(starts, model.types[id]...)
				}
			}
			originalLeft, originalRight := nodes[edge.Left], nodes[edge.Right]
			for _, start := range starts {
				if err := tick(); err != nil {
					return err
				}
				first := model.nodes[start]
				nodes[edge.Left] = &first
				minHops, maxHops := 1, 1
				if edge.VariableLength {
					minHops, maxHops = edge.MinHops, edge.MaxHops
				}
				segment := graphPathSegment{nodes: []interfaces.CypherObjectValue{first}, relationships: []interfaces.CypherRelationValue{}}
				var walk func(string, graphPathSegment) error
				walk = func(current string, path graphPathSegment) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					depth := len(path.relationships)
					endpoint := model.nodes[current]
					_, allowed := graph.NodeChoices[edge.Right][endpoint.ObjectTypeID]
					if originalRight != nil {
						key, err := expressionKey(*originalRight)
						if err != nil {
							return err
						}
						allowed = allowed && key == current
					}
					if edge.Left == edge.Right {
						allowed = allowed && current == start
					}
					if depth >= minHops && allowed {
						nodes[edge.Right] = &endpoint
						segments[index] = path
						name := "unwind:" + edge.Variable
						previous, hadPrevious := bound[name]
						accepted := true
						if edge.Variable != "" {
							var value any
							if edge.VariableLength {
								list := []any{}
								for _, relation := range path.relationships {
									list = append(list, relation)
								}
								value = list
							} else {
								value = path.relationships[0]
							}
							if hadPrevious {
								equal, err := expressionEquality(previous, value)
								if err != nil {
									return err
								}
								accepted = equal == true
							} else {
								bound[name] = value
							}
						}
						if accepted {
							if err := matchEdge(index+1, totalHops+depth); err != nil {
								return err
							}
						}
						if !hadPrevious {
							delete(bound, name)
						}
						nodes[edge.Right] = originalRight
						if edge.Left == edge.Right {
							nodes[edge.Left] = &first
						}
					}
					if maxHops >= 0 && depth >= maxHops {
						return nil
					}
					candidates := []graphFactEdge{}
					if edge.Direction != Incoming {
						candidates = append(candidates, model.outgoing[current]...)
					}
					if edge.Direction != Outgoing {
						candidates = append(candidates, model.incoming[current]...)
					}
					visited := map[string]bool{}
					for _, candidate := range candidates {
						if err := tick(); err != nil {
							return err
						}
						if _, ok := graph.EdgeChoices[index][candidate.value.RelationTypeID]; !ok {
							continue
						}
						next := candidate.target
						if edge.Direction == Incoming || (edge.Direction == Undirected && candidate.source != current) {
							next = candidate.source
						}
						unique := strconv.Itoa(edge.Clause) + ":" + candidate.key
						if used[unique] || visited[candidate.key] {
							continue
						}
						visited[candidate.key] = true
						if totalHops+depth >= interfaces.CYPHER_MAX_PATH_LENGTH {
							return errGraphBudget
						}
						used[unique] = true
						nextPath := graphPathSegment{nodes: append(append([]interfaces.CypherObjectValue{}, path.nodes...), model.nodes[next]), relationships: append(append([]interfaces.CypherRelationValue{}, path.relationships...), candidate.value)}
						if err := walk(next, nextPath); err != nil {
							return err
						}
						delete(used, unique)
					}
					return nil
				}
				if err := walk(start, segment); err != nil {
					return err
				}
				nodes[edge.Left], nodes[edge.Right] = originalLeft, originalRight
			}
			return nil
		}
		if compatible {
			if err := matchEdge(0, 0); err != nil {
				return nil, 0, pipelineError(ctx, err)
			}
		}
		if !matched && stage.Optional {
			row := copyPatternBindings(source)
			for _, projection := range compiled.plan.Result.Projections {
				key := "unwind:" + projection.Alias
				if _, exists := row[key]; !exists {
					row[key] = nil
				}
			}
			output = append(output, row)
			if len(output) > graphMatchBudget {
				return nil, 0, pipelineError(ctx, errGraphBudget)
			}
		}
	}
	return output, candidates, nil
}
