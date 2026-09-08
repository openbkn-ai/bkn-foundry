// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package capability holds the execution factory's unified capability index: one dataset carrying
// Skills, Function tools and MCP tools under a single three-part identity, so a knowledge network
// can rank all three against one query instead of concatenating three lists ordered by three
// incomparable rules.
package capability

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

const (
	// executionFactoryCatalogID is the execution factory's own logical namespace. The capability
	// dataset lives beside the Skill dataset it will eventually replace.
	executionFactoryCatalogID   = "bkn_execution_factory_catalog"
	executionFactoryCatalogDesc = "执行工厂的逻辑命名空间"

	capabilityDataset       = "bkn_execution_factory_capability_dataset"
	capabilityDatasetDesc   = "执行工厂的能力索引数据集：Skill、函数工具、MCP 工具"
	capabilityDatasetStatus = "active"

	// internalCatalogTag marks a built-in catalog. Studio does not read the backend's internal
	// flag and keys its "built-in" rendering off this tag.
	internalCatalogTag = "internal"
	// vegaMaxTags mirrors vega's TAGS_MAX_NUMBER; exceeding it fails the whole update with 400.
	vegaMaxTags = 5

	// ownerScanBatch bounds one page when reading an owner's documents back for deletion.
	ownerScanBatch = 500
	// vegaPagingModeSingle is the one-page read mode. Vega rejects anything but "single" or
	// "cursor" with 400 paging.mode must be either single or cursor.
	vegaPagingModeSingle = "single"

	retryInitInterval = 30 * time.Second
)

type capabilityIndexSync struct {
	modelManager interfaces.MFModelManager
	modelAPI     interfaces.MFModelAPIClient
	vegaClient   interfaces.VegaBackendClient
	logger       interfaces.Logger

	mu          sync.RWMutex
	initialized bool
	datasetID   string
	// embeddingModelName is the name the embeddings API accepts, not the model ID vega stores.
	// It is a build-time snapshot: the model that vectorised the documents must be the model that
	// vectorises the query, or the two live in different spaces.
	embeddingModelName string
	// analyzer is the full-text analyzer this dataset was built with. It is read back from the
	// existing resource rather than re-resolved, so a deployment whose capability probe flaps
	// cannot rebuild the index back and forth.
	analyzer  string
	retryOnce sync.Once
}

var (
	syncOnce sync.Once
	syncInst *capabilityIndexSync
)

// NewCapabilityIndexSyncService returns the capability index sync singleton.
func NewCapabilityIndexSyncService() interfaces.CapabilityIndexSyncService {
	syncOnce.Do(func() {
		conf := config.NewConfigLoader()
		syncInst = &capabilityIndexSync{
			modelManager: drivenadapters.NewMFModelManager(),
			modelAPI:     drivenadapters.NewMFModelAPIClient(),
			vegaClient:   drivenadapters.NewVegaBackendClient(),
			logger:       conf.GetLogger(),
		}
	})
	return syncInst
}

// EnsureInitialized initialises on first use and starts a background retry loop on failure.
func (s *capabilityIndexSync) EnsureInitialized(ctx context.Context) error {
	if err := s.Init(ctx); err != nil {
		s.retryOnce.Do(func() { go s.retryInit() })
		return err
	}
	return nil
}

