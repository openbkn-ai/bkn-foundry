// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"context"
	"reflect"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

type bindingResult struct {
	root      qm.BoundRelation
	schema    qm.Schema
	scope     bindingScope
	project   *qm.BoundProject
	distinct  bool
	sortInput *bindingScope
}

type queryBinder struct {
	ctx          context.Context
	semantics    qm.SemanticContext
	identity     qm.ExecutionIdentity
	resolver     interfaces.SQLQueryResourceResolver
	authorizer   interfaces.SQLQueryAuthorizer
	nextField    qm.FieldID
	nextInstance qm.RelationInstanceID
	nextScope    qm.ScopeID
	views        map[string]bool
	active       map[any]bool
	instances    map[qm.RelationInstanceID]bool
	scopes       map[qm.ScopeID]bool
	resources    map[string]*interfaces.Resource
	dependencies []qm.Dependency
	slots        []qm.RuntimeSlot
	nodes, depth int
}

// Bind 将未绑定 IR 绑定为新的查询，不修改输入，不执行数据查询。
// semantics 必须由编排层从服务 TZ 创建，不能使用客户端提供的任意语义配置。
func Bind(ctx context.Context, query *qm.UnboundQuery, semantics qm.SemanticContext, identity qm.ExecutionIdentity,
	resolver interfaces.SQLQueryResourceResolver, authorizer interfaces.SQLQueryAuthorizer) (*qm.BoundQuery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == nil || query.Scope == 0 {
		return nil, bindFailure(qm.Origin{}, "缺少查询或查询块身份")
	}
	if resolver == nil || authorizer == nil {
		return nil, bindFailure(qm.Origin{}, "缺少资源或授权端口")
	}
	if semantics.Version != qm.CurrentSemanticsVersion || semantics.Location == nil || semantics.TimeZone != semantics.Location.String() ||
		semantics.TypeRulesVersion != qm.CurrentSemanticsVersion || semantics.ConfigFingerprint == "" || semantics.TimeZoneRevision == "" || semantics.Comparison.StringRule != "utf8_binary" || !semantics.Comparison.CaseSensitive || !semantics.Comparison.TrailingSpaceSignificant {
		return nil, &qm.CompileError{Kind: qm.ErrorConfiguration, Message: "查询语义上下文无效"}
	}
	b := queryBinder{ctx: ctx, semantics: semantics, identity: identity, resolver: resolver, authorizer: authorizer,
		active: make(map[any]bool), instances: make(map[qm.RelationInstanceID]bool), scopes: map[qm.ScopeID]bool{query.Scope: true}, resources: make(map[string]*interfaces.Resource), nextScope: 1, views: make(map[string]bool), dependencies: []qm.Dependency{{Kind: qm.DependencyConfig, ID: "query_semantics", Version: semantics.Version, ConfigRevision: semantics.ConfigFingerprint}}}
	result, err := b.relation(query.Root)
	if err != nil {
		return nil, err
	}
	result = b.trimHidden(result)
	if b.nextField > qm.MaxNodes {
		return nil, &qm.CompileError{Kind: qm.ErrorLimit, Message: "绑定输出槽数量超限"}
	}
	return &qm.BoundQuery{
		Root:         result.root,
		Scope:        1,
		OutputSchema: result.schema,
		Semantics:    semantics,
		Dependencies: b.dependencies,
		RuntimeSlots: b.slots,
	}, nil
}

func bindFailure(origin qm.Origin, message string) error {
	return &qm.CompileError{Kind: qm.ErrorBinding, Origin: origin, Message: message}
}
func (b *queryBinder) enter(node any, origin qm.Origin) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if node == nil || reflect.ValueOf(node).IsNil() || b.active[node] {
		return bindFailure(origin, "查询节点为空或包含环")
	}
	b.nodes++
	b.depth++
	if b.nodes > qm.MaxNodes || b.depth > qm.MaxDepth {
		return &qm.CompileError{Kind: qm.ErrorLimit, Origin: origin, Message: "绑定查询复杂度超限"}
	}
	b.active[node] = true
	return nil
}
func (b *queryBinder) leave(node any)                    { delete(b.active, node); b.depth-- }
func (b *queryBinder) fieldID() qm.FieldID               { b.nextField++; return b.nextField }
func (b *queryBinder) instanceID() qm.RelationInstanceID { b.nextInstance++; return b.nextInstance }
func (b *queryBinder) scopeID() qm.ScopeID               { b.nextScope++; return b.nextScope }
