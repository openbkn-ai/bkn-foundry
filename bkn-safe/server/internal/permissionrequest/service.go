// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package permissionrequest owns the durable approval workflow for a user's
// missing resource permission. KN proxy grants remain bkn-backend's derived
// binding concern and are never approval recipients here.
// never the replace-style object-grant API.
package permissionrequest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/google/uuid"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/finegrained"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

const (
	StatusPending         = "pending"
	StatusResourceDeleted = "resource_deleted"
	StatusNoReviewer      = "no_reviewer"
	StatusGranted         = "granted"
	StatusCancelled       = "cancelled"
	StatusRejected        = "rejected"

	ReviewerActive  = "active"
	ReviewerRevoked = "revoked"
)

var (
	ErrInvalidRequest           = errors.New("invalid permission request")
	ErrNotFound                 = errors.New("permission request not found")
	ErrForbidden                = errors.New("permission request is forbidden")
	ErrClosed                   = errors.New("permission request is closed")
	ErrResourceDeleted          = errors.New("permission request resource is deleted")
	ErrResourceUnavailable      = errors.New("permission request resource is unavailable")
	ErrUnsupportedResourceType  = errors.New("permission request resource type is unsupported")
	ErrPermissionAlreadyGranted = errors.New("permission request permission is already granted")
)

type CreateInput struct {
	RequesterID                                       string
	ResourceType, ResourceID, ResourceName, Operation string // Operation is the legacy single-operation input.
	Operations                                        []string
	Reason                                            string
}

type DecisionInput struct{ ReviewerID, Decision, Comment string }

// PageOptions mirrors the platform list contract. Sort is deliberately an
// allowlisted logical field rather than SQL supplied by a client.
type PageOptions struct {
	Limit, Offset   int
	Sort, Direction string
}

type RequestPage struct {
	Entries    []model.PermissionRequest
	TotalCount int64
}

func normalizePage(page PageOptions) PageOptions {
	if page.Limit <= 0 || page.Limit > 200 {
		page.Limit = 20
	}
	if page.Offset < 0 {
		page.Offset = 0
	}
	// created_at is currently the only stable list ordering exposed by this
	// API. Keeping the mapping here prevents an ORDER BY injection when more
	// fields are added later.
	if page.Sort != "created_at" {
		page.Sort = "created_at"
	}
	if strings.EqualFold(page.Direction, "asc") {
		page.Direction = "asc"
	} else {
		page.Direction = "desc"
	}
	return page
}

type Service struct {
	db        *gorm.DB
	enforcer  *authz.Enforcer
	resources ResourceLivenessResolver
}

func New(db *gorm.DB, enforcer *authz.Enforcer, resources ...ResourceLivenessResolver) *Service {
	service := &Service{db: db, enforcer: enforcer}
	if len(resources) > 0 {
		service.resources = resources[0]
	}
	return service
}

func clean(v string, max int) (string, bool) {
	v = strings.TrimSpace(v)
	return v, v != "" && len(v) <= max && !strings.ContainsAny(v, "\x00\r\n")
}

// requireLiveResource asks the resource's owning service instead of inferring
// liveness from authorization. In particular, admin-authz:grant is global and
// cannot prove that an individual object still exists.
func (s *Service) requireLiveResource(ctx context.Context, resourceType, resourceID string) error {
	if s.resources == nil {
		return nil
	}
	exists, err := s.resources.Exists(ctx, resourceType, resourceID)
	if err != nil {
		if errors.Is(err, ErrUnsupportedResourceType) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrResourceUnavailable, err)
	}
	if !exists {
		return ErrResourceDeleted
	}
	return nil
}

// refreshResourceLiveness turns an active request terminal only after the
// resource's authoritative service returns 404. Read callers may choose to
// tolerate an unavailable upstream, but decision paths must fail closed.
func (s *Service) refreshResourceLiveness(ctx context.Context, db *gorm.DB, req *model.PermissionRequest) error {
	if req.Status != StatusPending && req.Status != StatusNoReviewer {
		return nil
	}
	err := s.requireLiveResource(ctx, req.ResourceType, req.ResourceID)
	if !errors.Is(err, ErrResourceDeleted) {
		return err
	}
	req.Status = StatusResourceDeleted
	retireRequestKey(req)
	if err := db.WithContext(ctx).Model(req).Updates(map[string]any{
		"status": req.Status, "request_key": req.RequestKey,
	}).Error; err != nil {
		return err
	}
	return nil
}

func newUUIDv7() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate UUID v7: %w", err)
	}
	return id.String(), nil
}

func retiredRequestKey(requestID, status string) string {
	sum := sha256.Sum256([]byte("retired-permission-request\x00" + requestID + "\x00" + status))
	return hex.EncodeToString(sum[:])
}

func retireRequestKey(req *model.PermissionRequest) {
	req.RequestKey = retiredRequestKey(req.ID, req.Status)
}

func grantID(id string) string {
	sum := sha256.Sum256([]byte("permission-request-grant\x00" + id))
	return hex.EncodeToString(sum[:])
}

func grantIDForOperation(requestID, operation string) string {
	sum := sha256.Sum256([]byte("permission-request-grant\x00" + requestID + "\x00" + operation))
	return hex.EncodeToString(sum[:])
}

