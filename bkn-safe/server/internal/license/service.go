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
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/licverify"
	"github.com/openbkn-ai/licverify/keys"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
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

// bindingCheckInterval is how often an online cluster asks the issuer whether
// its certificate is still bound. It runs on the hourly tick, so the real gap
// is 6–7 hours, and each cluster's phase is set by when its bkn-safe started.
// Compiled in on purpose: a cluster that wants to dodge the check can simply
// block egress, so a knob would protect nothing and only add a support case.
const bindingCheckInterval = 6 * time.Hour

// bindingCheckMinGap throttles checks triggered by an admin opening the
// license page, so a burst of refreshes is one issuer call, not many.
const bindingCheckMinGap = time.Minute

// bindingCheckTimeout bounds a check made while an admin waits on the page.
// Timing out is harmless: no answer means no change.
const bindingCheckTimeout = 5 * time.Second

// Service owns the license row and the verification/renewal loop. All gating
// answers come from the embedded licverify.Guard's atomic snapshot.
type Service struct {
	db        *gorm.DB
	guard     *licverify.Guard
	keys      map[string]ed25519.PublicKey
	fp        string
	serverURL string
	hc        *http.Client
	audit     interface {
		Record(context.Context, audit.Entry) error
	}

	// mu serializes mutations (import/activate/remove). Reads go through the
	// guard snapshot and need no lock.
	mu           sync.Mutex
	lastRenewErr string

	// binding caches the row's Binding column ("" / "unbound" / "revoked").
	// It is refreshed on every load of the row, which the guard does on each
	// evaluation and the distribution endpoint does on each module poll, so
	// replicas converge without a query on the gating hot path.
	binding atomic.Value // string

	// firstRunAt caches the resolved first-run time: the guard asks for it on
	// every state evaluation, and that must not become a query per call. Zero
	// means "not resolved yet" — see firstRun for why a failure is not cached.
	firstRunAt atomic.Int64

	// lastPageCheck throttles page-open binding checks on this replica (unix
	// nanoseconds). It is kept apart from the row's binding_checked_at because
	// a failed page check must not push the shared schedule forward; see
	// checkBindingOlderThan.
	lastPageCheck atomic.Int64
}

// New builds the service with the official compiled-in key table. The
// fingerprint comes from licverify (OPENBKN_INSTANCE_ID in K8s); failing to
// resolve one is an error — the caller decides whether to run without a
// license hub, bkn-safe itself must not be blocked by licensing.
func New(db *gorm.DB, cfg config.LicenseConfig, aud interface {
	Record(context.Context, audit.Entry) error
}) (*Service, error) {
	return NewWithKeyTable(db, cfg, aud, keys.Official())
}