// Init ensures the catalog and the dataset exist and are usable by this process.
func (s *capabilityIndexSync) Init(ctx context.Context) (err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer func() { oteltrace.EndSpan(ctx, err) }()

	initialized := false
	defer func() { s.setInitialized(initialized) }()

	catalogID, err := s.ensureCatalog(ctx)
	if err != nil {
		return err
	}

	resource, err := s.vegaClient.GetResourceByID(ctx, capabilityDataset)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get capability dataset failed, resource_id=%s, err=%v", capabilityDataset, err)
		return err
	}
	s.setDatasetID(capabilityDataset)

	embeddingModel, err := s.resolveEmbeddingModel(ctx)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("resolve embedding model failed, resource_id=%s, err=%v", capabilityDataset, err)
		return err
	}

	if resource != nil {
		// Adopt the analyzer the dataset already has. Re-resolving it here would make an analyzer
		// that appeared or disappeared between restarts look like a schema change, and a schema
		// change rebuilds the index.
		analyzer := schemaAnalyzer(resource.SchemaDefinition)
		if analyzer == "" {
			analyzer = defaultFulltextAnalyzer
		}
		s.setBuildState(embeddingModel.ModelName, analyzer)
		if err := s.ensureDatasetCatalogEnabled(ctx, resource, catalogID); err != nil {
			return err
		}
		if reason := rebuildReason(resource, embeddingModel, analyzer); reason != "" {
			s.logger.WithContext(ctx).Infof("rebuilding capability dataset, resource_id=%s, reason=%s",
				capabilityDataset, reason)
			if err := s.rebuildDataset(ctx, resource.CatalogID, embeddingModel, analyzer); err != nil {
				return err
			}
		}
		initialized = true
		s.logger.WithContext(ctx).Infof("capability dataset ready, resource_id=%s, embedding_model_id=%s, analyzer=%s",
			capabilityDataset, embeddingModel.ModelID, analyzer)
		return nil
	}

	analyzer := s.resolveAnalyzer(ctx)
	s.setBuildState(embeddingModel.ModelName, analyzer)
	s.logger.WithContext(ctx).Infof("creating capability dataset, resource_id=%s, catalog_id=%s, embedding_model_id=%s, dimension=%d, analyzer=%s",
		capabilityDataset, catalogID, embeddingModel.ModelID, embeddingModel.EmbeddingDim, analyzer)
	if err := s.createDataset(ctx, catalogID, embeddingModel, analyzer); err != nil {
		s.logger.WithContext(ctx).Errorf("create capability dataset failed, resource_id=%s, err=%v", capabilityDataset, err)
		return err
	}
	initialized = true
	return nil
}

func (s *capabilityIndexSync) retryInit() {
	ticker := time.NewTicker(retryInitInterval)
	defer ticker.Stop()

	s.logger.Warn("capability index sync init retry loop started")
	for range ticker.C {
		if err := s.Init(context.Background()); err != nil {
			s.logger.Warnf("retry init capability index sync failed: %v", err)
			continue
		}
		s.logger.Info("capability index sync init retry succeeded")
		return
	}
}

func (s *capabilityIndexSync) createDataset(ctx context.Context, catalogID string,
	embeddingModel *interfaces.EmbeddingModel, analyzer string) error {
	_, err := s.vegaClient.CreateResource(ctx, &interfaces.VegaResourceRequest{
		ID:               capabilityDataset,
		CatalogID:        catalogID,
		Name:             capabilityDataset,
		Tags:             []string{"execution-factory", "capability", "索引"},
		Description:      capabilityDatasetDesc,
		Category:         "dataset",
		Status:           capabilityDatasetStatus,
		SourceIdentifier: capabilityDataset,
		SchemaDefinition: buildCapabilityIndexSchema(embeddingModel.EmbeddingDim, analyzer),
		// Vega validates the resource-level index_config as a model ID. The embeddings API name
		// stays in memory: it must not reach a vector feature's config, which vega copies into
		// the knn_vector mapping verbatim and OpenSearch then rejects.
		IndexConfig: &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: embeddingModel.ModelID},
	})
	return err
}

// rebuildDataset drops and recreates the dataset. The caller is expected to repopulate it: this
// index is a projection of the execution factory's tables, and a full build is the way back.
func (s *capabilityIndexSync) rebuildDataset(ctx context.Context, catalogID string,
	embeddingModel *interfaces.EmbeddingModel, analyzer string) error {
	if err := s.vegaClient.DeleteResource(ctx, capabilityDataset); err != nil {
		return fmt.Errorf("delete capability dataset before rebuild: %w", err)
	}
	if catalogID == "" {
		catalogID = executionFactoryCatalogID
	}
	if err := s.createDataset(ctx, catalogID, embeddingModel, analyzer); err != nil {
		return fmt.Errorf("recreate capability dataset with embedding model ID %q: %w", embeddingModel.ModelID, err)
	}
	s.logger.WithContext(ctx).Infof("rebuilt capability dataset, resource_id=%s, catalog_id=%s, model_id=%s",
		capabilityDataset, catalogID, embeddingModel.ModelID)
	return nil
}