// requestFingerprint is the server-owned idempotency identity for a direct
// permission application. A browser-provided request ID cannot protect users
// from double-clicks or retries from another client. The reason and display
// name snapshot intentionally do not participate: neither changes the
// permission being requested.
func requestFingerprint(in CreateInput) string {
	parts := []string{
		"permission-request-v3", in.RequesterID,
		in.ResourceType, in.ResourceID, strings.Join(in.Operations, "\x00"),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func validCreate(in *CreateInput) bool {
	for _, field := range []*string{&in.RequesterID, &in.ResourceType, &in.ResourceID} {
		v, ok := clean(*field, 128)
		if !ok {
			return false
		}
		*field = v
	}
	in.ResourceName = strings.TrimSpace(in.ResourceName)
	if len(in.ResourceName) > 256 || strings.ContainsAny(in.ResourceName, "\x00\r\n") {
		return false
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Reason) > 512 || strings.ContainsAny(in.Reason, "\x00\r\n") {
		return false
	}
	operations := in.Operations
	if len(operations) == 0 && in.Operation != "" {
		operations = []string{in.Operation}
	}
	if len(operations) == 0 || len(operations) > 32 {
		return false
	}
	set := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		operation, valid := clean(operation, 64)
		if !valid {
			return false
		}
		// Resource creation is a platform lifecycle operation, not a
		// permission that can be delegated through this workflow.
		if operation == "create" || operation == "authorize" {
			return false
		}
		set[operation] = struct{}{}
	}
	operations = operations[:0]
	for operation := range set {
		operations = append(operations, operation)
	}
	sort.Strings(operations)
	if len(operations) > 1 && !finegrained.Assembled() {
		// Community has only the full-business bundle as a durable grant shape;
		// accepting a multi-operation request would silently over-grant it.
		return false
	}
	in.Operations, in.Operation = operations, operations[0]
	return true
}

func sameOperations(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *Service) requestOperations(ctx context.Context, db *gorm.DB, req *model.PermissionRequest) ([]string, error) {
	if len(req.Operations) > 0 {
		return req.Operations, nil
	}
	var rows []model.PermissionRequestOperation
	if err := db.WithContext(ctx).Where("request_id = ?", req.ID).Order("operation ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		req.Operations = []string{req.Operation}
		return req.Operations, nil
	}
	req.Operations = make([]string, 0, len(rows))
	for _, row := range rows {
		req.Operations = append(req.Operations, row.Operation)
	}
	return req.Operations, nil
}

func (s *Service) hydrateRequests(ctx context.Context, db *gorm.DB, requests []model.PermissionRequest) error {
	if len(requests) == 0 {
		return nil
	}
	ids := make([]string, 0, len(requests))
	requesterIDs := make([]string, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.ID)
		requesterIDs = append(requesterIDs, request.RequesterID)
	}
	var rows []model.PermissionRequestOperation
	if err := db.WithContext(ctx).Where("request_id IN ?", ids).Order("operation ASC").Find(&rows).Error; err != nil {
		return err
	}
	byRequest := make(map[string][]string, len(requests))
	for _, row := range rows {
		byRequest[row.RequestID] = append(byRequest[row.RequestID], row.Operation)
	}
	var users []model.User
	if err := db.WithContext(ctx).Where("id IN ?", requesterIDs).Find(&users).Error; err != nil {
		return err
	}
	requesterNames := make(map[string]string, len(users))
	for _, user := range users {
		name := strings.TrimSpace(user.Name)
		if name == "" {
			name = user.Account
		}
		requesterNames[user.ID] = name
	}
	for i := range requests {
		if operations := byRequest[requests[i].ID]; len(operations) > 0 {
			requests[i].Operations = operations
		} else {
			requests[i].Operations = []string{requests[i].Operation}
		}
		requests[i].RequesterName = requesterNames[requests[i].RequesterID]
	}
	return nil
}

// hydrateReviewedAt projects the current reviewer's decision timestamp for
// the "reviewed by me" list. ReviewedAt is an API-only field, not a column on
// permission_request, so this lookup must remain separate from request reads.
func (s *Service) hydrateReviewedAt(ctx context.Context, db *gorm.DB, reviewer string, requests []model.PermissionRequest) error {
	if len(requests) == 0 {
		return nil
	}
	requestIDs := make([]string, 0, len(requests))
	for _, request := range requests {
		requestIDs = append(requestIDs, request.ID)
	}
	var decisions []model.PermissionRequestDecision
	if err := db.WithContext(ctx).Where("request_id IN ? AND reviewer_id = ?", requestIDs, reviewer).Find(&decisions).Error; err != nil {
		return err
	}
	byRequest := make(map[string]time.Time, len(decisions))
	for _, decision := range decisions {
		byRequest[decision.RequestID] = decision.CreatedAt
	}
	for i := range requests {
		if reviewedAt, ok := byRequest[requests[i].ID]; ok {
			requests[i].ReviewedAt = &reviewedAt
		}
	}
	return nil
}

