// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knfindskills implements the find_skills skill recall service.
package knfindskills

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/localize"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

var requiredSkillsDataProperties = []string{"skill_id", "name"}

// bknBackendObjectTypeNotFoundCode is the error code bkn-backend returns when a
// requested object type id is absent from the knowledge network. Its batch lookup
// fails the whole call as soon as one id is missing, so this is exactly what a
// network with no skills object type looks like from here.
const bknBackendObjectTypeNotFoundCode = "BknBackend.ObjectType.ObjectTypeNotFound"

// errSkillsNotModeled marks "this knowledge network has no skills object type",
// which is a property of the network rather than a failure of the call.
var errSkillsNotModeled = errors.New("skills object type is not modeled in this knowledge network")

// isObjectTypeNotFound reports whether err is bkn-backend saying the object type
// does not exist. It matches the downstream error code rather than the bare 404,
// because an unknown kn_id is also a 404 and must keep surfacing as one.
func isObjectTypeNotFound(err error) bool {
	var he *infraErr.HTTPError
	return errors.As(err, &he) &&
		he.HTTPCode == http.StatusNotFound &&
		he.Code == bknBackendObjectTypeNotFoundCode
}

type findSkillsServiceImpl struct {
	logger        interfaces.Logger
	config        *config.Config
	ontologyQuery interfaces.DrivenOntologyQuery
	bknBackend    interfaces.BknBackendAccess
	coordinator   *recallCoordinator
}

var (
	fsOnce                sync.Once
	findSkillsServiceInst interfaces.IFindSkillsService
)

// NewFindSkillsService creates a singleton FindSkillsService.
func NewFindSkillsService() interfaces.IFindSkillsService {
	fsOnce.Do(func() {
		cfg := config.NewConfigLoader()
		oq := drivenadapters.NewOntologyQueryAccess()
		bkn := drivenadapters.NewBknBackendAccess()
		findSkillsServiceInst = &findSkillsServiceImpl{
			logger:        cfg.GetLogger(),
			config:        cfg,
			ontologyQuery: oq,
			bknBackend:    bkn,
			coordinator: &recallCoordinator{
				logger:        cfg.GetLogger(),
				config:        &cfg.FindSkills,
				ontologyQuery: oq,
				bknBackend:    bkn,
			},
		}
	})
	return findSkillsServiceInst
}

// NewFindSkillsServiceWith creates a FindSkillsService with injected dependencies (for testing).
func NewFindSkillsServiceWith(
	logger interfaces.Logger,
	cfg *config.Config,
	oq interfaces.DrivenOntologyQuery,
	bkn interfaces.BknBackendAccess,
) interfaces.IFindSkillsService {
	return &findSkillsServiceImpl{
		logger:        logger,
		config:        cfg,
		ontologyQuery: oq,
		bknBackend:    bkn,
		coordinator: &recallCoordinator{
			logger:        logger,
			config:        &cfg.FindSkills,
			ontologyQuery: oq,
			bknBackend:    bkn,
		},
	}
}

