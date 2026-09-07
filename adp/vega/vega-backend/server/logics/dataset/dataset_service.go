// Package dataset provides Dataset management business logic.
package dataset

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics"
	"vega-backend/logics/catalog"
	"vega-backend/logics/local_index"
	"vega-backend/logics/model_factory"
	"vega-backend/logics/permission"
)

var (
	dsServiceOnce sync.Once
	dsService     interfaces.DatasetService
)

type datasetService struct {
	appSetting *common.AppSetting
	lim        interfaces.LocalIndexManager
	mfs        interfaces.ModelFactoryService
	ra         interfaces.ResourceAccess
	cs         interfaces.CatalogService
	ps         interfaces.PermissionService
}

// NewDatasetService creates a new DatasetService.
func NewDatasetService(appSetting *common.AppSetting) interfaces.DatasetService {
	dsServiceOnce.Do(func() {
		dsService = &datasetService{
			appSetting: appSetting,
			lim:        local_index.NewLocalIndexManager(appSetting),
			mfs:        model_factory.NewModelFactoryService(appSetting),
			ra:         logics.RA,
			cs:         catalog.NewCatalogService(appSetting),
			ps:         permission.NewPermissionService(appSetting),
		}
	})
	return dsService
}

// Create a new Dataset.
func (ds *datasetService) Create(ctx context.Context, res *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create dataset")
	defer span.End()

	indexID, err := uuid.NewV7()
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails("generate dataset index UUIDv7 failed")
	}
	indexName := fmt.Sprintf("%s-%s", interfaces.DatasetIndexPrefix, indexID)
	err = ds.lim.CreateIndex(ctx, indexName, res.SchemaDefinition, map[string]string{"resource_id": res.ID})
	if err != nil {
		otellog.LogError(ctx, "Create dataset index failed", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}
	res.LocalIndexName = indexName
	res.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable

	span.SetStatus(codes.Ok, "")
	return nil
}