// ensureCatalog resolves the built-in catalog, creating it when absent.
func (s *capabilityIndexSync) ensureCatalog(ctx context.Context) (string, error) {
	catalog, err := s.vegaClient.GetCatalogByID(ctx, executionFactoryCatalogID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get catalog failed, catalog_id=%s, err=%v", executionFactoryCatalogID, err)
		return "", err
	}
	if catalog == nil {
		_, err = s.vegaClient.CreateCatalog(ctx, &interfaces.VegaCatalogRequest{
			ID:          executionFactoryCatalogID,
			Name:        executionFactoryCatalogID,
			Tags:        []string{"execution-factory", "索引", internalCatalogTag},
			Description: executionFactoryCatalogDesc,
			// Internal catalogs are visible to super administrators only; a business role's
			// catalog:* grant cannot match them.
			Internal: true,
			// A disabled catalog makes vega reject reads and writes of the datasets under it
			// with 409.
			Enabled: true,
		})
		if err != nil {
			s.logger.WithContext(ctx).Errorf("create catalog failed, catalog_id=%s, err=%v", executionFactoryCatalogID, err)
			return "", err
		}
		return executionFactoryCatalogID, nil
	}
	if err := s.ensureCatalogEnabled(ctx, catalog); err != nil {
		return "", err
	}
	return catalog.ID, nil
}

// ensureCatalogEnabled enables a disabled catalog. A failure is returned rather than logged: every
// read and write below a disabled catalog is answered with 409, so carrying on would mark the
// service initialised in a state where nothing can be written.
func (s *capabilityIndexSync) ensureCatalogEnabled(ctx context.Context, catalog *interfaces.VegaCatalog) error {
	if catalog.Enabled {
		return nil
	}
	if err := s.vegaClient.EnableCatalog(ctx, catalog.ID); err != nil {
		s.logger.WithContext(ctx).Errorf("enable catalog failed, catalog_id=%s, err=%v", catalog.ID, err)
		return err
	}
	s.logger.WithContext(ctx).Infof("catalog enabled, catalog_id=%s", catalog.ID)
	return nil
}

// ensureDatasetCatalogEnabled enables the catalog the dataset actually hangs under, which need not
// be the one this process resolved: writes are governed by the dataset's own parent.
func (s *capabilityIndexSync) ensureDatasetCatalogEnabled(ctx context.Context,
	resource *interfaces.VegaResource, resolvedCatalogID string) error {
	if resource == nil || resource.CatalogID == "" || resource.CatalogID == resolvedCatalogID {
		return nil
	}
	parent, err := s.vegaClient.GetCatalogByID(ctx, resource.CatalogID)
	if err != nil {
		return err
	}
	if parent == nil {
		return fmt.Errorf("capability dataset %s points to missing catalog %s", resource.ID, resource.CatalogID)
	}
	return s.ensureCatalogEnabled(ctx, parent)
}

// resolveEmbeddingModel prefers the system default embedding model and falls back to the model
// named "embedding".
func (s *capabilityIndexSync) resolveEmbeddingModel(ctx context.Context) (*interfaces.EmbeddingModel, error) {
	model, err := s.modelManager.GetDefaultEmbeddingModel(ctx, interfaces.SmallModelTypeEmbedding)
	if err != nil {
		s.logger.WithContext(ctx).Warnf("get default embedding model failed, fallback to named '%s': %v",
			interfaces.SmallModelTypeEmbedding, err)
	} else if model != nil {
		return validateEmbeddingModel(model)
	}
	model, err = s.modelManager.GetEmbeddingModel(ctx, interfaces.SmallModelTypeEmbedding, interfaces.SmallModelTypeEmbedding)
	if err != nil {
		return nil, err
	}
	return validateEmbeddingModel(model)
}

