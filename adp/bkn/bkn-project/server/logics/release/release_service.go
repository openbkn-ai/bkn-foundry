// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
)

var errEmptyCursorID = errors.New("cursor has no id")

var (
	rsOnce sync.Once
	rs     interfaces.ReleaseService
)

type releaseService struct {
	ra interfaces.ReleaseAccess
}

func NewReleaseService() interfaces.ReleaseService {
	rsOnce.Do(func() {
		rs = &releaseService{ra: logics.RA}
	})
	return rs
}

func internalError(ctx context.Context, operation string, err error) error {
	otellog.LogError(ctx, operation, err)
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, perrors.BknProject_Release_InternalError).
		WithErrorDetails(err.Error())
}

func (s *releaseService) ListReleases(ctx context.Context, lineageID string, limit int, cursor string) (*interfaces.ReleaseList, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListReleases")
	defer span.End()

	query := interfaces.ReleasesQuery{LineageID: lineageID, Limit: limit + 1}
	if cursor != "" {
		position, err := decodeCursor(cursor)
		if err != nil {
			span.SetStatus(codes.Error, "Decode cursor failed")
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_InvalidParameter_Cursor)
		}
		query.Cursor = position
	}

	// One row past the page tells whether another page follows.
	releases, err := s.ra.ListReleases(ctx, query)
	if err != nil {
		span.SetStatus(codes.Error, "List releases failed")
		return nil, internalError(ctx, "List releases failed", err)
	}
	total, err := s.ra.CountReleases(ctx, lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Count releases failed")
		return nil, internalError(ctx, "Count releases failed", err)
	}

	list := &interfaces.ReleaseList{Entries: releases, Total: total}
	if len(releases) > limit {
		list.Entries = releases[:limit]
		last := list.Entries[limit-1]
		next := encodeCursor(interfaces.ReleaseCursor{CreateTime: last.CreateTime, ID: last.ID})
		list.NextCursor = &next
	}
	span.SetStatus(codes.Ok, "")
	return list, nil
}

func (s *releaseService) GetRelease(ctx context.Context, networkID string) (*interfaces.Release, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetRelease")
	defer span.End()

	release, exist, err := s.ra.GetReleaseByNetworkID(ctx, networkID)
	if err != nil {
		span.SetStatus(codes.Error, "Get release failed")
		return nil, internalError(ctx, "Get release failed", err)
	}
	if !exist {
		span.SetStatus(codes.Error, "Release not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Release_NotFound)
	}
	span.SetStatus(codes.Ok, "")
	return release, nil
}

// encodeCursor makes the keyset position opaque to clients.
func encodeCursor(position interfaces.ReleaseCursor) string {
	b, _ := json.Marshal(position)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(cursor string) (*interfaces.ReleaseCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, err
	}
	position := &interfaces.ReleaseCursor{}
	if err = json.Unmarshal(b, position); err != nil {
		return nil, err
	}
	if position.ID == "" {
		return nil, errEmptyCursorID
	}
	return position, nil
}
