// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knowledge_network

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"ontology-query/common"
	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/locale"
	"ontology-query/logics"
	"ontology-query/logics/object_type"
)

var (
	knServiceOnce sync.Once
	knService     interfaces.KnowledgeNetworkService
)

type knowledgeNetworkService struct {
	appSetting *common.AppSetting
	omAccess   interfaces.OntologyManagerAccess
	ots        interfaces.ObjectTypeService
	vba        interfaces.VegaBackendAccess
}

func NewKnowledgeNetworkService(appSetting *common.AppSetting) interfaces.KnowledgeNetworkService {
	knServiceOnce.Do(func() {
		knService = &knowledgeNetworkService{
			appSetting: appSetting,
			omAccess:   logics.OMA,
			ots:        object_type.NewObjectTypeService(appSetting),
			vba:        logics.VBA,
		}
	})
	return knService
}

func (kns *knowledgeNetworkService) SearchSubgraph(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnSource) (interfaces.ObjectSubGraph, error) {

	// 1. Get object type information.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询对象子图")
	defer span.End()
	var resps interfaces.ObjectSubGraph

	// 1. Under the specified business knowledge network, get all paths by source object type, direction, and path length.
	typePaths := query.AuthorizedTypePaths
	if typePaths == nil {
		var err error
		typePaths, err = kns.omAccess.GetRelationTypePathsBaseOnSource(ctx, query.KNID, query.Branch,
			interfaces.PathsQueryBaseOnSource{
				ConceptGroups:     query.ConceptGroups,
				SourceObjecTypeId: query.SourceObjecTypeId,
				Direction:         query.Direction,
				PathLength:        query.PathLength,
			})
		if err != nil {
			span.SetAttributes(attribute.Key("kn_id").String(query.KNID))
			span.SetAttributes(attribute.Key("branch").String(query.Branch))
			otellog.LogError(ctx, fmt.Sprintf("Get RelationTypePathsBaseOnSource error: %v", err), err)

			return resps, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).WithErrorDetails(err.Error())
		}
	}

	// 2. Retrieve source object type instances.
	startObjectQuery := &interfaces.ObjectQueryBaseOnObjectType{
		ActualCondition: query.ActualCondition,
		PageQuery:       query.PageQuery,
		KNID:            query.KNID,
		Branch:          query.Branch,
		ObjectTypeID:    query.SourceObjecTypeId,
		CommonQueryParameters: interfaces.CommonQueryParameters{
			IncludeTypeInfo:    true,
			IncludeLogicParams: query.IncludeLogicParams,
			IgnoringStore:      query.IgnoringStore,
			// ExcludeSystemProperties: query.ExcludeSystemProperties,
		},
	}
	if startObjectQuery.Limit == 0 {
		startObjectQuery.Limit = interfaces.DEFAULT_LIMIT
	}

	// Sort fields are validated when object data is retrieved by object type; no validation is needed at this layer.
	startObjects, err := kns.ots.GetObjectsByObjectTypeID(ctx, startObjectQuery)
	if err != nil {
		return resps, err
	}

	// 3. Traverse paths and query object instances type by type from the source instances along each path.
	query.PathQuotaManager = &interfaces.PathQuotaManager{
		TotalLimit:         query.TotalLimit, // Total object path length.
		GlobalCount:        0,                // Object path count starts at 0.
		UsedQuota:          sync.Map{},
		RequestPathTypeNum: len(typePaths),
	}

	// The source type has already been queried and the limit has been obtained; subsequent path exploration uses the system default maximum.
	query.Limit = interfaces.MAX_PATHS
	objectGraph, err := kns.buildObjectSubgraph(ctx, query, typePaths, startObjects)
	if err != nil {
		return resps, err
	}

	// 4. Assemble the final result.
	objectGraph.TotalCount = startObjects.TotalCount
	objectGraph.SearchAfter = startObjects.SearchAfter
	objectGraph.CuurentPathNumber = len(objectGraph.RelationPaths)

	span.SetStatus(codes.Ok, "")
	return *objectGraph, nil
}

func (kns *knowledgeNetworkService) SearchSubgraphByTypePath(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnTypePath) (interfaces.PathsEntries, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询路径的对象子图")
	defer span.End()

	// Query multiple paths concurrently; each path runs independently with its own filters.
	errCh := make(chan error, len(query.Paths.TypePaths))

	typePathsObjectCtx := &typePathsObjectsContext{
		ctx:               ctx,
		objectSubGraphMap: map[int]interfaces.ObjectSubGraph{},
		errCh:             errCh,
		wg:                &sync.WaitGroup{},
	}

	for i, path := range query.Paths.TypePaths {
		typePathsObjectCtx.wg.Add(1)
		go kns.buildObjectSubgraphByTypePaths(typePathsObjectCtx, query, path, i)
		// kns.buildObjectSubgraphByTypePaths(typePathsObjectCtx, typePathsObjectCtx.wg, query, path, i)
	}

	// Wait for all goroutines to complete.
	typePathsObjectCtx.wg.Wait()
	if len(typePathsObjectCtx.errCh) > 0 {
		err := <-typePathsObjectCtx.errCh
		if err != nil {
			return interfaces.PathsEntries{}, err
		}
	}

	// Assemble results.
	graphs := make([]interfaces.ObjectSubGraph, len(query.Paths.TypePaths))
	for i := range query.Paths.TypePaths {
		if grahp, exist := typePathsObjectCtx.objectSubGraphMap[i]; exist {
			graphs[i] = grahp
		} else {
			graphs[i] = interfaces.ObjectSubGraph{}
		}

	}

	return interfaces.PathsEntries{Entries: graphs}, nil
}

