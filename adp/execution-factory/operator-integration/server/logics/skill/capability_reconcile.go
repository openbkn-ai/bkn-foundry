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

// The Skill half of the capability index is written at every Skill write (see mirrorCapability).
// This pass is the safety net underneath that, and it is not optional: the capability dataset can
// be recreated — a changed embedding model rebuilds it — and a recreated dataset comes back empty.
// Without a pass that puts Skills back, they would stay missing from search until somebody happened
// to edit one.
//
// It is shaped like the tool reconciler on purpose: read what the index holds, read what the source
// says, write the difference. Name and description are the entire embedding input, so comparing
// those two strings is what keeps a pass from re-vectorising every Skill on the platform.

// StartCapabilityReconciler runs a Skill pass now and then every interval.
func StartCapabilityReconciler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	syncer, ok := NewSkillIndexSyncService().(*skillIndexSync)
	if !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := syncer.reconcileCapabilities(ctx); err != nil {
				syncer.logger.WithContext(ctx).Warnf("reconcile skill capabilities failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
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
		if err := s.capabilitySync.UpsertCapability(ctx, doc); err != nil {
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
