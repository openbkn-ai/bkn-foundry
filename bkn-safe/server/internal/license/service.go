// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package license makes bkn-safe the cluster's license hub: it holds the one
// signed .lic (DB row), is the cluster's only egress to the license-server
// (activation + auto-renewal), re-verifies hourly, and hands the license text
// out to modules — which verify it locally with licverify. bkn-safe's own
// answers are weak judgements (UI, monitoring); the signature is the only
// trust root. Design: bkn-docs docs/foundry/bkn-safe/design/issue-224-license-hub.md,
// upstream spec: license-server docs/bkn-safe-license-integration.md.
package license

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/licverify"
	"github.com/openbkn-ai/licverify/keys"
	"gorm.io/gorm"

	"bkn-safe/config"
	"bkn-safe/internal/audit"
	"bkn-safe/internal/model"
)

const rowID = "current"

// clockTolerance is how far the clock may sit behind the persisted high-water
// mark before we call it a rollback (NTP steps and timezone fixes stay under
// this; a rollback that would un-expire a license does not).
const clockTolerance = 24 * time.Hour

var (
	// ErrBadLicense: malformed text, unknown kid, or bad signature — never stored.
	ErrBadLicense = errors.New("license: malformed or signature invalid")
	// ErrBoundElsewhere: the license embeds another instance's fingerprint (a
	// copied certificate) — never stored.
	ErrBoundElsewhere = errors.New("license: bound to a different instance")
	// ErrNoLicense: no license installed.
	ErrNoLicense = errors.New("license: no license installed")
	// ErrOfflineDeployment: the action needs a license server but none is
	// configured — use the offline request-code/receipt flow instead.
	ErrOfflineDeployment = errors.New("license: no license server configured (offline deployment)")
)

// Service owns the license row and the verification/renewal loop. All gating
// answers come from the embedded licverify.Guard's atomic snapshot.
type Service struct {
	db        *gorm.DB
	guard     *licverify.Guard
	keys      map[string]ed25519.PublicKey
	fp        string
	serverURL string
	hc        *http.Client
	audit     *audit.Store

	// mu serializes mutations (import/activate/remove). Reads go through the
	// guard snapshot and need no lock.
	mu           sync.Mutex
	lastRenewErr string
}

// New builds the service with the official compiled-in key table. The
// fingerprint comes from licverify (OPENBKN_INSTANCE_ID in K8s); failing to
// resolve one is an error — the caller decides whether to run without a
// license hub, bkn-safe itself must not be blocked by licensing.
func New(db *gorm.DB, cfg config.LicenseConfig, aud *audit.Store) (*Service, error) {
	return NewWithKeyTable(db, cfg, aud, keys.Official())
}

// NewWithKeyTable exists so tests can inject a self-signed test key table.
// Production code has exactly one caller: New with keys.Official(). Keys are
// compiled in, never read from config, env, or any endpoint (hard rule — a
// configurable key is a self-signing hole).
func NewWithKeyTable(db *gorm.DB, cfg config.LicenseConfig, aud *audit.Store, keyTable map[string]ed25519.PublicKey) (*Service, error) {
	fp, err := licverify.Fingerprint()
	if err != nil {
		return nil, fmt.Errorf("license: resolve instance fingerprint: %w", err)
	}
	hc, err := httpClientFor(cfg)
	if err != nil {
		return nil, err
	}
	s := &Service{
		db:        db,
		keys:      keyTable,
		fp:        fp,
		serverURL: strings.TrimRight(cfg.ServerURL, "/"),
		hc:        hc,
		audit:     aud,
	}
	renewURL := ""
	if s.serverURL != "" {
		renewURL = s.serverURL + "/api/licenses/renew"
	}
	g, err := licverify.NewGuard(licverify.GuardConfig{
		Keys:       keyTable,
		Load:       s.loadText,
		Store:      s.storeText,
		RenewURL:   renewURL,
		InstanceFP: fp,
		HTTPClient: hc,
		Logf: func(format string, args ...any) {
			slog.Info("license: " + fmt.Sprintf(format, args...))
		},
		OnChange: s.onChange,
	})
	if err != nil {
		return nil, err
	}
	s.guard = g
	return s, nil
}

// State returns the current gating snapshot (atomic, hot-path safe).
func (s *Service) State() licverify.Snapshot { return s.guard.State() }

// Fingerprint returns this cluster's instance fingerprint. Available with or
// without a license — the activation guide shows it before anything is imported.
func (s *Service) Fingerprint() string { return s.fp }

// Activated reports whether the current license is bound to this instance.
func (s *Service) Activated() bool {
	snap := s.guard.State()
	return snap.Payload != nil && snap.Payload.HWFingerprint != ""
}

// ActivationCode returns the offline activation request code the customer
// pastes into the license portal. With no license installed the lic_id half is
// empty; the portal then binds whichever license the code is submitted against.
func (s *Service) ActivationCode() (fp, code, licID string) {
	if snap := s.guard.State(); snap.Payload != nil {
		licID = snap.Payload.LicID
	}
	return s.fp, licverify.ActivationCode(licID, s.fp), licID
}

// Current returns the raw license text and its ETag for module distribution.
// The ETag changes exactly when the text changes (renewal, import).
func (s *Service) Current() (text, etag string, err error) {
	text, err = s.loadText()
	if err != nil {
		return "", "", err
	}
	if text == "" {
		return "", "", ErrNoLicense
	}
	return text, ETag(text), nil
}