func (kns *knowledgeNetworkService) buildObjectSubgraphByTypePaths(
	typePathsObjectCtx *typePathsObjectsContext,
	query *interfaces.SubGraphQueryBaseOnTypePath,
	path interfaces.QueryRelationTypePath, pathIndex int) {

	defer typePathsObjectCtx.wg.Done()

	ctx, span := oteltrace.StartNamedInternalSpan(typePathsObjectCtx.ctx, "查询路径的对象子图")
	defer span.End()

	// 1. Query relation type information for each edge and fill RelationType in typeEdge.
	typePath := interfaces.RelationTypePath{
		ObjectTypes: path.ObjectTypes,
	}
	for j, edge := range path.Edges {
		// Get relation type information.
		relationType, exists, err := kns.omAccess.GetRelationType(ctx, query.KNID, query.Branch, edge.RelationTypeId)
		if err != nil {
			span.SetAttributes(attribute.Key("rt_id").String(edge.RelationTypeId))
			otellog.LogError(ctx, fmt.Sprintf("Get relation type error: %v", err), err)

			err = rest.NewHTTPError(ctx, http.StatusInternalServerError,
				oerrors.OntologyQuery_KnowledgeNetwork_InternalError_GetRelationTypeFailed).WithErrorDetails(err.Error())

			typePathsObjectCtx.errCh <- err
			return
		}
		if !exists {
			logger.Debugf("relation type %d not found!", edge.RelationTypeId)

			span.SetAttributes(attribute.Key("rt_id").String(edge.RelationTypeId))
			err = rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_KnowledgeNetwork_RelationTypeNotFound)
			otellog.LogError(ctx, fmt.Sprintf("relation type [%s] not found!", edge.RelationTypeId), err)

			typePathsObjectCtx.errCh <- err
			return
		}
		path.Edges[j].RelationType = relationType
		// Record direction. If the path edge direction matches the corresponding relation type direction, treat it as forward; otherwise reverse.
		if path.Edges[j].SourceObjectTypeId == path.Edges[j].RelationType.SourceObjectTypeID {
			path.Edges[j].Direction = interfaces.DIRECTION_FORWARD
		} else {
			path.Edges[j].Direction = interfaces.DIRECTION_BACKWARD
		}
	}
	typePath.TypeEdges = path.Edges

	// 2. Retrieve source object type instances.
	startObjectQuery := &interfaces.ObjectQueryBaseOnObjectType{
		ActualCondition: path.ObjectTypes[0].ActualCondition,
		PageQuery:       path.ObjectTypes[0].PageQuery,
		KNID:            query.KNID,
		Branch:          query.Branch,
		ObjectTypeID:    path.Edges[0].SourceObjectTypeId,
		CommonQueryParameters: interfaces.CommonQueryParameters{
			IncludeTypeInfo:    true,
			IncludeLogicParams: query.IncludeLogicParams,
			IgnoringStore:      query.IgnoringStore,
			// ExcludeSystemProperties: query.CommonQueryParameters.ExcludeSystemProperties,
		},
	}
	if startObjectQuery.Limit == 0 {
		startObjectQuery.Limit = interfaces.DEFAULT_LIMIT
	}
	startObjects, err := kns.ots.GetObjectsByObjectTypeID(ctx, startObjectQuery)
	if err != nil {
		typePathsObjectCtx.errCh <- err
		return
	}

	// 3. Build the query.
	subGraphquery := &interfaces.SubGraphQueryBaseOnSource{
		KNID:              query.KNID,
		Branch:            query.Branch,
		SourceObjecTypeId: path.Edges[0].SourceObjectTypeId,
		ActualCondition:   path.ObjectTypes[0].ActualCondition,
		PageQuery: interfaces.PageQuery{
			Limit: path.Limit,
		},
		CommonQueryParameters: query.CommonQueryParameters,
		PathQuotaManager: &interfaces.PathQuotaManager{
			TotalLimit:         int64(path.Limit), // Total object path length.
			GlobalCount:        0,                 // Object path count starts at 0.
			UsedQuota:          sync.Map{},
			RequestPathTypeNum: 1,
		}, // Shared quota manager; must be protected by a lock.
	}
	// Initialize state.
	baseState := &interfaces.BatchQueryState{
		Visited:   map[string]bool{}, // Used to prevent cyclic paths.
		BatchSize: 50,                // Number of nodes queried per batch.
	}
	subGraphquery.BatchQueryState = *baseState

	// Build the subgraph from the source along the path. TODO: fix this by adding filters for each object type.
	typePathObjectCtx := &typePathObjectsContext{
		ctx:           typePathsObjectCtx.ctx,
		relationPaths: []interfaces.RelationPath{},
		objectsMap:    map[string]interfaces.ObjectInfoInSubgraph{},
		errCh:         typePathsObjectCtx.errCh,
		wg:            typePathsObjectCtx.wg,
		mu:            sync.Mutex{},
	}

	kns.buildSingleTypePathObjects(typePathObjectCtx, typePath, subGraphquery, startObjects)

	// Assemble the context for the current point.
	typePathsObjectCtx.objectSubGraphMap[pathIndex] = interfaces.ObjectSubGraph{
		RelationPaths:     typePathObjectCtx.relationPaths,
		Objects:           typePathObjectCtx.objectsMap,
		TotalCount:        startObjects.TotalCount,
		SearchAfter:       startObjects.SearchAfter,
		CuurentPathNumber: len(typePathObjectCtx.relationPaths),
	}
}

// Data query across multiple paths.
type typePathsObjectsContext struct {
	ctx               context.Context
	objectSubGraphMap map[int]interfaces.ObjectSubGraph // The key is the typePath index.
	// relationPathsMap  map[int][]interfaces.RelationPath
	// objectsMap        map[int]map[string]interfaces.ObjectInfoInSubgraph
	errCh chan error
	wg    *sync.WaitGroup
}

type typePathObjectsContext struct {
	ctx           context.Context
	relationPaths []interfaces.RelationPath
	objectsMap    map[string]interfaces.ObjectInfoInSubgraph
	errCh         chan error
	wg            *sync.WaitGroup
	mu            sync.Mutex
}

// Build object subgraphs for all paths from the source object.
func (kns *knowledgeNetworkService) buildObjectSubgraph(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnSource,
	typePaths []interfaces.RelationTypePath,
	startObjects interfaces.Objects,
) (*interfaces.ObjectSubGraph, error) {

	logger.Infof("开始构建对象子图 - 概念路径数: %d, 起点对象数: %d, 总限制: %d",
		len(typePaths), len(startObjects.Datas), query.TotalLimit)

	errCh := make(chan error, len(typePaths))
	typePathObjectCtx := &typePathObjectsContext{
		ctx:           ctx,
		relationPaths: []interfaces.RelationPath{},
		objectsMap:    map[string]interfaces.ObjectInfoInSubgraph{},
		errCh:         errCh,
		wg:            &sync.WaitGroup{},
		mu:            sync.Mutex{},
	}

	// Initialize state.
	baseState := &interfaces.BatchQueryState{
		Visited:   map[string]bool{}, // Used to prevent cyclic paths.
		BatchSize: 50,                // Number of nodes queried per batch.
	}
	query.BatchQueryState = *baseState

	// Generate object paths for each concept path. This can be optimized by running concept paths in parallel.
	for i := range typePaths {
		typePathObjectCtx.wg.Add(1)
		go kns.buildObjectSubgraphBySource(typePathObjectCtx, typePaths[i], query, startObjects)
		// kns.buildSingleTypePathObjects(typePathObjectCtx, typePaths[i], query, startObjects)
	}

	// Wait for all goroutines to complete.
	typePathObjectCtx.wg.Wait()
	if len(typePathObjectCtx.errCh) > 0 {
		err := <-typePathObjectCtx.errCh
		if err != nil {
			return nil, err
		}
	}

	return &interfaces.ObjectSubGraph{
		Objects:       typePathObjectCtx.objectsMap,
		RelationPaths: typePathObjectCtx.relationPaths,
	}, nil
}

func (kns *knowledgeNetworkService) buildObjectSubgraphBySource(
	typePathObjectCtx *typePathObjectsContext,
	typePath interfaces.RelationTypePath,
	query *interfaces.SubGraphQueryBaseOnSource,
	startObjects interfaces.Objects,
) {

	defer typePathObjectCtx.wg.Done()
	kns.buildSingleTypePathObjects(typePathObjectCtx, typePath, query, startObjects)
}

