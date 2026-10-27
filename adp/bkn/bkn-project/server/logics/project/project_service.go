// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package project

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/lineage"
)

const (
	maxNameRunes     = 128
	maxCatalogIDs    = 100
	maxCatalogIDLen  = 40
	projectBranchFmt = "project/%s"
)

var (
	psOnce sync.Once
	ps     interfaces.ProjectService
)

type projectService struct {
	pa  interfaces.ProjectAccess
	ra  interfaces.ReleaseAccess
	bba interfaces.BknBackendAccess
	ls  interfaces.LineageService
}

func NewProjectService() interfaces.ProjectService {
	psOnce.Do(func() {
		ps = &projectService{pa: logics.PA, ra: logics.RA, bba: logics.BBA, ls: lineage.NewLineageService()}
	})
	return ps
}

func (s *projectService) CreateProject(ctx context.Context, token string, req interfaces.CreateProjectRequest,
	owner interfaces.AccountInfo, author interfaces.GitAuthor) (*interfaces.ProjectEntry, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreateProject")
	defer span.End()

	name, catalogIDs, err := checkCreate(ctx, &req)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid request")
		return nil, err
	}

	knID := req.KNID
	if req.NewKN != nil {
		if knID, err = s.bba.CreateKN(ctx, token, req.NewKN.ID, strings.TrimSpace(req.NewKN.Name)); err != nil {
			span.SetStatus(codes.Error, "Create network failed")
			return nil, backendHTTPError(ctx, "Create network failed", err)
		}
	} else if knID, err = s.lineageOf(ctx, token, knID); err != nil {
		span.SetStatus(codes.Error, "Get network failed")
		return nil, err
	}

	// Every project of a network shares the network's lineage.
	if _, err = s.ls.CreateLineage(ctx, token, knID, author); err != nil && !hasCode(err, perrors.BknProject_Lineage_AlreadyExists) {
		span.SetStatus(codes.Error, "Create lineage failed")
		return nil, err
	}
	baseline, err := s.liveNetworkID(ctx, knID)
	if err != nil {
		span.SetStatus(codes.Error, "Get live network failed")
		return nil, internalError(ctx, "Get live network failed", err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		span.SetStatus(codes.Error, "Generate id failed")
		return nil, internalError(ctx, "Generate project id failed", err)
	}
	branch := fmt.Sprintf(projectBranchFmt, id.String())
	if _, err = s.ls.CreateBranch(ctx, knID, branch, interfaces.GIT_MAIN_BRANCH); err != nil {
		span.SetStatus(codes.Error, "Create branch failed")
		return nil, err
	}

	// The row comes last: a failure before it leaves at most an unused branch, never a project.
	now := time.Now().UnixMilli()
	project := &interfaces.Project{
		ID:                id.String(),
		Name:              name,
		KNID:              knID,
		Branch:            branch,
		BaselineNetworkID: baseline,
		CatalogIDs:        catalogIDs,
		Owner:             owner,
		CreateTime:        now,
		UpdateTime:        now,
	}
	if err = s.pa.CreateProject(ctx, project); err != nil {
		span.SetStatus(codes.Error, "Create project failed")
		return nil, internalError(ctx, "Create project failed", err)
	}
	entries, err := s.withOtherProjects(ctx, []*interfaces.Project{project})
	if err != nil {
		span.SetStatus(codes.Error, "Count projects failed")
		return nil, internalError(ctx, "Count projects failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return entries[0], nil
}

func (s *projectService) ListProjects(ctx context.Context, token string, archived string, name string, limit int,
	cursor string) (*interfaces.ProjectList, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListProjects")
	defer span.End()

	// Guard here as well as in the handler: the paging below indexes by limit.
	if limit < 1 || limit > interfaces.MAX_PROJECT_LIMIT {
		span.SetStatus(codes.Error, "Invalid limit")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_InvalidParameter_Limit)
	}
	query := interfaces.ProjectsQuery{Name: strings.TrimSpace(name), Limit: limit + 1}
	switch archived {
	case "", interfaces.PROJECT_ARCHIVED_FALSE:
		unarchived := false
		query.Archived = &unarchived
	case interfaces.PROJECT_ARCHIVED_TRUE:
		archivedOnly := true
		query.Archived = &archivedOnly
	case interfaces.PROJECT_ARCHIVED_ALL:
	default:
		span.SetStatus(codes.Error, "Invalid archived")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidArchived)
	}
	if utf8.RuneCountInString(query.Name) > maxNameRunes {
		span.SetStatus(codes.Error, "Invalid name")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidName)
	}
	if cursor != "" {
		position, err := decodeCursor(cursor)
		if err != nil {
			span.SetStatus(codes.Error, "Decode cursor failed")
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_InvalidParameter_Cursor)
		}
		query.Cursor = position
	}

	knIDs, err := s.bba.ListKNIDs(ctx, token)
	if err != nil {
		span.SetStatus(codes.Error, "List networks failed")
		return nil, backendHTTPError(ctx, "List networks failed", err)
	}
	query.KNIDs = knIDs

	// One row past the page tells whether another page follows.
	projects, err := s.pa.ListProjects(ctx, query)
	if err != nil {
		span.SetStatus(codes.Error, "List projects failed")
		return nil, internalError(ctx, "List projects failed", err)
	}
	total, err := s.pa.CountProjects(ctx, query)
	if err != nil {
		span.SetStatus(codes.Error, "Count projects failed")
		return nil, internalError(ctx, "Count projects failed", err)
	}
	list := &interfaces.ProjectList{Total: total}
	if len(projects) > limit {
		projects = projects[:limit]
		last := projects[limit-1]
		next := encodeCursor(interfaces.ProjectCursor{CreateTime: last.CreateTime, ID: last.ID})
		list.NextCursor = &next
	}
	if list.Entries, err = s.withOtherProjects(ctx, projects); err != nil {
		span.SetStatus(codes.Error, "Count projects failed")
		return nil, internalError(ctx, "Count projects failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return list, nil
}

func (s *projectService) GetProject(ctx context.Context, token string, id string) (*interfaces.ProjectEntry, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetProject")
	defer span.End()

	project, err := s.visibleProject(ctx, token, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	entries, err := s.withOtherProjects(ctx, []*interfaces.Project{project})
	if err != nil {
		span.SetStatus(codes.Error, "Count projects failed")
		return nil, internalError(ctx, "Count projects failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return entries[0], nil
}

func (s *projectService) SetProjectArchived(ctx context.Context, token string, id string, archived bool,
	caller interfaces.AccountInfo) (*interfaces.ProjectEntry, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SetProjectArchived")
	defer span.End()

	project, err := s.visibleProject(ctx, token, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if project.Owner != caller {
		span.SetStatus(codes.Error, "Not the owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Project_NotOwner)
	}
	if project.Archived != archived {
		project.Archived = archived
		project.UpdateTime = time.Now().UnixMilli()
		if err = s.pa.SetProjectArchived(ctx, id, archived, project.UpdateTime); err != nil {
			span.SetStatus(codes.Error, "Archive project failed")
			return nil, internalError(ctx, "Archive project failed", err)
		}
	}
	entries, err := s.withOtherProjects(ctx, []*interfaces.Project{project})
	if err != nil {
		span.SetStatus(codes.Error, "Count projects failed")
		return nil, internalError(ctx, "Count projects failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return entries[0], nil
}

// SetProjectCatalogs replaces the catalogs the project drafts from. The draft stays as it
// is; drafting again reads the new catalogs.
func (s *projectService) SetProjectCatalogs(ctx context.Context, token string, id string, catalogIDs []string,
	caller interfaces.AccountInfo) (*interfaces.ProjectEntry, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SetProjectCatalogs")
	defer span.End()

	catalogIDs, err := checkCatalogIDs(ctx, catalogIDs)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid catalog ids")
		return nil, err
	}
	project, err := s.visibleProject(ctx, token, id)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if project.Owner != caller {
		span.SetStatus(codes.Error, "Not the owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Project_NotOwner)
	}
	if !slices.Equal(project.CatalogIDs, catalogIDs) {
		project.CatalogIDs = catalogIDs
		project.UpdateTime = time.Now().UnixMilli()
		if err = s.pa.SetProjectCatalogs(ctx, id, catalogIDs, project.UpdateTime); err != nil {
			span.SetStatus(codes.Error, "Set catalogs failed")
			return nil, internalError(ctx, "Set catalogs failed", err)
		}
	}
	entries, err := s.withOtherProjects(ctx, []*interfaces.Project{project})
	if err != nil {
		span.SetStatus(codes.Error, "Count projects failed")
		return nil, internalError(ctx, "Count projects failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return entries[0], nil
}

// visibleProject returns the project when the caller can see its network. A
// project the caller cannot see answers like one that does not exist.
func (s *projectService) visibleProject(ctx context.Context, token string, id string) (*interfaces.Project, error) {
	project, exist, err := s.pa.GetProject(ctx, id)
	if err != nil {
		return nil, internalError(ctx, "Get project failed", err)
	}
	if !exist {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Project_NotFound)
	}
	if _, err = s.bba.GetKN(ctx, token, project.KNID); err != nil {
		var backendErr *interfaces.BknBackendError
		if errors.As(err, &backendErr) &&
			(backendErr.HTTPCode == http.StatusForbidden || backendErr.HTTPCode == http.StatusNotFound) {
			return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Project_NotFound)
		}
		return nil, backendHTTPError(ctx, "Get network failed", err)
	}
	return project, nil
}

// lineageOf returns the lineage a visible network belongs to: the base network
// of the release that published it, or the network itself. The caller must be
// able to see that base network too, as every later read of the project checks it.
func (s *projectService) lineageOf(ctx context.Context, token string, knID string) (string, error) {
	if _, err := s.bba.GetKN(ctx, token, knID); err != nil {
		return "", backendHTTPError(ctx, "Get network failed", err)
	}
	release, exist, err := s.ra.GetReleaseByNetworkID(ctx, knID)
	if err != nil {
		return "", internalError(ctx, "Get release failed", err)
	}
	if !exist {
		return knID, nil
	}
	if _, err = s.bba.GetKN(ctx, token, release.LineageID); err != nil {
		return "", backendHTTPError(ctx, "Get base network failed", err)
	}
	return release.LineageID, nil
}

// liveNetworkID is the network of the lineage's newest release, or the network
// itself before any release.
func (s *projectService) liveNetworkID(ctx context.Context, knID string) (string, error) {
	newest, err := s.ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: knID, Limit: 1})
	if err != nil {
		return "", err
	}
	if len(newest) == 0 {
		return knID, nil
	}
	return newest[0].NetworkID, nil
}

// withOtherProjects counts, for each project, the other unarchived projects of its network.
func (s *projectService) withOtherProjects(ctx context.Context, projects []*interfaces.Project) ([]*interfaces.ProjectEntry, error) {
	knIDs := []string{}
	seen := map[string]bool{}
	for _, project := range projects {
		if !seen[project.KNID] {
			seen[project.KNID] = true
			knIDs = append(knIDs, project.KNID)
		}
	}
	counts, err := s.pa.CountUnarchivedProjects(ctx, knIDs)
	if err != nil {
		return nil, err
	}
	entries := make([]*interfaces.ProjectEntry, 0, len(projects))
	for _, project := range projects {
		others := counts[project.KNID]
		if !project.Archived && others > 0 {
			others--
		}
		entries = append(entries, &interfaces.ProjectEntry{Project: project, OtherProjects: others})
	}
	return entries, nil
}

// checkCreate returns the trimmed name and the de-duplicated catalog ids of a valid request.
func checkCreate(ctx context.Context, req *interfaces.CreateProjectRequest) (string, []string, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > maxNameRunes {
		return "", nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidName)
	}
	existing := strings.TrimSpace(req.KNID) != ""
	creating := req.NewKN != nil
	if existing == creating || (creating && strings.TrimSpace(req.NewKN.Name) == "") {
		return "", nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidNetwork)
	}
	catalogIDs, err := checkCatalogIDs(ctx, req.CatalogIDs)
	if err != nil {
		return "", nil, err
	}
	return name, catalogIDs, nil
}

// checkCatalogIDs returns the de-duplicated catalog ids, in their order.
func checkCatalogIDs(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) > maxCatalogIDs {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidCatalogIDs)
	}
	catalogIDs := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > maxCatalogIDLen {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidCatalogIDs)
		}
		if !seen[id] {
			seen[id] = true
			catalogIDs = append(catalogIDs, id)
		}
	}
	return catalogIDs, nil
}

