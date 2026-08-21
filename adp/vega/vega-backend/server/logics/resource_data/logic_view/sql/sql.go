// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sql

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	sq "github.com/Masterminds/squirrel"
	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

type cachedSql struct {
	Query string
	Args  []any
}

// logicViewSQLGenerator is used to generate SQL
type logicViewSQLGenerator struct {
	nodes         map[string]*interfaces.LogicDefinitionNode
	outputNode    *interfaces.LogicDefinitionNode
	sqls          map[string]cachedSql
	nodeFieldsMap map[string]map[string]*interfaces.ViewProperty
	RefResources  map[string]*interfaces.Resource
	viewFieldMap  map[string]*interfaces.Property
}

// NewlogicViewSQLGenerator creates SQL generators
func NewlogicDefinitionSQLGenerator(view *interfaces.LogicView) *logicViewSQLGenerator {
	nodeMap := make(map[string]*interfaces.LogicDefinitionNode)
	var outputNode *interfaces.LogicDefinitionNode
	nodes := view.LogicDefinition
	for i := range nodes {
		nodeMap[nodes[i].ID] = nodes[i]
		if nodes[i].Type == interfaces.LogicDefinitionNodeType_Output {
			outputNode = nodes[i]
		}
	}

	viewFieldMap := make(map[string]*interfaces.Property)
	for _, field := range view.SchemaDefinition {
		viewFieldMap[field.Name] = field
	}

	return &logicViewSQLGenerator{
		nodes:         nodeMap,
		outputNode:    outputNode,
		sqls:          make(map[string]cachedSql),
		nodeFieldsMap: make(map[string]map[string]*interfaces.ViewProperty),
		RefResources:  view.RefResources,
		viewFieldMap:  viewFieldMap,
	}
}

// BuildLogicViewSQL: SQL for constructing logical views
func (g *logicViewSQLGenerator) BuildLogicDefinitionSQL(ctx context.Context, res *interfaces.LogicView) (string, error) {
	sql, args, err := g.buildLogicDefinitionSQLWithDepth(ctx, &res.Resource, interfaces.MaxRecursionDepth)
	if err != nil {
		return "", err
	}
	// Parameter interpolation is performed here to be compatible with downstream actuators that only support a single SQL string
	return g.interpolate(sql, args)
}

func (g *logicViewSQLGenerator) buildLogicDefinitionSQLWithDepth(ctx context.Context, res *interfaces.Resource, depth int) (string, []any, error) {
	if depth <= 0 {
		return "", nil, fmt.Errorf("max recursion depth (%d) exceeded, possible circular reference in logic view", interfaces.MaxRecursionDepth)
	}

	if res.LogicDefinition == nil {
		return "", nil, fmt.Errorf("logic definition is empty")
	}

	if g.outputNode == nil {
		return "", nil, fmt.Errorf("custom view '%s' output node not found", res.Name)
	}

	// 2. Build recursively starting from the output node
	if len(g.outputNode.Inputs) == 0 {
		return "", nil, fmt.Errorf("output node has no input")
	}

	sql, args, err := g.buildNodeSQL(ctx, g.outputNode.ID, depth)
	if err != nil {
		return "", nil, fmt.Errorf("build custom view '%s' sql failed: %w", res.Name, err)
	}

	return sql, args, nil
}

// buildNodeSQL generates SQL for the specified node
func (g *logicViewSQLGenerator) buildNodeSQL(ctx context.Context, nodeID string, depth int) (string, []any, error) {
	if res, ok := g.sqls[nodeID]; ok {
		// Return the Args after cloning to prevent cache pollution caused by external append modifications
		argsCopy := make([]any, len(res.Args))
		copy(argsCopy, res.Args)
		return res.Query, argsCopy, nil
	}

	node, ok := g.nodes[nodeID]
	if !ok {
		return "", nil, fmt.Errorf("node %s not found", nodeID)
	}

	var sql string
	var args []any
	var err error

	switch node.Type {
	case interfaces.LogicDefinitionNodeType_Resource:
		sql, args, err = g.buildResourceNodeSQL(ctx, node, depth)
	case interfaces.LogicDefinitionNodeType_Join:
		sql, args, err = g.buildJoinNodeSQL(ctx, node, depth)
	case interfaces.LogicDefinitionNodeType_Union:
		sql, args, err = g.buildUnionNodeSQL(ctx, node, depth)
	case interfaces.LogicDefinitionNodeType_Sql:
		sql, args, err = g.buildSqlNodeSQL(ctx, node, depth)
	case interfaces.LogicDefinitionNodeType_Output:
		sql, args, err = g.buildOutputNodeSQL(ctx, node, depth)
	default:
		return "", nil, fmt.Errorf("unsupported node type: %s", node.Type)
	}

	if err != nil {
		return "", nil, err
	}

	g.sqls[nodeID] = cachedSql{Query: sql, Args: args}
	return sql, args, nil
}