func (kns *knowledgeNetworkService) buildSingleTypePathObjects(
	typePathObjectCtx *typePathObjectsContext,
	typePath interfaces.RelationTypePath,
	query *interfaces.SubGraphQueryBaseOnSource,
	startObjects interfaces.Objects,
) {

	logger.Debugf("处理概念路径 - ID: %d, 边数: %d", typePath.ID, len(typePath.TypeEdges))

	// Check the global limit before processing starts.
	if !logics.CanGenerate(query.PathQuotaManager, typePath.ID) {
		logger.Debugf("路径ID %d 已达到限制，跳过处理", typePath.ID)
		return
	}

	// Create an independent state copy for each goroutine to avoid concurrent conflicts.
	localState := &interfaces.BatchQueryState{
		Visited:   make(map[string]bool),
		BatchSize: query.BatchSize,
	}

	localQuery := &interfaces.SubGraphQueryBaseOnSource{
		KNID:                  query.KNID,
		Branch:                query.Branch,
		SourceObjecTypeId:     query.SourceObjecTypeId,
		Direction:             query.Direction,
		PathLength:            query.PathLength,
		IncludeIncompletePath: query.IncludeIncompletePath,
		Condition:             query.Condition,
		ActualCondition:       query.ActualCondition,
		PageQuery:             query.PageQuery,
		PathQuotaManager:      query.PathQuotaManager, // Shared quota manager; must be protected by a lock.
		BatchQueryState:       *localState,
		CommonQueryParameters: query.CommonQueryParameters,
	}

	var (
		// localPaths      []interfaces.RelationPath
		localObjectsMap = make(map[string]interfaces.ObjectInfoInSubgraph)
	)

	// Batch-expand object paths.
	currentObjectPaths, err := kns.expandObjectPathsBatch(typePathObjectCtx.ctx, localQuery, typePath,
		startObjects, localObjectsMap)
	if err != nil {
		typePathObjectCtx.errCh <- err
		return
	}

	// Merge results into the main data structure; locking is required.
	// Check again before merging results in case another goroutine reached the limit during expansion.
	if len(currentObjectPaths) > 0 {
		typePathObjectCtx.mu.Lock()
		defer typePathObjectCtx.mu.Unlock()

		// Check whether merging would exceed the limit and merge only as needed.
		currentGlobal := atomic.LoadInt64(&query.GlobalCount)
		if currentGlobal > query.TotalLimit {
			// If merging the whole batch exceeds the limit, merge only the remaining capacity.
			fixedNum := query.TotalLimit - int64(len(typePathObjectCtx.relationPaths))
			// Limit fixedNum to the actual current batch size to avoid array bounds errors.
			if fixedNum > int64(len(currentObjectPaths)) {
				fixedNum = int64(len(currentObjectPaths))
			}
			for i := int64(0); i < fixedNum; i++ {
				typePathObjectCtx.relationPaths = append(typePathObjectCtx.relationPaths, currentObjectPaths[i])
				for _, edge := range currentObjectPaths[i].Relations {
					typePathObjectCtx.objectsMap[edge.SourceObjectId] = localObjectsMap[edge.SourceObjectId]
					typePathObjectCtx.objectsMap[edge.TargetObjectId] = localObjectsMap[edge.TargetObjectId]
				}
			}
			logger.Debugf("添加当前批次达到全局限制，只合并[%d]路径", fixedNum)
			return
		}

		typePathObjectCtx.relationPaths = append(typePathObjectCtx.relationPaths, currentObjectPaths...)
		for k, v := range localObjectsMap {
			typePathObjectCtx.objectsMap[k] = v
		}
	}
}

// Batch-expand object paths.
func (kns *knowledgeNetworkService) expandObjectPathsBatch(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnSource,
	typePath interfaces.RelationTypePath,
	startObjects interfaces.Objects,
	objectsMap map[string]interfaces.ObjectInfoInSubgraph) ([]interfaces.RelationPath, error) {

	var paths []interfaces.RelationPath

	// Use breadth-first search for batch expansion.
	var bfs func(currentLevelObjects []interfaces.LevelObjectWithPath, depth int) error

	bfs = func(currentLevel []interfaces.LevelObjectWithPath, depth int) error {
		// Check the global limit before starting each level.
		if !logics.CanGenerate(query.PathQuotaManager, typePath.ID) {
			logger.Debugf("达到限制，停止扩展路径，深度: %d", depth)
			return nil
		}

		if depth >= len(typePath.TypeEdges) || len(currentLevel) == 0 {
			// When the path endpoint is reached, save the path, skipping paths without any edges to match the IncludeIncompletePath branch.
			totalPathsInThisBatch := 0
			for _, current := range currentLevel {
				for _, path := range current.Paths {
					if len(path.Relations) == 0 {
						continue
					}
					paths = append(paths, path)
					totalPathsInThisBatch++
				}
			}

			if totalPathsInThisBatch > 0 {
				logics.RecordGenerated(query.PathQuotaManager, typePath.ID, totalPathsInThisBatch)
				logger.Debugf("路径扩展完成 - 路径ID: %d, 新增路径: %d, 深度: %d",
					typePath.ID, totalPathsInThisBatch, depth)
			}
			return nil
		}

		// Get the edge at the current depth.
		edge := typePath.TypeEdges[depth]
		// Get the target object type of the current edge.
		objectType := typePath.ObjectTypes[depth+1]

		// Prepare the batch query.
		currentLevelObjects := make([]interfaces.LevelObject, len(currentLevel))
		for i, obj := range currentLevel {
			currentLevelObjects[i] = obj.LevelObject
		}

		// Process objects in batches to avoid oversized single queries.
		batchSize := query.BatchSize
		if batchSize <= 0 {
			batchSize = 50
		}

		continueBatch := true
		for i := 0; i < len(currentLevelObjects) && continueBatch; i += batchSize {
			// Check the limit before processing each object.
			if !logics.CanGenerate(query.PathQuotaManager, typePath.ID) {
				// Stop and do not traverse the next batch.
				break
			}

			end := i + batchSize
			if end > len(currentLevelObjects) {
				end = len(currentLevelObjects)
			}

			batch := currentLevelObjects[i:end]
			// Batch-query next-layer objects.
			nextLevelObjects, err := kns.getNextObjectsBatchByRelation(ctx, query, batch, &edge, objectType)
			if err != nil {
				return err
			}
			if len(nextLevelObjects) == 0 {
				// No next-layer objects.
				if query.IncludeIncompletePath {
					// If incomplete paths should be included, add paths for all objects in the current batch to the result.
					batchObjectIDs := make(map[string]bool)
					for _, obj := range batch {
						batchObjectIDs[obj.ObjectID] = true
					}
					totalPathsInThisBatch := 0
					for _, currentObj := range currentLevel {
						if batchObjectIDs[currentObj.ObjectID] && len(currentObj.Paths) > 0 {
							for _, path := range currentObj.Paths {
								if len(path.Relations) > 0 {
									paths = append(paths, path)
									totalPathsInThisBatch += len(path.Relations)
								}
							}
						}
					}
					if totalPathsInThisBatch > 0 {
						logics.RecordGenerated(query.PathQuotaManager, typePath.ID, totalPathsInThisBatch)
						logger.Debugf("添加不完整路径 - 路径ID: %d, 新增路径: %d, 深度: %d",
							typePath.ID, totalPathsInThisBatch, depth)
					}
				}
				// Continue traversing the next batch.
				continue
			}

			// Build next-layer objects.
			var nextLevel []interfaces.LevelObjectWithPath

			for _, currentObj := range currentLevel {
				// Check the limit before processing each object.
				if !logics.CanGenerate(query.PathQuotaManager, typePath.ID) {
					// Stop and do not traverse the next batch.
					continueBatch = false
					break
				}

				nextObjects, exists := nextLevelObjects[currentObj.ObjectID]
				if !exists {
					// No next-layer object was found for the current object.
					if query.IncludeIncompletePath {
						// Incomplete paths: only keep paths that already have at least one edge (same as batch branch above).
						totalAdded := 0
						for _, path := range currentObj.Paths {
							if len(path.Relations) == 0 {
								continue
							}
							paths = append(paths, path)
							totalAdded += len(path.Relations)
						}
						if totalAdded > 0 {
							logics.RecordGenerated(query.PathQuotaManager, typePath.ID, totalAdded)
							logger.Debugf("添加不完整路径 - 路径ID: %d, 对象ID: %s, 新增路径: %d, 深度: %d",
								typePath.ID, currentObj.ObjectID, totalAdded, depth)
						}
					}
					continue
				}

				for _, nextObject := range nextObjects.Datas {
					// Check limits.
					if !logics.CanGenerate(query.PathQuotaManager, typePath.ID) {
						break
					}

					nextObjectID, uk := logics.GetObjectID(nextObject, nextObjects.ObjectType)
					if nextObjectID == "" {
						continue
					}

					// Build a path key to detect cycles.
					// pathKey := ""
					// if len(typePath.TypeEdges) == 0 {
					// 	pathKey = fmt.Sprintf("%s:%s->%s", edge.RelationTypeId, currentObj.ObjectID, nextObjectID)
					// } else {
					// 	pathKey = logics.BuildPathKey(currentPath, nextObjectID)
					// }

					// Add the object if it is absent from the object map.
					// _, exists = objectsMap[currentObj.ObjectID]
					// if !exists {
					// 	continue
					// }

					// If the current object has not been added, add it to the object mapping.
					_, exists = objectsMap[currentObj.ObjectID]
					if !exists {
						objInfo := interfaces.ObjectInfoInSubgraph{
							ObjectTypeId:   currentObj.ObjectType.OTID,
							ObjectTypeName: currentObj.ObjectType.OTName,
							Properties:     currentObj.ObjectData,
						}
						if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_ID, query.ExcludeSystemProperties) {
							objInfo.InstanceID = currentObj.ObjectID
						}
						if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY, query.ExcludeSystemProperties) {
							objInfo.InstanceIdentity = currentObj.ObjectUK
						}
						if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_DISPLAY, query.ExcludeSystemProperties) {
							objInfo.Display = currentObj.ObjectData[currentObj.ObjectType.DisplayKey]
						}
						objectsMap[currentObj.ObjectID] = objInfo
					}

					// Add the next-layer object to the object mapping.
					objInfo := interfaces.ObjectInfoInSubgraph{
						ObjectTypeId:   nextObjects.ObjectType.OTID,
						ObjectTypeName: nextObjects.ObjectType.OTName,
						Properties:     nextObject,
					}
					if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_ID, query.ExcludeSystemProperties) {
						objInfo.InstanceID = nextObjectID
					}
					if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY, query.ExcludeSystemProperties) {
						objInfo.InstanceIdentity = uk
					}
					if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_DISPLAY, query.ExcludeSystemProperties) {
						objInfo.Display = nextObject[nextObjects.ObjectType.DisplayKey]
					}
					objectsMap[nextObjectID] = objInfo

					// Add the new edge to all paths of the current object.
					newPaths, pathExisted := kns.extendPathsWithNewEdge(query, currentObj.Paths, currentObj.ObjectID, nextObjectID, edge)
					if pathExisted {
						continue
					}

					// Record next-layer objects for continued expansion. They must carry ObjectType/ObjectUK because later edges such as filtered_cross_join need to query instances again.
					nextLevel = append(nextLevel, interfaces.LevelObjectWithPath{
						LevelObject: interfaces.LevelObject{
							ObjectID:   nextObjectID,
							ObjectUK:   uk,
							ObjectData: nextObject,
							ObjectType: nextObjects.ObjectType,
							PathFrom:   currentObj.ObjectID,
						},
						Paths: newPaths, // Carry the expanded paths.
					})

				}
			}

			// Paths for the current batch at this level are complete; recursively process the next level.
			if len(nextLevel) > 0 {
				err = bfs(nextLevel, depth+1)
				if err != nil {
					return err
				}
				continue
			}
			// If the current batch at this level has no paths to expand, continue expanding paths for the next batch.
		}

		// Nothing was expanded at the current level; end traversal with no path.
		return nil
	}

	// Initialize first-level objects.
	var initialLevel []interfaces.LevelObjectWithPath
	for _, startObjectData := range startObjects.Datas {
		startObjectID, startObjectUK := logics.GetObjectID(startObjectData, startObjects.ObjectType)
		if startObjectID == "" {
			continue
		}

		// Create an initial empty path for each source object.
		initialPath := interfaces.RelationPath{
			Relations: []interfaces.Relation{},
			Length:    0,
		}

		initialLevel = append(initialLevel, interfaces.LevelObjectWithPath{
			LevelObject: interfaces.LevelObject{
				ObjectID:   startObjectID,
				ObjectType: startObjects.ObjectType,
				ObjectUK:   startObjectUK,
				ObjectData: startObjectData,
				PathFrom:   "", // A starting object has no origin.
			},
			Paths: []interfaces.RelationPath{initialPath},
		})
	}

	// Start breadth-first search.
	err := bfs(initialLevel, 0)
	if err != nil {
		return nil, err
	}

	logger.Debugf("路径扩展完成 - 路径ID: %d, 总路径数: %d", typePath.ID, len(paths))
	return paths, nil
}

