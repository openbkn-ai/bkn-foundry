// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package object_type_stats counts the instances behind a knowledge network's object types.
package object_type_stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/object_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

// queryConcurrency bounds exact scans across all requests in this process,
// including scans whose callers have already received an in-progress response.
const queryConcurrency = 4

// countColumn names the column of the counting statement.
const countColumn = "n"

// resourceCategoryDataset is the only vega resource category whose row_count is counted live.
const resourceCategoryDataset = "dataset"

type objectTypeStatsService struct {
	ota   interfaces.ObjectTypeAccess
	ots   interfaces.ObjectTypeService
	ps    interfaces.PermissionService
	cqs   interfaces.CypherQueryService
	cache *countCache
	// running names the counts carrying on in the background, so a table is scanned once at a time.
	running *inflightCounts
	slots   chan struct{}

	queryTimeout      time.Duration
	backgroundTimeout time.Duration
	now               func() time.Time
}

var (
	serviceOnce sync.Once
	serviceInst interfaces.ObjectTypeStatsService
)

// NewObjectTypeStatsService creates the object type statistics service.
func NewObjectTypeStatsService(appSetting *common.AppSetting) interfaces.ObjectTypeStatsService {
	serviceOnce.Do(func() {
		serviceInst = NewObjectTypeStatsServiceWith(logics.OTA, object_type.NewObjectTypeService(appSetting),
			permission.NewPermissionService(appSetting), cypher.NewCypherQueryService(appSetting),
			secondsOr(appSetting.ServerSetting.ObjectTypeStatsCacheTTL, interfaces.OBJECT_TYPE_STATS_DEFAULT_CACHE_TTL_SEC),
			secondsOr(appSetting.ServerSetting.ObjectTypeStatsQueryTimeout, interfaces.OBJECT_TYPE_STATS_DEFAULT_QUERY_TIMEOUT_SEC),
			secondsOr(appSetting.ServerSetting.ObjectTypeStatsBackgroundTimeout,
				interfaces.OBJECT_TYPE_STATS_DEFAULT_BACKGROUND_TIMEOUT_SEC))
	})
	return serviceInst
}

// NewObjectTypeStatsServiceWith builds the service from injected dependencies (for testing).
func NewObjectTypeStatsServiceWith(ota interfaces.ObjectTypeAccess, ots interfaces.ObjectTypeService,
	ps interfaces.PermissionService, cqs interfaces.CypherQueryService,
	cacheTTL, queryTimeout, backgroundTimeout time.Duration) *objectTypeStatsService {
	return &objectTypeStatsService{
		ota:               ota,
		ots:               ots,
		ps:                ps,
		cqs:               cqs,
		cache:             newCountCache(cacheTTL),
		running:           newInflightCounts(),
		slots:             make(chan struct{}, queryConcurrency),
		queryTimeout:      queryTimeout,
		backgroundTimeout: min(backgroundTimeout, time.Hour),
		now:               time.Now,
	}
}

func secondsOr(configured, fallback int) time.Duration {
	if configured > 0 {
		return time.Duration(configured) * time.Second
	}
	return time.Duration(fallback) * time.Second
}

// countTask is one object type the caller may read, on its way to a count.
type countTask struct {
	objectType *interfaces.ObjectType
	entry      *interfaces.ObjectTypeStatsEntry
	cacheKey   string
	// unfiltered is true when no row policy narrows what the caller sees. Only then may a count come
	// from an index, which holds every row whether the caller may see it or not.
	unfiltered bool
	refresh    bool
	digest     string
	active     *countFlight
}