// buildResourceNodeSQL to construct the SQL for resource nodes
func (g *logicViewSQLGenerator) buildResourceNodeSQL(ctx context.Context,
	node *interfaces.LogicDefinitionNode, depth int) (string, []any, error) {

	var cfg interfaces.ResourceNodeCfg
	if err := mapstructure.Decode(node.Config, &cfg); err != nil {
		return "", nil, fmt.Errorf("failed to decode resource node config: %w", err)
	}

	resource := g.RefResources[cfg.ResourceID]

	// If the resource itself is also a logical view, recursive construction (consuming one layer of depth)
	if resource.Category == interfaces.ResourceCategoryLogicView {
		return g.buildLogicDefinitionSQLWithDepth(ctx, resource, depth-1)
	}

	// Build the original field mapping for filtering and alias use
	fieldMap := make(map[string]*interfaces.Property)
	for _, prop := range resource.SchemaDefinition {
		fieldMap[prop.Name] = prop
	}

	// Build a list of SELECT fields
	var fields []string
	outputFieldsMap := make(map[string]*interfaces.ViewProperty)
	if len(node.OutputFields) > 0 {
		fields = make([]string, 0, len(node.OutputFields))
		for _, f := range node.OutputFields {
			outputFieldsMap[f.Name] = f // Maintenance status

			sourceProp, ok := fieldMap[f.Name]
			if !ok {
				fields = append(fields, QuotationMark(f.Name))
			} else {
				if sourceProp.OriginalName != "" && sourceProp.OriginalName != f.Name {
					// Use QuotationMark instead of hard-coded quotation marks to support multiple databases
					fields = append(fields, fmt.Sprintf("%s AS %s",
						QuotationMark(sourceProp.OriginalName),
						QuotationMark(f.Name)))
				} else {
					fields = append(fields, QuotationMark(f.Name))
				}
			}
		}
	} else {
		fields = []string{"*"}
	}
	// Maintain the output fields map of each node (the core function of A)
	g.nodeFieldsMap[node.ID] = outputFieldsMap

	// Build the table source
	builder := sq.Select(fields...).From(fmt.Sprintf("{{%s}}", resource.ID)).PlaceholderFormat(sq.Dollar)

	// Process deduplication
	if cfg.Distinct {
		builder = builder.Distinct()
	}

	// Handle the filtration conditions
	filterCond, filterArgs, err := g.buildFilterSQL(ctx, cfg.Filters, fieldMap)
	if err != nil {
		return "", nil, fmt.Errorf("failed to build resource node filter: %w", err)
	}
	if filterCond != nil {
		builder = builder.Where(filterCond)
	}

	sqlStr, args, err := builder.ToSql()
	if err != nil {
		return "", nil, err
	}
	// Merge filtering parameters
	args = append(args, filterArgs...)
	return sqlStr, args, nil
}

// buildFilterSQL converts FilterCondCfg to squirrel conditions
func (g *logicViewSQLGenerator) buildFilterSQL(ctx context.Context, filters *interfaces.FilterCondCfg,
	fieldMap map[string]*interfaces.Property) (sq.Sqlizer, []any, error) {

	if filters == nil {
		return nil, nil, nil
	}

	// filters comes from the node config stored in the view definition — resource, join and
	// union nodes all land here — which is server-side data the caller cannot edit. Applying
	// the new like contract to it would let one upgrade break an existing view, so those
	// conditions keep their pre-change behaviour and only get a warning.
	if marked := filter_condition.MarkLegacyLikeWildcards(filters); marked > 0 {
		logger.Warnf("%d stored like/not_like condition(s) in this logic view use '%%' as a wildcard; "+
			"kept on the pre-change behaviour of this backend. Escape it as '\\%%' or switch the condition to [regex] in the view definition.",
			marked)
	}

	filterCond, err := filter_condition.NewFilterCondition(ctx, filters, fieldMap)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create filter condition: %w", err)
	}
	if filterCond == nil {
		return nil, nil, nil
	}

	sqlCond, err := g.ConvertFilterCondition(ctx, filterCond, fieldMap)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert filter condition: %w", err)
	}

	if sqlCond != nil {
		return sqlCond, nil, nil
	}

	// natively. logicViewSQLGenerator handles this via ConvertFilterCondition now.
	// We'll leave it as a TODO or return a mock for now
	return sq.Expr("1=1"), nil, nil
}