// hydrateReviewerSummary projects the latest decision maker after processing,
// or the active reviewer candidates while an application is still pending.
func (s *Service) hydrateReviewerSummary(ctx context.Context, db *gorm.DB, requests []model.PermissionRequest) error {
	if len(requests) == 0 {
		return nil
	}
	requestIDs := make([]string, 0, len(requests))
	for _, request := range requests {
		requestIDs = append(requestIDs, request.ID)
	}
	var decisions []model.PermissionRequestDecision
	if err := db.WithContext(ctx).Where("request_id IN ?", requestIDs).Order("created_at DESC").Find(&decisions).Error; err != nil {
		return err
	}
	latestReviewer := make(map[string]string, len(decisions))
	for _, decision := range decisions {
		if _, exists := latestReviewer[decision.RequestID]; !exists {
			latestReviewer[decision.RequestID] = decision.ReviewerID
		}
	}
	var candidates []model.PermissionRequestReviewer
	if err := db.WithContext(ctx).Where("request_id IN ? AND eligibility_status = ?", requestIDs, ReviewerActive).Order("reviewer_id ASC").Find(&candidates).Error; err != nil {
		return err
	}
	candidatesByRequest := make(map[string][]string, len(requests))
	for _, candidate := range candidates {
		candidatesByRequest[candidate.RequestID] = append(candidatesByRequest[candidate.RequestID], candidate.ReviewerID)
	}
	reviewerIDSet := make(map[string]struct{}, len(latestReviewer)+len(candidates))
	for _, reviewerID := range latestReviewer {
		reviewerIDSet[reviewerID] = struct{}{}
	}
	for _, reviewerIDs := range candidatesByRequest {
		for _, reviewerID := range reviewerIDs {
			reviewerIDSet[reviewerID] = struct{}{}
		}
	}
	reviewerIDs := make([]string, 0, len(reviewerIDSet))
	for reviewerID := range reviewerIDSet {
		reviewerIDs = append(reviewerIDs, reviewerID)
	}
	var users []model.User
	if len(reviewerIDs) > 0 {
		if err := db.WithContext(ctx).Where("id IN ?", reviewerIDs).Find(&users).Error; err != nil {
			return err
		}
	}
	reviewerNames := make(map[string]string, len(users))
	for _, user := range users {
		name := strings.TrimSpace(user.Name)
		if name == "" {
			name = user.Account
		}
		reviewerNames[user.ID] = name
	}
	for i := range requests {
		reviewerIDs := candidatesByRequest[requests[i].ID]
		if latestReviewerID := latestReviewer[requests[i].ID]; latestReviewerID != "" {
			reviewerIDs = []string{latestReviewerID}
		}
		reviewerNamesForRequest := make([]string, 0, len(reviewerIDs))
		for _, reviewerID := range reviewerIDs {
			if reviewerName := reviewerNames[reviewerID]; reviewerName != "" {
				reviewerNamesForRequest = append(reviewerNamesForRequest, reviewerName)
			}
		}
		requests[i].ReviewerID = strings.Join(reviewerIDs, ",")
		requests[i].ReviewerName = strings.Join(reviewerNamesForRequest, ",")
	}
	return nil
}

// Create is idempotent only while an equivalent request remains active. A
// terminal request releases its active key so the user may start a new review
// workflow for the same resource and operations.
func (s *Service) Create(ctx context.Context, in CreateInput) (*model.PermissionRequest, bool, error) {
	if !validCreate(&in) {
		return nil, false, ErrInvalidRequest
	}
	if err := s.requireLiveResource(ctx, in.ResourceType, in.ResourceID); err != nil {
		return nil, false, err
	}
	if err := s.validateRequestOperations(ctx, in.ResourceType, in.Operations); err != nil {
		return nil, false, err
	}
	alreadyGranted, err := s.hasRequestedPermission(ctx, in.RequesterID, in.ResourceType, in.ResourceID, in.Operations)
	if err != nil {
		return nil, false, err
	}
	if alreadyGranted {
		return nil, false, ErrPermissionAlreadyGranted
	}
	requestKey := requestFingerprint(in)
	id, err := newUUIDv7()
	if err != nil {
		return nil, false, err
	}
	req := model.PermissionRequest{
		ID: id, RequestKey: requestKey, RequesterID: in.RequesterID,
		ResourceType: in.ResourceType, ResourceID: in.ResourceID, ResourceName: in.ResourceName,
		Operation: in.Operation, Reason: in.Reason, Status: StatusPending, GrantID: grantID(id),
	}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&req)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		rows := make([]model.PermissionRequestOperation, 0, len(in.Operations))
		for _, operation := range in.Operations {
			operationID, err := newUUIDv7()
			if err != nil {
				return nil, false, err
			}
			rows = append(rows, model.PermissionRequestOperation{ID: operationID, RequestID: req.ID, Operation: operation})
		}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
			return nil, false, err
		}
	}
	var stored model.PermissionRequest
	if err := s.db.WithContext(ctx).First(&stored, "request_key = ?", requestKey).Error; err != nil {
		return nil, false, err
	}
	if stored.RequesterID != in.RequesterID || stored.ResourceType != in.ResourceType || stored.ResourceID != in.ResourceID {
		return nil, false, ErrInvalidRequest
	}
	storedOperations, err := s.requestOperations(ctx, s.db, &stored)
	if err != nil || !sameOperations(storedOperations, in.Operations) {
		return nil, false, ErrInvalidRequest
	}
	if err := s.syncAllReviewers(ctx, s.db, &stored); err != nil {
		return nil, false, err
	}
	if stored.Status != StatusPending && stored.Status != StatusNoReviewer {
		retireRequestKey(&stored)
		if err := s.db.WithContext(ctx).Model(&stored).Update("request_key", stored.RequestKey).Error; err != nil {
			return nil, false, err
		}
		return s.Create(ctx, in)
	}
	return &stored, result.RowsAffected == 1, nil
}