// ObjectTypeStats counts the instances of each object type, taking every count from the cheapest
// source that can answer it truthfully:
//
//  1. catalog: a recent timestamped exact count supplied by Vega;
//  2. index: a live dataset count or the object type's approximate index count;
//  3. query: an exact COUNT(*), run as the caller through the Cypher engine, with a timeout. A count
//     that outlasts the timeout carries on in the background and lands in the cache, and its row says
//     so rather than failing outright.
//
// Authorization matches run_cypher. Each object type needs query_data; one the caller cannot see
// at all is reported as not found, one they can see but not query as forbidden, and neither
// carries a number. A row policy on an object type rules out the index, which counts rows the
// caller may not see: its count is always a query, which applies the policy.
//
// Counts are cached per object type, model version and effective row policy, so callers with the
// same view share them and a changed model or policy is never answered from an older count. One
// object type failing leaves an error on its own row and the others unaffected.
func (s *objectTypeStatsService) ObjectTypeStats(ctx context.Context,
	query interfaces.ObjectTypeStatsQuery) (*interfaces.ObjectTypeStatsResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Count object type instances")
	defer span.End()

	if query.Branch == "" {
		query.Branch = interfaces.MAIN_BRANCH
	}
	requested := common.DuplicateSlice(query.OTIDs)
	if err := permission.ValidateKNChildAuthorizationIDs(ctx, query.KNID, requested); err != nil {
		return nil, err
	}

	objectTypes, err := s.loadObjectTypes(ctx, query.KNID, query.Branch, requested)
	if err != nil {
		span.SetStatus(codes.Error, "Load object types failed")
		return nil, err
	}

	entries, tasks, err := s.authorize(ctx, query.KNID, requested, objectTypes)
	if err != nil {
		span.SetStatus(codes.Error, "Authorize object types failed")
		return nil, err
	}
	s.applyRowFilters(ctx, query.KNID, query.Branch, tasks)

	var misses []*countTask
	for _, task := range tasks {
		if task.entry.Error != nil {
			continue
		}
		task.active = s.running.get(task.cacheKey)
		if !query.Refresh && task.active == nil {
			if cached, ok := s.cache.get(task.cacheKey, s.now()); ok {
				cached.fill(task.entry)
				continue
			}
		}
		misses = append(misses, task)
	}
	for _, task := range misses {
		task.refresh = query.Refresh
	}
	s.count(ctx, query.KNID, query.Branch, misses)

	span.SetStatus(codes.Ok, "")
	return &interfaces.ObjectTypeStatsResult{Entries: entries, Total: len(entries)}, nil
}

// loadObjectTypes reads the requested object types, or every object type of the network, with
// their index status. The object types come back keyed by ID.
func (s *objectTypeStatsService) loadObjectTypes(ctx context.Context, knID, branch string,
	requested []string) (map[string]*interfaces.ObjectType, error) {
	ids := requested
	if len(ids) == 0 {
		all, err := s.ota.GetObjectTypeIDsByKnID(ctx, knID, branch)
		if err != nil {
			common.LogSafeError(ctx, "Object type stats failed to list object type ids", err)
			return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed)
		}
		ids = all
	}
	byID := make(map[string]*interfaces.ObjectType, len(ids))
	if len(ids) == 0 {
		return byID, nil
	}
	objectTypes, err := s.ota.GetObjectTypesByIDs(ctx, nil, knID, branch, ids)
	if err != nil {
		common.LogSafeError(ctx, "Object type stats failed to read object types", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed)
	}
	for _, objectType := range objectTypes {
		if objectType != nil {
			byID[objectType.OTID] = objectType
		}
	}
	return byID, nil
}