// NewWithKeyTable exists so tests can inject a self-signed test key table.
// Production code has exactly one caller: New with keys.Official(). Keys are
// compiled in, never read from config, env, or any endpoint (hard rule — a
// configurable key is a self-signing hole).
func NewWithKeyTable(db *gorm.DB, cfg config.LicenseConfig, aud interface {
	Record(context.Context, audit.Entry) error
}, keyTable map[string]ed25519.PublicKey) (*Service, error) {
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
		FirstRun:   s.firstRun,
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

// State returns the current gating snapshot (atomic, hot-path safe). A
// certificate the issuer has since unbound or revoked reads as unlicensed: its
// signature is still good, but it no longer belongs to this cluster (#1782).
// The payload stays, so the admin page can still say which license it was.
func (s *Service) State() licverify.Snapshot { return s.withBinding(s.guard.State()) }

func (s *Service) withBinding(snap licverify.Snapshot) licverify.Snapshot {
	if snap.Payload != nil && s.Binding() != "" {
		snap.State = licverify.StateUnlicensed
	}
	return snap
}

// Binding is the issuer's last definitive answer that took this certificate
// away: BindingUnbound, BindingRevoked, or "" while it is still ours.
func (s *Service) Binding() string {
	b, _ := s.binding.Load().(string)
	return b
}

// Gate turns the hub's own verified snapshot into the tier every paid call site
// judges against. bkn-safe is the cluster's licence holder, not a consumer, so
// it wires this instead of running the hub client the other services use
// (ee-design.md §4.1).
//
// It lives here rather than at the call site because there must be exactly one
// definition of how a certificate becomes a tier. When it was written out at
// the wiring point, the capabilities endpoint grew a second copy that answered
// differently for an expired certificate — licensed by one, refused by the
// other.
//
// The returned Gate re-reads State() on every call. Do not memoise it into a
// bool: an imported, renewed or lapsed certificate has to take effect on the
// next request, not the next restart.
//
// It reads the snapshot exactly ONCE per call and derives every field from that
// one reading. Reading twice — say State() here and InForce() again below —
// straddles a certificate swap: the returned Snapshot would then carry the old
// certificate's edition next to the new one's state, describing a deployment
// that never existed. The window is nanoseconds, which is precisely why such a
// bug would never be reproduced from a report.
func Gate(s *Service) entitlement.Gate {
	return entitlement.GateFunc(func() entitlement.Snapshot {
		if s == nil {
			return entitlement.Snapshot{Edition: licverify.EditionCommunity}
		}
		snap := s.State()
		// Outside valid/grace the certificate grants nothing, even though an
		// expired one still carries a payload with a paid edition in it.
		// Reading that edition would hand out capability the licence no longer
		// covers, which is the exact bug this predicate exists to prevent.
		if !inForce(snap) || snap.Payload == nil {
			return entitlement.Snapshot{Edition: licverify.EditionCommunity, State: snap.State}
		}
		return entitlement.Snapshot{
			Licensed: true,
			Edition:  snap.Payload.Edition,
			State:    snap.State,
			// Display and audit only. Nothing gates on this (ee-design.md §3.2).
			Features: snap.Payload.Features,
		}
	})
}

// inForce is the single predicate for "does this snapshot grant anything".
// Both InForce and Gate go through it so there is one definition rather than
// two that can drift — the drift this migration was cleaning up in the first
// place.
func inForce(snap licverify.Snapshot) bool {
	return snap.State == licverify.StateValid || snap.State == licverify.StateGrace
}

// InForce reports whether the licence currently grants anything. It reads the
// live snapshot on every call and caches nothing, so a hot-reload, an expiry or
// a revocation takes effect everywhere without a restart (open-core-gating §2.7
// rule 5).
//
// Only StateValid and StateGrace grant. Every other state — a licence lapsed
// past grace, an unverifiable file, a fresh install inside its trial window, an
// unactivated cluster — behaves as community. That is a downgrade of paid
// capability only: community capability is not gated at all, and data stays
// readable and exportable in every state.
//
// There is deliberately no per-feature form of this. Authorisation is by tier
// (ee-design.md §3.1); the certificate's features[] is display and audit data,
// and a FeatureEnabled(key) helper is how fine-grained gating creeps back in.
func (s *Service) InForce() bool { return inForce(s.State()) }

// Fingerprint returns this cluster's instance fingerprint. Available with or
// without a license — the activation guide shows it before anything is imported.
func (s *Service) Fingerprint() string { return s.fp }

// Activated reports whether the current license is bound to this instance: the
// certificate names our fingerprint and the issuer has not since taken it back.
func (s *Service) Activated() bool {
	snap := s.guard.State()
	return snap.Payload != nil && snap.Payload.HWFingerprint != "" && s.Binding() == ""
}

// ActivationRequest returns what the customer pastes into the license portal
// for offline activation: this instance's fingerprint, plus the id of the
// license already installed, if any. With no license the lic_id half is empty
// and the portal binds whichever license the fingerprint is submitted against.
//
// It used to also return an "activation code" — a base64 of exactly these two
// fields, and nothing else. licverify retired that helper (dd891e6, "offline
// activation pastes the fingerprint") because the issuer only ever read
// instance_fp back out of it, so the encoding bought nothing and cost the
// customer a step that could go wrong in a copy-paste.
func (s *Service) ActivationRequest() (fp, licID string) {
	if snap := s.guard.State(); snap.Payload != nil {
		licID = snap.Payload.LicID
	}
	return s.fp, licID
}

// Current returns the raw license text and its ETag for module distribution.
// The ETag changes exactly when the text changes (renewal, import).
//
// An unbound or revoked certificate is withheld. Modules verify the text
// themselves and would keep honouring its signature, so not handing it out is
// the only way the downgrade reaches them; they already read "no license" as
// community.
func (s *Service) Current() (text, etag string, err error) {
	text, err = s.loadText()
	if err != nil {
		return "", "", err
	}
	if text == "" || s.Binding() != "" {
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

// CheckBinding asks the issuer whether the installed certificate is still
// bound to this cluster and records a definitive answer. Every failure to get
// one (offline deployment, network, older issuer) leaves the state alone and
// returns the error for logging: an issuer outage must never downgrade anyone.
//
// Only a certificate that names our fingerprint is checked; an unactivated one
// is already shown as pending activation and has nothing to lose.
//
// The answer is written only if the row still holds the text that was asked
// about. An admin re-activating while the check is in flight stores a fresh
// text, and a late "unbound" about the old one must not land on it.
func (s *Service) CheckBinding(ctx context.Context) error {
	return s.checkBinding(ctx, true)
}

// checkBinding is CheckBinding with a choice about failed attempts.
// recordFailure stamps binding_checked_at even when no answer came back, so
// the periodic path retries a dead issuer on its own schedule instead of every
// tick. The page path passes false: its 5-second budget can fail against an
// issuer the periodic path's 30 seconds would reach, and stamping those
// failures would keep pushing the periodic check back for as long as someone
// keeps opening the page.
func (s *Service) checkBinding(ctx context.Context, recordFailure bool) error {
	if s.serverURL == "" {
		return ErrOfflineDeployment
	}
	var row model.License
	if err := s.db.WithContext(ctx).First(&row, "id = ?", rowID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoLicense
		}
		return err
	}
	if _, p := licverify.Eval(row.Text, s.keys); p == nil || p.HWFingerprint == "" {
		return nil
	}
	status, err := checkBinding(ctx, s.hc, s.serverURL, row.Text, s.fp)
	now := time.Now().Unix()
	if err != nil {
		// Keep whatever the last real answer was either way.
		if recordFailure {
			s.db.Model(&model.License{}).Where("id = ? AND version = ?", rowID, row.Version).
				Update("binding_checked_at", now)
		}
		return err
	}
	binding := ""
	if status != BindingBound {
		binding = status
	}
	res := s.db.WithContext(ctx).Model(&model.License{}).
		Where("id = ? AND version = ?", rowID, row.Version).
		Updates(map[string]any{"binding": binding, "binding_checked_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil // the text changed under us; the next check asks about the new one
	}
	if binding != row.Binding {
		slog.Warn("license: issuer binding changed", "from", bindingLabel(row.Binding), "to", status)
		s.auditRecord("license.binding-change", fmt.Sprintf("%s -> %s", bindingLabel(row.Binding), status))
	}
	s.guard.Refresh()
	return nil
}

// CheckBindingIfStale is the page-open trigger: at most one issuer call per
// bindingCheckMinGap on this replica, bounded by bindingCheckTimeout. Its
// failures are not recorded in the shared schedule (see checkBinding).
func (s *Service) CheckBindingIfStale(ctx context.Context) {
	if s.serverURL == "" {
		return
	}
	now := time.Now()
	if last := s.lastPageCheck.Load(); last != 0 && now.Sub(time.Unix(0, last)) < bindingCheckMinGap {
		s.guard.Refresh() // still pick up another replica's answer
		return
	}
	s.lastPageCheck.Store(now.UnixNano())
	s.checkBindingOlderThan(ctx, bindingCheckMinGap, bindingCheckTimeout, false)
}

func (s *Service) checkBindingOlderThan(ctx context.Context, age, timeout time.Duration, recordFailure bool) {
	if s.serverURL == "" {
		return
	}
	var row model.License
	if err := s.db.WithContext(ctx).Select("binding_checked_at").First(&row, "id = ?", rowID).Error; err != nil {
		return
	}
	if time.Since(time.Unix(row.BindingCheckedAt, 0)) < age {
		// Another replica may have recorded an answer since this one last
		// read the row; pick it up so every pod tells the admin the same thing.
		s.guard.Refresh()
		return
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := s.checkBinding(cctx, recordFailure); err != nil {
		slog.Info("license: binding check skipped", "err", err)
	}
}

func bindingLabel(b string) string {
	if b == "" {
		return BindingBound
	}
	return b
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
			s.checkBindingOlderThan(ctx, bindingCheckInterval, 30*time.Second, true)
		}
	}
}

// firstRun is when this deployment first started, in unix seconds. licverify
// uses it to tell a fresh install (trial: quiet, nothing to do yet) apart from
// one whose trial window has elapsed (unlicensed: standing prompt to activate).
// Neither state carries paid capability — InForce only honours valid and grace
// — so this only decides which of the two an admin screen shows.
//
// The timestamp is the oldest user row. bkn-safe seeds the built-in admin at
// install and there is no path that deletes every user, so the minimum is the
// moment this deployment came up, it survives restarts and replicas agree on
// it. A dedicated install-metadata row would be more direct but would cost a
// migration for a value that is already sitting in the database.
//
// Read it as ORDER BY ... LIMIT 1 into the model, not as SELECT MIN(...) into a
// sql.NullTime: scanning an expression column bypasses GORM's field decoding
// and lands on the driver's raw value, which for SQLite is a string and fails
// to scan into a time. That failure is silent — it degrades to 0, i.e. "brand
// new" — so the whole distinction would quietly never happen. TestFirstRun is
// what keeps this honest.
//
// Answering 0 (empty table, or a database that will not answer) reads as
// "brand new", i.e. trial. That is the quiet state and it grants nothing, so
// the failure mode is a missing activation prompt rather than a false one. A
// failed read is not memoised: the value is cached only once it is real, so a
// database that was briefly unreachable does not pin this replica to trial for
// the rest of its life.
func (s *Service) firstRun() int64 {
	if at := s.firstRunAt.Load(); at != 0 {
		return at
	}
	var first model.User
	if err := s.db.Order("created_at ASC").Limit(1).Find(&first).Error; err != nil {
		slog.Warn("license: cannot resolve first-run time; treating this deployment as new",
			"err", err)
		return 0
	}
	if first.CreatedAt.IsZero() {
		return 0
	}
	at := first.CreatedAt.Unix()
	s.firstRunAt.Store(at)
	return at
}

// loadText is the Guard's Load hook: the license text, "" when none installed.
func (s *Service) loadText() (string, error) {
	var row model.License
	err := s.db.First(&row, "id = ?", rowID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s.binding.Store("")
		return "", nil
	}
	if err != nil {
		return "", err
	}
	s.binding.Store(row.Binding)
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
		// A new text is a new answer from the issuer (activation, renewal) or a
		// deliberate admin import; either way the old verdict no longer applies.
		// It is not a fresh verdict either: re-importing the very certificate
		// the issuer revoked stores it without contacting the issuer, so the
		// schedule is reset and the next tick or page open asks straight away.
		Updates(map[string]any{"text": text, "version": row.Version + 1, "binding": "", "binding_checked_at": 0})
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