// Batch-fetch next-layer objects by relation.
func (kns *knowledgeNetworkService) getNextObjectsBatchByRelation(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnSource,
	batch []interfaces.LevelObject,
	edge *interfaces.TypeEdge,
	objectType interfaces.ObjectTypeWithKeyField) (map[string]interfaces.Objects, error) {

	// Determine the next object type from the relation direction.
	var nextObjectTypeID string
	isForward := true
	if edge.Direction == interfaces.DIRECTION_FORWARD {
		nextObjectTypeID = edge.RelationType.TargetObjectTypeID
	} else {
		nextObjectTypeID = edge.RelationType.SourceObjectTypeID
		isForward = false
	}

	result := make(map[string]interfaces.Objects)

	if edge.RelationType.Type == interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN {
		rules, ok := edge.RelationType.MappingRules.(*interfaces.FilteredCrossJoinMapping)
		if !ok {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ObjectType_InvalidParameter).
				WithErrorDetails("relation type filtered_cross_join requires FilteredCrossJoinMapping rules")
		}
		return kns.expandFilteredCrossJoin(ctx, query, batch, edge, objectType, isForward, rules)
	}

	// Process objects in batches to avoid oversized single queries.
	// batchSize := query.BatchSize
	// if batchSize <= 0 {
	// 	batchSize = 50
	// }

	// for i := 0; i < len(currentLevelObjects); i += batchSize {
	// 	end := i + batchSize
	// 	if end > len(currentLevelObjects) {
	// 		end = len(currentLevelObjects)
	// 	}

	// 	batch := currentLevelObjects[i:end]

	// Build batch query conditions and also return associated view data for indirect associations.
	conditions, viewDataMap, err := kns.buildBatchConditions(ctx, query, batch, edge, isForward)
	if err != nil {
		return nil, err
	}
	if len(conditions) == 0 {
		// Skip when filter conditions cannot be built from the relation type mapping.
		// continue
		return nil, nil
	}

	nextObjectQuery := &interfaces.ObjectQueryBaseOnObjectType{
		KNID:         query.KNID,
		Branch:       query.Branch,
		ObjectTypeID: nextObjectTypeID,
		CommonQueryParameters: interfaces.CommonQueryParameters{
			IncludeTypeInfo:    true,
			IncludeLogicParams: query.IncludeLogicParams,
			IgnoringStore:      query.IgnoringStore,
			// ExcludeSystemProperties: query.ExcludeSystemProperties. System fields for subgraph queries are generated by the subgraph query and do not need to be generated by object instances.
		},
	}

	if len(conditions) > 1 {
		nextObjectQuery.ActualCondition = &cond.CondCfg{
			Operation: "or", // Combine multiple objects with OR.
			SubConds:  conditions,
		}
	} else if len(conditions) == 1 {
		nextObjectQuery.ActualCondition = conditions[0]
	}

	// Add filter conditions configured on the object type.
	if objectType.ActualCondition != nil {
		nextObjectQuery.ActualCondition = &cond.CondCfg{
			Operation: "and", // Combine object constraints with AND.
			SubConds:  []*cond.CondCfg{nextObjectQuery.ActualCondition, objectType.ActualCondition},
		}
	}
	// Pagination and sorting information.
	if len(objectType.Sort) > 0 {
		nextObjectQuery.Sort = objectType.Sort
	}
	if objectType.Limit > 0 {
		nextObjectQuery.Limit = objectType.Limit
	} else {
		nextObjectQuery.Limit = query.Limit // Adjust the limit as needed.
	}

	nextObjects, err := kns.ots.GetObjectsByObjectTypeID(ctx, nextObjectQuery)
	if err != nil {
		return nil, err
	}
	// logger.Debugf("从对象类[%s]中获取到的数据条数为[%d]", nextObjectTypeID, len(nextObjects.Datas))

	// Map results back to each object according to mapping rules.
	kns.mapResultsToObjects(batch, nextObjects, result, edge, isForward, viewDataMap)
	// }

	return result, nil
}