// authorize decides, in one permission decision, what the caller may learn about each object type,
// and returns the response rows in order together with the object types left to count.
//
// An object type the caller holds no operation on is treated as absent: listed by ID it reads as
// not found, exactly like an ID that does not exist, and in a whole-network listing it is left out.
// One the caller can see but not query reads as forbidden.
func (s *objectTypeStatsService) authorize(ctx context.Context, knID string, requested []string,
	objectTypes map[string]*interfaces.ObjectType) ([]*interfaces.ObjectTypeStatsEntry, []*countTask, error) {
	ids := requested
	if len(ids) == 0 {
		ids = make([]string, 0, len(objectTypes))
		for id := range objectTypes {
			ids = append(ids, id)
		}
		slices.Sort(ids)
	}

	existing := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := objectTypes[id]; ok {
			existing = append(existing, id)
		}
	}
	visible := map[string]interfaces.PermissionResourceOps{}
	if len(existing) > 0 {
		var err error
		visible, err = permission.FilterKNChildResourceIDsWithAnyOperation(ctx, s.ps,
			interfaces.RESOURCE_TYPE_OBJECT_TYPE, interfaces.KNChildResourceIDs(knID, existing))
		if err != nil {
			return nil, nil, err
		}
	}

	entries := make([]*interfaces.ObjectTypeStatsEntry, 0, len(ids))
	tasks := make([]*countTask, 0, len(ids))
	for _, id := range ids {
		entry := &interfaces.ObjectTypeStatsEntry{ID: id}
		ops, seen := visible[interfaces.KNChildResourceID(knID, id)]
		switch {
		case !seen && len(requested) == 0:
			continue
		case !seen:
			entry.Error = rowError(httperrors.NewHTTPError(ctx, http.StatusNotFound,
				berrors.BknBackend_ObjectType_ObjectTypeNotFound))
		case !slices.Contains(ops.Operations, interfaces.OPERATION_TYPE_QUERY_DATA):
			entry.Error = rowError(httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden))
		default:
			tasks = append(tasks, &countTask{objectType: objectTypes[id], entry: entry})
		}
		entries = append(entries, entry)
	}
	return entries, tasks, nil
}

// applyRowFilters records, for every object type left to count, whether a row policy narrows it and
// the cache key its count lives under. Failing to resolve the policies fails those rows only: a
// count must never be taken without knowing which rows it may include.
func (s *objectTypeStatsService) applyRowFilters(ctx context.Context, knID, branch string, tasks []*countTask) {
	if len(tasks) == 0 {
		return
	}
	refs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		refs = append(refs, interfaces.KNChildResourceID(knID, task.objectType.OTID))
	}
	decisions, err := s.ps.ResolveRowFilters(ctx, refs)
	if err != nil {
		common.LogSafeError(ctx, "Object type stats failed to resolve row filters", err)
		for _, task := range tasks {
			task.entry.Error = errorFor(ctx, err)
		}
		return
	}
	byRef := make(map[string]interfaces.RowFilterDecisionEntry, len(decisions))
	duplicates := make(map[string]bool)
	for _, decision := range decisions {
		if _, exists := byRef[decision.ObjectTypeRef]; exists {
			duplicates[decision.ObjectTypeRef] = true
		}
		byRef[decision.ObjectTypeRef] = decision
	}
	for i, task := range tasks {
		decision, ok := byRef[refs[i]]
		if !ok || duplicates[refs[i]] || strings.TrimSpace(decision.EffectiveRowFilterDigest) == "" {
			task.entry.Error = rowError(httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_StatsQueryFailed))
			continue
		}
		task.unfiltered = decision.Predicate.Kind == "true"
		task.digest = decision.EffectiveRowFilterDigest
		task.cacheKey = strings.Join([]string{knID, branch, task.objectType.OTID,
			strconv.FormatInt(task.objectType.UpdateTime, 10), decision.EffectiveRowFilterDigest}, "\x00")
	}
}