// buildJoinNodeSQL: SQL for building a JOIN node
func (g *logicViewSQLGenerator) buildJoinNodeSQL(ctx context.Context, node *interfaces.LogicDefinitionNode, depth int) (string, []any, error) {
	var cfg interfaces.JoinNodeCfg
	if err := mapstructure.Decode(node.Config, &cfg); err != nil {
		return "", nil, fmt.Errorf("failed to decode join node config: %w", err)
	}

	if len(node.Inputs) != 2 {
		return "", nil, fmt.Errorf("join node must have exactly 2 inputs, got %d", len(node.Inputs))
	}

	leftID := node.Inputs[0]
	rightID := node.Inputs[1]

	leftSQL, leftArgs, err := g.buildNodeSQL(ctx, leftID, depth)
	if err != nil {
		return "", nil, fmt.Errorf("failed to build left input for join: %w", err)
	}
	rightSQL, rightArgs, err := g.buildNodeSQL(ctx, rightID, depth)
	if err != nil {
		return "", nil, fmt.Errorf("failed to build right input for join: %w", err)
	}

	// Build a list of SELECT fields and use from/from_node to determine the source
	fields := make([]string, 0, len(node.OutputFields))
	outputFieldsMap := make(map[string]*interfaces.ViewProperty)
	for _, f := range node.OutputFields {
		outputFieldsMap[f.Name] = f // Maintenance status

		alias := "l"
		if f.FromNode == rightID {
			alias = "r"
		}
		// from is the source field name and name is the output field name.
		srcField := f.From
		if srcField == "" {
			srcField = f.Name
		}
		// Use QuotationMark instead of hard-coded quotation marks to support multiple databases
		fields = append(fields, fmt.Sprintf("%s.%s AS %s", alias, QuotationMark(srcField), QuotationMark(f.Name)))
	}
	// Maintain the output fields map of each node
	g.nodeFieldsMap[node.ID] = outputFieldsMap

	// Construct the JOIN ON condition
	joinOnParts := make([]string, 0, len(cfg.JoinOn))
	for _, on := range cfg.JoinOn {
		joinOnParts = append(joinOnParts, fmt.Sprintf("l.%s = r.%s", QuotationMark(on.LeftField), QuotationMark(on.RightField)))
	}
	joinOn := strings.Join(joinOnParts, " AND ")

	joinType := strings.ToUpper(cfg.JoinType)
	if joinType == "" {
		joinType = "INNER"
	}

	// Merge parameters: Note that do not directly append to leftArgs to avoid contamination
	allArgs := make([]any, 0, len(leftArgs)+len(rightArgs))
	allArgs = append(allArgs, leftArgs...)
	allArgs = append(allArgs, rightArgs...)

	sqlStr := fmt.Sprintf("SELECT %s FROM ((%s) AS l %s JOIN (%s) AS r ON %s)",
		strings.Join(fields, ", "), leftSQL, joinType, rightSQL, joinOn)

	// Handle the filtering conditions of the Join node itself
	if cfg.Filters != nil {
		// A temporary fieldMap needs to be constructed for the fields after the Join
		joinFieldMap := make(map[string]*interfaces.Property)
		for _, f := range node.OutputFields {
			joinFieldMap[f.Name] = &interfaces.Property{
				Name:         f.Name,
				Type:         f.Type,
				OriginalName: f.From,
			}
		}

		filterCond, filterArgs, err := g.buildFilterSQL(ctx, cfg.Filters, joinFieldMap)
		if err != nil {
			return "", nil, fmt.Errorf("failed to build join node filter: %w", err)
		}
		if filterCond != nil {
			whereSql, whereArgs, err := filterCond.ToSql()
			if err != nil {
				return "", nil, fmt.Errorf("failed to convert join filter to SQL: %w", err)
			}
			sqlStr = fmt.Sprintf("SELECT * FROM (%s) AS j WHERE %s", sqlStr, whereSql)
			allArgs = append(allArgs, whereArgs...)
			allArgs = append(allArgs, filterArgs...)
		}
	}

	return sqlStr, allArgs, nil
}

