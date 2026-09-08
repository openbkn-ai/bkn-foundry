// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knfindskills implements find_skills over the knowledge network's capability bindings.
//
// Recall used to run over a skills object type the user modelled inside the network, and the two
// implicit rules that came with it — "no relation type means the whole network" and "no relation
// type means empty" — were the reason the result could not be explained. Scope is now what the
// network explicitly bound: an unbound network recalls nothing, and there is no shape of the model
// that quietly widens it.
package knfindskills

import (
	"context"
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
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/permission"
)

// defaultTopK bounds a reply when the caller does not.
const defaultTopK = 10

type findSkillsServiceImpl struct {
	logger     interfaces.Logger
	config     *config.Config
	bknBackend interfaces.BknBackendAccess
	operator   interfaces.DrivenOperatorIntegration
	knAuthz    interfaces.KnowledgeNetworkAuthorizer
}

var (
	fsOnce                sync.Once
	findSkillsServiceInst interfaces.IFindSkillsService
)

// NewFindSkillsService creates a singleton FindSkillsService.
func NewFindSkillsService() interfaces.IFindSkillsService {
	fsOnce.Do(func() {
		cfg := config.NewConfigLoader()
		findSkillsServiceInst = &findSkillsServiceImpl{
			logger:     cfg.GetLogger(),
			config:     cfg,
			bknBackend: drivenadapters.NewBknBackendAccess(),
			operator:   drivenadapters.NewOperatorIntegrationClient(),
			knAuthz:    permission.NewKnowledgeNetworkAuthorizer(cfg),
		}
	})
	return findSkillsServiceInst
}

// NewFindSkillsServiceWith creates a FindSkillsService with injected dependencies (for testing).
func NewFindSkillsServiceWith(
	logger interfaces.Logger,
	cfg *config.Config,
	bkn interfaces.BknBackendAccess,
	operator interfaces.DrivenOperatorIntegration,
	knAuthz interfaces.KnowledgeNetworkAuthorizer,
) interfaces.IFindSkillsService {
	return &findSkillsServiceImpl{
		logger:     logger,
		config:     cfg,
		bknBackend: bkn,
		operator:   operator,
		knAuthz:    knAuthz,
	}
}

// FindSkills returns the Skills this knowledge network has bound, ranked against skill_query when
// one is given.
//
// object_type_id is accepted and ignored. It stays in the contract so the MCP surface does not
// change under callers, but object-type-scoped recall is a later step: today a binding belongs to
// the network branch, not to one object type, and answering differently per object type would be
// inventing a scope the data does not carry.
func (s *findSkillsServiceImpl) FindSkills(ctx context.Context,
	req *interfaces.FindSkillsReq) (*interfaces.FindSkillsResp, error) {
	var err error
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	if req == nil || strings.TrimSpace(req.KnID) == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest, "kn_id is required")
	}
	topK := req.TopK
	if topK <= 0 {
		topK = defaultTopK
	}

	// The old object-type path got its per-caller check for free: every route ended in an
	// ontology-query call that authorized the caller against the data. This one reads the
	// bindings and resolves them in the execution factory, touching neither — so without this
	// check the answer would be scoped by nothing but the kn_id the caller typed.
	if s.knAuthz == nil {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusServiceUnavailable,
			"knowledge network authorization is not configured")
	}
	if err = s.knAuthz.AuthorizeRead(ctx, strings.TrimSpace(req.KnID)); err != nil {
		return nil, err
	}

	fsCfg := &s.config.FindSkills
	totalTimeoutMs := fsCfg.TotalTimeoutMs
	if totalTimeoutMs <= 0 {
		totalTimeoutMs = 10000
	}
	recallCtx, cancel := context.WithTimeout(ctx, time.Duration(totalTimeoutMs)*time.Millisecond)
	defer cancel()

	// The binding list is the scope, so a failure to read it fails the call. Falling back to an
	// unfiltered listing would hand a network every Skill on the platform at exactly the moment
	// the service could not tell which ones it was allowed to show.
	refs, err := s.bknBackend.ListKNCapabilities(recallCtx, req.KnID, "", interfaces.CapabilityTypeSkill)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("[FindSkills] kn_id=%s capability lookup failed: %v", req.KnID, err)
		return nil, err
	}

	skillIDs := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref == nil || ref.CapabilityType != interfaces.CapabilityTypeSkill {
			continue
		}
		id := strings.TrimSpace(ref.CapabilityID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		skillIDs = append(skillIDs, id)
	}

	if len(skillIDs) == 0 {
		s.logger.WithContext(ctx).Infof("[FindSkills] kn_id=%s has no bound skills", req.KnID)
		return &interfaces.FindSkillsResp{
			Entries: []*interfaces.SkillItem{},
			Message: translateMessage(ctx, "find_skills.no_bound_skills"),
		}, nil
	}

	s.logger.WithContext(ctx).Infof("[FindSkills] kn_id=%s bound=%d has_skill_query=%v",
		req.KnID, len(skillIDs), req.SkillQuery != "")

	entries, err := s.rank(recallCtx, skillIDs, strings.TrimSpace(req.SkillQuery), topK)
	if err != nil {
		return nil, err
	}

	resp := &interfaces.FindSkillsResp{Entries: entries}
	if len(entries) == 0 {
		resp.Entries = []*interfaces.SkillItem{}
		resp.Message = translateMessage(ctx, "find_skills.skill_query_no_match")
	}
	s.logger.WithContext(ctx).Infof("[FindSkills] returning %d skills for kn_id=%s", len(resp.Entries), req.KnID)
	return resp, nil
}