// count takes the count of every task from the cheapest source that can answer it, and caches what
// it gets. At most queryConcurrency exact counts run at once.
func (s *objectTypeStatsService) count(ctx context.Context, knID, branch string, tasks []*countTask) {
	var needResource []*interfaces.ObjectType
	for _, task := range tasks {
		if task.active == nil {
			task.active = s.running.get(task.cacheKey)
		}
		if task.active == nil && task.unfiltered && boundToResource(task.objectType) {
			needResource = append(needResource, task.objectType)
		}
	}
	resources := map[string]*interfaces.VegaResource{}
	if len(needResource) > 0 {
		resources = s.ots.GetObjectTypeResources(ctx, needResource)
	}

	var queries []*countTask
	for _, task := range tasks {
		if !boundToResource(task.objectType) {
			task.entry.Error = rowError(httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_NoDataSource))
			continue
		}
		if task.active != nil {
			queries = append(queries, task)
			continue
		}
		if result, ok := s.metadataCount(task, resources[task.objectType.OTID]); ok {
			s.settle(task, result)
			continue
		}
		queries = append(queries, task)
	}

	// The wait budget covers the entire batch. Long counts retain their global slot
	// after this request returns; later requests join that same flight.
	waitCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()
	jobs := make(chan *countTask)
	var wg sync.WaitGroup
	for range min(queryConcurrency, len(queries)) {
		wg.Go(func() {
			for task := range jobs {
				flight := s.startCount(waitCtx, ctx, knID, branch, task)
				if flight != nil {
					select {
					case <-flight.done:
						if flight.err != nil {
							task.entry.Error = errorFor(ctx, flight.err)
						} else {
							flight.result.fill(task.entry)
						}
						continue
					case <-waitCtx.Done():
					}
				}
				task.entry.Error = rowError(httperrors.NewHTTPError(ctx, http.StatusAccepted,
					berrors.BknBackend_ObjectType_StatsCountInProgress))
			}
		})
	}
	for _, task := range queries {
		jobs <- task
	}
	close(jobs)
	wg.Wait()
}

// startCount admits at most four scans across requests, deduplicating each cache
// key both before and after admission. It never starts a new scan after the caller's
// wait budget expires. Rows waiting for capacity can be retried by polling.
func (s *objectTypeStatsService) startCount(waitCtx, caller context.Context, knID, branch string, task *countTask) *countFlight {
	// Retain the observed flight even if it completes before this worker starts,
	// so polling cannot replace its fresh result with an older metadata count.
	if task.active != nil {
		return task.active
	}
	if flight := s.running.get(task.cacheKey); flight != nil {
		return flight
	}
	if waitCtx.Err() != nil {
		return nil
	}
	select {
	case s.slots <- struct{}{}:
	case <-waitCtx.Done():
		return nil
	}
	if waitCtx.Err() != nil {
		<-s.slots
		return nil
	}
	flight, started := s.running.begin(task.cacheKey)
	if !started {
		<-s.slots
		return flight
	}
	// A concurrent request may have filled the cache while we awaited capacity.
	if !task.refresh {
		if cached, ok := s.cache.get(task.cacheKey, s.now()); ok {
			s.running.finish(task.cacheKey, flight, cached, nil)
			<-s.slots
			return flight
		}
	}
	go func() {
		defer func() { <-s.slots }()
		background, cancel := context.WithTimeout(context.WithoutCancel(caller), s.backgroundTimeout)
		defer cancel()
		result, err := s.runCount(background, knID, branch, task)
		if err == nil {
			s.cache.put(task.cacheKey, result, s.now())
		}
		s.running.finish(task.cacheKey, flight, result, err)
	}()
	return flight
}

// metadataCount answers from what is already known about the data, without reading it. It never
// answers for an object type under a row policy.
func (s *objectTypeStatsService) metadataCount(task *countTask, resource *interfaces.VegaResource) (cachedCount, bool) {
	if !task.unfiltered {
		return cachedCount{}, false
	}
	now := s.now()
	if resource != nil && resource.RowCount != nil && resource.RowCountTime != nil && *resource.RowCount >= 0 {
		asOf := time.UnixMilli(*resource.RowCountTime)
		// Force refresh bypasses persisted counts. A dataset read itself counts live.
		if *resource.RowCountTime > 0 && !asOf.After(now) && now.Sub(asOf) < s.cache.ttl &&
			(!task.refresh || resource.Category == resourceCategoryDataset) {
			source := interfaces.OBJECT_TYPE_COUNT_SOURCE_CATALOG
			if resource.Category == resourceCategoryDataset {
				source = interfaces.OBJECT_TYPE_COUNT_SOURCE_INDEX
			}
			return cachedCount{count: *resource.RowCount, exact: true, source: source, asOf: asOf}, true
		}
	}
	if !task.refresh && indexAvailable(task.objectType) && task.objectType.Status.DocCount >= 0 {
		asOf := now
		if updated := task.objectType.Status.UpdateTime; updated > 0 {
			asOf = time.UnixMilli(updated)
		}
		return cachedCount{count: task.objectType.Status.DocCount, exact: false,
			source: interfaces.OBJECT_TYPE_COUNT_SOURCE_INDEX, asOf: asOf}, true
	}
	return cachedCount{}, false
}

