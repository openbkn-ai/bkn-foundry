package skill

import (
	"context"
	"fmt"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/capability"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// The Skill index used to be a dataset of its own, holding Skills and nothing else. It is retired:
// Skills now live in the unified capability index beside Function tools and MCP tools, which is
// what lets a knowledge network rank all three against one query instead of concatenating three
// lists ordered by three incomparable rules (#1370).
//
// This service survives as the seam it always was — the one point every Skill write passes
// through, and the place that knows which snapshot of a Skill counts — but it now writes one
// document to one index. Everything it used to own about a dataset of its own (its schema, its
// embedding model, its rebuild and restore) went with the dataset.
//
// The old vega resource is deliberately left in place rather than deleted on startup. Deleting it
// would make a rollback to the previous version unrecoverable: that version reads it and cannot
// rebuild what is gone. It stops being written, so it stops being a second answer to the same
// question; removing the resource is an operator's decision, not an upgrade's.

type skillIndexSync struct {
	capabilitySync interfaces.CapabilityIndexSyncService
	skillRepo      model.ISkillRepository
	releaseRepo    model.ISkillReleaseDB
	logger         interfaces.Logger
}

var (
	ssOnce     = sync.Once{}
	ssInstance *skillIndexSync
)

func NewSkillIndexSyncService() interfaces.SkillIndexSyncService {
	ssOnce.Do(func() {
		conf := config.NewConfigLoader()
		ssInstance = &skillIndexSync{
			capabilitySync: capability.NewCapabilityIndexSyncService(),
			skillRepo:      dbaccess.NewSkillRepositoryDB(),
			releaseRepo:    dbaccess.NewSkillReleaseDB(),
			logger:         conf.GetLogger(),
		}
	})
	return ssInstance
}

// Init makes sure the capability index is ready to accept Skill documents.
func (s *skillIndexSync) Init(ctx context.Context) (err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer func() { oteltrace.EndSpan(ctx, err) }()
	return s.capabilitySync.Init(ctx)
}

// EnsureInitialized initialises on first use; the capability index owns the retry loop.
func (s *skillIndexSync) EnsureInitialized(ctx context.Context) error {
	return s.capabilitySync.EnsureInitialized(ctx)
}

// UpsertSkill writes the Skill into the capability index.
func (s *skillIndexSync) UpsertSkill(ctx context.Context, skill *model.SkillRepositoryDB) error {
	return s.write(ctx, skill)
}

// UpdateSkill is an upsert: the index holds one document per Skill, addressed by its identity.
func (s *skillIndexSync) UpdateSkill(ctx context.Context, skill *model.SkillRepositoryDB) error {
	return s.write(ctx, skill)
}

// DeleteSkill removes the Skill from the capability index.
func (s *skillIndexSync) DeleteSkill(ctx context.Context, skillID string) error {
	err := s.capabilitySync.DeleteCapability(ctx, skillCapabilityRef(skillID))
	if err != nil {
		s.logger.WithContext(ctx).Errorf("delete skill from capability index failed, skill_id=%s, err=%v", skillID, err)
	}
	return err
}

func (s *skillIndexSync) write(ctx context.Context, skill *model.SkillRepositoryDB) error {
	if skill == nil {
		return fmt.Errorf("skill is required")
	}
	err := s.capabilitySync.UpsertCapability(ctx, skillCapabilityDocument(skill))
	if err != nil {
		s.logger.WithContext(ctx).Errorf("write skill into capability index failed, skill_id=%s, err=%v", skill.SkillID, err)
	}
	return err
}

func skillCapabilityRef(skillID string) interfaces.CapabilityRef {
	// A Skill has no owner: it belongs to the platform, not to a box or a server.
	return interfaces.CapabilityRef{
		CapabilityType: interfaces.CapabilityTypeSkill,
		CapabilityID:   skillID,
	}
}

func skillCapabilityDocument(skill *model.SkillRepositoryDB) *interfaces.CapabilityDocument {
	return &interfaces.CapabilityDocument{
		CapabilityRef: skillCapabilityRef(skill.SkillID),
		Name:          skill.Name,
		Description:   skill.Description,
		Version:       skill.Version,
		Category:      skill.Category,
		CreateUser:    skill.CreateUser,
		CreateTime:    skill.CreateTime,
		UpdateUser:    skill.UpdateUser,
		UpdateTime:    skill.UpdateTime,
	}
}

// skillIndexPayload decides which snapshot of a Skill the index should hold.
//
// A published Skill is indexed from its release; an editing Skill only while a published release
// still exists, because that release is what a caller would actually run. A deleted Skill is not
// indexed at all. This rule lives here, in the one place every Skill write passes through, so it
// has a single answer.
func (s *skillIndexSync) skillIndexPayload(ctx context.Context, skill *model.SkillRepositoryDB) (*model.SkillRepositoryDB, error) {
	if skill.IsDeleted {
		return nil, nil
	}
	switch interfaces.BizStatus(skill.Status) {
	case interfaces.BizStatusPublished:
		release, err := s.releaseRepo.SelectBySkillID(ctx, nil, skill.SkillID)
		if err != nil {
			return nil, err
		}
		if release != nil {
			return releaseToSkillRepository(release), nil
		}
		return skill, nil
	case interfaces.BizStatusEditing:
		release, err := s.releaseRepo.SelectBySkillID(ctx, nil, skill.SkillID)
		if err != nil {
			return nil, err
		}
		if release != nil {
			return releaseToSkillRepository(release), nil
		}
	}
	return nil, nil
}