// Map query results back to each object.
func (kns *knowledgeNetworkService) mapResultsToObjects(currentLevelObjects []interfaces.LevelObject,
	nextObjects interfaces.Objects, result map[string]interfaces.Objects,
	edge *interfaces.TypeEdge, isForward bool,
	viewDataMap map[string][]map[string]any) {

	// Filter next-layer objects that belong to each object according to mapping rules.
	for _, levelObj := range currentLevelObjects {
		filteredObjects := interfaces.Objects{
			Datas:       []map[string]any{},
			ObjectType:  nextObjects.ObjectType,
			TotalCount:  0,
			SearchAfter: nextObjects.SearchAfter,
		}

		for _, nextObj := range nextObjects.Datas {
			// Get view data for this object when using indirect mapping.
			var objectViewData []map[string]any
			if _, isIndirect := edge.RelationType.MappingRules.(*interfaces.InDirectMapping); isIndirect {
				objectViewData = viewDataMap[levelObj.ObjectID]
			}

			if kns.isObjectRelated(levelObj.ObjectData, nextObj, edge, isForward, objectViewData) {
				filteredObjects.Datas = append(filteredObjects.Datas, nextObj)
				filteredObjects.TotalCount++
			}
		}

		if len(filteredObjects.Datas) > 0 {
			result[levelObj.ObjectID] = filteredObjects
		}
	}
}

// Determine whether objects are associated according to mapping rules.
func (kns *knowledgeNetworkService) isObjectRelated(currentObjectData map[string]any,
	nextObject map[string]any, edge *interfaces.TypeEdge, isForward bool,
	viewData []map[string]any) bool {

	switch mappingRules := edge.RelationType.MappingRules.(type) {
	case []interfaces.Mapping:
		// Check whether direct mapping conditions are satisfied.
		return logics.CheckDirectMappingConditions(currentObjectData, nextObject, mappingRules, isForward)

	case *interfaces.InDirectMapping:
		// Indirect mapping check logic.
		// Needs to be implemented according to specific business rules.
		return logics.CheckIndirectMappingConditionsWithViewData(currentObjectData, nextObject, mappingRules, isForward, viewData)

	case *interfaces.FilteredCrossJoinMapping:
		// Pairing is done in expandFilteredCrossJoin; this path is not used for that type.
		return false
	}

	return false
}

// Build batch query conditions.
func (kns *knowledgeNetworkService) buildBatchConditions(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnSource,
	currentLevelObjects []interfaces.LevelObject,
	edge *interfaces.TypeEdge,
	isForward bool) ([]*cond.CondCfg, map[string][]map[string]any, error) {

	var conditions []*cond.CondCfg
	viewDataMap := make(map[string][]map[string]any) // objectID -> []viewData

	// Handle direct mappings first.
	directObjects := make([]interfaces.LevelObject, 0)
	indirectObjects := make([]interfaces.LevelObject, 0)

	for _, levelObj := range currentLevelObjects {
		switch edge.RelationType.MappingRules.(type) {
		case []interfaces.Mapping:
			directObjects = append(directObjects, levelObj)
		case *interfaces.InDirectMapping:
			indirectObjects = append(indirectObjects, levelObj)
		}
	}

	// Handle direct mappings.
	if len(directObjects) > 0 {
		directConditions, err := logics.BuildDirectBatchConditions(directObjects, edge, isForward)
		if err != nil {
			return nil, nil, err
		}
		conditions = append(conditions, directConditions...)
	}

	// Handle indirect mappings by batch-querying view data.
	if len(indirectObjects) > 0 {
		indirectConditions, indirectViewData, err := kns.buildIndirectBatchConditions(ctx, query, indirectObjects, edge, isForward)
		if err != nil {
			return nil, nil, err
		}
		conditions = append(conditions, indirectConditions...)

		// Merge view data mappings.
		for k, v := range indirectViewData {
			viewDataMap[k] = v
		}
	}

	return conditions, viewDataMap, nil
}

// Build batch conditions for indirect mappings and return view data mappings.
func (kns *knowledgeNetworkService) buildIndirectBatchConditions(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnSource,
	currentLevelObjects []interfaces.LevelObject,
	edge *interfaces.TypeEdge, isForward bool) ([]*cond.CondCfg, map[string][]map[string]any, error) {

	var conditions []*cond.CondCfg
	viewDataMap := make(map[string][]map[string]any)
	mappingRules := edge.RelationType.MappingRules.(*interfaces.InDirectMapping)

	if mappingRules.BackingDataSource.ID == "" {
		// If the view is empty, return an error without sending a request.
		return nil, nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
			oerrors.OntologyQuery_ObjectType_InvalidParameter).
			WithErrorDetails(locale.ValidationDetail(ctx, "RelationViewIDRequired", map[string]any{"relationType": edge.RelationType.RTName}))
	}

	// Mapping relationship from view to target object.
	var targetMappingRules []interfaces.Mapping
	if isForward {
		targetMappingRules = mappingRules.TargetMappingRules
	} else {
		targetMappingRules = mappingRules.SourceMappingRules
	}

	// Batch-query view data for all objects.
	batchViewData, err := kns.batchGetViewData(ctx, query, edge, currentLevelObjects, mappingRules, isForward)
	if err != nil {
		return nil, nil, err
	}

	var inValues []any
	var inField string
	// Build query conditions for each object.
	for _, levelObj := range currentLevelObjects {
		objectViewData, exists := batchViewData[levelObj.ObjectID]
		if !exists || len(objectViewData) == 0 {
			continue
		}

		// Save view data mappings for later object association checks.
		viewDataMap[levelObj.ObjectID] = objectViewData

		// Traverse view data, build filter conditions one by one, and finally join them with OR.
		multiConds := []*cond.CondCfg{}
		for _, viewData := range objectViewData {
			viewConditions, targetField, inValue := logics.BuildCondition(nil, targetMappingRules, isForward, viewData)
			multiConds = append(multiConds, viewConditions...)
			inValues = append(inValues, inValue)
			inField = targetField
		}

		if len(multiConds) > 1 {
			conditions = append(conditions, &cond.CondCfg{
				Operation: "or",
				SubConds:  multiConds,
			})
		} else if len(multiConds) == 1 {
			conditions = append(conditions, multiConds[0])
		}
	}

	if len(targetMappingRules) == 1 && len(inValues) > 0 {
		return []*cond.CondCfg{
			{
				Name:      inField,
				Operation: "in",
				ValueOptCfg: cond.ValueOptCfg{
					ValueFrom: "const",
					Value:     inValues,
				},
			},
		}, viewDataMap, nil
	}
	return conditions, viewDataMap, nil
}

