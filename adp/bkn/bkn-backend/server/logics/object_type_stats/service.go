// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package object_type_stats adapts Vega resource counts to BKN object types.
package object_type_stats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/object_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

type objectTypeStatsService struct {
	ota interfaces.ObjectTypeAccess
	ots interfaces.ObjectTypeService
	ps  interfaces.PermissionService
	cqs interfaces.CypherQueryService
}

func NewObjectTypeStatsService(setting *common.AppSetting) interfaces.ObjectTypeStatsService {
	return &objectTypeStatsService{ota: logics.OTA, ots: object_type.NewObjectTypeService(setting), ps: permission.NewPermissionService(setting), cqs: cypher.NewCypherQueryService(setting)}
}

// ObjectTypeStats only reads Vega measurements for unfiltered objects. Missing
// measurements stay null; count collection and refresh belong to Vega. Row
// policies require the existing Cypher path, which counts only visible rows.
func (s *objectTypeStatsService) ObjectTypeStats(ctx context.Context, q interfaces.ObjectTypeStatsQuery) (*interfaces.ObjectTypeStatsResult, error) {
	if q.Branch == "" {
		q.Branch = interfaces.MAIN_BRANCH
	}
	ids := common.DuplicateSlice(q.OTIDs)
	if err := permission.ValidateKNChildAuthorizationIDs(ctx, q.KNID, ids); err != nil {
		return nil, err
	}
	var err error
	if len(ids) == 0 {
		ids, err = s.ota.GetObjectTypeIDsByKnID(ctx, q.KNID, q.Branch)
		if err != nil {
			return nil, err
		}
		slices.Sort(ids)
	}
	result := &interfaces.ObjectTypeStatsResult{Entries: []*interfaces.ObjectTypeStatsEntry{}}
	if len(ids) == 0 {
		return result, nil
	}
	objects, err := s.ota.GetObjectTypesByIDs(ctx, nil, q.KNID, q.Branch, ids)
	if err != nil {
		common.LogSafeError(ctx, "Object type statistics failed to read model", err)
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed)
	}
	byID := make(map[string]*interfaces.ObjectType, len(objects))
	var refs []string
	for _, ot := range objects {
		if ot != nil {
			byID[ot.OTID] = ot
			refs = append(refs, interfaces.KNChildResourceID(q.KNID, ot.OTID))
		}
	}
	visible := map[string]interfaces.PermissionResourceOps{}
	if len(refs) > 0 {
		visible, err = permission.FilterKNChildResourceIDsWithAnyOperation(ctx, s.ps, interfaces.RESOURCE_TYPE_OBJECT_TYPE, refs)
		if err != nil {
			return nil, err
		}
	}
	entries := map[string]*interfaces.ObjectTypeStatsEntry{}
	var allowed []*interfaces.ObjectType
	for _, id := range ids {
		ot := byID[id]
		ops, seen := visible[interfaces.KNChildResourceID(q.KNID, id)]
		if ot != nil && !seen && len(q.OTIDs) == 0 {
			continue
		}
		entry := &interfaces.ObjectTypeStatsEntry{ID: id}
		result.Entries = append(result.Entries, entry)
		switch {
		case ot == nil || !seen:
			entry.Error = statsError(ctx, berrors.BknBackend_ObjectType_ObjectTypeNotFound)
		case !slices.Contains(ops.Operations, interfaces.OPERATION_TYPE_QUERY_DATA):
			entry.Error = statsError(ctx, httperrors.PublicError_Forbidden)
		case ot.DataSource == nil || ot.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE || ot.DataSource.ID == "":
			entry.Error = statsError(ctx, berrors.BknBackend_ObjectType_NoDataSource)
		default:
			allowed = append(allowed, ot)
			entries[id] = entry
		}
	}
	result.Total = len(result.Entries)
	if len(allowed) == 0 {
		return result, nil
	}
	refs = nil
	for _, ot := range allowed {
		refs = append(refs, interfaces.KNChildResourceID(q.KNID, ot.OTID))
	}
	decisions, err := s.ps.ResolveRowFilters(ctx, refs)
	predicates := map[string]string{}
	duplicates := map[string]bool{}
	for _, d := range decisions {
		if _, exists := predicates[d.ObjectTypeRef]; exists {
			duplicates[d.ObjectTypeRef] = true
		}
		predicates[d.ObjectTypeRef] = d.Predicate.Kind
	}
	var unfiltered, filtered []*interfaces.ObjectType
	for _, ot := range allowed {
		ref := interfaces.KNChildResourceID(q.KNID, ot.OTID)
		switch {
		case err != nil || predicates[ref] == "" || duplicates[ref]:
			entries[ot.OTID].Error = statsError(ctx, berrors.BknBackend_ObjectType_InternalError_StatsQueryFailed)
		case predicates[ref] == "true":
			unfiltered = append(unfiltered, ot)
		default:
			filtered = append(filtered, ot)
		}
	}
	if len(unfiltered) > 0 {
		resources := s.ots.GetObjectTypeResources(ctx, unfiltered)
		for _, ot := range unfiltered {
			resource := resources[ot.OTID]
			// Do not promote an old discovery estimate or an untimestamped measurement
			// to an exact count. Absence never triggers a BKN scan or background task.
			if resource == nil || resource.RowCount == nil || *resource.RowCount < 0 || resource.RowCountTime == nil || *resource.RowCountTime <= 0 {
				continue
			}
			entry := entries[ot.OTID]
			count := *resource.RowCount
			entry.Count = &count
			entry.Exact = true
			entry.Source = "catalog"
			entry.AsOf = *resource.RowCountTime
			if resource.Category == "dataset" {
				entry.Source = "index"
			}
		}
	}
	// Only filtered counts need execution. Bound concurrency within this request;
	// leave cancellation, execution timeouts and permission enforcement to Cypher.
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, ot := range filtered {
		slots <- struct{}{}
		wg.Go(func() { defer func() { <-slots }(); s.countFiltered(ctx, q, ot.OTID, entries[ot.OTID]) })
	}
	wg.Wait()
	return result, nil
}

func (s *objectTypeStatsService) countFiltered(ctx context.Context, q interfaces.ObjectTypeStatsQuery, id string, entry *interfaces.ObjectTypeStatsEntry) {
	result, err := s.cqs.Query(ctx, interfaces.CypherQuery{KNID: q.KNID, Branch: q.Branch, Query: "MATCH (t:`" + strings.ReplaceAll(id, "`", "``") + "`) RETURN count(*) AS n"})
	if err == nil && result != nil && len(result.Entries) == 1 {
		count, parseErr := json.Number(fmt.Sprint(result.Entries[0]["n"])).Int64()
		if parseErr == nil && count >= 0 {
			entry.Count = &count
			entry.Exact = true
			entry.Source = "query"
			entry.AsOf = time.Now().UnixMilli()
			return
		}
	}
	entry.Error = statsError(ctx, berrors.BknBackend_ObjectType_InternalError_StatsQueryFailed)
}

func statsError(ctx context.Context, code string) *interfaces.ObjectTypeStatsError {
	status := http.StatusInternalServerError
	if code == berrors.BknBackend_ObjectType_ObjectTypeNotFound {
		status = http.StatusNotFound
	}
	if code == httperrors.PublicError_Forbidden {
		status = http.StatusForbidden
	}
	if code == berrors.BknBackend_ObjectType_NoDataSource {
		status = http.StatusBadRequest
	}
	e := httperrors.NewHTTPError(ctx, status, code)
	message := code
	if e.Render() == nil {
		message = e.Description
	}
	return &interfaces.ObjectTypeStatsError{Code: code, Message: message}
}
