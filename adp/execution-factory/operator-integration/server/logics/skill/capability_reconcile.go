// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package skill

import (
	"context"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// A notification coalesces committed changes into the existing reconciliation pass.
// The repository remains the source of truth; this is not a queue of snapshots.
var skillCapabilitiesChanged = make(chan struct{}, 1)

func notifySkillCapabilitiesChanged() {
	select {
	case skillCapabilitiesChanged <- struct{}{}:
	default:
	}
}

// Committed Skill writes wake this existing worker; semantic indexing never holds
// the source transaction open. Periodic passes also recover missed notifications:
// the capability dataset can
// be recreated — a changed embedding model rebuilds it — and a recreated dataset comes back empty.
// Without a pass that puts Skills back, they would stay missing from search until somebody happened
// to edit one.
//
// It is shaped like the tool reconciler on purpose: read what the index holds, read what the source
// says, write the difference. Name and description are the entire embedding input, so comparing
// those two strings is what keeps a pass from re-vectorising every Skill on the platform.

// StartCapabilityReconciler indexes committed writes, with optional periodic passes.
// A zero interval disables startup/periodic scans while retaining write notifications.
func StartCapabilityReconciler(ctx context.Context, interval time.Duration) {
	syncer, ok := NewSkillIndexSyncService().(*skillIndexSync)
	if !ok {
		return
	}
	go syncer.runCapabilityReconciler(ctx, interval)
}

func (s *skillIndexSync) runCapabilityReconciler(ctx context.Context, interval time.Duration) {
	var ticks <-chan time.Time
	if interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		ticks = ticker.C
	} else {
		select {
		case <-ctx.Done():
			return
		case <-skillCapabilitiesChanged:
		}
	}
	for {
		passCtx, cancel := context.WithTimeout(ctx, skillIndexOperationTimeout)
		err := s.reconcileCapabilities(passCtx)
		cancel()
		if err != nil {
			s.logger.WithContext(ctx).Warnf("reconcile skill capabilities failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		case <-skillCapabilitiesChanged:
		}
	}
}

// reconcileCapabilities makes the Skill half of the capability index match the Skill repository.
func (s *skillIndexSync) reconcileCapabilities(ctx context.Context) error {
	if s.capabilitySync == nil || s.skillRepo == nil || s.releaseRepo == nil {
		return nil
	}
	if err := s.capabilitySync.EnsureInitialized(ctx); err != nil {
		return err
	}

	desired, err := s.desiredSkillCapabilities(ctx)
	if err != nil {
		// An incomplete desired set read as complete is a purge, so a failed read stops the pass
		// before anything is deleted.
		return err
	}

	indexed, err := s.capabilitySync.ListIndexed(ctx, interfaces.CapabilityTypeSkill)
	if err != nil {
		return err
	}

	var errs []error
	written, deleted := 0, 0
	current := make(map[string]interfaces.IndexedCapability, len(indexed))
	for _, entry := range indexed {
		current[entry.CapabilityID] = entry
	}
	for skillID, doc := range desired {
		existing, ok := current[skillID]
		if ok && existing.Name == doc.Name && existing.Description == doc.Description {
			continue
		}
		if err := s.upsertReconciledSkill(ctx, doc); err != nil {
			errs = append(errs, err)
			continue
		}
		written++
	}
	for skillID, entry := range current {
		if _, ok := desired[skillID]; ok {
			continue
		}
		if err := s.capabilitySync.DeleteCapability(ctx, entry.CapabilityRef); err != nil {
			errs = append(errs, err)
			continue
		}
		deleted++
	}
	s.logger.WithContext(ctx).Infof("skill capabilities reconciled, desired=%d, indexed=%d, written=%d, deleted=%d, errors=%d",
		len(desired), len(current), written, deleted, len(errs))
	return errors.Join(errs...)
}

// upsertReconciledSkill rechecks the committed source after the slow model call.
// A delete/offline operation may have purged the index while the upsert was in
// flight. In that case remove the stale document rather than resurrecting it.
func (s *skillIndexSync) upsertReconciledSkill(ctx context.Context, doc *interfaces.CapabilityDocument) error {
	if err := s.capabilitySync.UpsertCapability(ctx, doc); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), skillIndexOperationTimeout)
	defer cancel()
	current, err := s.skillRepo.SelectSkillByID(checkCtx, nil, doc.CapabilityID)
	if err != nil {
		return err
	}
	if current == nil || current.IsDeleted {
		return s.capabilitySync.DeleteCapability(checkCtx, doc.CapabilityRef)
	}
	payload, err := s.skillIndexPayload(checkCtx, current)
	if err != nil {
		return err
	}
	if payload == nil {
		return s.capabilitySync.DeleteCapability(checkCtx, doc.CapabilityRef)
	}
	// A later committed edit is handled by the coalesced follow-up pass.
	if payload.Name != doc.Name || payload.Description != doc.Description {
		notifySkillCapabilitiesChanged()
	}
	return nil
}

// desiredSkillCapabilities walks the Skill repository and returns what the index should hold.
//
// Which snapshot of a Skill counts is decided by skillIndexPayload, the same rule the Skill index
// itself uses. Asking it here rather than restating it is the point: two answers to "is this Skill
// indexable" is how the two indexes drift apart.
func (s *skillIndexSync) desiredSkillCapabilities(ctx context.Context) (map[string]*interfaces.CapabilityDocument, error) {
	desired := make(map[string]*interfaces.CapabilityDocument)
	var cursorUpdateTime int64
	var cursorSkillID string
	for {
		skills, err := s.skillRepo.SelectSkillBuildPage(ctx, nil, cursorUpdateTime, cursorSkillID, skillIndexBuildBatchSize)
		if err != nil {
			return nil, err
		}
		if len(skills) == 0 {
			return desired, nil
		}
		for _, skill := range skills {
			if skill == nil {
				continue
			}
			cursorUpdateTime = skill.UpdateTime
			cursorSkillID = skill.SkillID
			payload, err := s.skillIndexPayload(ctx, skill)
			if err != nil {
				return nil, err
			}
			if payload == nil {
				continue
			}
			desired[payload.SkillID] = &interfaces.CapabilityDocument{
				CapabilityRef: interfaces.CapabilityRef{
					CapabilityType: interfaces.CapabilityTypeSkill,
					CapabilityID:   payload.SkillID,
				},
				Name:        payload.Name,
				Description: payload.Description,
				Version:     payload.Version,
				Category:    payload.Category,
				CreateUser:  payload.CreateUser,
				CreateTime:  payload.CreateTime,
				UpdateUser:  payload.UpdateUser,
				UpdateTime:  payload.UpdateTime,
			}
		}
	}
}