// Batch-fetch view data.
func (kns *knowledgeNetworkService) batchGetViewData(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnSource,
	edge *interfaces.TypeEdge,
	currentLevelObjects []interfaces.LevelObject,
	mappingRules *interfaces.InDirectMapping, isForward bool) (map[string][]map[string]any, error) {

	result := make(map[string][]map[string]any)
	batchSize := 50 // Batch size for view queries.
	var mappingRulesToUse []interfaces.Mapping
	if isForward {
		mappingRulesToUse = mappingRules.SourceMappingRules
	} else {
		mappingRulesToUse = mappingRules.TargetMappingRules
	}

	// Process objects by batch.
	for i := 0; i < len(currentLevelObjects); i += batchSize {
		end := i + batchSize
		if end > len(currentLevelObjects) {
			end = len(currentLevelObjects)
		}

		batch := currentLevelObjects[i:end]

		// Build combined query conditions for all objects in the batch.
		batchConditions := []*cond.CondCfg{}
		objectMapping := make(map[int]string) // Mapping from condition index to object ID.
		var inValues []any
		var inField string
		for _, levelObj := range batch {
			objectConditions, targetField, inValue := logics.BuildCondition(nil, mappingRulesToUse, isForward, levelObj.ObjectData)
			inValues = append(inValues, inValue)
			inField = targetField

			if len(objectConditions) > 1 {
				batchConditions = append(batchConditions, &cond.CondCfg{
					Operation: "and",
					SubConds:  objectConditions,
				})
			} else if len(objectConditions) == 1 {
				batchConditions = append(batchConditions, objectConditions[0])
			} else {
				continue
			}
			objectMapping[len(batchConditions)-1] = levelObj.ObjectID
		}

		// if len(batchConditions) == 0 {
		// 	continue
		// }

		// Build the view query.
		viewQuery := &interfaces.ViewQuery{
			NeedTotal: query.NeedTotal,
			Limit:     interfaces.MAX_LIMIT, // When querying the relation table, do not limit the count; fetch all relations.
		}

		if len(mappingRulesToUse) == 1 && len(inValues) > 0 {
			viewQuery.Filters = &cond.CondCfg{
				Name:      inField,
				Operation: "in",
				ValueOptCfg: cond.ValueOptCfg{
					ValueFrom: "const",
					Value:     inValues,
				},
			}
		} else {
			if len(batchConditions) > 1 {
				viewQuery.Filters = &cond.CondCfg{
					Operation: "or",
					SubConds:  batchConditions,
				}
			} else {
				viewQuery.Filters = batchConditions[0]
			}
		}

		// Build sorting by association fields.
		sort := []*interfaces.SortParams{}
		for _, mapping := range mappingRulesToUse {
			targetName := mapping.TargetProp.Name
			if !isForward {
				targetName = mapping.SourceProp.Name
			}

			sort = append(sort, &interfaces.SortParams{
				Field:     targetName,
				Direction: interfaces.ASC_DIRECTION,
			})
		}
		viewQuery.Sort = sort

		var backingRows []map[string]any
		if mappingRules.BackingDataSource != nil && mappingRules.BackingDataSource.Type == interfaces.DATA_SOURCE_TYPE_RESOURCE {
			params := &interfaces.ResourceDataQueryParams{
				NeedTotal: viewQuery.NeedTotal,
				Paging: interfaces.ResourceDataPagingRequest{
					Mode:  "single",
					Limit: viewQuery.Limit,
				},
				Sort:            viewQuery.Sort,
				SearchAfter:     viewQuery.SearchAfter,
				FilterCondition: logics.CondCfgToFilterMap(viewQuery.Filters),
			}
			resp, err := kns.vba.QueryResourceData(ctx, mappingRules.BackingDataSource.ID, params)
			if err != nil {
				return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
					oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).WithErrorDetails(err.Error())
			}
			if resp == nil {
				return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
					oerrors.OntologyQuery_ObjectType_InternalError_GetViewDataByIDFailed).WithErrorDetails("vega resource query returned nil")
			}
			backingRows = resp.Entries
			logger.Debugf("relation [%s] from resource [%s] rows [%d]", edge.RelationType.RTName, mappingRules.BackingDataSource.ID, len(backingRows))
		} else {
			backingType := ""
			if mappingRules.BackingDataSource != nil {
				backingType = mappingRules.BackingDataSource.Type
			}
			return nil, logics.UnsupportedRelationBackingDataSourceError(ctx, backingType)
		}

		kns.mapViewDataToObjects(backingRows, batchConditions, objectMapping, mappingRules, isForward, result)
	}

	return result, nil
}

// Map view data back to each object.
func (kns *knowledgeNetworkService) mapViewDataToObjects(viewData []map[string]any,
	batchConditions []*cond.CondCfg,
	objectMapping map[int]string,
	mappingRules *interfaces.InDirectMapping,
	isForward bool,
	result map[string][]map[string]any) {

	for _, data := range viewData {
		// Find which object this view data belongs to.
		for condIndex, objectID := range objectMapping {
			if condIndex >= len(batchConditions) {
				continue
			}

			var mappingRulesToUse []interfaces.Mapping
			if isForward {
				mappingRulesToUse = mappingRules.SourceMappingRules
			} else {
				mappingRulesToUse = mappingRules.TargetMappingRules
			}

			// Check whether view data satisfies this object's query conditions.
			if logics.CheckViewDataMatchesCondition(data, batchConditions[condIndex], mappingRulesToUse, isForward) {
				if result[objectID] == nil {
					result[objectID] = make([]map[string]any, 0)
				}
				result[objectID] = append(result[objectID], data)
				break // One view record belongs to only one object.
			}
		}
	}
}

// Add a new edge to the path set and check for duplicate paths.
func (kns *knowledgeNetworkService) extendPathsWithNewEdge(query *interfaces.SubGraphQueryBaseOnSource,
	paths []interfaces.RelationPath,
	sourceObjectID string, targetObjectID string, edge interfaces.TypeEdge) ([]interfaces.RelationPath, bool) {

	var newPaths []interfaces.RelationPath
	var pathExisted bool

	for _, path := range paths {
		// Check whether this path ends with sourceObjectID.
		if !kns.isPathEndsWith(path, sourceObjectID) {
			continue
		}

		// Create a new path by deep copy.
		newPath := interfaces.RelationPath{
			Relations: make([]interfaces.Relation, len(path.Relations)),
			Length:    path.Length + 1,
		}
		copy(newPath.Relations, path.Relations)

		// Add the new edge.
		newPath.Relations = append(newPath.Relations, interfaces.Relation{
			RelationTypeId:   edge.RelationTypeId,
			RelationTypeName: edge.RelationType.RTName,
			SourceObjectId:   sourceObjectID,
			TargetObjectId:   targetObjectID,
		})

		// Build a path key to detect cycles.
		pathKey := ""
		for _, edge := range newPath.Relations {
			pathKey = fmt.Sprintf("%s-%s:%s->%s", pathKey, edge.RelationTypeId, edge.SourceObjectId, edge.TargetObjectId)
		}
		if query.Visited[pathKey] {
			logger.Warnf("检测到重复路径: %s", pathKey)
			pathExisted = true
		}
		query.Visited[pathKey] = true

		newPaths = append(newPaths, newPath)
	}

	return newPaths, pathExisted
}

// Check whether the path ends with the specified object ID.
func (kns *knowledgeNetworkService) isPathEndsWith(path interfaces.RelationPath, objectID string) bool {
	if len(path.Relations) == 0 {
		// For an empty path, check whether it is the source object.
		// Additional logic is needed here to track the source object; return true for now.
		return true
	}

	// Check whether the target object of the last edge matches.
	lastEdge := path.Relations[len(path.Relations)-1]
	return lastEdge.TargetObjectId == objectID
}

