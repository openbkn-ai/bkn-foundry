// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

const AnswerArtifactRequested = "artifact.persist_requested"
const MaxAnswerArtifactBytes = 16 << 20

type answerArtifactRequest struct {
	Owner    sessionvo.Owner             `json:"owner"`
	Artifact evidencevo.EvidenceArtifact `json:"artifact"`
}

func (s *Service) DurableAnswerEnabled() bool { return s.durableAnswerEnabled }
func answerArtifactEventID(interactionID, key string) string {
	sum := sha256.Sum256([]byte(interactionID + "|" + key))
	return "answer_" + hex.EncodeToString(sum[:])
}
func (s *Service) prepareAnswerArtifact(command TerminateInteractionCommand) (*answerArtifactRequest, error) {
	invalid := func() (*answerArtifactRequest, error) {
		return nil, domainError(CodeClosureManifestInvalid, "invalid durable answer artifact or owner")
	}
	if !s.durableAnswerEnabled || !command.DeriveManifest || command.Status != sessionvo.InteractionCompleted || len(command.AnswerArtifact) > MaxAnswerArtifactBytes {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(command.AnswerArtifact))
	decoder.UseNumber()
	var artifact evidencevo.EvidenceArtifact
	if decoder.Decode(&artifact) != nil {
		return invalid()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return invalid()
	}
	if artifact.ArtifactType != evidencevo.ArtifactTypeResult || artifact.InteractionID != command.InteractionID || artifact.OperationID != "" || artifact.ClaimID != "" || artifact.Content == nil || artifact.SnapshotRef != "" {
		return invalid()
	}
	accountType := "user"
	if command.Owner.EffectiveSubjectType == sessionvo.SubjectService {
		accountType = "app"
	}
	if artifact.AccountID != command.Owner.EffectiveSubjectID || artifact.AccountType != accountType || artifact.ApplicationPrincipalID != command.Owner.ApplicationPrincipalID || artifact.EffectiveSubjectID != command.Owner.EffectiveSubjectID {
		return invalid()
	}
	normalized, validation := evidencevo.NormalizeArtifact(artifact)
	if len(validation) > 0 {
		return invalid()
	}
	if command.Manifest.AnswerArtifactRef != "" && command.Manifest.AnswerArtifactRef != "artifact:"+normalized.ArtifactID {
		return invalid()
	}
	raw, err := json.Marshal(normalized)
	if err != nil || len(raw) > MaxAnswerArtifactBytes {
		return invalid()
	}
	return &answerArtifactRequest{Owner: command.Owner, Artifact: normalized}, nil
}

type AnswerArtifactHandler struct {
	service   *Service
	artifacts iartifactstore.Writer
}

func NewAnswerArtifactHandler(service *Service, artifacts iartifactstore.Writer) *AnswerArtifactHandler {
	return &AnswerArtifactHandler{service: service, artifacts: artifacts}
}
func decodeAnswerArtifactTask(item iprojectionoutbox.Item) (answerArtifactRequest, error) {
	var request answerArtifactRequest
	if item.EventType != AnswerArtifactRequested || len(item.Payload) > iprojectionoutbox.MaxPayloadBytes {
		return request, iprojectionoutbox.Permanent(errors.New("invalid answer task budget or type"))
	}
	decoder := json.NewDecoder(bytes.NewReader(item.Payload))
	decoder.UseNumber()
	if decoder.Decode(&request) != nil || request.Artifact.InteractionID != item.AggregateID {
		return request, iprojectionoutbox.Permanent(errors.New("invalid answer task"))
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return request, iprojectionoutbox.Permanent(errors.New("invalid answer task envelope"))
	}
	return request, nil
}
func (h *AnswerArtifactHandler) HandleAnswerArtifact(ctx context.Context, item iprojectionoutbox.Item) error {
	request, err := decodeAnswerArtifactTask(item)
	if err != nil {
		return err
	}
	// The immutable artifact fingerprint makes response loss and lease reclamation
	// safe. Delivery of already accepted work does not re-evaluate new-admission policy.
	if h.artifacts == nil {
		return errors.New("answer artifact store is unavailable")
	}
	_, err = h.artifacts.StoreArtifact(ctx, request.Artifact)
	if err != nil {
		if errors.Is(err, iartifactstore.ErrArtifactIDConflict) || errors.Is(err, iartifactstore.ErrInvalidAcceptedArtifact) {
			return iprojectionoutbox.Permanent(errors.New("answer artifact identity conflict"))
		}
		return err
	}
	return h.service.finishAnswerArtifact(ctx, item, request, sessionvo.AnswerArtifactSettled)
}
func (h *AnswerArtifactHandler) MarkAnswerArtifactDead(ctx context.Context, item iprojectionoutbox.Item) error {
	request, err := decodeAnswerArtifactTask(item)
	if err != nil {
		request = answerArtifactRequest{}
	}
	return h.service.finishAnswerArtifact(ctx, item, request, sessionvo.AnswerArtifactDead)
}
func (s *Service) finishAnswerArtifact(ctx context.Context, item iprojectionoutbox.Item, request answerArtifactRequest, state string) error {
	return s.store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		ref, found := tx.PeekInteraction(item.AggregateID)
		if !found {
			return iprojectionoutbox.Permanent(errors.New("answer interaction is missing"))
		}
		owner := request.Owner
		if owner.ApplicationPrincipalID == "" {
			conversation, ok := tx.PeekConversation(ref.ConversationID)
			if !ok {
				return errors.New("answer conversation is missing")
			}
			owner = conversation.Owner
		}
		if _, err := ownedConversation(tx, owner, ref.ConversationID); err != nil {
			return iprojectionoutbox.Permanent(err)
		}
		current, found := tx.FindInteraction(item.AggregateID)
		if !found || current.ClosureManifest == nil {
			return iprojectionoutbox.Permanent(errors.New("answer interaction is missing"))
		}
		manifest := *current.ClosureManifest
		persistence := manifest.AnswerArtifactPersistence
		if persistence == nil || persistence.EventID != item.EventID || (request.Artifact.ArtifactID != "" && (persistence.ContentHash != request.Artifact.ContentHash || manifest.AnswerArtifactRef != "artifact:"+request.Artifact.ArtifactID)) || current.ExecutionStatus != sessionvo.InteractionCompleted {
			return iprojectionoutbox.Permanent(errors.New("answer task does not match committed finish"))
		}
		if guard, ok := tx.(isessionstore.ProjectionLeaseGuard); ok {
			if err := guard.CheckProjectionLease(item.ID, item.LeaseToken); err != nil {
				return err
			}
		} else if item.LeaseToken != "" {
			return iprojectionoutbox.ErrLeaseLost
		}
		if persistence.State == sessionvo.AnswerArtifactSettled || persistence.State == state {
			return nil
		}
		updated := *persistence
		updated.State = state
		manifest.AnswerArtifactPersistence = &updated
		current.ClosureManifest = &manifest
		current.EvidenceStatus = evidenceStatusAtTermination(tx, manifest)
		current.RowVersion++
		current.UpdatedAt = tx.Now()
		tx.SaveInteraction(current)
		if current.EvidenceStatus != sessionvo.EvidenceAssembling {
			trigger := "answer_artifact_persisted"
			if state == sessionvo.AnswerArtifactDead {
				trigger = "answer_artifact_dead"
			}
			if err := s.freezeAssemblyRevision(tx, current, manifest, trigger); err != nil {
				return err
			}
		}
		return s.appendProjection(tx, "interaction", current.ID, "interaction.answer_artifact."+state, current)
	})
}