// resolveAnalyzer picks the full-text analyzer for a dataset being created.
//
// It is not probed. Vega exposes its analyzer capabilities only on the public face behind OAuth,
// while the execution factory talks to the internal one — but there is nothing to probe: the
// platform's own OpenSearch image carries the IK analyzer, so it is part of the baseline.
//
// Whatever is chosen here is decided once per dataset and read back afterwards (see
// schemaAnalyzer), never re-derived. That matters more than the choice itself: making the analyzer
// a live decision would turn a deployment difference into a schema difference, and a schema
// difference rebuilds the index.
func (s *capabilityIndexSync) resolveAnalyzer(_ context.Context) string {
	return defaultFulltextAnalyzer
}

func validateEmbeddingModel(model *interfaces.EmbeddingModel) (*interfaces.EmbeddingModel, error) {
	if model == nil {
		return nil, fmt.Errorf("embedding model is required")
	}
	if strings.TrimSpace(model.ModelID) == "" {
		return nil, fmt.Errorf("embedding model ID is required")
	}
	if strings.TrimSpace(model.ModelName) == "" {
		return nil, fmt.Errorf("embedding model name is required")
	}
	if model.EmbeddingDim <= 0 {
		return nil, fmt.Errorf("embedding model dimension must be positive")
	}
	return model, nil
}

// rebuildReason says why an adopted dataset cannot be used as it stands, or "" when it can.
//
// A dataset row can outlive the index behind it. Vega assigns a managed index when the dataset is
// created, and a dataset created by a build that did not — or one whose index was lost — comes back
// as a row with no index name and a local status of unavailable. It accepts no writes at all, and
// the failure is a 400 on every document rather than anything visible at startup. Adopting such a
// row and carrying on means retrying forever; the only way out is to build the dataset again.
func rebuildReason(resource *interfaces.VegaResource,
	embeddingModel *interfaces.EmbeddingModel, analyzer string) string {
	if resource == nil {
		return "resource is missing"
	}
	if strings.TrimSpace(resource.LocalIndexName) == "" {
		return "dataset has no managed index"
	}
	if resource.LocalIndexStatus == interfaces.VegaLocalIndexUnavailable {
		return "managed index is unavailable"
	}
	if !sameDatasetDefinition(resource, embeddingModel, analyzer) {
		return "schema or embedding model changed"
	}
	return ""
}

// sameDatasetDefinition compares the managed definition, not just the embedding model reference.
// Property and feature order carry no meaning in vega, so the comparison is name based.
func sameDatasetDefinition(resource *interfaces.VegaResource,
	embeddingModel *interfaces.EmbeddingModel, analyzer string) bool {
	if resource == nil || embeddingModel == nil {
		return false
	}
	expected := buildCapabilityIndexSchema(embeddingModel.EmbeddingDim, analyzer)
	return sameSchema(expected, resource.SchemaDefinition) &&
		sameDefaultEmbeddingModel(resource.IndexConfig, embeddingModel.ModelID)
}

func sameDefaultEmbeddingModel(indexConfig *interfaces.VegaResourceIndexConfig, expectedModelID string) bool {
	if indexConfig == nil {
		return expectedModelID == ""
	}
	return indexConfig.DefaultEmbeddingModel == expectedModelID
}

func sameSchema(expected, actual []interfaces.VegaProperty) bool {
	if len(expected) != len(actual) {
		return false
	}
	actualByName := make(map[string]interfaces.VegaProperty, len(actual))
	for _, property := range actual {
		actualByName[property.Name] = property
	}
	for _, expectedProperty := range expected {
		actualProperty, ok := actualByName[expectedProperty.Name]
		if !ok || !sameProperty(expectedProperty, actualProperty) {
			return false
		}
	}
	return true
}