// buildUnionNodeSQL: SQL for building UNION nodes
func (g *logicViewSQLGenerator) buildUnionNodeSQL(ctx context.Context, node *interfaces.LogicDefinitionNode, depth int) (string, []any, error) {
	var cfg interfaces.UnionNodeCfg
	if err := mapstructure.Decode(node.Config, &cfg); err != nil {
		return "", nil, fmt.Errorf("failed to decode union node config: %w", err)
	}

	unionParts := make([]string, 0, len(node.Inputs))
	var allArgs []any

	for i, inputID := range node.Inputs {
		subSQL, subArgs, err := g.buildNodeSQL(ctx, inputID, depth)
		if err != nil {
			return "", nil, fmt.Errorf("failed to build union input %d: %w", i, err)
		}

		inputNodeFieldsMap, _ := g.GetNodeFieldsMap(inputID)
		inputNodeType, _ := g.GetNodeType(inputID)

		fields := make([]string, 0, len(node.OutputFields))
		for _, outField := range node.OutputFields {
			outputFieldName := outField.Name
			srcField := outField.Name // Default alignment of fields with the same name

			// Search for the original field corresponding to the current input node from FromList
			for _, ref := range outField.FromList {
				if ref.FromNode == inputID {
					if ref.From != "" {
						srcField = ref.From
					}
					break
				}
			}

			// Compatible with the old logic: If it is a Resource node, try to obtain the OriginalName
			if inputNodeType == interfaces.LogicDefinitionNodeType_Resource && inputNodeFieldsMap != nil {
				if inputField, ok := inputNodeFieldsMap[srcField]; ok {
					fields = append(fields, fmt.Sprintf("%s AS %s", QuotationMark(inputField.OriginalName), QuotationMark(outputFieldName)))
				} else {
					fields = append(fields, fmt.Sprintf("%s AS %s", QuotationMark(srcField), QuotationMark(outputFieldName)))
				}
			} else {
				fields = append(fields, fmt.Sprintf("%s AS %s", QuotationMark(srcField), QuotationMark(outputFieldName)))
			}
		}

		allArgs = append(allArgs, subArgs...)
		unionParts = append(unionParts, fmt.Sprintf("SELECT %s FROM (%s) AS u%d",
			strings.Join(fields, ", "), subSQL, i))
	}

	unionOp := "UNION ALL"
	if cfg.UnionType == interfaces.UnionType_Distinct {
		unionOp = "UNION"
	}

	// Maintain the output status
	outputFieldsMap := make(map[string]*interfaces.ViewProperty)
	for _, field := range node.OutputFields {
		outputFieldsMap[field.Name] = field
	}
	g.nodeFieldsMap[node.ID] = outputFieldsMap

	sql := strings.Join(unionParts, " "+unionOp+" ")

	// Handle the filtering conditions after UNION
	if cfg.Filters != nil {
		filterCond, filterArgs, err := g.buildFilterSQL(ctx, cfg.Filters, nil)
		if err != nil {
			return "", nil, fmt.Errorf("failed to build union node filter: %w", err)
		}
		if filterCond != nil {
			whereSql, whereArgs, err := filterCond.ToSql()
			if err != nil {
				return "", nil, fmt.Errorf("failed to convert union filter to SQL: %w", err)
			}
			sql = fmt.Sprintf("SELECT * FROM (%s) AS union_result WHERE %s", sql, whereSql)
			allArgs = append(allArgs, whereArgs...)
			allArgs = append(allArgs, filterArgs...)
		}
	}

	return "SELECT * FROM (" + sql + ") AS union_final", allArgs, nil
}