// validateRequestOperations rejects operations that do not exist on the
// target type. Child permissions inherit only the explicitly configured parent
// operation mapping; accepting arbitrary knowledge-network operations would
// create applications that no legitimate reviewer can approve.
func (s *Service) validateRequestOperations(ctx context.Context, resourceType string, operations []string) error {
	if !finegrained.Assembled() {
		if len(operations) != 1 || operations[0] != authz.ActFullBusinessAccess {
			return ErrInvalidRequest
		}
		if _, ok := authz.CommunityBundleOperations(resourceType); !ok {
			return ErrInvalidRequest
		}
		return nil
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&model.Operation{}).
		Where("resource_type_id = ? AND id IN ? AND grantable = ?", resourceType, operations, true).
		Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(operations)) {
		return ErrInvalidRequest
	}
	return nil
}

// hasRequestedPermission checks whether an approval would duplicate an
// existing effective permission. Community stores one full-business bundle, so
// it is already granted only when every bundled operation is effective. In a
// fine-grained request, one existing requested operation is sufficient: callers
// must submit only the operations that are actually missing.
func (s *Service) hasRequestedPermission(ctx context.Context, accessorID, resourceType, resourceID string, operations []string) (bool, error) {
	checkOperations := operations
	communityBundle := false
	if !finegrained.Assembled() {
		var ok bool
		checkOperations, ok = authz.CommunityBundleOperations(resourceType)
		if !ok {
			return false, ErrInvalidRequest
		}
		communityBundle = true
	}
	for _, operation := range checkOperations {
		allowed, err := s.enforcer.CheckContext(ctx, accessorID, resourceType, resourceID, operation)
		if err != nil {
			return false, err
		}
		if allowed && !communityBundle {
			return true, nil
		}
		if !allowed && communityBundle {
			return false, nil
		}
	}
	return communityBundle, nil
}

// authorizationRoot follows the registered instance parent chain. This covers
// both BKN children and Resource -> Catalog without hard-coding resource types.
func (s *Service) authorizationRoot(ctx context.Context, resourceType, resourceID string) (string, string, error) {
	seen := map[string]bool{}
	for i := 0; i < 16; i++ {
		key := resourceType + ":" + resourceID
		if seen[key] {
			return "", "", ErrInvalidRequest
		}
		seen[key] = true
		var parent model.ResourceParent
		err := s.db.WithContext(ctx).First(&parent, "resource_type_id = ? AND resource_id = ?", resourceType, resourceID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Knowledge-network child IDs are canonically stored as
			// "<knowledge-network-id>/<child-id>". Use that trusted identity
			// shape as a fallback while lifecycle synchronization catches up,
			// so inherited root authorization remains effective.
			if parentID, ok := knowledgeNetworkParentFromChildID(resourceType, resourceID); ok {
				return "knowledge_network", parentID, nil
			}
			return resourceType, resourceID, nil
		}
		if err != nil {
			return "", "", err
		}
		resourceType, resourceID = parent.ParentTypeID, parent.ParentID
	}
	return "", "", ErrInvalidRequest
}

func knowledgeNetworkParentFromChildID(resourceType, resourceID string) (string, bool) {
	switch resourceType {
	case "concept_group", "object_type", "relation_type", "action_type", "metric":
		parts := strings.SplitN(strings.TrimSpace(resourceID), "/", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], true
		}
	}
	return "", false
}

// syncReviewer updates one user's materialized eligibility. The final
// authorization decision is still checked at review time, so this row is an
// inbox index rather than an authority source.
func (s *Service) syncReviewer(ctx context.Context, db *gorm.DB, req *model.PermissionRequest, reviewerID string) (bool, error) {
	reviewerID = strings.TrimSpace(reviewerID)
	if reviewerID == "" || reviewerID == req.RequesterID {
		return false, nil
	}
	rootType, rootID, err := s.authorizationRoot(ctx, req.ResourceType, req.ResourceID)
	if err != nil {
		return false, err
	}
	eligible, err := s.canReview(ctx, reviewerID, req)
	if err != nil {
		return false, err
	}
	status := ReviewerRevoked
	if eligible {
		status = ReviewerActive
	}
	reviewerRecordID, err := newUUIDv7()
	if err != nil {
		return false, err
	}
	record := model.PermissionRequestReviewer{
		ID: reviewerRecordID, RequestID: req.ID, ReviewerID: reviewerID,
		EligibilityStatus: status, AuthorizationRootType: rootType, AuthorizationRootID: rootID,
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "request_id"}, {Name: "reviewer_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"eligibility_status":      status,
			"authorization_root_type": rootType,
			"authorization_root_id":   rootID,
			"updated_at":              time.Now().UTC(),
		}),
	}).Create(&record).Error; err != nil {
		return false, err
	}
	return eligible, nil
}

