// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

var (
	_ interfaces.SQLQueryResourceResolver = (*sqlQueryResolver)(nil)
	_ interfaces.SQLQueryAuthorizer       = (*sqlQueryResolver)(nil)
	_ interfaces.SQLQuerySourceResolver   = (*sqlQueryResolver)(nil)
)

// sqlQueryResolver 仅供一次编译使用，不跨用户、请求或 cursor 复用权限决定。
// 只加载不含敏感连接字段的目录；建立实际源连接留给执行层。
type sqlQueryResolver struct {
	resourceService  interfaces.ResourceService
	catalogService   interfaces.CatalogService
	identity         qm.ExecutionIdentity
	ignoreLocalIndex bool
	authorized       map[string]qm.AuthorizationBasis
	resources        map[string]*interfaces.Resource
	catalogs         map[string]*interfaces.Catalog
	catalogOrder     []string
	dependencies     []qm.Dependency
}

func newSQLQueryResolver(ctx context.Context, resources interfaces.ResourceService, catalogs interfaces.CatalogService) (*sqlQueryResolver, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || account.ID == "" {
		return nil, queryResolverForbidden(ctx)
	}
	if resources == nil || catalogs == nil {
		return nil, queryResolverError(qm.ErrorConfiguration, qm.Origin{}, "查询资源服务未配置")
	}
	return &sqlQueryResolver{resourceService: resources, catalogService: catalogs,
		identity:   qm.ExecutionIdentity{SubjectID: account.ID, SubjectType: account.Type, AuthorizationNamespace: interfaces.AUTH_RESOURCE_TYPE_RESOURCE},
		authorized: make(map[string]qm.AuthorizationBasis), resources: make(map[string]*interfaces.Resource), catalogs: make(map[string]*interfaces.Catalog)}, nil
}

func (r *sqlQueryResolver) checkIdentity(ctx context.Context, identity qm.ExecutionIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || identity != r.identity || account.ID != identity.SubjectID || account.Type != identity.SubjectType {
		return queryResolverForbidden(ctx)
	}
	return nil
}

func (r *sqlQueryResolver) AuthorizeSQLQueryResource(ctx context.Context, id string, identity qm.ExecutionIdentity, operation string) (qm.AuthorizationBasis, error) {
	if err := r.checkIdentity(ctx, identity); err != nil {
		return qm.AuthorizationBasis{}, err
	}
	if operation != interfaces.OPERATION_TYPE_QUERY_DATA || id == "" {
		return qm.AuthorizationBasis{}, queryResolverForbidden(ctx)
	}
	if err := r.resourceService.CheckResourcePermission(ctx, id, operation); err != nil {
		return qm.AuthorizationBasis{}, err
	}
	if err := ctx.Err(); err != nil {
		return qm.AuthorizationBasis{}, err
	}
	// 现有 PEP 只返回决定，没有可声明的 ACL 版本；执行和续页必须重新授权。
	basis := qm.AuthorizationBasis{ResourceID: id, Operation: operation, Namespace: identity.AuthorizationNamespace}
	r.authorized[id] = basis
	return basis, nil
}

func (r *sqlQueryResolver) ResolveSQLQueryResource(ctx context.Context, id string, identity qm.ExecutionIdentity) (*interfaces.Resource, string, error) {
	if err := r.checkIdentity(ctx, identity); err != nil {
		return nil, "", err
	}
	if r.authorized[id].ResourceID == "" {
		return nil, "", queryResolverForbidden(ctx)
	}
	if resource := r.resources[id]; resource != nil {
		return resource, r.catalogs[resource.CatalogID].ConnectorType, nil
	}
	resource, err := r.resourceService.InternalGetByID(ctx, nil, id)
	if err != nil {
		return nil, "", err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if resource == nil {
		return nil, "", httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound)
	}
	if resource.ID != id || resource.CatalogID == "" || resource.UpdateTime <= 0 {
		return nil, "", queryResolverError(qm.ErrorConfiguration, qm.Origin{ResourceID: id}, "资源元数据或版本无效")
	}
	if resource.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, "", queryResolverForbidden(ctx)
	}
	if _, err := resourcelogic.EnsureResourceQueryable(ctx, resource); err != nil {
		return nil, "", err
	}
	catalog := r.catalogs[resource.CatalogID]
	if catalog == nil {
		catalog, err = r.catalogService.InternalGetByID(ctx, resource.CatalogID, false)
		if err != nil {
			return nil, "", err
		}
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if catalog == nil || catalog.ID != resource.CatalogID || catalog.UpdateTime <= 0 {
			return nil, "", queryResolverError(qm.ErrorConfiguration, qm.Origin{ResourceID: id}, "目录元数据或版本无效")
		}
		if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
			return nil, "", queryResolverForbidden(ctx)
		}
		if err := ensureCatalogEnabled(ctx, catalog); err != nil {
			return nil, "", err
		}
		r.catalogs[catalog.ID] = catalog
		r.catalogOrder = append(r.catalogOrder, catalog.ID)
	}
	snapshot := any(struct {
		Connector string
		Schema    []*interfaces.Property
	}{catalog.ConnectorType, resource.SchemaDefinition})
	kind := qm.DependencyResource
	if resource.Category == interfaces.ResourceCategoryLogicalView {
		kind = qm.DependencyView
		snapshot = struct {
			Schema     []*interfaces.Property
			LogicType  string
			Definition any
		}{resource.SchemaDefinition, resource.LogicType, resource.LogicDefinition}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", queryResolverError(qm.ErrorConfiguration, qm.Origin{ResourceID: id}, "资源字段元数据无效")
	}
	digest := sha256.Sum256(encoded)
	r.dependencies = append(r.dependencies, qm.Dependency{Kind: kind, ID: id, Version: strconv.FormatInt(resource.UpdateTime, 10), SchemaFingerprint: hex.EncodeToString(digest[:]), Authorization: []qm.AuthorizationBasis{r.authorized[id]}})
	r.resources[id] = resource
	return resource, catalog.ConnectorType, nil
}