// Build custom SQL nodes with buildSqlNodeSQL
func (g *logicViewSQLGenerator) buildSqlNodeSQL(ctx context.Context, node *interfaces.LogicDefinitionNode, depth int) (string, []any, error) {
	var cfg interfaces.SQLNodeCfg
	if err := mapstructure.Decode(node.Config, &cfg); err != nil {
		return "", nil, fmt.Errorf("failed to decode sql node config: %w", err)
	}

	// Maintenance status
	outputFieldsMap := make(map[string]*interfaces.ViewProperty)
	for _, field := range node.OutputFields {
		outputFieldsMap[field.Name] = field
	}
	g.nodeFieldsMap[node.ID] = outputFieldsMap

	var allArgs []any

	// Pre-build the SQL for all input nodes and generate aliases
	type nodeContext struct {
		SQL     string
		Alias   string
		WithSQL string // Full SQL with alias: (subquery) AS alias
	}
	nodeContexts := make(map[string]*nodeContext)

	for _, inputID := range node.Inputs {
		subSQL, subArgs, err := g.buildNodeSQL(ctx, inputID, depth)
		if err != nil {
			return "", nil, fmt.Errorf("failed to build sql node input %s: %w", inputID, err)
		}
		allArgs = append(allArgs, subArgs...)

		// Generate a unique alias: Replace the special characters in the node ID with underscores
		alias := sanitizeAlias(inputID)

		nodeContexts[inputID] = &nodeContext{
			SQL:     subSQL,
			Alias:   alias,
			WithSQL: fmt.Sprintf("(%s) AS %s", subSQL, alias),
		}
	}

	// Create a template function mapping
	funcMap := template.FuncMap{
		// node function: Returns subquery SQL with aliases
		"node": func(nodeID string) (string, error) {
			ctx, ok := nodeContexts[nodeID]
			if !ok {
				return "", fmt.Errorf("unknown node ID in template: %s", nodeID)
			}
			return ctx.WithSQL, nil
		},
		// nodeSQL function: Only returns subquery SQL (without aliases and parentheses)
		"nodeSQL": func(nodeID string) (string, error) {
			ctx, ok := nodeContexts[nodeID]
			if !ok {
				return "", fmt.Errorf("unknown node ID in template: %s", nodeID)
			}
			return ctx.SQL, nil
		},
		// The nodeAlias function: Returns the alias of the node
		"nodeAlias": func(nodeID string) (string, error) {
			ctx, ok := nodeContexts[nodeID]
			if !ok {
				return "", fmt.Errorf("unknown node ID in template: %s", nodeID)
			}
			return ctx.Alias, nil
		},
	}

	// Prepare the template context: Provide two reference methods
	// 1. Directly using.node1 will obtain SQL with aliases (backward compatible)
	// 2. Use the template functions node()/nodeSQL()/nodeAlias() to obtain different forms
	contextMap := make(map[string]string)
	for inputID, ctx := range nodeContexts {
		contextMap[inputID] = ctx.WithSQL
	}

	// Parsing template
	tmpl, err := template.New("sql").Funcs(funcMap).Parse(cfg.SQL)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse SQL template for node %s: %w", node.ID, err)
	}

	var result strings.Builder
	if err := tmpl.Execute(&result, contextMap); err != nil {
		return "", nil, fmt.Errorf("failed to execute SQL template for node %s: %w", node.ID, err)
	}

	// Remove the semicolons at the end of SQL (there may be multiple semicolons or Spaces)
	finalSQL := strings.TrimSpace(result.String())
	for strings.HasSuffix(finalSQL, ";") {
		finalSQL = strings.TrimSuffix(finalSQL, ";")
		finalSQL = strings.TrimSpace(finalSQL)
	}

	return finalSQL, allArgs, nil
}

// sanitizeAlias cleans up node ids to generate legitimate SQL aliases
func sanitizeAlias(nodeID string) string {
	// Replace all non-alphanumeric characters with underscores
	alias := regexp.MustCompile(`[^a-zA-Z0-9_]`).ReplaceAllString(nodeID, "_")
	// If it starts with a number, add a prefix
	if len(alias) > 0 && alias[0] >= '0' && alias[0] <= '9' {
		alias = "n_" + alias
	}
	// Length limit (MySQL identifier maximum 64 characters)
	if len(alias) > 60 {
		alias = alias[:60]
	}
	return alias
}

// GetNodeFieldsMap gets the output field map of the node
func (g *logicViewSQLGenerator) GetNodeFieldsMap(nodeID string) (map[string]*interfaces.ViewProperty, error) {
	nodeMap, ok := g.nodeFieldsMap[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %s fields map not found", nodeID)
	}
	return nodeMap, nil
}

// Get the node type with GetNodeType
func (g *logicViewSQLGenerator) GetNodeType(nodeID string) (string, error) {
	node, ok := g.nodes[nodeID]
	if !ok {
		return "", fmt.Errorf("node %s not found", nodeID)
	}
	return node.Type, nil
}

// interpolate implements parameter interpolation, filling args into the query?
func (g *logicViewSQLGenerator) interpolate(query string, args []any) (string, error) {
	if len(args) == 0 {
		return query, nil
	}

	parts := strings.Split(query, "?")
	if len(parts)-1 != len(args) {
		return "", fmt.Errorf("placeholder count (%d) does not match args count (%d)", len(parts)-1, len(args))
	}

	var sb strings.Builder
	for i, part := range parts {
		sb.WriteString(part)
		if i < len(args) {
			sb.WriteString(formatArg(args[i]))
		}
	}
	return sb.String(), nil
}