func (s *Service) refreshRequestReviewerStatus(ctx context.Context, db *gorm.DB, req *model.PermissionRequest) error {
	if req.Status != StatusPending && req.Status != StatusNoReviewer {
		return nil
	}
	var active int64
	if err := db.WithContext(ctx).Model(&model.PermissionRequestReviewer{}).
		Where("request_id = ? AND eligibility_status = ?", req.ID, ReviewerActive).Count(&active).Error; err != nil {
		return err
	}
	next := StatusNoReviewer
	if active > 0 {
		next = StatusPending
	}
	if next == req.Status {
		return nil
	}
	if err := db.WithContext(ctx).Model(req).Update("status", next).Error; err != nil {
		return err
	}
	req.Status = next
	return nil
}

// syncAllReviewers materializes every enabled human who can currently review
// this active request. It is used on create and immediately before a decision
// so role expansion is evaluated by the authorization engine, not by a raw
// policy-table scan.
func (s *Service) syncAllReviewers(ctx context.Context, db *gorm.DB, req *model.PermissionRequest) error {
	if req.Status != StatusPending && req.Status != StatusNoReviewer {
		return nil
	}
	var users []model.User
	if err := db.WithContext(ctx).Where("enabled = ? AND account_type NOT IN ?", true, []model.AccountType{model.AccountTypeApp, model.AccountTypeContactor}).Find(&users).Error; err != nil {
		return err
	}
	userIDs := make([]string, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
		if _, err := s.syncReviewer(ctx, db, req, user.ID); err != nil {
			return err
		}
	}
	// A disabled/deleted non-human account no longer appears in the directory
	// query above. Retain its row for audit but remove it from the actionable
	// reviewer set.
	stale := db.WithContext(ctx).Model(&model.PermissionRequestReviewer{}).
		Where("request_id = ? AND eligibility_status = ?", req.ID, ReviewerActive)
	if len(userIDs) > 0 {
		stale = stale.Where("reviewer_id NOT IN ?", userIDs)
	}
	if err := stale.Update("eligibility_status", ReviewerRevoked).Error; err != nil {
		return err
	}
	return s.refreshRequestReviewerStatus(ctx, db, req)
}

// syncReviewerInbox catches authorization changes without relying on a caller
// to enumerate requests: when a user opens the todo list, all active requests
// are refreshed for that user.
func (s *Service) syncReviewerInbox(ctx context.Context, reviewerID string) error {
	var requests []model.PermissionRequest
	if err := s.db.WithContext(ctx).Where("status IN ? AND requester_id <> ?", []string{StatusPending, StatusNoReviewer}, reviewerID).Find(&requests).Error; err != nil {
		return err
	}
	for i := range requests {
		if _, err := s.syncReviewer(ctx, s.db, &requests[i], reviewerID); err != nil {
			return err
		}
		if err := s.refreshRequestReviewerStatus(ctx, s.db, &requests[i]); err != nil {
			return err
		}
	}
	return nil
}

// SyncReviewerInbox is the authorization-change hook for bkn-safe write
// paths. A grant or revoke for one user can call it after commit to make that
// user's active reviewer rows immediately consistent, without scanning the
// entire directory.
func (s *Service) SyncReviewerInbox(ctx context.Context, reviewerID string) error {
	reviewerID = strings.TrimSpace(reviewerID)
	if reviewerID == "" || len(reviewerID) > 64 {
		return ErrInvalidRequest
	}
	return s.syncReviewerInbox(ctx, reviewerID)
}