func sameProperty(expected, actual interfaces.VegaProperty) bool {
	if expected.Name != actual.Name ||
		expected.Type != actual.Type ||
		expected.DisplayName != actual.DisplayName ||
		expected.OriginalName != actual.OriginalName ||
		expected.Description != actual.Description ||
		len(expected.Features) != len(actual.Features) {
		return false
	}
	actualByName := make(map[string]interfaces.VegaPropertyFeature, len(actual.Features))
	for _, feature := range actual.Features {
		actualByName[feature.Name] = feature
	}
	for _, expectedFeature := range expected.Features {
		actualFeature, ok := actualByName[expectedFeature.Name]
		if !ok || !sameFeature(expectedFeature, actualFeature) {
			return false
		}
	}
	return true
}

func sameFeature(expected, actual interfaces.VegaPropertyFeature) bool {
	if expected.Name != actual.Name ||
		expected.DisplayName != actual.DisplayName ||
		expected.FeatureType != actual.FeatureType ||
		expected.Description != actual.Description ||
		expected.RefProperty != actual.RefProperty ||
		expected.IsDefault != actual.IsDefault ||
		expected.IsNative != actual.IsNative ||
		len(expected.Config) != len(actual.Config) {
		return false
	}
	for key, expectedValue := range expected.Config {
		actualValue, ok := actual.Config[key]
		if !ok || fmt.Sprintf("%v", expectedValue) != fmt.Sprintf("%v", actualValue) {
			return false
		}
	}
	return true
}

// UpsertCapability writes one capability into the index, vectorising its name and description.
func (s *capabilityIndexSync) UpsertCapability(ctx context.Context, doc *interfaces.CapabilityDocument) error {
	if doc == nil {
		return fmt.Errorf("capability document is required")
	}
	if err := validateRef(doc.CapabilityRef); err != nil {
		return err
	}
	if !s.isInitialized() {
		s.logger.WithContext(ctx).Warnf("skip capability index upsert, dataset not initialized, key=%s",
			readableKey(doc.CapabilityRef))
		return nil
	}
	document, err := s.buildDocument(ctx, doc)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("build capability document failed, key=%s, err=%v",
			readableKey(doc.CapabilityRef), err)
		return err
	}
	return s.vegaClient.WriteDatasetDocument(ctx, s.getDatasetID(), capabilityDocID(doc.CapabilityRef), document)
}

// DeleteCapability removes one capability from the index.
func (s *capabilityIndexSync) DeleteCapability(ctx context.Context, ref interfaces.CapabilityRef) error {
	if err := validateRef(ref); err != nil {
		return err
	}
	if !s.isInitialized() {
		s.logger.WithContext(ctx).Warnf("skip capability index delete, dataset not initialized, key=%s", readableKey(ref))
		return nil
	}
	return s.vegaClient.DeleteDatasetDocumentByID(ctx, s.getDatasetID(), capabilityDocID(ref))
}

// DeleteOwner removes every capability belonging to one owner.
//
// Vega has no delete-by-query, so the owner's documents are read back and deleted by id. The read
// asks for the identity fields only: the document id is derived from them, and pulling vectors
// back to throw them away would be the expensive half of the call.
func (s *capabilityIndexSync) DeleteOwner(ctx context.Context, capabilityType, ownerID string) error {
	capabilityType = strings.TrimSpace(capabilityType)
	ownerID = strings.TrimSpace(ownerID)
	if capabilityType == "" || ownerID == "" {
		return fmt.Errorf("capability type and owner ID are required")
	}
	if !s.isInitialized() {
		s.logger.WithContext(ctx).Warnf("skip capability owner purge, dataset not initialized, type=%s, owner=%s",
			capabilityType, ownerID)
		return nil
	}

	indexed, err := s.ListIndexedByOwner(ctx, capabilityType, ownerID)
	if err != nil {
		return fmt.Errorf("read capability documents of owner %s: %w", ownerID, err)
	}
	for _, entry := range indexed {
		if err := s.DeleteCapability(ctx, entry.CapabilityRef); err != nil {
			return fmt.Errorf("delete capability %s: %w", readableKey(entry.CapabilityRef), err)
		}
	}
	return nil
}

