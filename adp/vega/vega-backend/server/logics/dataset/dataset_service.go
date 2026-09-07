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
func (ds *datasetService) Delete(ctx context.Context, indexName string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset")
	defer span.End()

	// Check dataset exist first
	exist, err := ds.lim.CheckIndexExist(ctx, indexName)
	if err != nil {
		span.SetStatus(codes.Error, "Check dataset exist failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}
	if exist {
		// Delete from storage
		if err := ds.lim.DeleteIndex(ctx, indexName); err != nil {
			span.SetStatus(codes.Error, "Delete dataset failed")
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
				WithErrorDetails(err.Error())
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// CheckExist checks if a dataset exists.
func (ds *datasetService) CheckExist(ctx context.Context, id string) (bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Check dataset exist")
	defer span.End()

	exist, err := ds.lim.CheckIndexExist(ctx, id)
	if err != nil {
		span.SetStatus(codes.Error, "Check dataset exist failed")
		return false, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return exist, nil
}

// ListDocuments lists the documents in the dataset
func (ds *datasetService) ListDocuments(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "List dataset documents")
	defer span.End()

	// Call the local index store to list the documents
	documents, total, err := ds.lim.ListDocuments(ctx, indexName, res, params)
	if err != nil {
		span.SetStatus(codes.Error, "List dataset documents failed")
		return nil, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return documents, total, nil
}

// CreateDocuments to batch create dataset documents
func (ds *datasetService) CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create dataset documents")
	defer span.End()

	// Call the local index store to create documents in batches
	docIDs, err := ds.lim.CreateDocuments(ctx, indexName, documents)
	if err != nil {
		span.SetStatus(codes.Error, "Create dataset documents failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_CreateFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return docIDs, nil
}

// GetDocument retrieves the dataset document
func (ds *datasetService) GetDocument(ctx context.Context, indexName string, docID string) (map[string]any, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Get dataset document")
	defer span.End()

	// Call the local index store to obtain the document
	document, err := ds.lim.GetDocument(ctx, indexName, docID)
	if err != nil {
		span.SetStatus(codes.Error, "Get dataset document failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return document, nil
}

// CreateDocument materializes a Dataset document and writes it only after all
// vector work has completed successfully.
func (ds *datasetService) CreateDocument(ctx context.Context, res *interfaces.Resource, document map[string]any) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create dataset document")
	defer span.End()

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

// DeleteDocument deletes the dataset document
func (ds *datasetService) DeleteDocument(ctx context.Context, indexName string, docID string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset document")
	defer span.End()

	// Call the local index store to delete the document
	if err := ds.lim.DeleteDocument(ctx, indexName, docID); err != nil {
		span.SetStatus(codes.Error, "Delete dataset document failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// UpsertDocuments batch updates dataset documents
func (ds *datasetService) UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update dataset documents")
	defer span.End()

	// Call the local index store to update documents in batches
	docIDs, err := ds.lim.UpsertDocuments(ctx, indexName, updateRequests)
	if err != nil {
		span.SetStatus(codes.Error, "Update dataset documents failed")
		return docIDs, err
	}

	span.SetStatus(codes.Ok, "")
	return docIDs, nil
}

// DeleteDocuments to batch delete dataset documents
func (ds *datasetService) DeleteDocuments(ctx context.Context, indexName string, docIDs string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset documents")
	defer span.End()

	// Call the local index store to batch delete documents
	if err := ds.lim.DeleteDocuments(ctx, indexName, docIDs); err != nil {
		span.SetStatus(codes.Error, "Delete dataset documents failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteDocumentsByQuery for batch deletion of dataset documents
func (ds *datasetService) DeleteDocumentsByQuery(ctx context.Context, indexName string, res *interfaces.Resource, params *interfaces.ResourceDataQueryParams) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete dataset documents by query")
	defer span.End()

	// Call the local index store to batch delete documents
	if err := ds.lim.DeleteDocumentsByQuery(ctx, indexName, res, params); err != nil {
		span.SetStatus(codes.Error, "Delete dataset documents failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Resource_InternalError_DeleteFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
