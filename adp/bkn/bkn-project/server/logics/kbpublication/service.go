// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
)

type Target struct {
	ProjectID, LineageID, Branch, BranchCommit, NetworkID string
	DeclaredProjectID                                     string
	Publisher                                             interfaces.AccountInfo
}

// Plan is request-local execution input. It must never be returned by an HTTP
// handler or stored in a process row; only Bundle.Runtime goes to private Vega.
type Plan struct {
	Draft     *interfaces.Draft
	Selection *interfaces.KBSelection
	Full      *interfaces.KBBuildRecord
	Bundle    *Bundle
}
type Service struct {
	Git        interfaces.GitStore
	Projects   interfaces.ProjectAccess
	Drafts     interfaces.DraftAccess
	Selections interfaces.KBResourceAccess
	Builds     interfaces.KBBuildQueue
	References interfaces.KBReferenceAccess
	Store      interfaces.KBPublicationAccess
	Vega       interfaces.VegaAccess
	Source     kb.Source
	Storage    kb.PublicationDatasets
	Network    kb.NetworkPublication
}

func NewService(git interfaces.GitStore) *Service {
	s := &Service{Git: git, Projects: logics.PA, Drafts: logics.DA, Selections: logics.KBRA, Builds: logics.KBBA, References: logics.KBRefs, Store: logics.KBPublications, Vega: logics.VA}
	if providers, ok := kb.Lookup(); ok {
		s.Source = providers.Source
		s.Storage, _ = providers.Datasets.(kb.PublicationDatasets)
		s.Network = providers.Publication
	}
	return s
}
func hasDocuments(draft *interfaces.Draft) bool {
	if draft == nil {
		return false
	}
	for _, object := range draft.Items.ObjectTypes {
		if object != nil && object.Adopted && object.Document != nil {
			return true
		}
	}
	return false
}
func (s *Service) scope(ctx context.Context, token string, target Target, draft *interfaces.Draft, selection *interfaces.KBSelection) error {
	project, found, err := s.Projects.GetProject(ctx, target.ProjectID)
	if err != nil {
		return err
	}
	if !found || project == nil || project.Archived || project.Owner != target.Publisher || project.KNID != target.LineageID || project.Branch != target.Branch {
		return ErrInvalidSnapshot
	}
	current, found, err := s.Drafts.GetDraft(ctx, target.ProjectID)
	if err != nil {
		return err
	}
	if !found || current == nil || current.Status != interfaces.DRAFT_STATUS_READY || current.UpdateTime != draft.UpdateTime {
		return ErrInvalidSnapshot
	}
	expected, err := kbdataset.ModelFingerprint(draft)
	if err != nil {
		return err
	}
	actual, err := kbdataset.ModelFingerprint(current)
	if err != nil || actual != expected {
		return ErrInvalidSnapshot
	}
	state, err := s.Selections.Read(ctx, target.ProjectID)
	if err != nil {
		return err
	}
	if state == nil || state.Current == nil || state.Current.State != "active" || state.Current.ID != selection.ID || state.Current.Revision != selection.Revision || state.Current.Owner != target.Publisher {
		return ErrInvalidSnapshot
	}
	if s.Source == nil {
		return kb.ErrUnavailable
	}
	seen := map[string]bool{}
	for _, source := range state.Current.Ranges {
		if seen[source.KnowledgeBaseID] {
			continue
		}
		seen[source.KnowledgeBaseID] = true
		if _, err := s.Source.SummarizeFiles(kb.WithOperatorAuthorization(ctx, token), kb.SourceScope{KnowledgeBaseID: source.KnowledgeBaseID}, kb.FileQuery{}); err != nil {
			return providerError(err)
		}
	}
	if len(seen) == 0 {
		return ErrInvalidSnapshot
	}
	return nil
}

