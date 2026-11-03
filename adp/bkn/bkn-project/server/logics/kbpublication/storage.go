// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"context"
	"errors"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
)

func snapshotSpec(project, network, catalog string, owner interfaces.AccountInfo) (interfaces.DraftDatasetSpec, error) {
	if project == "" || network == "" || catalog == "" || owner.ID == "" || owner.Type != "app" {
		return interfaces.DraftDatasetSpec{}, ErrInvalidSnapshot
	}
	object := &interfaces.DraftObjectType{ID: "published_model", Name: "Published model", Adopted: true, Document: &interfaces.DraftDocumentModel{Role: "evidence_document"}, PrimaryKeys: []string{"_id"}, Properties: []*interfaces.DraftProperty{{Name: "_id", Type: "string", ReadOnly: true, SourceOfRecord: "document"}, {Name: "runtime_model", Type: "text", ReadOnly: true, SourceOfRecord: "document"}}}
	layout, err := kbdataset.BuildLayout(object)
	if err != nil {
		return interfaces.DraftDatasetSpec{}, err
	}
	identity := digest([]byte("published-model-v1/" + project + "/" + network))
	return interfaces.DraftDatasetSpec{ProjectID: project, GenerationID: identity[:32], Owner: owner, ObjectTypeID: "published_model", ResourceID: identity[32:], CatalogID: catalog, Name: object.Name, Layout: *layout}, nil
}
func readSnapshot(ctx context.Context, storage kb.PublicationDatasets, record *interfaces.KBPublication) (*interfaces.Draft, error) {
	spec := record.Snapshot
	spec.Ready = true
	row, err := storage.ReadPublishedModel(ctx, spec, record.RuntimeRowID)
	if err != nil {
		return nil, err
	}
	if len(row) != 4 || row["_id"] != record.RuntimeRowID || row["model_fingerprint"] != record.ModelFingerprint || row["extraction_version"] != "published-model-v1" {
		return nil, ErrInvalidSnapshot
	}
	payload, ok := row["runtime_model"].(string)
	if !ok {
		return nil, ErrInvalidSnapshot
	}
	return Restore([]byte(payload), record.RuntimeDigest, record.ModelFingerprint)
}

// LoadPublishedModel restores the acknowledged private model without consulting
// the mutable project. Refresh keeps working when that project is archived.
func LoadPublishedModel(ctx context.Context, storage kb.PublicationDatasets, record *interfaces.KBPublication) (*interfaces.Draft, error) {
	if storage == nil || record == nil || record.State != "active" || record.RuntimeRowID == "" || record.RuntimeDigest == "" || record.ModelFingerprint == "" {
		return nil, ErrInvalidSnapshot
	}
	return readSnapshot(ctx, storage, record)
}

// persistSnapshot permits a write only after a complete, explicit missing-row
// result. A lost write acknowledgement is recovered by reading that same row;
// an arbitrary read failure never becomes permission to replace it.
func persistSnapshot(ctx context.Context, storage kb.PublicationDatasets, record *interfaces.KBPublication, bundle *Bundle, guard func(context.Context) error) error {
	if storage == nil || record == nil || bundle == nil || guard == nil || record.RuntimeDigest != bundle.RuntimeDigest || record.ModelFingerprint != bundle.ModelFingerprint {
		return ErrInvalidSnapshot
	}
	if err := guard(ctx); err != nil {
		return err
	}
	if err := storage.Ensure(ctx, record.Snapshot); err != nil {
		return err
	}
	_, err := readSnapshot(ctx, storage, record)
	if err == nil {
		return guard(ctx)
	}
	if !errors.Is(err, kb.ErrPublishedModelMissing) {
		return err
	}
	if err := guard(ctx); err != nil {
		return err
	}
	spec := record.Snapshot
	spec.Ready = true
	row := map[string]any{"_id": record.RuntimeRowID, "model_fingerprint": record.ModelFingerprint, "extraction_version": "published-model-v1", "runtime_model": string(bundle.Runtime)}
	if err := storage.Replace(ctx, spec, row); err != nil {
		return err
	}
	if err := guard(ctx); err != nil {
		return err
	}
	if _, err := readSnapshot(ctx, storage, record); err != nil {
		return err
	}
	return guard(ctx)
}