// ListIndexed reads every document of one capability type out of the index.
func (s *capabilityIndexSync) ListIndexed(ctx context.Context, capabilityType string) ([]interfaces.IndexedCapability, error) {
	return s.listIndexed(ctx, capabilityType, "")
}

// ListIndexedByOwner reads one owner's documents.
//
// Scoping the read to the owner is not an optimisation detail: reconciling N MCP Servers with the
// unscoped call would scan the whole capability type N times.
func (s *capabilityIndexSync) ListIndexedByOwner(ctx context.Context,
	capabilityType, ownerID string) ([]interfaces.IndexedCapability, error) {
	if strings.TrimSpace(ownerID) == "" {
		return nil, fmt.Errorf("owner ID is required")
	}
	return s.listIndexed(ctx, capabilityType, ownerID)
}

// listIndexed walks the index for one capability type, optionally narrowed to one owner.
//
// It walks capability_key in ascending order and asks for the keys strictly after the last one
// seen, rather than paging by offset: vega's read modes are "single" and "cursor", and the cursor
// token is not carried on the response this client parses. The key is the cursor because it is the
// only unique field — a tool id is unique inside its box, so a page boundary landing in the middle
// of a run of equal capability_ids would skip the rest of that run. Vectors are excluded: the
// caller compares name and description, and pulling embeddings back to discard them is the
// expensive half of the call.
func (s *capabilityIndexSync) listIndexed(ctx context.Context,
	capabilityType, ownerID string) ([]interfaces.IndexedCapability, error) {
	capabilityType = strings.TrimSpace(capabilityType)
	if capabilityType == "" {
		return nil, fmt.Errorf("capability type is required")
	}

	indexed := make([]interfaces.IndexedCapability, 0)
	after := ""
	for {
		conditions := []map[string]any{termCondition("capability_type", capabilityType)}
		if ownerID != "" {
			conditions = append(conditions, termCondition("owner_id", ownerID))
		}
		if after != "" {
			conditions = append(conditions, map[string]any{
				"field": "capability_key", "operation": "gt", "value": after, "value_from": "const",
			})
		}
		resp, err := s.vegaClient.QueryDatasetData(ctx, s.getDatasetID(), &interfaces.VegaDataQueryParams{
			FilterCondition: map[string]any{"operation": "and", "sub_conditions": conditions},
			Paging:          &interfaces.VegaDataPaging{Mode: vegaPagingModeSingle, Limit: ownerScanBatch},
			Sort:            []*interfaces.VegaDataSort{{Field: "capability_key", Direction: "asc"}},
			OutputFields: []string{"capability_type", "owner_id", "capability_id",
				"metadata_type", "name", "description"},
		})
		if err != nil {
			return nil, fmt.Errorf("read indexed capabilities of type %s: %w", capabilityType, err)
		}
		if resp == nil || len(resp.Entries) == 0 {
			return indexed, nil
		}
		previous := after
		for _, entry := range resp.Entries {
			ref := interfaces.CapabilityRef{
				CapabilityType: stringField(entry, "capability_type"),
				OwnerID:        stringField(entry, "owner_id"),
				CapabilityID:   stringField(entry, "capability_id"),
			}
			if validateRef(ref) != nil {
				continue
			}
			indexed = append(indexed, interfaces.IndexedCapability{
				CapabilityRef: ref,
				MetadataType:  stringField(entry, "metadata_type"),
				Name:          stringField(entry, "name"),
				Description:   stringField(entry, "description"),
			})
			after = capabilityKey(ref)
		}
		if len(resp.Entries) < ownerScanBatch {
			return indexed, nil
		}
		// A full page that advanced the cursor nowhere would re-read itself forever. It takes
		// every row on the page failing validation, and there is nothing further to read anyway.
		if after == previous {
			s.logger.WithContext(ctx).Warnf("capability scan stalled, no readable row on a full page, type=%s, after=%q",
				capabilityType, previous)
			return indexed, nil
		}
	}
}