func (s *Service) canReview(ctx context.Context, reviewer string, req *model.PermissionRequest) (bool, error) {
	if reviewer == "" || reviewer == req.RequesterID {
		return false, nil
	}
	admin, err := s.enforcer.CheckContext(ctx, reviewer, "admin-authz", "*", "grant")
	if err != nil || admin {
		return admin, err
	}
	rootType, rootID, err := s.authorizationRoot(ctx, req.ResourceType, req.ResourceID)
	if err != nil {
		return false, err
	}
	// A user may be explicitly delegated authorize on the child itself. The
	// parent root check keeps inherited knowledge-network authorization valid.
	authorize, err := s.enforcer.CheckContext(ctx, reviewer, req.ResourceType, req.ResourceID, "authorize")
	if err != nil {
		return false, err
	}
	if !authorize && (rootType != req.ResourceType || rootID != req.ResourceID) {
		authorize, err = s.enforcer.CheckContext(ctx, reviewer, rootType, rootID, "authorize")
		if err != nil {
			return false, err
		}
	}
	operations, err := s.requestOperations(ctx, s.db, req)
	if err != nil {
		return false, err
	}
	// full_business_access is the one logical Community request operation. It
	// never matches CheckContext by design, so only this exact operation may use
	// the durable bundle as reviewer evidence; authorize and every other
	// operation still follow the normal checks below.
	if !finegrained.Assembled() && len(operations) == 1 && operations[0] == authz.ActFullBusinessAccess {
		records, err := s.enforcer.PolicyRecords(authz.PolicyFilter{
			AccessorID: reviewer, Object: req.ResourceType + ":" + req.ResourceID,
			Operation: authz.ActFullBusinessAccess, Effect: authz.EffectAllow,
			PolicySource: authz.PolicySourceCommunityBundle,
		})
		return len(records) > 0, err
	}
	for _, operation := range operations {
		allowedOperation, err := s.enforcer.CheckContext(ctx, reviewer, req.ResourceType, req.ResourceID, operation)
		if err != nil || !allowedOperation {
			return false, err
		}
	}
	if authorize {
		return true, nil
	}
	// Community authorization is a full-business bundle rather than a
	// professional object rule. A bundle holder may approve requests for this
	// exact resource, while the bundle deliberately remains non-authorize at
	// runtime and cannot be used through generic object-grant endpoints.
	if !finegrained.Assembled() {
		records, err := s.enforcer.PolicyRecords(authz.PolicyFilter{
			AccessorID: reviewer, Object: req.ResourceType + ":" + req.ResourceID,
			Operation: authz.ActFullBusinessAccess, Effect: authz.EffectAllow,
			PolicySource: authz.PolicySourceCommunityBundle,
		})
		return len(records) > 0, err
	}
	return false, nil
}

// CanReview exposes the same current-time reviewer check used by decisions.
func (s *Service) CanReview(ctx context.Context, reviewer string, req *model.PermissionRequest) (bool, error) {
	return s.canReview(ctx, reviewer, req)
}

// CanView allows the requester and an actual historical reviewer to read an
// immutable request record even after the resource has been deleted or the
// reviewer's grant has since been revoked. It deliberately falls back to the
// realtime CanReview check only for users that have not made a decision.
// Decision endpoints continue to use CanReview, so historical access never
// revives approval authority.
func (s *Service) CanView(ctx context.Context, accessor string, req *model.PermissionRequest) (bool, error) {
	if accessor == "" {
		return false, nil
	}
	if accessor == req.RequesterID {
		return true, nil
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&model.PermissionRequestDecision{}).
		Where("request_id = ? AND reviewer_id = ?", req.ID, accessor).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	return s.canReview(ctx, accessor, req)
}