// ETag derives the distribution ETag of a license text.
func ETag(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

// Import verifies and stores a .lic (admin import and offline receipt share
// this). Signature-invalid or foreign-bound texts are rejected without
// storing. An unbound license on an online deployment is auto-activated;
// activation trouble comes back in actErr with the license already stored, so
// the caller can report "stored, activation pending" instead of losing the
// import.
func (s *Service) Import(ctx context.Context, text string) (snap licverify.Snapshot, actErr, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text = strings.TrimSpace(text)
	state, p := licverify.Eval(text, s.keys)
	if state == licverify.StateInvalid {
		return s.guard.State(), nil, ErrBadLicense
	}
	if licverify.VerifyBound(p, s.fp) != nil {
		return s.guard.State(), nil, ErrBoundElsewhere
	}
	if err := s.storeText(text); err != nil {
		return s.guard.State(), nil, err
	}
	snap = s.guard.Refresh()
	if p.HWFingerprint == "" && s.serverURL != "" {
		fresh, aerr := activate(ctx, s.hc, s.serverURL, text, s.fp)
		if aerr != nil {
			return snap, aerr, nil
		}
		if serr := s.storeText(fresh); serr != nil {
			return snap, serr, nil
		}
		snap = s.guard.Refresh()
	}
	return snap, nil, nil
}

// Activate reports the installed license to the issuer and stores the reissued
// (fingerprint-bound) text. Re-activating an already-bound license is
// idempotent on the issuer side.
func (s *Service) Activate(ctx context.Context) (licverify.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.serverURL == "" {
		return s.guard.State(), ErrOfflineDeployment
	}
	text, err := s.loadText()
	if err != nil {
		return s.guard.State(), err
	}
	if text == "" {
		return s.guard.State(), ErrNoLicense
	}
	fresh, err := activate(ctx, s.hc, s.serverURL, text, s.fp)
	if err != nil {
		return s.guard.State(), err
	}
	if err := s.storeText(fresh); err != nil {
		return s.guard.State(), err
	}
	return s.guard.Refresh(), nil
}

// Remove deletes the installed license (back to the unactivated state). Data
// is never locked by license state; this only drops paid gating.
func (s *Service) Remove(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.db.WithContext(ctx).Delete(&model.License{}, "id = ?", rowID).Error; err != nil {
		return err
	}
	s.guard.Refresh()
	return nil
}

// RenewNow forces one renewing evaluation (what an hourly tick does). Blocking.
func (s *Service) RenewNow() licverify.Snapshot {
	snap := s.guard.RenewNow()
	s.trackRenewErr(snap)
	return snap
}

// Run drives the hourly loop: re-evaluate (renew when the window calls for
// it — the Guard renews inside the last third and throughout grace) and
// advance the clock high-water mark. Call as a goroutine.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RenewNow()
			s.checkClock(time.Now())
		}
	}
}

// loadText is the Guard's Load hook: the license text, "" when none installed.
func (s *Service) loadText() (string, error) {
	var row model.License
	err := s.db.First(&row, "id = ?", rowID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Text, nil
}

// storeText persists a license under an optimistic lock. Losing the race means
// another replica just wrote (typically its own renewal of the same license);
// the loser reports failure and re-reads on its next cycle rather than
// clobbering a fresher certificate with a stale one.
func (s *Service) storeText(text string) error {
	var row model.License
	err := s.db.First(&row, "id = ?", rowID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.Create(&model.License{ID: rowID, Text: text, Version: 1}).Error
	}
	if err != nil {
		return err
	}
	res := s.db.Model(&model.License{}).
		Where("id = ? AND version = ?", rowID, row.Version).
		Updates(map[string]any{"text": text, "version": row.Version + 1})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("license: lost concurrent update, keeping the other writer's text")
	}
	return nil
}

// checkClock persists the max timestamp ever seen and flags large rollbacks —
// the one lever an offline deployment has to stretch an expired license. It
// detects and audits; it never blocks (matching the never-lock-data promise).
func (s *Service) checkClock(now time.Time) {
	var row model.License
	err := s.db.First(&row, "id = ?", rowID).Error
	if err != nil {
		return // nothing installed (or DB blip): nothing to guard
	}
	if now.Unix() < row.HighWater-int64(clockTolerance/time.Second) {
		behind := time.Duration(row.HighWater-now.Unix()) * time.Second
		slog.Warn("license: system clock is far behind the recorded high-water mark", "behind", behind)
		s.auditRecord("license.clock-rollback", fmt.Sprintf("clock behind high-water mark by %s", behind))
		return // never lower the mark
	}
	if now.Unix() > row.HighWater {
		s.db.Model(&model.License{}).Where("id = ?", rowID).Update("high_water", now.Unix())
	}
}

// onChange logs and audits state transitions. The Guard fires this only on
// change, not steady state; the boot transition from the zero state is logged
// but not audited (one row per restart would be noise).
func (s *Service) onChange(old, cur licverify.Snapshot) {
	slog.Info("license state", "from", old.State, "to", cur.State)
	if old.State == "" {
		return
	}
	s.auditRecord("license.state-change", fmt.Sprintf("%s -> %s", old.State, cur.State))
}

// trackRenewErr audits the first failure of a renewal streak (hourly repeats
// only log) and resets on success.
func (s *Service) trackRenewErr(snap licverify.Snapshot) {
	if snap.RenewErr == nil {
		s.lastRenewErr = ""
		return
	}
	msg := snap.RenewErr.Error()
	if msg == s.lastRenewErr {
		return
	}
	s.lastRenewErr = msg
	s.auditRecord("license.renew-failed", msg)
}

func (s *Service) auditRecord(action, detail string) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Record(context.Background(), audit.Entry{
		ActorID:  "system:license",
		Method:   "SYSTEM",
		Resource: "license",
		Action:   action,
		Detail:   detail,
		Status:   http.StatusOK,
	}); err != nil {
		slog.Error("license: audit record failed", "err", err)
	}
}