// buildDocument vectorises the capability and returns the document body.
func (s *capabilityIndexSync) buildDocument(ctx context.Context,
	doc *interfaces.CapabilityDocument) (map[string]any, error) {
	// The model locked in when the dataset was built, not the current system default: documents
	// and queries must be vectorised by the same model or they do not share a space.
	embeddingResp, err := s.modelAPI.Embeddings(ctx, &interfaces.EmbeddingReq{
		Model: s.getEmbeddingModelName(),
		Input: []string{buildEmbeddingInput(doc.Name, doc.Description)},
	})
	if err != nil {
		return nil, err
	}
	if embeddingResp == nil || len(embeddingResp.Data) == 0 || len(embeddingResp.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding result is empty")
	}

	ref := doc.CapabilityRef
	return map[string]any{
		"_id":             capabilityDocID(ref),
		"capability_type": strings.TrimSpace(ref.CapabilityType),
		"owner_id":        strings.TrimSpace(ref.OwnerID),
		"capability_id":   strings.TrimSpace(ref.CapabilityID),
		"capability_key":  capabilityKey(ref),
		"metadata_type":   doc.MetadataType,
		"name":            doc.Name,
		"description":     doc.Description,
		"version":         doc.Version,
		"category":        doc.Category,
		"create_user":     doc.CreateUser,
		"create_time":     doc.CreateTime,
		"update_user":     doc.UpdateUser,
		"update_time":     doc.UpdateTime,
		"_vector":         embeddingResp.Data[0].Embedding,
	}, nil
}

// buildEmbeddingInput is what gets vectorised. Name and description on separate lines, the same
// input the Skill index has always used, so a document carried over from it lands in the same
// place in the vector space.
func buildEmbeddingInput(name, description string) string {
	return strings.Join([]string{name, description}, "\n")
}

// validateRef rejects an identity the index cannot address.
//
// owner_id is allowed to be empty: a Skill has no owner. The other two are not, and a blank
// capability type would let a Function tool and an MCP tool of the same id collide.
func validateRef(ref interfaces.CapabilityRef) error {
	switch strings.TrimSpace(ref.CapabilityType) {
	case interfaces.CapabilityTypeSkill, interfaces.CapabilityTypeFunction, interfaces.CapabilityTypeMCPTool:
	default:
		return fmt.Errorf("unknown capability type %q", ref.CapabilityType)
	}
	if strings.TrimSpace(ref.CapabilityID) == "" {
		return fmt.Errorf("capability ID is required")
	}
	if ref.CapabilityType != interfaces.CapabilityTypeSkill && strings.TrimSpace(ref.OwnerID) == "" {
		return fmt.Errorf("owner ID is required for capability type %q", ref.CapabilityType)
	}
	return nil
}

// readableKey is for logs. The stored key uses a control character as its separator, which reads
// as nothing at all in a terminal.
func readableKey(ref interfaces.CapabilityRef) string {
	return fmt.Sprintf("%s:%s/%s", ref.CapabilityType, ref.OwnerID, ref.CapabilityID)
}

func termCondition(field, value string) map[string]any {
	return map[string]any{
		"field":      field,
		"operation":  "eq",
		"value":      value,
		"value_from": "const",
	}
}

func stringField(entry map[string]any, key string) string {
	if value, ok := entry[key].(string); ok {
		return value
	}
	return ""
}

func (s *capabilityIndexSync) isInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

func (s *capabilityIndexSync) setInitialized(initialized bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = initialized
}

func (s *capabilityIndexSync) getDatasetID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.datasetID == "" {
		return capabilityDataset
	}
	return s.datasetID
}

func (s *capabilityIndexSync) setDatasetID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.datasetID = id
}

func (s *capabilityIndexSync) getEmbeddingModelName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.embeddingModelName == "" {
		return interfaces.SmallModelTypeEmbedding
	}
	return s.embeddingModelName
}

func (s *capabilityIndexSync) setBuildState(embeddingModelName, analyzer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.embeddingModelName = embeddingModelName
	s.analyzer = analyzer
}