// Build a relation subgraph from a set of object instances.
func (kns *knowledgeNetworkService) SearchSubgraphByObjects(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnObjects) (interfaces.ObjectSubGraph, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "基于一组对象实例组织关系子图")
	defer span.End()

	var result interfaces.ObjectSubGraph
	result.Objects = make(map[string]interfaces.ObjectInfoInSubgraph)
	result.IsolatedObjects = make(map[string]interfaces.ObjectInfoInSubgraph)
	result.RelationPaths = []interfaces.RelationPath{}

	// 1. Handle input object instances, then validate and query object data.
	objectsByType, objectTypeMap, err := kns.processInputObjects(ctx, query)
	if err != nil {
		return result, err
	}

	if len(objectsByType) == 0 {
		return result, nil
	}

	// 2. Discover related relation types.
	allRelationTypes, err := kns.discoverRelationTypes(ctx, query, objectTypeMap)
	if err != nil {
		return result, err
	}

	// 3. Match relations.
	relations, objectsInRelations, err := kns.matchRelations(ctx, query, objectsByType, allRelationTypes)
	if err != nil {
		return result, err
	}

	// 4. Build the subgraph.
	result = kns.buildSubgraphFromObjects(query, objectsByType, relations, objectsInRelations)

	return result, nil
}

// Handle input object instances, then validate and query object data.
func (kns *knowledgeNetworkService) processInputObjects(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnObjects) (map[string][]interfaces.LevelObject, map[string]*interfaces.ObjectType, error) {

	objectsByType := make(map[string][]interfaces.LevelObject)
	objectTypeMap := make(map[string]*interfaces.ObjectType)

	// Group input objects by object type.
	typeGroups := make(map[string][]interfaces.InputObjectInstance)
	for _, entry := range query.Entries {
		typeGroups[entry.ObjectTypeID] = append(typeGroups[entry.ObjectTypeID], entry)
	}

	// For each object type, batch-query object data.
	for otID, entries := range typeGroups {
		// Get object type information.
		objectType, exists, err := kns.omAccess.GetObjectType(ctx, query.KNID, query.Branch, otID)
		if err != nil {
			return nil, nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				oerrors.OntologyQuery_ObjectType_InternalError_GetObjectTypesByIDFailed).WithErrorDetails(err.Error())
		}
		if !exists {
			return nil, nil, rest.NewHTTPError(ctx, http.StatusNotFound,
				oerrors.OntologyQuery_ObjectType_ObjectTypeNotFound).WithErrorDetails(
				locale.ValidationDetail(ctx, "ObjectTypeNotFound", map[string]any{"objectTypeID": otID}),
			)
		}
		objectTypeMap[otID] = &objectType

		// Build the unique identity condition.
		instanceIdentities := make([]map[string]any, len(entries))
		for i, entry := range entries {
			instanceIdentities[i] = entry.InstanceIdentity
		}
		ukCond := logics.BuildInstanceIdentitiesCondition(instanceIdentities)

		// Queryobject data.
		objectQuery := &interfaces.ObjectQueryBaseOnObjectType{
			ActualCondition: ukCond,
			PageQuery: interfaces.PageQuery{
				Limit:     interfaces.MAX_LIMIT,
				NeedTotal: false,
			},
			KNID:         query.KNID,
			Branch:       query.Branch,
			ObjectTypeID: otID,
			CommonQueryParameters: interfaces.CommonQueryParameters{
				IncludeTypeInfo:    true,
				IncludeLogicParams: query.IncludeLogicParams,
				IgnoringStore:      query.IgnoringStore,
			},
		}

		objects, err := kns.ots.GetObjectsByObjectTypeID(ctx, objectQuery)
		if err != nil {
			return nil, nil, err
		}

		// Build the LevelObject list.
		levelObjects := make([]interfaces.LevelObject, 0, len(objects.Datas))
		for _, objData := range objects.Datas {
			objectID, uk := logics.GetObjectID(objData, objects.ObjectType)
			if objectID == "" {
				continue
			}
			levelObjects = append(levelObjects, interfaces.LevelObject{
				ObjectID:   objectID,
				ObjectUK:   uk,
				ObjectData: objData,
				ObjectType: objects.ObjectType,
			})
		}
		objectsByType[otID] = levelObjects
	}

	return objectsByType, objectTypeMap, nil
}

// Discover relation types related to the input object types.
func (kns *knowledgeNetworkService) discoverRelationTypes(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnObjects, objectTypeMap map[string]*interfaces.ObjectType) (map[string]interfaces.RelationType, error) {

	// Extract the set of all object type IDs.
	objectTypeIDs := make([]string, 0, len(objectTypeMap))
	for otID := range objectTypeMap {
		objectTypeIDs = append(objectTypeIDs, otID)
	}

	// Query all related relation types at once.
	relationTypes, err := kns.omAccess.ListRelationTypes(ctx, query.KNID, query.Branch, interfaces.RelationTypesQuery{
		SourceObjectTypeIDs: objectTypeIDs,
		TargetObjectTypeIDs: objectTypeIDs,
	})
	if err != nil {
		return nil, err
	}

	// Collect and filter relation types, ensuring both source and target are in the input set.
	allRelationTypes := make(map[string]interfaces.RelationType) // key: relationTypeID
	for _, rt := range relationTypes {
		if query.AuthorizedRelationTypeIDs != nil {
			if _, allowed := query.AuthorizedRelationTypeIDs[rt.RTID]; !allowed {
				continue
			}
		}
		// Ensure both source and target are in the input set.
		if _, exists := objectTypeMap[rt.SourceObjectTypeID]; !exists {
			continue
		}
		if _, exists := objectTypeMap[rt.TargetObjectTypeID]; !exists {
			continue
		}
		allRelationTypes[rt.RTID] = rt
	}

	return allRelationTypes, nil
}

// Match relations between object instances.
func (kns *knowledgeNetworkService) matchRelations(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnObjects,
	objectsByType map[string][]interfaces.LevelObject,
	allRelationTypes map[string]interfaces.RelationType) ([]interfaces.Relation, map[string]bool, error) {

	relations := []interfaces.Relation{}
	objectsInRelations := make(map[string]bool) // Record object IDs that participate in relations.

	// Directly iterate over all relation types.
	for _, relationType := range allRelationTypes {

		logger.Debugf("匹配关系类: %s, 源对象类型: %s, 目标对象类型: %s", relationType.RTID, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID)

		sourceOTID := relationType.SourceObjectTypeID
		targetOTID := relationType.TargetObjectTypeID

		sourceObjects := objectsByType[sourceOTID]
		targetObjects := objectsByType[targetOTID]

		if len(sourceObjects) == 0 || len(targetObjects) == 0 {
			continue
		}

		// Build TypeEdge to reuse existing logic.
		edge := &interfaces.TypeEdge{
			RelationTypeId:     relationType.RTID,
			RelationType:       relationType,
			SourceObjectTypeId: sourceOTID,
			TargetObjectTypeId: targetOTID,
			Direction:          interfaces.DIRECTION_FORWARD,
		}

		// Use the relation type source and target object sets to match relations and return matched relations.
		matchedRelations, err := kns.matchRelationsForPair(ctx, query, sourceObjects, targetObjects, edge)
		if err != nil {
			logger.Warnf("匹配关系失败: relationType=%s, error=%v", relationType.RTID, err)
			continue
		}

		// Add to the result.
		for _, rel := range matchedRelations {
			relations = append(relations, rel)
			objectsInRelations[rel.SourceObjectId] = true
			objectsInRelations[rel.TargetObjectId] = true
		}
	}

	return relations, objectsInRelations, nil
}