func (r *sqlQueryResolver) ResolveSQLQuerySource(ctx context.Context, scan qm.Scan, identity qm.ExecutionIdentity) (qm.Scan, error) {
	if err := r.checkIdentity(ctx, identity); err != nil {
		return qm.Scan{}, err
	}
	resource := r.resources[scan.Resource.ID]
	if r.authorized[scan.Resource.ID].ResourceID == "" || resource == nil {
		return qm.Scan{}, queryResolverForbidden(ctx)
	}
	if scan.Resource.CatalogID != resource.CatalogID || scan.Resource.Version != strconv.FormatInt(resource.UpdateTime, 10) {
		return qm.Scan{}, queryResolverError(qm.ErrorInvalidTree, scan.Origin, "目标资源与绑定快照不一致")
	}
	catalog := r.catalogs[resource.CatalogID]
	if catalog == nil {
		return qm.Scan{}, queryResolverError(qm.ErrorConfiguration, scan.Origin, "缺少绑定目录快照")
	}
	if !r.ignoreLocalIndex && resourcelogic.HasAvailableLocalIndex(resource) {
		return qm.Scan{}, queryResolverError(qm.ErrorPlanningUnsupported, scan.Origin, "索引路由尚未接入此 SQL 执行链路")
	}
	if resource.Category != interfaces.ResourceCategoryTable || !sqlCatalogType(catalog.ConnectorType) {
		return qm.Scan{}, queryResolverError(qm.ErrorPlanningUnsupported, scan.Origin, "资源没有受支持的 SQL 执行目标")
	}
	identifier, err := sourceRelationIdentifier(resource, catalog.ConnectorType, scan.Origin)
	if err != nil {
		return qm.Scan{}, err
	}
	candidate := scan
	candidate.Identifier = identifier
	candidate.Fields = slices.Clone(scan.Fields)
	properties := make(map[string]*interfaces.Property, len(resource.SchemaDefinition))
	for _, property := range resource.SchemaDefinition {
		if property == nil || property.Name == "" || properties[property.Name] != nil {
			return qm.Scan{}, queryResolverError(qm.ErrorConfiguration, scan.Origin, "资源字段元数据无效")
		}
		properties[property.Name] = property
	}
	if len(scan.Fields) != len(properties) {
		return qm.Scan{}, queryResolverError(qm.ErrorInvalidTree, scan.Origin, "来源字段与绑定快照不一致")
	}
	for i, field := range scan.Fields {
		property := properties[field.LogicalName]
		if property == nil || field.NativeType.ConnectorType != catalog.ConnectorType || field.NativeType.Name != property.OriginalType {
			return qm.Scan{}, queryResolverError(qm.ErrorInvalidTree, scan.Origin, "来源字段与绑定快照不一致")
		}
		delete(properties, field.LogicalName)
		name := property.OriginalName
		if name == "" {
			name = property.Name
		}
		if !rawIdentifierPart(name) {
			return qm.Scan{}, queryResolverError(qm.ErrorPlanningUnsupported, scan.Origin, "原生列名无法安全表示")
		}
		candidate.Fields[i].Identifier = qm.PhysicalIdentifier{name}
	}
	return candidate, nil
}

func sourceRelationIdentifier(resource *interfaces.Resource, connector string, origin qm.Origin) (qm.PhysicalIdentifier, error) {
	name, _ := resource.SourceMetadata["name"].(string)
	schema, _ := resource.SourceMetadata["schema"].(string)
	database, _ := resource.SourceMetadata["database"].(string)
	var identifier qm.PhysicalIdentifier
	switch connector {
	case interfaces.ConnectorTypeMySQL, interfaces.ConnectorTypeMariaDB, interfaces.ConnectorTypeDoris:
		if database == "" {
			database = schema
		}
		identifier = qm.PhysicalIdentifier{database, name}
	case interfaces.ConnectorTypeSQLServer:
		identifier = qm.PhysicalIdentifier{schema, name}
		if database != "" {
			identifier = append(qm.PhysicalIdentifier{database}, identifier...)
		}
	default:
		identifier = qm.PhysicalIdentifier{schema, name}
	}
	for _, part := range identifier {
		if !rawIdentifierPart(part) {
			return nil, queryResolverError(qm.ErrorPlanningUnsupported, origin, "缺少可安全表示的原生表名分段")
		}
	}
	return identifier, nil
}

func rawIdentifierPart(part string) bool {
	return part != "" && utf8.ValidString(part) && !strings.ContainsRune(part, '\x00')
}
func catalogRevision(catalog *interfaces.Catalog) string {
	return strconv.FormatInt(catalog.UpdateTime, 10)
}
func sqlCatalogType(connector string) bool {
	switch connector {
	case interfaces.ConnectorTypeMySQL, interfaces.ConnectorTypeMariaDB, interfaces.ConnectorTypePostgreSQL, interfaces.ConnectorTypeSQLServer, interfaces.ConnectorTypeOracle, interfaces.ConnectorTypeDoris, interfaces.ConnectorTypeHANA:
		return true
	default:
		return false
	}
}
func queryResolverError(kind qm.ErrorKind, origin qm.Origin, message string) error {
	return &qm.CompileError{Kind: kind, Origin: origin, Message: message}
}
func queryResolverForbidden(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
}