// Update a Dataset.
func (ds *datasetService) Update(ctx context.Context, res *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update dataset")
	defer span.End()

	if err := ds.lim.UpdateIndex(ctx, res.LocalIndexName, res.SchemaDefinition); err != nil {
		span.SetStatus(codes.Error, "Update dataset failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// Delete a Dataset.
func (ds *datasetService) Delete(ctx context.Context, res *interfaces.Resource) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset")
	defer span.End()

	// Check dataset exist first
	exist, err := ds.lim.CheckIndexExist(ctx, res.LocalIndexName)
	if err != nil {
		span.SetStatus(codes.Error, "Check dataset exist failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}
	if exist {
		// Delete from storage
		if err := ds.lim.DeleteIndex(ctx, res.LocalIndexName); err != nil {
			span.SetStatus(codes.Error, "Delete dataset failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
				WithErrorDetails(err.Error())
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// ListDocuments lists the documents in the dataset
func (ds *datasetService) ListDocuments(ctx context.Context, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List dataset documents")
	defer span.End()

	// Call the local index store to list the documents
	documents, total, err := ds.lim.ListDocuments(ctx, res.LocalIndexName, res, params)
	if err != nil {
		span.SetStatus(codes.Error, "List dataset documents failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return documents, total, nil
}

// GetDocuments retrieves documents in input order. With ignoreMissing enabled,
// a missing document is represented by a nil entry at the same position.
func (ds *datasetService) GetDocuments(ctx context.Context, res *interfaces.Resource, docIDs []string, ignoreMissing bool) ([]map[string]any, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get dataset documents")
	defer span.End()

	documents := make([]map[string]any, len(docIDs))
	loadedDocuments, err := ds.lim.GetDocuments(ctx, res.LocalIndexName, docIDs)
	if err != nil {
		span.SetStatus(codes.Error, "Get dataset documents failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}
	if len(loadedDocuments) != len(docIDs) {
		span.SetStatus(codes.Error, "Get dataset documents returned unexpected count")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails("local index returned a document count different from the requested IDs")
	}
	for i, document := range loadedDocuments {
		if document == nil && !ignoreMissing {
			span.SetStatus(codes.Error, "Dataset document not found")
			return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound).
				WithErrorDetails(fmt.Sprintf("document %s not found", docIDs[i]))
		}
		documents[i] = document
	}

	span.SetStatus(codes.Ok, "")
	return documents, nil
}

// CreateDocument materializes a Dataset document and writes it only after all
// vector work has completed successfully.
func (ds *datasetService) CreateDocument(ctx context.Context, res *interfaces.Resource, document map[string]any) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create dataset document")
	defer span.End()

	if err := ds.checkDocumentModifyPermission(ctx, res); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return "", err
	}
	if _, exists := document["_id"]; exists {
		return "", invalidDocumentError(ctx, "document id is generated by the service; use PUT with a path document id instead")
	}
	materialized, err := ds.materializeDocument(ctx, res, document)
	if err != nil {
		span.SetStatus(codes.Error, "Materialize dataset document failed")
		return "", err
	}
	docIDs, err := ds.lim.CreateDocuments(ctx, res.LocalIndexName, []map[string]any{materialized})
	if err != nil {
		span.SetStatus(codes.Error, "Create dataset document failed")
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}
	if len(docIDs) != 1 {
		span.SetStatus(codes.Error, "Create dataset document returned no id")
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails("local index did not return a document ID")
	}
	span.SetStatus(codes.Ok, "")
	return docIDs[0], nil
}

// ReplaceDocument performs full document replacement for path-based Dataset PUT.
func (ds *datasetService) ReplaceDocument(ctx context.Context, res *interfaces.Resource, docID string, document map[string]any) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Replace dataset document")
	defer span.End()

	if err := ds.checkDocumentModifyPermission(ctx, res); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return err
	}
	materialized, err := ds.materializeDocument(ctx, res, document)
	if err != nil {
		span.SetStatus(codes.Error, "Materialize dataset document failed")
		return err
	}
	if _, err := ds.lim.IndexDocuments(ctx, res.LocalIndexName, map[string]map[string]any{docID: materialized}); err != nil {
		span.SetStatus(codes.Error, "Replace dataset document failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_UpdateFailed).
			WithErrorDetails(err.Error())
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteDocuments to batch delete dataset documents
func (ds *datasetService) DeleteDocuments(ctx context.Context, res *interfaces.Resource, docIDs []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset documents")
	defer span.End()

	if err := ds.checkDocumentModifyPermission(ctx, res); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return err
	}
	// Call the local index store to batch delete documents
	if err := ds.lim.DeleteDocuments(ctx, res.LocalIndexName, docIDs); err != nil {
		span.SetStatus(codes.Error, "Delete dataset documents failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteDocumentsByQuery for batch deletion of dataset documents
func (ds *datasetService) DeleteDocumentsByQuery(ctx context.Context, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset documents by query")
	defer span.End()

	if err := ds.checkDocumentModifyPermission(ctx, res); err != nil {
		span.SetStatus(codes.Error, "Permission denied")
		return err
	}
	// Call the local index store to batch delete documents
	if err := ds.lim.DeleteDocumentsByQuery(ctx, res.LocalIndexName, res, params); err != nil {
		span.SetStatus(codes.Error, "Delete dataset documents failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ds *datasetService) checkDocumentModifyPermission(ctx context.Context, res *interfaces.Resource) error {
	internalCatalogs, err := ds.cs.InternalCatalogIDSet(ctx)
	if err != nil {
		return err
	}
	_, parentInternal := internalCatalogs[res.CatalogID]
	if parentInternal && interfaces.IsS2SInternalAccess(ctx) {
		return nil
	}
	catalogType := interfaces.AUTH_RESOURCE_TYPE_CATALOG
	if parentInternal {
		catalogType = interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG
	}
	return ds.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: catalogType,
		ID:   res.CatalogID,
	}, []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE})
}