// FindSkills is the main entry point for skill recall.
func (s *findSkillsServiceImpl) FindSkills(ctx context.Context, req *interfaces.FindSkillsReq) (*interfaces.FindSkillsResp, error) {
	var err error
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	fsCfg := &s.config.FindSkills

	// 1. Normalize & detect recall mode
	mode, err := NormalizeAndDetectMode(req, fsCfg)
	if err != nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
	}

	// Validate the caller-supplied object type FIRST. A wrong object_type_id is the
	// caller's error and must keep reporting ObjectTypeNotFound; a missing skills
	// object type is a property of the network and is answered with an empty result
	// just below. Running the skills contract first made the two indistinguishable.
	if req.ObjectTypeID != fsCfg.SkillsObjectTypeID {
		if err := s.validateObjectTypeExists(ctx, req.KnID, req.ObjectTypeID); err != nil {
			return nil, err
		}
	}

	skillsObjType, err := s.loadAndValidateSkillsContract(ctx, req.KnID, fsCfg.SkillsObjectTypeID)
	if err != nil {
		// A network that binds no skills has nothing to recall. The caller asked
		// "what skills does this object type have"; the answer is "none", not a
		// failure — and reporting bkn-backend's ObjectTypeNotFound sent triage
		// after the caller's object_type_id, which does exist. See #1224.
		if errors.Is(err, errSkillsNotModeled) {
			s.logger.WithContext(ctx).Infof(
				"[FindSkills] kn_id=%s has no skills object type %q; returning an empty result",
				req.KnID, fsCfg.SkillsObjectTypeID)
			return &interfaces.FindSkillsResp{
				Entries: []*interfaces.SkillItem{},
				Message: translateMessage(ctx, "find_skills.skills_not_modeled"),
			}, nil
		}
		return nil, err
	}

	s.logger.WithContext(ctx).Infof("[FindSkills] kn_id=%s, mode=%d, object_type_id=%s, instance_count=%d, has_skill_query=%v",
		req.KnID, mode, req.ObjectTypeID, len(req.InstanceIdentities), req.SkillQuery != "")

	// 2. Build skill_query condition (reuse validated skills ObjectType metadata)
	var skillQueryCond *interfaces.KnCondition
	if req.SkillQuery != "" {
		skillQueryCond = BuildSkillQueryCondition(req.SkillQuery, skillsObjType, req.TopK)
	}

	// 3. Apply total timeout
	totalTimeoutMs := fsCfg.TotalTimeoutMs
	if totalTimeoutMs <= 0 {
		totalTimeoutMs = 10000
	}
	recallCtx, cancel := context.WithTimeout(ctx, time.Duration(totalTimeoutMs)*time.Millisecond)
	defer cancel()

	// 4. Execute recall based on mode
	var matches []interfaces.SkillMatch
	var emptyHint interfaces.EmptyResultHint
	switch mode {
	case interfaces.RecallModeNetwork:
		matches, emptyHint, err = s.coordinator.recallNetwork(recallCtx, req, skillQueryCond)
	case interfaces.RecallModeObjectType:
		matches, emptyHint, err = s.coordinator.recallObjectType(recallCtx, req, skillQueryCond)
	case interfaces.RecallModeInstance:
		matches, emptyHint, err = s.coordinator.recallInstance(recallCtx, req, skillQueryCond)
	default:
		err = fmt.Errorf("unknown recall mode: %d", mode)
	}

	if err != nil {
		s.logger.WithContext(ctx).Errorf("[FindSkills] recall failed: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadGateway, err.Error())
	}

	// 5. Assemble result
	resp := Assemble(matches, req.TopK)

	// 6. Generate empty-result message
	if len(resp.Entries) == 0 {
		msgKey := resolveEmptyResultMessageKey(emptyHint, mode, req.SkillQuery != "")
		resp.Message = translateMessage(ctx, msgKey)
	}

	s.logger.WithContext(ctx).Infof("[FindSkills] returning %d skills for kn_id=%s", len(resp.Entries), req.KnID)
	return resp, nil
}

func (s *findSkillsServiceImpl) loadAndValidateSkillsContract(ctx context.Context, knID, skillsObjectTypeID string) (*interfaces.ObjectType, error) {
	objectTypes, err := s.bknBackend.GetObjectTypeDetail(ctx, knID, []string{skillsObjectTypeID}, true)
	if err != nil {
		if isObjectTypeNotFound(err) {
			return nil, errSkillsNotModeled
		}
		return nil, err
	}
	if len(objectTypes) == 0 {
		return nil, errSkillsNotModeled
	}

	skillsObjType := objectTypes[0]
	existingProps := make(map[string]struct{}, len(skillsObjType.DataProperties))
	for _, prop := range skillsObjType.DataProperties {
		if prop == nil {
			continue
		}
		name := strings.TrimSpace(prop.Name)
		if name == "" {
			continue
		}
		existingProps[name] = struct{}{}
	}

	var missingProps []string
	for _, name := range requiredSkillsDataProperties {
		if _, ok := existingProps[name]; !ok {
			missingProps = append(missingProps, name)
		}
	}
	if len(missingProps) > 0 {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest, map[string]interface{}{
			"kn_id":                   knID,
			"skills_object_type_id":   skillsObjectTypeID,
			"missing_data_properties": missingProps,
			"reason":                  "skills object type contract is incomplete",
		})
	}

	return skillsObjType, nil
}

func (s *findSkillsServiceImpl) validateObjectTypeExists(ctx context.Context, knID, objectTypeID string) error {
	objectTypes, err := s.bknBackend.GetObjectTypeDetail(ctx, knID, []string{objectTypeID}, false)
	if err != nil {
		return err
	}
	if len(objectTypes) > 0 {
		return nil
	}

	return infraErr.DefaultHTTPError(ctx, http.StatusNotFound, map[string]interface{}{
		"kn_id":          knID,
		"object_type_id": objectTypeID,
		"reason":         "object_type_id not found in current knowledge network",
	})
}

func resolveEmptyResultMessageKey(hint interfaces.EmptyResultHint, mode interfaces.RecallMode, hasSkillQuery bool) string {
	if hint != interfaces.HintNone {
		return string(hint)
	}
	if hasSkillQuery {
		return "find_skills.skill_query_no_match"
	}
	switch mode {
	case interfaces.RecallModeNetwork:
		return "find_skills.network_no_skills"
	case interfaces.RecallModeObjectType:
		return "find_skills.object_type_no_match"
	case interfaces.RecallModeInstance:
		return "find_skills.instance_no_match"
	default:
		return "find_skills.network_no_skills"
	}
}

func translateMessage(ctx context.Context, msgKey string) string {
	lang := common.GetLanguageFromCtx(ctx)
	langKey := strings.ReplaceAll(lang, "-", "_")
	tr := localize.NewI18nTranslator(langKey)
	return tr.Trans(msgKey)
}