// Inspect is read-only. It uses the publishing user's current query permission,
// current model/source revisions, acknowledged full build and independent gold
// revision. It never starts inference, changes a Dataset or mounts a network.
func (s *Service) Inspect(ctx context.Context, token string, target Target) (*Plan, error) {
	modelGit, available := s.Git.(interfaces.ModelGitStore)
	var storedRules []byte
	var err error
	if available {
		storedRules, err = modelGit.ReadModelRules(ctx, target.LineageID, target.BranchCommit)
		if err != nil {
			return nil, err
		}
	}
	var draft *interfaces.Draft
	if s.Drafts != nil {
		draft, _, err = s.Drafts.GetDraft(ctx, target.ProjectID)
		if err != nil {
			return nil, err
		}
	}
	if len(storedRules) == 0 && !hasDocuments(draft) {
		return nil, nil
	}
	if target.DeclaredProjectID != "" && target.DeclaredProjectID != target.ProjectID {
		return nil, ErrInvalidSnapshot
	}
	if !available || len(storedRules) == 0 || !hasDocuments(draft) || s.Projects == nil || s.Selections == nil {
		return nil, ErrInvalidSnapshot
	}
	if target.Publisher.ID == "" || target.Publisher.Type != "user" || draft.ProjectID != target.ProjectID || draft.Status != interfaces.DRAFT_STATUS_READY || draft.Items.Confirmation == nil || draft.Items.Confirmation.UpdateTime != draft.UpdateTime {
		return nil, ErrInvalidSnapshot
	}
	state, err := s.Selections.Read(ctx, target.ProjectID)
	if err != nil {
		return nil, err
	}
	if state == nil || state.Current == nil || state.Current.State != "active" || state.Current.Owner != target.Publisher {
		return nil, ErrInvalidSnapshot
	}
	selection := state.Current
	guard := func(check context.Context) error { return s.scope(check, token, target, draft, selection) }
	if err := guard(ctx); err != nil {
		return nil, err
	}
	if s.Builds == nil || s.References == nil || s.Storage == nil || s.Vega == nil || s.Network == nil {
		return nil, kb.ErrUnavailable
	}
	rules, err := Definition(draft.Items)
	if err != nil || !bytes.Equal(rules, storedRules) {
		return nil, ErrInvalidSnapshot
	}
	confirmed, err := s.Git.ReadTree(ctx, target.LineageID, draft.Items.Confirmation.Commit)
	if err != nil {
		return nil, err
	}
	branch, err := s.Git.ReadTree(ctx, target.LineageID, target.BranchCommit)
	if err != nil {
		return nil, err
	}
	for _, path := range draft.Items.Confirmation.Files {
		if len(confirmed[path]) == 0 || !bytes.Equal(confirmed[path], branch[path]) {
			return nil, ErrInvalidSnapshot
		}
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil {
		return nil, err
	}
	builds, err := s.Builds.List(ctx, target.ProjectID, 100)
	if err != nil {
		return nil, err
	}
	var full *interfaces.KBBuildRecord
	for _, candidate := range builds {
		if candidate == nil || candidate.NetworkID != target.LineageID || candidate.Mode != "full" || candidate.State != "succeeded" || candidate.Owner != target.Publisher || candidate.ModelFingerprint != fingerprint || candidate.SelectionID != selection.ID || candidate.SelectionRevision != selection.Revision || candidate.DraftUpdateTime != draft.UpdateTime || candidate.DefinitionCommit != draft.Items.Confirmation.Commit {
			continue
		}
		full, err = s.Builds.Get(ctx, target.ProjectID, candidate.ID)
		if err != nil {
			return nil, err
		}
		break
	}
	if full == nil || full.Build == nil {
		return nil, ErrInvalidSnapshot
	}
	files := map[string]interfaces.KBResourceFile{}
	for _, file := range selection.Files {
		files[file.Key()] = file
	}
	if len(files) != len(selection.Files) || len(full.Build.Files)+len(full.Build.Skipped) != len(files) {
		return nil, ErrInvalidSnapshot
	}
	for _, step := range full.Build.Files {
		if !step.Completed() || files[step.File.Key()] != step.File {
			return nil, ErrInvalidSnapshot
		}
		delete(files, step.File.Key())
	}
	// A file left out without a person's recorded decision cannot be released.
	if err := verifyFileAcceptance(full.Build); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(full.Build)
	if err != nil {
		return nil, err
	}
	var measured interfaces.KBBuild
	if json.Unmarshal(raw, &measured) != nil {
		return nil, ErrInvalidSnapshot
	}
	reference := &kbinstance.ReferenceService{Store: s.References, Builds: &kbinstance.BuildService{Store: s.Builds}}
	if err := reference.FullReady(ctx, &measured); err != nil {
		return nil, err
	}
	if measured.ReferenceID != full.Build.ReferenceID || measured.ReferenceRevision != full.Build.ReferenceRevision || measured.AgentVersion != full.Build.AgentVersion || !reflect.DeepEqual(measured.PromptVersion, full.Build.PromptVersion) || measured.ModelID != full.Build.ModelID || measured.Model != full.Build.Model {
		return nil, ErrInvalidSnapshot
	}
	datasets, ok := s.Storage.(interface {
		kb.DatasetFileSnapshot
		kb.DatasetFileRecords
	})
	if !ok {
		return nil, kb.ErrUnavailable
	}
	verifier := &kbinstance.SystemLinkService{Datasets: datasets, Vega: s.Vega}
	if _, err := verifier.VerifyCompleted(ctx, full, draft, guard); err != nil {
		return nil, providerError(err)
	}
	var generation *interfaces.DraftDatasetPlan
	for _, plan := range draft.Items.DatasetPlans {
		if plan != nil && plan.ID == full.Build.GenerationID {
			generation = plan
			break
		}
	}
	if err := kbdataset.VerifyBindings(draft, generation); err != nil {
		return nil, ErrInvalidSnapshot
	}
	bundle, err := Freeze(draft, generation)
	if err != nil {
		return nil, err
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return &Plan{Draft: draft, Selection: selection, Full: full, Bundle: bundle}, nil
}

// verifyFileAcceptance ties every file left out to the recorded decision.
func verifyFileAcceptance(build *interfaces.KBBuild) error {
	listed := map[string]bool{}
	if build.Acceptance != nil {
		for _, file := range build.Acceptance.Files {
			listed[file.File.Key()] = true
		}
	}
	count := len(build.Skipped)
	for _, step := range build.Skipped {
		if !listed[step.File.Key()] {
			return ErrInvalidSnapshot
		}
	}
	for i := range build.Files {
		if build.Files[i].State == interfaces.KBFileExcluded {
			count++
			if !listed[build.Files[i].File.Key()] {
				return ErrInvalidSnapshot
			}
		}
	}
	if count != len(listed) {
		return ErrInvalidSnapshot
	}
	return nil
}

// releasedFiles are the versions a release must keep: not the ones left out.
func releasedFiles(build *interfaces.KBBuild) []interfaces.KBResourceFile {
	files := []interfaces.KBResourceFile{}
	for i := range build.Files {
		if build.Files[i].State != interfaces.KBFileExcluded {
			files = append(files, build.Files[i].File)
		}
	}
	return files
}

func publicationHolds(files []interfaces.KBResourceFile) []interfaces.KBHoldGroup {
	versions := map[string][]string{}
	for _, file := range files {
		versions[file.KnowledgeBaseID] = append(versions[file.KnowledgeBaseID], file.ContentVersionID)
	}
	groups := []interfaces.KBHoldGroup{}
	for id, list := range versions {
		slices.Sort(list)
		groups = append(groups, interfaces.KBHoldGroup{ID: uuid.NewString(), KnowledgeBaseID: id, VersionIDs: slices.Compact(list)})
	}
	slices.SortFunc(groups, func(a, b interfaces.KBHoldGroup) int {
		if a.KnowledgeBaseID < b.KnowledgeBaseID {
			return -1
		}
		if a.KnowledgeBaseID > b.KnowledgeBaseID {
			return 1
		}
		return 0
	})
	return groups
}

// Prepare records intent before independent publication holds and the private
// model snapshot. Only an acknowledged, verified snapshot can seal a generation.
func (s *Service) Prepare(ctx context.Context, token string, target Target) (*interfaces.KBPublication, error) {
	plan, err := s.Inspect(ctx, token, target)
	if err != nil || plan == nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, kb.ErrUnavailable
	}
	guard := func(check context.Context) error { return s.scope(check, token, target, plan.Draft, plan.Selection) }
	owner, err := s.Storage.PublicationOwner(ctx)
	if err != nil {
		return nil, providerError(err)
	}
	catalog, err := s.Storage.Catalog(ctx, target.ProjectID)
	if err != nil {
		return nil, providerError(err)
	}
	spec, err := snapshotSpec(target.ProjectID, target.NetworkID, catalog, owner)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	record := &interfaces.KBPublication{NetworkID: target.NetworkID, ProjectID: target.ProjectID, LineageID: target.LineageID, DefinitionCommit: plan.Full.Build.DefinitionCommit, ModelFingerprint: plan.Bundle.ModelFingerprint, GenerationID: plan.Full.Build.GenerationID, FullBuildID: plan.Full.Build.ID, Publisher: target.Publisher, Snapshot: spec, RuntimeRowID: plan.Bundle.RuntimeDigest, RuntimeDigest: plan.Bundle.RuntimeDigest, DefinitionDigest: digest(plan.Bundle.Definition), Ranges: plan.Selection.Ranges, Holds: publicationHolds(releasedFiles(plan.Full.Build)), Acceptance: plan.Full.Build.Acceptance, State: "preparing", Revision: 1, DraftUpdateTime: plan.Draft.UpdateTime, CreateTime: now, UpdateTime: now}
	existing, err := s.Store.Get(ctx, target.NetworkID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.ProjectID != record.ProjectID || existing.Publisher != record.Publisher || existing.RuntimeDigest != record.RuntimeDigest || existing.GenerationID != record.GenerationID || existing.DefinitionDigest != record.DefinitionDigest || !reflect.DeepEqual(existing.Ranges, record.Ranges) {
			return nil, ErrInvalidSnapshot
		}
		record = existing
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	if existing == nil {
		ok, err := s.Store.Prepare(ctx, record)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrInvalidSnapshot
		}
		saved, err := s.Store.Get(ctx, target.NetworkID)
		if err != nil {
			return nil, err
		}
		if saved == nil || saved.RuntimeDigest != record.RuntimeDigest || saved.Publisher != record.Publisher {
			return nil, ErrInvalidSnapshot
		}
		record = saved
	}
	for _, hold := range record.Holds {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		versions := []kb.VersionRef{}
		for _, version := range hold.VersionIDs {
			for _, file := range plan.Selection.Files {
				if file.KnowledgeBaseID == hold.KnowledgeBaseID && file.ContentVersionID == version {
					versions = append(versions, kb.VersionRef{FileID: file.FileID, ContentVersionID: version})
					break
				}
			}
		}
		if len(versions) != len(hold.VersionIDs) {
			return nil, ErrInvalidSnapshot
		}
		if err := s.Source.Hold(kb.WithOperatorAuthorization(ctx, token), kb.SourceScope{KnowledgeBaseID: hold.KnowledgeBaseID}, kb.HoldRequest{ID: hold.ID, Versions: versions}); err != nil {
			return nil, providerError(err)
		}
	}
	if err := persistSnapshot(ctx, s.Storage, record, plan.Bundle, guard); err != nil {
		return nil, providerError(err)
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	ok, err := s.Store.Seal(ctx, record.NetworkID, record.RuntimeDigest, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidSnapshot
	}
	return s.Store.Get(ctx, record.NetworkID)
}

// Mount follows the successful network import and must be acknowledged before
// the ordinary release is recorded. Re-import retries always recheck bindings.
func (s *Service) Mount(ctx context.Context, token string, record *interfaces.KBPublication) error {
	if record == nil {
		return nil
	}
	if s.Network == nil || s.Source == nil || s.Store == nil || s.Storage == nil {
		return kb.ErrUnavailable
	}
	if record.State != "sealed" && record.State != "mounted" && record.State != "active" {
		return ErrInvalidSnapshot
	}
	if s.Projects == nil {
		return kb.ErrUnavailable
	}
	project, found, err := s.Projects.GetProject(ctx, record.ProjectID)
	if err != nil {
		return err
	}
	if !found || project == nil || project.Archived || project.Owner != record.Publisher {
		return ErrInvalidSnapshot
	}
	ids := []string{}
	for _, scope := range record.Ranges {
		ids = append(ids, scope.KnowledgeBaseID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if _, err := s.Source.SummarizeFiles(kb.WithOperatorAuthorization(ctx, token), kb.SourceScope{KnowledgeBaseID: id}, kb.FileQuery{}); err != nil {
			return providerError(err)
		}
	}
	if _, err := readSnapshot(ctx, s.Storage, record); err != nil {
		return providerError(err)
	}
	if err := s.Network.MountKnowledgeBases(ctx, token, record.NetworkID, ids); err != nil {
		return providerError(err)
	}
	ok, err := s.Store.MarkMounted(ctx, record.NetworkID, record.RuntimeDigest, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidSnapshot
	}
	return nil
}
func (s *Service) Activate(ctx context.Context, record *interfaces.KBPublication) error {
	if record == nil {
		return nil
	}
	if s.Store == nil {
		return kb.ErrUnavailable
	}
	ok, err := s.Store.Activate(ctx, record.NetworkID, record.RuntimeDigest, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidSnapshot
	}
	return nil
}

// ReviewSignature binds the owner review to the acknowledged generation and
// independent assessment revision as well as the existing branch/model review.
func ReviewSignature(base string, plan *Plan) string {
	if plan == nil || plan.Full == nil || plan.Full.Build == nil {
		return base
	}
	build := plan.Full.Build
	link := ""
	if build.Linking != nil {
		link = build.Linking.Signature
	}
	raw, _ := json.Marshal([]any{base, build.ID, build.Revision, build.GenerationID, build.ModelFingerprint, build.ReferenceID, build.ReferenceRevision, link})
	return digest(raw)
}

func HasDocuments(draft *interfaces.Draft) bool { return hasDocuments(draft) }
