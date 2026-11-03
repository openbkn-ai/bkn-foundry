// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

var ErrBuildChanged = errors.New("document build changed while processing")

// Coordinator advances one durable intent. Only the process journal is saved;
// complete validated rows are replayed from the original known Agent task after
// a crash. External calls never run while holding a database transaction.
type Coordinator struct {
	Assessor interface {
		Guard(context.Context, *interfaces.KBBuild) error
		Evaluate(context.Context, *interfaces.KBBuild, int, *interfaces.Draft, *kb.FileInput, []kb.DatasetWrite) (*AcceptanceCounts, error)
	}
	Store  interfaces.KBBuildAccess
	Drafts interface {
		GetDraft(context.Context, string) (*interfaces.Draft, bool, error)
	}
	Runtime kb.BuildRuntime
	Records kb.DatasetFileRecords
	Now     func() time.Time
}

func (c *Coordinator) now() int64 {
	if c.Now != nil {
		return c.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// save is also a fresh live guard before every remote operation. A successful
// previous Claim is not reusable permission after cancellation or model edits.
func (c *Coordinator) save(ctx context.Context, r *interfaces.KBBuildRecord) error {
	old := r.Build.Revision
	r.Build.Revision++
	r.Build.UpdateTime = max(c.now(), r.Build.UpdateTime+1)
	ok, err := c.Store.Save(ctx, r, old, c.now())
	if err != nil {
		return err
	}
	if !ok {
		return ErrBuildChanged
	}
	return nil
}

func (c *Coordinator) model(ctx context.Context, r *interfaces.KBBuildRecord) (*interfaces.Draft, error) {
	draft, found, err := c.Drafts.GetDraft(ctx, r.Build.ProjectID)
	if err != nil {
		return nil, err
	}
	if !found || draft == nil || draft.UpdateTime != r.Build.DraftUpdateTime || draft.Items.Confirmation == nil || draft.Items.Confirmation.Commit != r.Build.DefinitionCommit {
		return nil, ErrBuildChanged
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != r.Build.ModelFingerprint {
		return nil, ErrBuildChanged
	}
	return draft, nil
}

// Step is deliberately bounded to one file and never restarts an uncertain
// submission. The worker reclaims expired leases after a process restart.
func (c *Coordinator) Step(ctx context.Context, project, id, lease string) error {
	if c.Store == nil || c.Runtime == nil || c.Drafts == nil || c.Records == nil {
		return kb.ErrUnavailable
	}
	now := c.now()
	r, err := c.Store.Claim(ctx, project, id, lease, now, now+240000)
	if err != nil || r == nil {
		return err
	}
	if r.Build.State == "cancel_pending" || r.Build.State == "finishing" {
		return c.cleanup(ctx, r)
	}
	draft, err := c.model(ctx, r)
	if err != nil {
		if errors.Is(err, ErrBuildChanged) {
			r.Build.State = "cancel_pending"
			return c.cleanup(ctx, r)
		}
		return err
	}
	if r.Build.Mode == "acceptance" {
		if c.Assessor == nil {
			r.Build.State = "cancel_pending"
			return c.cleanup(ctx, r)
		}
		if err = c.Assessor.Guard(ctx, r.Build); err != nil {
			if errors.Is(err, ErrAcceptanceRequired) || errors.Is(err, ErrBuildChanged) {
				r.Build.State = "cancel_pending"
				return c.cleanup(ctx, r)
			}
			return err
		}
	}
	if r.Build.State == "preparing" {
		if err = c.save(ctx, r); err != nil {
			return err
		}
		identity, err := c.Runtime.Delegate(ctx, r.Build, r.Identity)
		if err != nil {
			return c.accessFailure(ctx, r, err)
		}
		if identity.GrantID != r.Identity.GrantID || identity.PrincipalID == "" || identity.Revision == 0 || identity.Outcome != "" {
			return ErrInvalidBuild
		}
		r.Identity = identity
		if err = c.save(ctx, r); err != nil {
			return err
		}
		if err = c.Runtime.Hold(ctx, r.Build, r.Identity); err != nil {
			return c.accessFailure(ctx, r, err)
		}
		r.Build.State = "queued"
		return c.save(ctx, r)
	}
	for index := range r.Build.Files {
		file := &r.Build.Files[index]
		switch file.State {
		case "submitting":
			// This was persisted by a previous process before submitting. Its
			// acknowledgement was not saved; it cannot be inferred or replayed.
			next, err := InterruptFileSubmission(r.Build, index, c.now())
			if err != nil {
				return err
			}
			r.Build.Files, r.Build.State = next.Files, next.State
			return c.save(ctx, r)
		case "pending", "reading":
			if submissionUncertain(r.Build) {
				continue
			}
			if err = c.save(ctx, r); err != nil {
				return err
			}
			if err = c.Runtime.Check(ctx, r.Build, r.Identity, file.File); err != nil {
				return c.accessFailure(ctx, r, err)
			}
			input, err := c.Runtime.ReadFile(ctx, r.Build, r.Identity, index)
			if err != nil {
				if errors.Is(err, kb.ErrInputNotExtractable) || errors.Is(err, ErrInvalidBuild) {
					return c.fail(ctx, r, index, "source_requires_review")
				}
				return c.accessFailure(ctx, r, err)
			}
			digest, err := FileInputDigest(fileRef(file.File), input)
			if err != nil || (file.InputDigest != "" && file.InputDigest != digest) {
				return c.fail(ctx, r, index, "source_content_changed")
			}
			file.InputDigest, file.State = digest, "reading"
			if err = c.save(ctx, r); err != nil {
				return err
			}
			if !hasFileText(input) {
				return c.writeNative(ctx, r, index, draft, input)
			}
			// Record intent before handing any plaintext to the extractor.
			next, err := PrepareFileSubmission(r.Build, index, c.now())
			if err != nil {
				return err
			}
			r.Build.Files, r.Build.State = next.Files, next.State
			if err = c.save(ctx, r); err != nil {
				return err
			}
			if err = c.Runtime.Check(ctx, r.Build, r.Identity, r.Build.Files[index].File); err != nil {
				return c.accessFailure(ctx, r, err)
			}
			task, startErr := c.Runtime.Start(ctx, r.Build, r.Identity, index, draft, input)
			if startErr != nil {
				next, _ = InterruptFileSubmission(r.Build, index, c.now())
				r.Build.Files, r.Build.State = next.Files, next.State
				return c.save(ctx, r)
			}
			return c.attach(ctx, r, index, task)

		case "native_writing":
			if err = c.save(ctx, r); err != nil {
				return err
			}
			if err = c.Runtime.Check(ctx, r.Build, r.Identity, file.File); err != nil {
				return c.accessFailure(ctx, r, err)
			}
			input, err := c.Runtime.ReadFile(ctx, r.Build, r.Identity, index)
			if err != nil {
				return c.accessFailure(ctx, r, err)
			}
			return c.writeNative(ctx, r, index, draft, input)
		case "waiting", "validating", "writing":
			if err = c.save(ctx, r); err != nil {
				return err
			}
			if err = c.Runtime.Check(ctx, r.Build, r.Identity, file.File); err != nil {
				return c.accessFailure(ctx, r, err)
			}
			task, err := c.Runtime.Poll(ctx, r.Identity, file.AgentTaskID)
			if err != nil {
				return err
			}
			if file.State == "waiting" {
				return c.attach(ctx, r, index, task)
			}
			// The acknowledged execution may never be replaced, even by an
			// output with an identical shape from another task or Agent version.
			if task == nil || task.TaskID != file.AgentTaskID || task.AgentID != kb.DocumentExtractionAgentID || task.Status != interfaces.BKN_AGENT_TASK_SUCCEEDED || task.AgentVersion == nil || *task.AgentVersion != r.Build.AgentVersion || file.AgentVersion == nil || *task.AgentVersion != *file.AgentVersion || (file.PromptVersion != nil && (task.PromptVersion == nil || *task.PromptVersion != *file.PromptVersion)) || task.ModelID != file.ModelID {
				return c.fail(ctx, r, index, "execution_changed")
			}
			input, err := c.Runtime.ReadFile(ctx, r.Build, r.Identity, index)
			if err != nil {
				if errors.Is(err, kb.ErrInputNotExtractable) || errors.Is(err, ErrInvalidBuild) {
					return c.fail(ctx, r, index, "source_requires_review")
				}
				return c.accessFailure(ctx, r, err)
			}
			inputDigest, err := FileInputDigest(fileRef(file.File), input)
			if err != nil || inputDigest != file.InputDigest {
				return c.fail(ctx, r, index, "source_content_changed")
			}
			writes, err := c.Runtime.Materialize(ctx, r.Build, r.Identity, index, draft, input, task)
			if err != nil {
				if errors.Is(err, ErrSentenceIdentity) {
					return c.fail(ctx, r, index, "sentence_identity_required")
				}
				return c.fail(ctx, r, index, "invalid_extraction")
			}
			if r.Build.Mode == "acceptance" && file.Assessment == nil {
				if c.Assessor == nil {
					return c.fail(ctx, r, index, "acceptance_requires_review")
				}
				assessment, err := c.Assessor.Evaluate(ctx, r.Build, index, draft, input, writes)
				if err != nil {
					return c.fail(ctx, r, index, "acceptance_requires_review")
				}
				file.Assessment = assessment
			}
			digest, err := validateWrites(r.Build, draft, writes)
			if err != nil || (file.RowsDigest != "" && file.RowsDigest != digest) || file.RowsWritten < 0 || file.RowsWritten > int64(len(writes)) {
				return c.fail(ctx, r, index, "write_set_changed")
			}
			file.RowsDigest, file.State = digest, "writing"
			if err = c.save(ctx, r); err != nil {
				return err
			}
			for file.RowsWritten < int64(len(writes)) {
				if err = c.save(ctx, r); err != nil {
					return err
				}
				if err = c.Runtime.Check(ctx, r.Build, r.Identity, file.File); err != nil {
					return c.accessFailure(ctx, r, err)
				}
				write := writes[file.RowsWritten]
				if err = c.Records.Replace(ctx, write.Spec, write.Row); err != nil {
					file.Reason = "storage_unavailable"
					if saveErr := c.save(ctx, r); saveErr != nil {
						return saveErr
					}
					return err
				}
				file.RowsWritten++
				if err = c.save(ctx, r); err != nil {
					return err
				}
			}
			if err = c.prune(ctx, r, index, draft, writes, false); err != nil {
				return err
			}
			file.Reason = ""
			file.State = "succeeded"
			return c.save(ctx, r)
		}
	}
	// Failed/uncertain files remain visible and are never resubmitted. Other
	// acknowledged files finish first, then grant cleanup becomes mandatory.
	outcome := "succeeded"
	for _, file := range r.Build.Files {
		if file.State != "succeeded" {
			outcome = "attention"
		}
	}
	if r.Build.Mode == "acceptance" {
		total := &AcceptanceCounts{Issues: map[string]int{}}
		complete := true
		for _, file := range r.Build.Files {
			if file.State != "succeeded" || file.Assessment == nil {
				complete = false
				break
			}
			score := file.Assessment
			total.Expected += score.Expected
			total.Predicted += score.Predicted
			total.Correct += score.Correct
			total.ExpectedLinks += score.ExpectedLinks
			total.PredictedLinks += score.PredictedLinks
			total.CorrectLinks += score.CorrectLinks
			for issue, count := range score.Issues {
				total.Issues[issue] += count
			}
		}
		if complete {
			calculateAcceptance(total)
			r.Build.Assessment = total
		}
	}
	r.Identity.Outcome, r.Build.State = outcome, "finishing"
	return c.save(ctx, r)
}

// Stop only revokes a journaled delegation and releases its holds. It cannot
// read source contents, invoke the extractor, or write Dataset rows.
func (c *Coordinator) Stop(ctx context.Context, project, id, lease string) error {
	if c.Store == nil || c.Runtime == nil {
		return kb.ErrUnavailable
	}
	now := c.now()
	r, err := c.Store.Claim(ctx, project, id, lease, now, now+240000)
	if err != nil || r == nil {
		return err
	}
	if r.Build.State != "finishing" {
		r.Build.State = "cancel_pending"
	}
	return c.cleanup(ctx, r)
}

func (c *Coordinator) prune(ctx context.Context, r *interfaces.KBBuildRecord, index int, draft *interfaces.Draft, writes []kb.DatasetWrite, nativeOnly bool) error {
	file := r.Build.Files[index].File
	document, err := DocumentID(file.KnowledgeBaseID, file.FileID)
	if err != nil {
		return err
	}
	scope := kb.DatasetFileScope{KnowledgeBaseID: file.KnowledgeBaseID, FileID: file.FileID, ContentVersionID: file.ContentVersionID, DocumentID: document, ModelFingerprint: r.Build.ModelFingerprint}
	keep := map[string][]string{}
	for _, write := range writes {
		keep[write.Spec.ResourceID] = append(keep[write.Spec.ResourceID], write.Row["_id"].(string))
	}
	guard := func(ctx context.Context) error {
		if err := c.save(ctx, r); err != nil {
			return err
		}
		return c.Runtime.Check(ctx, r.Build, r.Identity, file)
	}
	for _, plan := range draft.Items.DatasetPlans {
		if plan == nil || plan.ID != r.Build.GenerationID {
			continue
		}
		for _, spec := range plan.Datasets {

			if nativeOnly {
				if spec.ObjectTypeID != "" {
					object := instanceObject(draft.Items, spec.ObjectTypeID)
					if object == nil || !strings.HasPrefix(object.Document.Role, "evidence_") {
						continue
					}
				} else {
					allowed := false
					for _, relation := range draft.Items.RelationTypes {
						if relation != nil && relation.ID == spec.RelationTypeID && relation.Document != nil && relation.Document.Role == "evidence_occurrence" {
							allowed = true
						}
					}
					if !allowed {
						continue
					}
				}
			}
			if spec.ObjectTypeID != "" {
				if object := instanceObject(draft.Items, spec.ObjectTypeID); object != nil && object.Document.Role == "entity" {
					continue
				}
			}
			if err = guard(ctx); err != nil {
				return c.accessFailure(ctx, r, err)
			}
			if err = c.Records.Prune(ctx, spec, scope, keep[spec.ResourceID], guard); err != nil {
				return c.accessFailure(ctx, r, err)
			}
		}
	}
	return nil
}

func fileRef(file interfaces.KBResourceFile) kb.SampleVersion {
	return kb.SampleVersion{KnowledgeBaseID: file.KnowledgeBaseID, VersionRef: kb.VersionRef{FileID: file.FileID, ContentVersionID: file.ContentVersionID}}
}

func (c *Coordinator) accessFailure(ctx context.Context, r *interfaces.KBBuildRecord, err error) error {
	var source *kb.SourceError
	if errors.Is(err, kb.ErrVersionGone) || (errors.As(err, &source) && (source.StatusCode == 401 || source.StatusCode == 403 || source.StatusCode == 409 || source.StatusCode == 410)) {
		r.Build.State = "cancel_pending"
		if saveErr := c.save(ctx, r); saveErr != nil {
			return saveErr
		}
		return ErrBuildChanged
	}
	return err
}

func (c *Coordinator) attach(ctx context.Context, r *interfaces.KBBuildRecord, index int, task *interfaces.BknAgentTask) error {
	next, err := AttachFileTask(r.Build, index, kb.DocumentExtractionAgentID, task, c.now())
	if err != nil {
		return c.fail(ctx, r, index, "execution_changed")
	}
	r.Build.Files, r.Build.State = next.Files, next.State
	r.Build.PromptVersion, r.Build.ModelID, r.Build.Model = next.PromptVersion, next.ModelID, next.Model
	if task.Status == interfaces.BKN_AGENT_TASK_SUCCEEDED && (task.PromptVersion == nil || task.ModelID == "") {
		return c.fail(ctx, r, index, "execution_version_unknown")
	}
	return c.save(ctx, r)
}
func (c *Coordinator) fail(ctx context.Context, r *interfaces.KBBuildRecord, index int, reason string) error {
	r.Build.Files[index].State, r.Build.Files[index].Reason = "failed", reason
	r.Build.State = "attention"
	return c.save(ctx, r)
}
func (c *Coordinator) cleanup(ctx context.Context, r *interfaces.KBBuildRecord) error {
	if err := c.save(ctx, r); err != nil {
		return err
	}
	if r.Identity.CleanupPhase == "" {
		if err := c.Runtime.Revoke(ctx, r.Build, r.Identity); err != nil {
			return err
		}
		r.Identity.CleanupPhase = "revoked"
		if err := c.save(ctx, r); err != nil {
			return err
		}
	}
	if r.Identity.CleanupPhase == "revoked" {
		if err := c.Runtime.ReleaseHolds(ctx, r.Build, r.Identity); err != nil {
			return err
		}
		r.Identity.CleanupPhase = "holds_released"
		if err := c.save(ctx, r); err != nil {
			return err
		}
	}
	if r.Identity.CleanupPhase != "holds_released" {
		return ErrInvalidBuild
	}
	if err := c.Runtime.Complete(ctx, r.Build, r.Identity); err != nil {
		return err
	}
	r.Identity.Cleaned = true
	if r.Build.State == "cancel_pending" {
		r.Build.State = "cancelled"
	} else {
		if r.Identity.Outcome != "succeeded" && r.Identity.Outcome != "attention" {
			return ErrInvalidBuild
		}
		r.Build.State = r.Identity.Outcome
		if r.Build.State == "attention" {
			r.Build.State = "needs_review"
		}
	}
	return c.save(ctx, r)
}

func validateWrites(build *interfaces.KBBuild, draft *interfaces.Draft, writes []kb.DatasetWrite) (string, error) {
	if len(writes) > 100000 {
		return "", ErrInvalidBuild
	}
	var plan *interfaces.DraftDatasetPlan
	for _, p := range draft.Items.DatasetPlans {
		if p != nil && p.ID == build.GenerationID {
			plan = p
		}
	}
	if plan == nil || plan.State != "ready" || plan.ModelFingerprint != build.ModelFingerprint {
		return "", ErrInvalidBuild
	}
	allowed := map[string]interfaces.DraftDatasetSpec{}
	for _, spec := range plan.Datasets {
		allowed[spec.ResourceID] = spec
	}
	seen := map[string]bool{}
	for _, write := range writes {
		spec, found := allowed[write.Spec.ResourceID]
		a, _ := json.Marshal(spec)
		b, _ := json.Marshal(write.Spec)
		id, ok := write.Row["_id"].(string)
		key := write.Spec.ResourceID + "/" + id
		if !found || string(a) != string(b) || !ok || id == "" || seen[key] || !write.Spec.Ready || len(write.Row) != len(write.Spec.Layout.Fields) || write.Row["model_fingerprint"] != build.ModelFingerprint {
			return "", ErrInvalidBuild
		}
		seen[key] = true
		for _, field := range write.Spec.Layout.Fields {
			if _, exists := write.Row[field.OriginalName]; !exists {
				return "", ErrInvalidBuild
			}
		}
	}
	slices.SortFunc(writes, func(a, b kb.DatasetWrite) int {
		left, right := a.Spec.ResourceID+"/"+fmt.Sprint(a.Row["_id"]), b.Spec.ResourceID+"/"+fmt.Sprint(b.Row["_id"])
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	})
	raw, err := json.Marshal(writes)
	if err != nil || len(raw) > 32<<20 {
		return "", ErrInvalidBuild
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func hasFileText(input *kb.FileInput) bool {
	for _, text := range input.Text {
		if strings.TrimSpace(text) != "" {
			return true
		}
	}
	return false
}
func submissionUncertain(build *interfaces.KBBuild) bool {
	for _, file := range build.Files {
		if file.State == "submission_unknown" || file.State == "submitting" || file.Reason == "execution_changed" || file.Reason == "execution_version_unknown" {
			return true
		}
	}
	return false
}
func (c *Coordinator) writeNative(ctx context.Context, r *interfaces.KBBuildRecord, index int, draft *interfaces.Draft, input *kb.FileInput) error {
	writes, err := MaterializeNativeFile(r.Build, index, draft, input)
	if err != nil {
		return c.fail(ctx, r, index, "source_requires_review")
	}
	file := &r.Build.Files[index]
	digest, err := validateWrites(r.Build, draft, writes)
	if err != nil || (file.RowsDigest != "" && file.RowsDigest != digest) || file.RowsWritten < 0 || file.RowsWritten > int64(len(writes)) {
		return c.fail(ctx, r, index, "write_set_changed")
	}
	file.RowsDigest, file.State = digest, "native_writing"
	if err = c.save(ctx, r); err != nil {
		return err
	}
	for file.RowsWritten < int64(len(writes)) {
		if err = c.save(ctx, r); err != nil {
			return err
		}
		if err = c.Runtime.Check(ctx, r.Build, r.Identity, file.File); err != nil {
			return c.accessFailure(ctx, r, err)
		}
		write := writes[file.RowsWritten]
		if err = c.Records.Replace(ctx, write.Spec, write.Row); err != nil {
			file.Reason = "storage_unavailable"
			if saveErr := c.save(ctx, r); saveErr != nil {
				return saveErr
			}
			return err
		}
		file.RowsWritten++
		if err = c.save(ctx, r); err != nil {
			return err
		}
	}
	if err = c.prune(ctx, r, index, draft, writes, true); err != nil {
		return err
	}
	file.State, file.Reason = "evidence_only", "no_supported_text"
	r.Build.State = "attention"
	return c.save(ctx, r)
}

// ValidateWriteSet applies the construction write-set checks to a staged
// published refresh before any live Dataset mutation. The returned digest
// fences recovery to exactly the same complete, validated materialization.
func ValidateWriteSet(build *interfaces.KBBuild, draft *interfaces.Draft, writes []kb.DatasetWrite) (string, error) {
	if build == nil || draft == nil {
		return "", ErrInvalidBuild
	}
	return validateWrites(build, draft, writes)
}