func formatArg(arg any) string {
	switch v := arg.(type) {
	case string:
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	case int, int64, int32, int16, int8, uint, uint64, uint32, uint16, uint8:
		return fmt.Sprintf("%v", v)
	case float64, float32:
		return fmt.Sprintf("%g", v)
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "1"
		}
		return "0"
	case nil:
		return "NULL"
	default:
		return fmt.Sprintf("'%v'", v)
	}
}

// buildOutputNodeSQL to construct the SQL of the output node
func (g *logicViewSQLGenerator) buildOutputNodeSQL(ctx context.Context, node *interfaces.LogicDefinitionNode, depth int) (string, []any, error) {
	if len(node.Inputs) != 1 {
		return "", nil, fmt.Errorf("output node %s requires exactly one input node", node.ID)
	}

	// Build a list of SELECT fields
	var fields []string
	outputFieldsMap := make(map[string]*interfaces.ViewProperty)
	if len(node.OutputFields) > 0 {
		fields = make([]string, 0, len(node.OutputFields))

		// First, build the SQL of the upstream node to obtain the output field mapping of the upstream node
		upstreamNodeID := node.Inputs[0]
		_, _, err := g.buildNodeSQL(ctx, upstreamNodeID, depth)
		if err != nil {
			return "", nil, fmt.Errorf("failed to build upstream node SQL for output node %s: %w", node.ID, err)
		}

		// Obtain field information from the output field mapping of the upstream node
		upstreamFieldsMap, hasUpstreamFields := g.nodeFieldsMap[upstreamNodeID]

		for _, f := range node.OutputFields {
			outputFieldsMap[f.Name] = f // Maintenance status

			// Try to search from the output field of the upstream node
			var sourceField *interfaces.ViewProperty
			if hasUpstreamFields {
				sourceField = upstreamFieldsMap[f.Name]
			}

			if sourceField == nil {
				// If there is no field mapping upstream, use the field name directly
				fields = append(fields, QuotationMark(f.Name))
			} else {
				// Use the output field name of the upstream node (which may have been renamed)
				// sourceField.Name is the field name (alias) output by the upstream node.
				// f.Name is the expected output field name of the current node
				if sourceField.Name != f.Name {
					// The field names are different and require aliases
					fields = append(fields, fmt.Sprintf("%s AS %s",
						QuotationMark(sourceField.Name),
						QuotationMark(f.Name)))
				} else {
					// If the field names are the same, use them directly
					fields = append(fields, QuotationMark(f.Name))
				}
			}
		}
	} else {
		fields = []string{"*"}
	}
	g.nodeFieldsMap[node.ID] = outputFieldsMap

	sql, args, err := g.buildNodeSQL(ctx, node.Inputs[0], depth)
	return "SELECT " + strings.Join(fields, ", ") + " FROM (" + sql + ") AS " + sanitizeAlias(node.ID), args, err
}

// Build sort
func buildSQLSortParams(sort []*interfaces.SortField) string {
	if len(sort) == 0 {
		return ""
	}

	var sortSql strings.Builder
	for i, sortParam := range sort {
		if i > 0 {
			sortSql.WriteString(", ")
		}
		fmt.Fprintf(&sortSql, "%s %s", QuotationMark(sortParam.Field), sortParam.Direction)
	}

	return sortSql.String()
}

// SQLBuilder - SQLBuilder structure
type SQLBuilder struct {
	g                *logicViewSQLGenerator
	baseQuery        string
	whereClauses     []string
	isSubQuery       bool
	hasExistingWhere bool
	orderBySql       string
	limitCount       int
}

// NewQueryBuilder is an SQL builder for creating logical views
func (g *logicViewSQLGenerator) NewQueryBuilder(ctx context.Context, view *interfaces.LogicView) (*SQLBuilder, error) {
	sql, err := g.BuildLogicDefinitionSQL(ctx, view)
	if err != nil {
		return nil, err
	}
	return g.NewSQLBuilder(sql), nil
}

// NewSQLBuilder creates a new SQL builder
func (g *logicViewSQLGenerator) NewSQLBuilder(baseQuery string) *SQLBuilder {
	builder := &SQLBuilder{
		g:            g,
		baseQuery:    strings.TrimSpace(baseQuery),
		whereClauses: []string{},
	}

	// Detect the query type and structure
	builder.analyzeQuery()
	return builder
}