// runCount runs an exact count through the Cypher engine as the caller, which applies the same
// authorization, row policies and proxy rules as any run_cypher statement.
func (s *objectTypeStatsService) runCount(ctx context.Context,
	knID, branch string, task *countTask) (cachedCount, error) {
	otID := task.objectType.OTID

	label := "`" + strings.ReplaceAll(otID, "`", "``") + "`"
	result, err := s.cqs.Query(ctx, interfaces.CypherQuery{
		KNID:                       knID,
		ExecutionTimeout:           s.backgroundTimeout,
		ExpectedRowFilterDigests:   map[string]string{otID: task.digest},
		ExpectedObjectTypeVersions: map[string]int64{otID: task.objectType.UpdateTime},
		Branch:                     branch,
		Query:                      fmt.Sprintf("MATCH (t:%s) RETURN count(*) AS %s", label, countColumn),
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return cachedCount{}, errQueryTimeout
		}
		return cachedCount{}, err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return cachedCount{}, errQueryTimeout
	}
	if result == nil || len(result.Entries) != 1 {
		return cachedCount{}, fmt.Errorf("the count returned no row")
	}
	count, ok := asInt64(result.Entries[0][countColumn])
	if !ok {
		return cachedCount{}, fmt.Errorf("the count returned a non-numeric value")
	}
	return cachedCount{count: count, exact: true, source: interfaces.OBJECT_TYPE_COUNT_SOURCE_QUERY,
		asOf: s.now()}, nil
}

// settle writes a count into its row and the cache.
func (s *objectTypeStatsService) settle(task *countTask, result cachedCount) {
	result.fill(task.entry)
	s.cache.put(task.cacheKey, result, s.now())
}

func indexAvailable(objectType *interfaces.ObjectType) bool {
	return objectType.Status != nil && objectType.Status.IndexAvailable
}

func boundToResource(objectType *interfaces.ObjectType) bool {
	return objectType.DataSource != nil && objectType.DataSource.ID != "" &&
		objectType.DataSource.Type == interfaces.DATA_SOURCE_TYPE_RESOURCE
}

var errQueryTimeout = errors.New("object type count timed out")

// errorFor turns a failure into a row error. Only a service error's code and description travel:
// a dependency's own message may quote the statement, which names physical tables.
func errorFor(ctx context.Context, err error) *interfaces.ObjectTypeStatsError {
	if errors.Is(err, errQueryTimeout) {
		return rowError(httperrors.NewHTTPError(ctx, http.StatusGatewayTimeout, berrors.BknBackend_ObjectType_StatsQueryTimeout))
	}
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) {
		return rowError(httpErr)
	}
	common.LogSafeError(ctx, "Object type count failed", err)
	return rowError(httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
		berrors.BknBackend_ObjectType_InternalError_StatsQueryFailed))
}

func rowError(httpErr *httperrors.HTTPError) *interfaces.ObjectTypeStatsError {
	// Render a copy because multiple rows may share the same dependency error.
	rendered := *httpErr
	message := rendered.ErrorCode
	if err := rendered.Render(); err == nil {
		message = rendered.Description
	}
	return &interfaces.ObjectTypeStatsError{Code: rendered.ErrorCode, Message: message}
}

// asInt64 reads a count out of a driver-shaped value. The vega adapter decodes numbers as
// json.Number; connectors may also return a count as an integer, a float or a string.
func asInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case json.Number:
		if parsed, err := v.Int64(); err == nil {
			return parsed, parsed >= 0
		}

	case int64:
		return v, v >= 0
	case int:
		return int64(v), v >= 0
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v < math.Exp2(63) && math.Trunc(v) == v {
			return int64(v), true
		}
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			return parsed, parsed >= 0
		}
	}
	return 0, false
}