// hasUnreviewedEligibleReviewer determines whether rejection should leave the
// request pending. The final eligibility check is intentionally realtime.
func (s *Service) hasUnreviewedEligibleReviewer(ctx context.Context, db *gorm.DB, req *model.PermissionRequest) (bool, error) {
	var users []model.User
	if err := db.WithContext(ctx).Where("enabled = ? AND account_type NOT IN ?", true, []model.AccountType{model.AccountTypeApp, model.AccountTypeContactor}).Find(&users).Error; err != nil {
		return false, err
	}
	var reviewed []string
	if err := db.WithContext(ctx).Model(&model.PermissionRequestDecision{}).Where("request_id = ?", req.ID).Pluck("reviewer_id", &reviewed).Error; err != nil {
		return false, err
	}
	done := make(map[string]bool, len(reviewed))
	for _, id := range reviewed {
		done[id] = true
	}
	for _, user := range users {
		if done[user.ID] {
			continue
		}
		ok, err := s.canReview(ctx, user.ID, req)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// Decide records one review. The first valid approval closes the request and
// grants the requesting user's exact permission tuple. Rejection only removes that reviewer's todo;
// another currently eligible reviewer may still approve.
func (s *Service) Decide(ctx context.Context, requestID string, in DecisionInput) (*model.PermissionRequest, error) {
	in.ReviewerID = strings.TrimSpace(in.ReviewerID)
	in.Decision = strings.TrimSpace(in.Decision)
	in.Comment = strings.TrimSpace(in.Comment)
	if in.ReviewerID == "" || len(in.ReviewerID) > 64 || (in.Decision != "approve" && in.Decision != "reject") || len(in.Comment) > 512 {
		return nil, ErrInvalidRequest
	}
	var result model.PermissionRequest
	err := s.enforcer.Transaction(ctx, func(tx *authz.PolicyTransaction) error {
		var req model.PermissionRequest
		if err := tx.DB().Clauses(clause.Locking{Strength: "UPDATE"}).First(&req, "id = ?", requestID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if req.Status != StatusPending && req.Status != StatusNoReviewer {
			return ErrClosed
		}
		if err := s.refreshResourceLiveness(ctx, tx.DB(), &req); err != nil {
			return err
		}
		if req.Status == StatusResourceDeleted {
			return ErrResourceDeleted
		}
		// Refresh the materialized reviewer set under the same transaction that
		// records the decision. A no_reviewer request may only become pending
		// through this resolver; it cannot be claimed by an arbitrary caller.
		if err := s.syncAllReviewers(ctx, tx.DB(), &req); err != nil {
			return err
		}
		if req.Status != StatusPending {
			return ErrForbidden
		}
		ok, err := s.canReview(ctx, in.ReviewerID, &req)
		if err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		decisionID, err := newUUIDv7()
		if err != nil {
			return err
		}
		decision := model.PermissionRequestDecision{ID: decisionID, RequestID: req.ID, ReviewerID: in.ReviewerID, Decision: in.Decision, Comment: in.Comment}
		if err := tx.DB().Create(&decision).Error; err != nil {
			return err
		}
		if in.Decision == "reject" {
			hasNext, err := s.hasUnreviewedEligibleReviewer(ctx, tx.DB(), &req)
			if err != nil {
				return err
			}
			if !hasNext {
				now := time.Now().UTC()
				req.Status, req.RejectedAt = StatusRejected, &now
				retireRequestKey(&req)
				if err := tx.DB().Save(&req).Error; err != nil {
					return err
				}
			}
		}
		if in.Decision == "approve" {
			operations, err := s.requestOperations(ctx, tx.DB(), &req)
			if err != nil {
				return err
			}
			alreadyGranted, err := s.hasRequestedPermission(ctx, req.RequesterID, req.ResourceType, req.ResourceID, operations)
			if err != nil {
				return err
			}
			if alreadyGranted {
				return ErrPermissionAlreadyGranted
			}
			if finegrained.Assembled() {
				for _, operation := range operations {
					grantID := req.GrantID
					if len(operations) > 1 {
						grantID = grantIDForOperation(req.ID, operation)
					}
					if _, err = tx.GrantPolicy(authz.PolicyGrant{
						GrantID: grantID, AccessorID: req.RequesterID,
						Object: req.ResourceType + ":" + req.ResourceID, Operation: operation,
						Effect: authz.EffectAllow, PolicySource: authz.PolicySourceProfessionalRule,
						AuthoritySource: authz.AuthoritySourcePermissionRequest, CreatedBy: in.ReviewerID,
					}); err != nil {
						return err
					}
				}
			} else {
				_, err = tx.GrantCommunityBundleBy(req.GrantID, req.RequesterID, req.ResourceType, req.ResourceID,
					authz.AuthoritySourcePermissionRequest, in.ReviewerID)
			}
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			req.Status, req.ApprovedBy, req.ApprovedAt = StatusGranted, in.ReviewerID, &now
			retireRequestKey(&req)
			if err := tx.DB().Save(&req).Error; err != nil {
				return err
			}
		}
		result = req
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Service) ListRequested(ctx context.Context, requester string) ([]model.PermissionRequest, error) {
	var rows []model.PermissionRequest
	err := s.db.WithContext(ctx).Where("requester_id = ?", requester).Order("created_at DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		// A temporary resource-service outage must not hide the applicant's
		// history. Approval remains fail-closed in Decide.
		_ = s.refreshResourceLiveness(ctx, s.db, &rows[i])
	}
	if err := s.hydrateRequests(ctx, s.db, rows); err != nil {
		return nil, err
	}
	return rows, s.hydrateReviewerSummary(ctx, s.db, rows)
}

func (s *Service) ListRequestedPage(ctx context.Context, requester string, page PageOptions) (RequestPage, error) {
	page = normalizePage(page)
	q := s.db.WithContext(ctx).Model(&model.PermissionRequest{}).Where("requester_id = ?", requester)
	var result RequestPage
	if err := q.Count(&result.TotalCount).Error; err != nil {
		return result, err
	}
	err := q.Order(page.Sort + " " + page.Direction).Limit(page.Limit).Offset(page.Offset).Find(&result.Entries).Error
	if err != nil {
		return result, err
	}
	for i := range result.Entries {
		_ = s.refreshResourceLiveness(ctx, s.db, &result.Entries[i])
	}
	if err := s.hydrateRequests(ctx, s.db, result.Entries); err != nil {
		return result, err
	}
	return result, s.hydrateReviewerSummary(ctx, s.db, result.Entries)
}

func (s *Service) Get(ctx context.Context, id string) (*model.PermissionRequest, error) {
	var request model.PermissionRequest
	if err := s.db.WithContext(ctx).First(&request, "id = ?", strings.TrimSpace(id)).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	// Detail should show a confirmed deletion, but an unavailable resource
	// service does not make the request itself unreadable.
	_ = s.refreshResourceLiveness(ctx, s.db, &request)
	requests := []model.PermissionRequest{request}
	if err := s.hydrateRequests(ctx, s.db, requests); err != nil {
		return nil, err
	}
	if err := s.hydrateReviewerSummary(ctx, s.db, requests); err != nil {
		return nil, err
	}
	return &requests[0], nil
}

func (s *Service) ListDecisions(ctx context.Context, id string) ([]model.PermissionRequestDecision, error) {
	var rows []model.PermissionRequestDecision
	if err := s.db.WithContext(ctx).Where("request_id = ?", strings.TrimSpace(id)).Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return rows, nil
	}
	reviewerIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		reviewerIDs = append(reviewerIDs, row.ReviewerID)
	}
	var users []model.User
	if err := s.db.WithContext(ctx).Where("id IN ?", reviewerIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	names := make(map[string]string, len(users))
	for _, user := range users {
		name := strings.TrimSpace(user.Name)
		if name == "" {
			name = user.Account
		}
		names[user.ID] = name
	}
	for i := range rows {
		rows[i].ReviewerName = names[rows[i].ReviewerID]
	}
	return rows, nil
}

// Cancel lets the applicant withdraw an active request before a grant exists.
func (s *Service) Cancel(ctx context.Context, id, requester string) (*model.PermissionRequest, error) {
	var result model.PermissionRequest
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request model.PermissionRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&request, "id = ?", strings.TrimSpace(id)).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if request.RequesterID != requester {
			return ErrForbidden
		}
		if err := s.refreshResourceLiveness(ctx, tx, &request); err != nil {
			return err
		}
		if request.Status != StatusPending && request.Status != StatusNoReviewer {
			return ErrClosed
		}
		request.Status = StatusCancelled
		retireRequestKey(&request)
		if err := tx.Save(&request).Error; err != nil {
			return err
		}
		result = request
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Service) ListReviewed(ctx context.Context, reviewer string) ([]model.PermissionRequest, error) {
	var rows []model.PermissionRequest
	err := s.db.WithContext(ctx).Model(&model.PermissionRequest{}).Joins("JOIN permission_request_decision d ON d.request_id = permission_request.id").Where("d.reviewer_id = ?", reviewer).Order("d.created_at DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if err := s.hydrateRequests(ctx, s.db, rows); err != nil {
		return nil, err
	}
	return rows, s.hydrateReviewedAt(ctx, s.db, reviewer, rows)
}

func (s *Service) ListReviewedPage(ctx context.Context, reviewer string, page PageOptions) (RequestPage, error) {
	page = normalizePage(page)
	q := s.db.WithContext(ctx).Model(&model.PermissionRequest{}).Joins("JOIN permission_request_decision d ON d.request_id = permission_request.id").Where("d.reviewer_id = ?", reviewer)
	var result RequestPage
	if err := q.Count(&result.TotalCount).Error; err != nil {
		return result, err
	}
	// "created_at" means the time of the review for this view, rather than
	// the original submission time.
	err := q.Order("d." + page.Sort + " " + page.Direction).Limit(page.Limit).Offset(page.Offset).Find(&result.Entries).Error
	if err != nil {
		return result, err
	}
	if err := s.hydrateRequests(ctx, s.db, result.Entries); err != nil {
		return result, err
	}
	return result, s.hydrateReviewedAt(ctx, s.db, reviewer, result.Entries)
}

func (s *Service) ListPending(ctx context.Context) ([]model.PermissionRequest, error) {
	var rows []model.PermissionRequest
	err := s.db.WithContext(ctx).Where("status = ?", StatusPending).Order("created_at ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	active := rows[:0]
	for i := range rows {
		_ = s.refreshResourceLiveness(ctx, s.db, &rows[i])
		if rows[i].Status != StatusResourceDeleted {
			active = append(active, rows[i])
		}
	}
	rows = active
	return rows, s.hydrateRequests(ctx, s.db, rows)
}

func (s *Service) ListAllDecisions(ctx context.Context) ([]model.PermissionRequestDecision, error) {
	var rows []model.PermissionRequestDecision
	err := s.db.WithContext(ctx).Order("created_at DESC").Find(&rows).Error
	return rows, err
}

func (s *Service) ListTodo(ctx context.Context, reviewer string) ([]model.PermissionRequest, error) {
	page, err := s.ListTodoPage(ctx, reviewer, PageOptions{Limit: 200, Sort: "created_at", Direction: "asc"})
	return page.Entries, err
}

// ListTodoPage uses the materialized reviewer set for database pagination.
// Before querying, it refreshes this reviewer's eligibility against current
// authorization so a revoked grant cannot leave a stale actionable todo.
func (s *Service) ListTodoPage(ctx context.Context, reviewer string, page PageOptions) (RequestPage, error) {
	page = normalizePage(page)
	if err := s.syncReviewerInbox(ctx, reviewer); err != nil {
		return RequestPage{}, err
	}
	q := s.db.WithContext(ctx).Model(&model.PermissionRequest{}).
		Joins("JOIN permission_request_reviewer r ON r.request_id = permission_request.id").
		Where("r.reviewer_id = ? AND r.eligibility_status = ? AND permission_request.status = ?", reviewer, ReviewerActive, StatusPending).
		Where("NOT EXISTS (SELECT 1 FROM permission_request_decision d WHERE d.request_id = permission_request.id AND d.reviewer_id = ?)", reviewer)
	var result RequestPage
	if err := q.Count(&result.TotalCount).Error; err != nil {
		return result, err
	}
	err := q.Order("permission_request." + page.Sort + " " + page.Direction).Limit(page.Limit).Offset(page.Offset).Find(&result.Entries).Error
	if err != nil {
		return result, err
	}
	active := result.Entries[:0]
	for i := range result.Entries {
		_ = s.refreshResourceLiveness(ctx, s.db, &result.Entries[i])
		if result.Entries[i].Status != StatusResourceDeleted {
			active = append(active, result.Entries[i])
		} else if result.TotalCount > 0 {
			result.TotalCount--
		}
	}
	result.Entries = active
	return result, s.hydrateRequests(ctx, s.db, result.Entries)
}