// analyzeQuery analyzes the structure of basic queries
func (b *SQLBuilder) analyzeQuery() {
	upperQuery := strings.ToUpper(b.baseQuery)

	// Check whether it is a subquery (starting with parentheses or containing multiple selects)
	b.isSubQuery = strings.HasPrefix(b.baseQuery, "(") ||
		(strings.Contains(upperQuery, "SELECT") &&
			strings.Count(upperQuery, "SELECT") > 1)

	// Check whether the WHERE clause has been included
	b.hasExistingWhere = strings.Contains(upperQuery, " WHERE ")
}

// AddWhere adds the WHERE condition
func (b *SQLBuilder) AddWhere(condition string) *SQLBuilder {
	if strings.TrimSpace(condition) != "" {
		b.whereClauses = append(b.whereClauses, condition)
	}
	return b
}

// Add WHERE conditions in batches with AddWheres
func (b *SQLBuilder) AddWheres(conditions []string) *SQLBuilder {
	for _, condition := range conditions {
		b.AddWhere(condition)
	}
	return b
}

// OrderBy sets the sorting statement
func (b *SQLBuilder) OrderBy(sql string) *SQLBuilder {
	b.orderBySql = sql
	return b
}

// Limit sets the paging limit.
func (b *SQLBuilder) Limit(count int) *SQLBuilder {
	b.limitCount = count
	return b
}

// ApplyParams uniformly applies query parameters (filtering, sorting, pagination)
func (b *SQLBuilder) ApplyParams(ctx context.Context, params *interfaces.ResourceDataQueryParams, res *interfaces.LogicView) error {
	// 1. Handle the filtration conditions
	fieldsMap := make(map[string]*interfaces.Property)
	for _, prop := range res.SchemaDefinition {
		fieldsMap[prop.Name] = prop
	}

	globalFilterCond, err := filter_condition.NewFilterCondition(ctx, params.FilterCondCfg, fieldsMap)
	if err != nil {
		return err
	}

	if globalFilterCond != nil {
		sqlCond, err := b.g.ConvertFilterCondition(ctx, globalFilterCond, fieldsMap)
		if err != nil {
			return err
		}

		if sqlCond != nil {
			sqlCondStr, args, err := sqlCond.ToSql()
			if err != nil {
				return err
			}
			sqlCondStr, err = b.g.interpolate(sqlCondStr, args)
			if err != nil {
				return err
			}
			b.AddWhere(sqlCondStr)
		}
	}

	// 2. Handle sorting
	if len(params.Sort) > 0 {
		b.OrderBy(buildSQLSortParams(params.Sort))
	}

	// 3. Handle pagination/restrictions
	if (params.QueryType == "" || params.QueryType == interfaces.QueryType_Standard) && params.Limit > 0 {
		b.Limit(params.Limit)
	}

	return nil
}

// Build: Construct the final SQL statement
func (b *SQLBuilder) Build() string {
	sql := b.baseQuery
	if len(b.whereClauses) > 0 {
		whereStr := strings.Join(b.whereClauses, " AND ")
		if b.isSubQuery {
			sql = b.wrapSubQuery(whereStr)
		} else {
			sql = b.buildStandardQuery(whereStr)
		}
	}

	if b.orderBySql != "" {
		sql = fmt.Sprintf("%s ORDER BY %s", sql, b.orderBySql)
	}

	if b.limitCount > 0 {
		sql = AddLimitIfMissing(sql, b.limitCount)
	}

	return sql
}

// wrapSubQuery wraps subqueries
func (b *SQLBuilder) wrapSubQuery(whereStr string) string {
	// If the subquery already has an alias, use it directly
	if b.hasAlias() {
		return fmt.Sprintf("%s WHERE %s", b.baseQuery, whereStr)
	}

	// Add default aliases to the subquery
	return fmt.Sprintf("(%s) AS subquery WHERE %s", b.baseQuery, whereStr)
}

// buildStandardQuery to construct standard queries
func (b *SQLBuilder) buildStandardQuery(whereStr string) string {
	if b.hasExistingWhere {
		// If there is already WHERE, use AND to connect
		return b.insertWhereCondition(whereStr, "AND")
	}

	// If there is no WHERE, add a WHERE clause
	return b.insertWhereCondition(whereStr, "WHERE")
}

