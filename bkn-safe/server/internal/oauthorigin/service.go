// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package oauthorigin persists BKN Studio browser origins and reconciles the
// derived callback/logout URI sets into Hydra's openbkn-studio client.
package oauthorigin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

const (
	StudioClientID     = "openbkn-studio"
	callbackPath       = "/studio/callback"
	logoutPath         = "/studio"
	maxRuntimeOrigins  = 64
	desiredStateActive = "active"
	desiredStateDelete = "deleting"
	syncStatePending   = "pending"
	syncStateSynced    = "synced"
	syncStateError     = "error"
)

var (
	ErrInvalidOrigin = errors.New("invalid OAuth access origin")
	ErrOriginLimit   = errors.New("OAuth access origin limit reached")
	ErrNotFound      = errors.New("OAuth access origin not found")
	ErrReadOnly      = errors.New("deployment-owned OAuth access origin is read-only")
)

// DuplicateError reports the resource ID callers can return in a 409 envelope.
type DuplicateError struct{ ExistingID string }

func (e *DuplicateError) Error() string { return "OAuth access origin already exists" }

// Client is the Hydra client-management subset used by the reconciler.
type Client interface {
	GetOAuthClientURIs(context.Context, string) (auth.OAuthClientURIs, error)
	SetOAuthClientURIs(context.Context, string, auth.OAuthClientURIs) error
}