func hasCode(err error, code string) bool {
	var httpErr *httperrors.HTTPError
	return errors.As(err, &httpErr) && httpErr.ErrorCode == code
}

func internalError(ctx context.Context, operation string, err error) error {
	otellog.LogError(ctx, operation, err)
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, perrors.BknProject_Project_InternalError).
		WithErrorDetails(err.Error())
}

// backendHTTPError keeps a 4xx from bkn-backend, the caller's to fix, and turns
// anything else into a bad gateway.
func backendHTTPError(ctx context.Context, operation string, err error) error {
	var backendErr *interfaces.BknBackendError
	if !errors.As(err, &backendErr) {
		otellog.LogError(ctx, operation, err)
		return httperrors.NewHTTPError(ctx, http.StatusBadGateway, perrors.BknProject_Project_BknBackendRequestFailed).
			WithErrorDetails(err.Error())
	}
	status := backendErr.HTTPCode
	if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
		status = http.StatusBadGateway
	}
	otellog.LogWarn(ctx, fmt.Sprintf("%s: %v", operation, err))
	return httperrors.NewHTTPError(ctx, status, perrors.BknProject_Project_BknBackendRequestFailed).
		WithErrorDetails(map[string]any{"error_code": backendErr.ErrorCode, "description": backendErr.Description})
}

// encodeCursor makes the keyset position opaque to clients.
func encodeCursor(position interfaces.ProjectCursor) string {
	b, _ := json.Marshal(position)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(cursor string) (*interfaces.ProjectCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, err
	}
	position := &interfaces.ProjectCursor{}
	if err = json.Unmarshal(b, position); err != nil {
		return nil, err
	}
	if position.ID == "" {
		return nil, errors.New("cursor has no id")
	}
	return position, nil
}