// rank turns the bound ids into an answer.
//
// With a query, Execution Factory ranks the whitelist in the unified capability index and its
// order is the answer. Skills are asked for by type rather than by a Skill-only endpoint: the
// index holds all three kinds in one ranking space, and narrowing by type is how this surface
// stays about Skills without going back to a separate ranking of its own.
//
// Without a query there is nothing to rank against, so the binding order is kept — it is the order
// the network declared, which is at least stable and explainable, unlike whatever the index
// happens to return.
func (s *findSkillsServiceImpl) rank(ctx context.Context, skillIDs []string, query string,
	topK int) ([]*interfaces.SkillItem, error) {
	refs := make([]interfaces.SearchCapabilityRef, 0, len(skillIDs))
	for _, id := range skillIDs {
		refs = append(refs, interfaces.SearchCapabilityRef{
			CapabilityType: interfaces.CapabilityTypeSkill,
			CapabilityID:   id,
		})
	}

	hits, err := s.operator.SearchCapabilities(ctx, &interfaces.SearchCapabilitiesRequest{
		Query: query,
		Refs:  refs,
		TopK:  topK,
		Types: []string{interfaces.CapabilityTypeSkill},
	})
	if err != nil {
		if query != "" {
			return nil, err
		}
		// An unfiltered listing must not fail because the index was unreachable: the memberships
		// are known from the bindings, and the registry can still name them. Ranking has nothing
		// to fall back to, so a query still surfaces the error.
		s.logger.WithContext(ctx).Warnf("[FindSkills] capability search failed, falling back to the registry: %v", err)
		hits = nil
	}

	byID := make(map[string]interfaces.CapabilityHit, len(hits))
	for _, hit := range hits {
		if hit.CapabilityType == interfaces.CapabilityTypeSkill && hit.CapabilityID != "" {
			byID[hit.CapabilityID] = hit
		}
	}

	if query != "" {
		entries := make([]*interfaces.SkillItem, 0, len(hits))
		for _, hit := range hits {
			if hit.CapabilityType != interfaces.CapabilityTypeSkill || hit.CapabilityID == "" {
				continue
			}
			entries = append(entries, &interfaces.SkillItem{
				SkillID:     hit.CapabilityID,
				Name:        hit.Name,
				Description: hit.Description,
			})
			if len(entries) >= topK {
				break
			}
		}
		return entries, nil
	}

	// No query: list what is bound, in binding order. The index can be missing or behind — it is
	// built asynchronously and a fresh install has none — so anything it did not return is filled
	// from the registry. Listing a bound Skill by name alone is a degraded answer; omitting it
	// because an index was not ready is a wrong one.
	wanted := skillIDs
	if len(wanted) > topK {
		wanted = wanted[:topK]
	}
	var missing []string
	for _, id := range wanted {
		if _, ok := byID[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		names, nameErr := s.operator.GetSkillNamesByIDs(ctx, missing)
		if nameErr != nil {
			// The names are decoration; the memberships are not. Report what the index knew
			// rather than failing a listing the network is entitled to.
			s.logger.WithContext(ctx).Warnf("[FindSkills] name fallback failed for %d skills: %v",
				len(missing), nameErr)
		} else {
			for id, name := range names {
				byID[id] = interfaces.CapabilityHit{
					SearchCapabilityRef: interfaces.SearchCapabilityRef{
						CapabilityType: interfaces.CapabilityTypeSkill,
						CapabilityID:   id,
					},
					Name: name,
				}
			}
		}
	}

	entries := make([]*interfaces.SkillItem, 0, len(wanted))
	for _, id := range wanted {
		hit, ok := byID[id]
		if !ok {
			// Neither the index nor the registry knows this id: the Skill is gone from the
			// execution factory while the binding survives. Reporting it as a callable Skill
			// would send the caller after something that cannot run.
			s.logger.WithContext(ctx).Warnf("[FindSkills] bound skill %s is unknown to the execution factory", id)
			continue
		}
		entries = append(entries, &interfaces.SkillItem{
			SkillID:     id,
			Name:        hit.Name,
			Description: hit.Description,
		})
	}
	return entries, nil
}

func translateMessage(ctx context.Context, msgKey string) string {
	lang := common.GetLanguageFromCtx(ctx)
	langKey := strings.ReplaceAll(lang, "-", "_")
	tr := localize.NewI18nTranslator(langKey)
	return tr.Trans(msgKey)
}