// Entry is the admin API representation of one effective access origin.
type Entry struct {
	ID            string     `json:"id"`
	Origin        string     `json:"origin"`
	RedirectURI   string     `json:"redirect_uri"`
	LogoutURI     string     `json:"post_logout_redirect_uri"`
	Source        string     `json:"source"`
	ReadOnly      bool       `json:"read_only"`
	DesiredState  string     `json:"desired_state"`
	SyncState     string     `json:"sync_state"`
	LastSyncError string     `json:"last_sync_error,omitempty"`
	CreatedBy     string     `json:"created_by,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
}

type baselineOrigin struct {
	id     string
	origin string
	source string
}

// Service owns the durable desired state for the Studio OAuth client.
type Service struct {
	db       *gorm.DB
	client   Client
	baseline []baselineOrigin
	mu       sync.Mutex
}

// New validates and normalizes the deployment baseline before accepting any
// requests. The first URI is the system accessAddress; subsequent URIs come
// from clientSeed.extraWebRedirectUris.
func New(db *gorm.DB, client Client, baselineRedirectURIs []string) (*Service, error) {
	if db == nil || client == nil {
		return nil, errors.New("OAuth access-origin service requires database and Hydra client")
	}
	seen := make(map[string]bool, len(baselineRedirectURIs))
	baseline := make([]baselineOrigin, 0, len(baselineRedirectURIs))
	for i, callback := range baselineRedirectURIs {
		origin, err := OriginFromCallback(callback)
		if err != nil {
			return nil, fmt.Errorf("baseline redirect URI %q: %w", callback, err)
		}
		if seen[origin] {
			continue
		}
		seen[origin] = true
		source := "deployment"
		if i == 0 {
			source = "system"
		}
		baseline = append(baseline, baselineOrigin{
			id: stableID(source, origin), origin: origin, source: source,
		})
	}
	return &Service{db: db, client: client, baseline: baseline}, nil
}

// NormalizeOrigin accepts only an HTTP(S) origin. Paths, credentials, query,
// fragments, and wildcards are rejected; case and default ports are normalized.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "*") {
		return "", ErrInvalidOrigin
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" || u.Hostname() == "" {
		return "", ErrInvalidOrigin
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", ErrInvalidOrigin
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", ErrInvalidOrigin
	}
	hostname := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "80" && scheme == "http" || port == "443" && scheme == "https" {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return scheme + "://" + host, nil
}

// OriginFromCallback converts the exact Studio callback shape into an origin.
func OriginFromCallback(raw string) (string, error) {
	return originFromManagedURI(raw, callbackPath)
}

func originFromLogout(raw string) (string, error) {
	return originFromManagedURI(raw, logoutPath)
}

func originFromManagedURI(raw, path string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Path != path || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", ErrInvalidOrigin
	}
	u.Path = ""
	u.RawPath = ""
	return NormalizeOrigin(u.String())
}

func callbackURI(origin string) string { return origin + callbackPath }
func logoutURI(origin string) string   { return origin + logoutPath }

// List returns the effective deployment + runtime origin set in stable order.
func (s *Service) List(ctx context.Context) ([]Entry, error) {
	var rows []model.OAuthAccessOrigin
	if err := s.db.WithContext(ctx).Order("origin ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var state model.OAuthClientSyncState
	if err := s.db.WithContext(ctx).First(&state, "client_id = ?", StudioClientID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	entries := make([]Entry, 0, len(s.baseline)+len(rows))
	for _, b := range s.baseline {
		syncState := state.SyncState
		if syncState == "" {
			syncState = syncStatePending
		}
		entries = append(entries, Entry{
			ID: b.id, Origin: b.origin, RedirectURI: callbackURI(b.origin), LogoutURI: logoutURI(b.origin),
			Source: b.source, ReadOnly: true, DesiredState: desiredStateActive,
			SyncState: syncState, LastSyncError: state.LastSyncError,
		})
	}
	for _, row := range rows {
		entries = append(entries, entryFromRow(row))
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ri, rj := sourceRank(entries[i].Source), sourceRank(entries[j].Source)
		if ri != rj {
			return ri < rj
		}
		return entries[i].Origin < entries[j].Origin
	})
	return entries, nil
}

// Add persists a runtime origin and immediately attempts reconciliation. A
// Hydra outage leaves the row in error state so the background loop can retry.
func (s *Service) Add(ctx context.Context, rawOrigin, actorID string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	origin, err := NormalizeOrigin(rawOrigin)
	if err != nil {
		return Entry{}, err
	}
	if b, ok := s.baselineByOrigin(origin); ok {
		return Entry{}, &DuplicateError{ExistingID: b.id}
	}

	var row model.OAuthAccessOrigin
	err = s.db.WithContext(ctx).Where("origin = ?", origin).First(&row).Error
	if err == nil {
		if row.DesiredState == desiredStateDelete {
			row.DesiredState = desiredStateActive
			row.SyncState = syncStatePending
			row.LastSyncError = ""
			row.CreatedBy = actorID
			if err := s.db.WithContext(ctx).Save(&row).Error; err != nil {
				return Entry{}, err
			}
		} else {
			return Entry{}, &DuplicateError{ExistingID: row.ID}
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Entry{}, err
	} else {
		var count int64
		if err := s.db.WithContext(ctx).Model(&model.OAuthAccessOrigin{}).
			Where("desired_state = ?", desiredStateActive).Count(&count).Error; err != nil {
			return Entry{}, err
		}
		if count >= maxRuntimeOrigins {
			return Entry{}, ErrOriginLimit
		}
		row = model.OAuthAccessOrigin{
			ID: newID(), Origin: origin, DesiredState: desiredStateActive,
			SyncState: syncStatePending, CreatedBy: actorID,
		}
		if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
			var existing model.OAuthAccessOrigin
			if findErr := s.db.WithContext(ctx).Where("origin = ?", origin).First(&existing).Error; findErr == nil {
				return Entry{}, &DuplicateError{ExistingID: existing.ID}
			}
			return Entry{}, err
		}
	}

	_ = s.reconcile(ctx)
	if err := s.db.WithContext(ctx).First(&row, "id = ?", row.ID).Error; err != nil {
		return Entry{}, err
	}
	return entryFromRow(row), nil
}

// Delete marks a runtime origin for removal and immediately reconciles. The
// return value reports whether Hydra is already in sync.
func (s *Service) Delete(ctx context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, b := range s.baseline {
		if b.id == id {
			return false, ErrReadOnly
		}
	}
	var row model.OAuthAccessOrigin
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrNotFound
		}
		return false, err
	}
	if err := s.db.WithContext(ctx).Model(&row).Updates(map[string]any{
		"desired_state": desiredStateDelete, "sync_state": syncStatePending, "last_sync_error": "",
	}).Error; err != nil {
		return false, err
	}
	if err := s.reconcile(ctx); err != nil {
		return false, nil
	}
	return true, nil
}

// Callbacks returns the desired active callback set for the compatibility API.
func (s *Service) Callbacks(ctx context.Context) ([]string, error) {
	origins, err := s.activeOrigins(ctx)
	if err != nil {
		return nil, err
	}
	callbacks := make([]string, 0, len(origins))
	for _, origin := range origins {
		callbacks = append(callbacks, callbackURI(origin))
	}
	return callbacks, nil
}

// AddCallback routes the legacy Studio redirect-URI API through durable origin
// storage. Only the fixed Studio callback path is accepted.
func (s *Service) AddCallback(ctx context.Context, uri, actorID string) ([]string, error) {
	origin, err := OriginFromCallback(uri)
	if err != nil {
		return nil, err
	}
	if _, err := s.Add(ctx, origin, actorID); err != nil {
		var duplicate *DuplicateError
		if !errors.As(err, &duplicate) {
			return nil, err
		}
	}
	return s.Callbacks(ctx)
}

// RemoveCallback removes a runtime callback through the durable origin API.
// Missing callbacks remain a no-op for compatibility with the old endpoint.
func (s *Service) RemoveCallback(ctx context.Context, uri string) ([]string, error) {
	origin, err := OriginFromCallback(uri)
	if err != nil {
		return nil, err
	}
	if _, ok := s.baselineByOrigin(origin); ok {
		return nil, ErrReadOnly
	}
	var row model.OAuthAccessOrigin
	if err := s.db.WithContext(ctx).Where("origin = ?", origin).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.Callbacks(ctx)
		}
		return nil, err
	}
	if _, err := s.Delete(ctx, row.ID); err != nil {
		return nil, err
	}
	return s.Callbacks(ctx)
}

// Run performs startup reconciliation and retries periodically until shutdown.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if err := s.Reconcile(ctx); err != nil {
		slog.Warn("OAuth client reconciliation failed", "client_id", StudioClientID, "err", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Reconcile(ctx); err != nil {
				slog.Warn("OAuth client reconciliation failed", "client_id", StudioClientID, "err", err)
			}
		}
	}
}

// Reconcile imports pre-existing Studio callbacks once, then makes Hydra match
// the deployment baseline plus active runtime origins. Unrecognized URI shapes
// are preserved so this controller never destroys third-party configuration.
func (s *Service) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconcile(ctx)
}

func (s *Service) reconcile(ctx context.Context) error {
	current, err := s.client.GetOAuthClientURIs(ctx, StudioClientID)
	if err != nil {
		s.markFailure(ctx, err)
		return err
	}
	if err := s.importLegacy(ctx, current.RedirectURIs); err != nil {
		s.markFailure(ctx, err)
		return err
	}
	origins, err := s.activeOrigins(ctx)
	if err != nil {
		s.markFailure(ctx, err)
		return err
	}

	next := auth.OAuthClientURIs{}
	for _, uri := range current.RedirectURIs {
		if _, err := OriginFromCallback(uri); err != nil {
			next.RedirectURIs = appendUnique(next.RedirectURIs, uri)
		}
	}
	for _, uri := range current.PostLogoutRedirectURIs {
		if _, err := originFromLogout(uri); err != nil {
			next.PostLogoutRedirectURIs = appendUnique(next.PostLogoutRedirectURIs, uri)
		}
	}
	for _, origin := range origins {
		next.RedirectURIs = appendUnique(next.RedirectURIs, callbackURI(origin))
		next.PostLogoutRedirectURIs = appendUnique(next.PostLogoutRedirectURIs, logoutURI(origin))
	}
	sort.Strings(next.RedirectURIs)
	sort.Strings(next.PostLogoutRedirectURIs)

	if !slices.Equal(current.RedirectURIs, next.RedirectURIs) ||
		!slices.Equal(current.PostLogoutRedirectURIs, next.PostLogoutRedirectURIs) {
		if err := s.client.SetOAuthClientURIs(ctx, StudioClientID, next); err != nil {
			s.markFailure(ctx, err)
			return err
		}
	}
	return s.markSuccess(ctx, desiredHash(next))
}

func (s *Service) importLegacy(ctx context.Context, redirects []string) error {
	var state model.OAuthClientSyncState
	err := s.db.WithContext(ctx).First(&state, "client_id = ?", StudioClientID).Error
	if err == nil && state.LegacyImported {
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	baseline := make(map[string]bool, len(s.baseline))
	for _, b := range s.baseline {
		baseline[b.origin] = true
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, redirect := range redirects {
			origin, err := OriginFromCallback(redirect)
			if err != nil || baseline[origin] {
				continue
			}
			row := model.OAuthAccessOrigin{
				ID: newID(), Origin: origin, DesiredState: desiredStateActive,
				SyncState: syncStatePending, CreatedBy: "legacy-import",
			}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "origin"}}, DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		state.ClientID = StudioClientID
		state.LegacyImported = true
		state.SyncState = syncStatePending
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "client_id"}}, DoUpdates: clause.AssignmentColumns([]string{
			"legacy_imported", "sync_state", "updated_at",
		})}).Create(&state).Error
	})
}

func (s *Service) activeOrigins(ctx context.Context) ([]string, error) {
	origins := make([]string, 0, len(s.baseline))
	seen := make(map[string]bool, len(s.baseline))
	for _, b := range s.baseline {
		if !seen[b.origin] {
			seen[b.origin] = true
			origins = append(origins, b.origin)
		}
	}
	var rows []model.OAuthAccessOrigin
	if err := s.db.WithContext(ctx).Where("desired_state = ?", desiredStateActive).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if !seen[row.Origin] {
			seen[row.Origin] = true
			origins = append(origins, row.Origin)
		}
	}
	sort.Strings(origins)
	return origins, nil
}

func (s *Service) markFailure(ctx context.Context, syncErr error) {
	message := truncate(syncErr.Error(), 1024)
	_ = s.db.WithContext(ctx).Model(&model.OAuthAccessOrigin{}).
		Where("sync_state <> ?", syncStateSynced).
		Updates(map[string]any{"sync_state": syncStateError, "last_sync_error": message}).Error
	state := model.OAuthClientSyncState{ClientID: StudioClientID, SyncState: syncStateError, LastSyncError: message}
	_ = s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "client_id"}}, DoUpdates: clause.AssignmentColumns([]string{
		"sync_state", "last_sync_error", "updated_at",
	})}).Create(&state).Error
}

func (s *Service) markSuccess(ctx context.Context, hash string) error {
	now := time.Now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("desired_state = ?", desiredStateDelete).Delete(&model.OAuthAccessOrigin{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.OAuthAccessOrigin{}).Where("desired_state = ?", desiredStateActive).
			Updates(map[string]any{"sync_state": syncStateSynced, "last_sync_error": ""}).Error; err != nil {
			return err
		}
		state := model.OAuthClientSyncState{
			ClientID: StudioClientID, LegacyImported: true, DesiredHash: hash,
			SyncState: syncStateSynced, LastSyncError: "", LastReconciledAt: &now,
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "client_id"}}, DoUpdates: clause.AssignmentColumns([]string{
			"legacy_imported", "desired_hash", "sync_state", "last_sync_error", "last_reconciled_at", "updated_at",
		})}).Create(&state).Error
	})
}

func (s *Service) baselineByOrigin(origin string) (baselineOrigin, bool) {
	for _, b := range s.baseline {
		if b.origin == origin {
			return b, true
		}
	}
	return baselineOrigin{}, false
}

func entryFromRow(row model.OAuthAccessOrigin) Entry {
	return Entry{
		ID: row.ID, Origin: row.Origin, RedirectURI: callbackURI(row.Origin), LogoutURI: logoutURI(row.Origin),
		Source: "runtime", ReadOnly: false, DesiredState: row.DesiredState,
		SyncState: row.SyncState, LastSyncError: row.LastSyncError,
		CreatedBy: row.CreatedBy, CreatedAt: &row.CreatedAt,
	}
}

func sourceRank(source string) int {
	switch source {
	case "system":
		return 0
	case "deployment":
		return 1
	default:
		return 2
	}
}

func appendUnique(values []string, value string) []string {
	if !slices.Contains(values, value) {
		return append(values, value)
	}
	return values
}

func desiredHash(uris auth.OAuthClientURIs) string {
	b, _ := json.Marshal(uris)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func stableID(source, origin string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + origin))
	return source + "-" + hex.EncodeToString(sum[:8])
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("oauth access origin id: %v", err))
	}
	return hex.EncodeToString(b)
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