// Match relations between a pair of object types.
func (kns *knowledgeNetworkService) matchRelationsForPair(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnObjects,
	sourceObjects []interfaces.LevelObject,
	targetObjects []interfaces.LevelObject,
	edge *interfaces.TypeEdge) ([]interfaces.Relation, error) {

	// Determine the relation type and handle direct and indirect associations separately.
	switch edge.RelationType.MappingRules.(type) {
	case []interfaces.Mapping:
		// Direct association: match directly with existing targetObjects to avoid database queries.
		return kns.matchDirectRelations(sourceObjects, targetObjects, edge)

	case *interfaces.InDirectMapping:
		// Indirect association: query view data, but match only input target objects.
		return kns.matchIndirectRelations(ctx, query, sourceObjects, targetObjects, edge)

	case *interfaces.FilteredCrossJoinMapping:
		return kns.matchFilteredCrossJoinRelations(ctx, sourceObjects, targetObjects, edge)
	}

	return []interfaces.Relation{}, nil
}

// Match direct associations without querying the database.
func (kns *knowledgeNetworkService) matchDirectRelations(
	sourceObjects []interfaces.LevelObject,
	targetObjects []interfaces.LevelObject,
	edge *interfaces.TypeEdge) ([]interfaces.Relation, error) {

	relations := []interfaces.Relation{}
	mappingRules := edge.RelationType.MappingRules.([]interfaces.Mapping)

	// Directly iterate over source and target objects for matching.
	for _, sourceObj := range sourceObjects {
		for _, targetObj := range targetObjects {
			if logics.CheckDirectMappingConditions(sourceObj.ObjectData, targetObj.ObjectData, mappingRules, true) {
				relations = append(relations, interfaces.Relation{
					RelationTypeId:   edge.RelationTypeId,
					RelationTypeName: edge.RelationType.RTName,
					SourceObjectId:   sourceObj.ObjectID,
					TargetObjectId:   targetObj.ObjectID,
				})
			}
		}
	}

	return relations, nil
}

// Match indirect associations; view data must be queried, but only input target objects are matched.
func (kns *knowledgeNetworkService) matchIndirectRelations(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnObjects,
	sourceObjects []interfaces.LevelObject,
	targetObjects []interfaces.LevelObject,
	edge *interfaces.TypeEdge) ([]interfaces.Relation, error) {

	relations := []interfaces.Relation{}

	// Query view data; this is necessary because view data is not in the input objects.
	_, viewDataMap, err := kns.buildBatchConditionsForObjects(ctx, query, sourceObjects, edge, true)
	if err != nil {
		return nil, err
	}

	// Match directly with input target objects instead of querying the database.
	for _, sourceObj := range sourceObjects {
		objectViewData := viewDataMap[sourceObj.ObjectID]

		for _, targetObj := range targetObjects {
			mappingRules := edge.RelationType.MappingRules.(*interfaces.InDirectMapping)
			if logics.CheckIndirectMappingConditionsWithViewData(
				sourceObj.ObjectData,
				targetObj.ObjectData,
				mappingRules,
				true,
				objectViewData) {
				relations = append(relations, interfaces.Relation{
					RelationTypeId:   edge.RelationTypeId,
					RelationTypeName: edge.RelationType.RTName,
					SourceObjectId:   sourceObj.ObjectID,
					TargetObjectId:   targetObj.ObjectID,
				})
			}
		}
	}

	return relations, nil
}

// Build batch query conditions for objects, adapted for SubGraphQueryBaseOnObjects.
func (kns *knowledgeNetworkService) buildBatchConditionsForObjects(ctx context.Context,
	query *interfaces.SubGraphQueryBaseOnObjects,
	currentLevelObjects []interfaces.LevelObject,
	edge *interfaces.TypeEdge,
	isForward bool) ([]*cond.CondCfg, map[string][]map[string]any, error) {

	var conditions []*cond.CondCfg
	viewDataMap := make(map[string][]map[string]any)

	// Handle direct mappings first.
	directObjects := make([]interfaces.LevelObject, 0)
	indirectObjects := make([]interfaces.LevelObject, 0)

	for _, levelObj := range currentLevelObjects {
		switch edge.RelationType.MappingRules.(type) {
		case []interfaces.Mapping:
			directObjects = append(directObjects, levelObj)
		case *interfaces.InDirectMapping:
			indirectObjects = append(indirectObjects, levelObj)
		}
	}

	// Handle direct mappings.
	if len(directObjects) > 0 {
		directConditions, err := logics.BuildDirectBatchConditions(directObjects, edge, isForward)
		if err != nil {
			return nil, nil, err
		}
		conditions = append(conditions, directConditions...)
	}

	// Handle indirect mappings by batch-querying view data.
	if len(indirectObjects) > 0 {
		// Build a temporary SubGraphQueryBaseOnSource to call buildIndirectBatchConditions.
		tempQuery := &interfaces.SubGraphQueryBaseOnSource{
			KNID:   query.KNID,
			Branch: query.Branch,
			PageQuery: interfaces.PageQuery{
				NeedTotal: false,
			},
			CommonQueryParameters: interfaces.CommonQueryParameters{
				IncludeLogicParams: query.IncludeLogicParams,
				IgnoringStore:      query.IgnoringStore,
			},
		}
		indirectConditions, indirectViewData, err := kns.buildIndirectBatchConditions(ctx, tempQuery, indirectObjects, edge, isForward)
		if err != nil {
			return nil, nil, err
		}
		conditions = append(conditions, indirectConditions...)

		// Merge view data mappings.
		for k, v := range indirectViewData {
			viewDataMap[k] = v
		}
	}

	return conditions, viewDataMap, nil
}

// Build the subgraph.
func (kns *knowledgeNetworkService) buildSubgraphFromObjects(query *interfaces.SubGraphQueryBaseOnObjects,
	objectsByType map[string][]interfaces.LevelObject,
	relations []interfaces.Relation,
	objectsInRelations map[string]bool) interfaces.ObjectSubGraph {

	result := interfaces.ObjectSubGraph{
		Objects:         make(map[string]interfaces.ObjectInfoInSubgraph),
		IsolatedObjects: make(map[string]interfaces.ObjectInfoInSubgraph),
		RelationPaths:   []interfaces.RelationPath{},
	}

	// Build the object mapping table.
	for _, levelObjects := range objectsByType {
		for _, levelObj := range levelObjects {
			objInfo := interfaces.ObjectInfoInSubgraph{
				ObjectTypeId:   levelObj.ObjectType.OTID,
				ObjectTypeName: levelObj.ObjectType.OTName,
				Properties:     levelObj.ObjectData,
			}
			if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_ID, query.ExcludeSystemProperties) {
				objInfo.InstanceID = levelObj.ObjectID
			}
			if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY, query.ExcludeSystemProperties) {
				objInfo.InstanceIdentity = levelObj.ObjectUK
			}
			if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_DISPLAY, query.ExcludeSystemProperties) {
				objInfo.Display = levelObj.ObjectData[levelObj.ObjectType.DisplayKey]
			}

			// Determine whether it is an isolated object.
			if objectsInRelations[levelObj.ObjectID] {
				result.Objects[levelObj.ObjectID] = objInfo
			} else {
				result.IsolatedObjects[levelObj.ObjectID] = objInfo
			}
		}
	}

	// Build relation paths with length 1.
	for _, rel := range relations {
		result.RelationPaths = append(result.RelationPaths, interfaces.RelationPath{
			Relations: []interfaces.Relation{rel},
			Length:    1,
		})
	}

	return result
}