// insertWhereCondition: Insert the WHERE condition at the appropriate position
func (b *SQLBuilder) insertWhereCondition(condition, keyword string) string {
	upperQuery := strings.ToUpper(b.baseQuery)
	hasWhere := strings.Contains(upperQuery, " WHERE ")

	// Search for keyword positions (GROUP BY, ORDER BY, LIMIT, etc.)
	keywordPositions := []struct {
		keyword string
		index   int
	}{
		{" GROUP BY ", strings.Index(upperQuery, " GROUP BY ")},
		{" ORDER BY ", strings.Index(upperQuery, " ORDER BY ")},
		{" LIMIT ", strings.Index(upperQuery, " LIMIT ")},
		{" HAVING ", strings.Index(upperQuery, " HAVING ")},
	}

	// Find the first keyword that appears
	insertPosition := -1
	for _, kp := range keywordPositions {
		if kp.index != -1 && (insertPosition == -1 || kp.index < insertPosition) {
			insertPosition = kp.index
		}
	}

	// Determine the conjunctions to be used
	var actualKeyword string
	if hasWhere {
		// If there is already a WHERE clause, use AND OR
		actualKeyword = keyword
	} else {
		// If there is no WHERE clause, use WHERE
		actualKeyword = "WHERE"
	}

	if insertPosition != -1 {
		// Insert conditions before the keywords
		return b.baseQuery[:insertPosition] + " " + actualKeyword + " " + condition + " " + b.baseQuery[insertPosition:]
	}

	// No keywords were found. Add them at the end
	var connector string
	if hasWhere {
		// If there is already a WHERE clause, use AND OR concatenation
		connector = " " + keyword + " "
	} else {
		// If there is no WHERE clause, add the WHERE keyword
		connector = " WHERE "
	}
	return b.baseQuery + connector + condition
}

// hasAlias detects whether the subquery already has an alias
func (b *SQLBuilder) hasAlias() bool {
	// Simple alias detection logic
	if !b.isSubQuery {
		return false
	}

	// Check if it ends with a name like "AS"
	trimmed := strings.TrimSpace(b.baseQuery)
	if strings.HasSuffix(trimmed, ")") {
		return false
	}

	// Check if the "AS" keyword is included
	upperQuery := strings.ToUpper(b.baseQuery)
	lastParen := strings.LastIndex(upperQuery, ")")
	if lastParen == -1 {
		return false
	}

	// After the last parenthesis, there is the keyword "AS"
	afterParen := strings.TrimSpace(upperQuery[lastParen+1:])
	return strings.HasPrefix(afterParen, "AS ")
}

// String implements the Stringer interface
func (b *SQLBuilder) String() string {
	return b.Build()
}

// HasLimit checks whether the SQL already contains the LIMIT clause
func HasLimit(sql string) bool {
	// Convert to lowercase for easier matching
	lowerSQL := strings.ToLower(sql)

	// Remove the comment
	cleanedSQL := removeSQLComments(lowerSQL)

	// A regular expression matching the LIMIT clause
	// Matching format: LIMIT digit or LIMIT digit, digit or LIMIT digit OFFSET digit
	limitPattern := `\blimit\s+(\d+)(?:\s*,\s*\d+|\s+offset\s+\d+)?\s*$`

	matched, _ := regexp.MatchString(limitPattern, cleanedSQL)
	return matched
}

// removeSQLComments removes SQL comments
func removeSQLComments(sql string) string {
	// Remove single-line comments (-- comments)
	singleLineComment := `--[^\n]*`
	re := regexp.MustCompile(singleLineComment)
	sql = re.ReplaceAllString(sql, "")

	// Remove multi-line comments (/* comments */)
	multiLineComment := `/\*.*?\*/`
	re = regexp.MustCompile(multiLineComment)
	sql = re.ReplaceAllString(sql, "")

	return strings.TrimSpace(sql)
}

// AddLimitIfMissing If the SQL has no LIMIT, add LIMIT
func AddLimitIfMissing(sql string, limit int) string {
	if HasLimit(sql) {
		return sql
	}

	// Make sure the SQL ends with a semicolon and then add LIMIT
	trimmedSQL := strings.TrimSpace(sql)
	trimmedSQL = strings.TrimSuffix(trimmedSQL, ";")

	return trimmedSQL + " LIMIT " + strconv.Itoa(limit)
}

// Put double quotes on the string
func QuotationMark(s string) string {
	if s == "*" { // * is a wildcard and does not require quotation marks
		return s
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") { //Prevent over-splicing situations
		return s
	}
	return "`" + s + "`"
}
