package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/parsers"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// metadataService unified metadata management service.
type metadataService struct {
	Logger             interfaces.Logger
	APIMetadataDB      model.IAPIMetadataDB
	FuncMetadataDB     model.IFunctionMetadataDB
	OperatorRegisterDB model.IOperatorRegisterDB
	ParserRegistry     *parsers.Registry
}

var (
	mOnce    sync.Once
	mManager interfaces.IMetadataService
)

// NewMetadataService creates a unified metadata management module.
func NewMetadataService() interfaces.IMetadataService {
	mOnce.Do(func() {
		mManager = &metadataService{
			Logger:             config.NewConfigLoader().GetLogger(),
			APIMetadataDB:      dbaccess.NewAPIMetadataDB(),
			FuncMetadataDB:     dbaccess.NewFunctionMetadataDB(),
			OperatorRegisterDB: dbaccess.NewOperatorManagerDB(),
			ParserRegistry:     parsers.NewRegistry(),
		}
	})
	return mManager
}

// GetMetadataBySource queries metadata based on SourceID and SourceType.
func (m *metadataService) GetMetadataBySource(ctx context.Context, sourceID string, sourceType model.SourceType) (has bool, metadata interfaces.IMetadataDB, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"source_id":   sourceID,
		"source_type": string(sourceType),
	})
	// Query metadata based on SourceType.
	switch sourceType {
	case model.SourceTypeOpenAPI:
		has, metadata, err = m.APIMetadataDB.SelectByVersion(ctx, sourceID)
	case model.SourceTypeFunction:
		has, metadata, err = m.FuncMetadataDB.SelectByVersion(ctx, sourceID)
	case model.SourceTypeOperator:
		var operatorDB *model.OperatorRegisterDB
		has, operatorDB, err = m.OperatorRegisterDB.SelectByOperatorID(ctx, nil, sourceID)
		if err == nil && has {
			has, metadata, err = m.GetMetadataBySource(ctx, operatorDB.MetadataVersion, model.SourceType(operatorDB.MetadataType))
		}
	default:
		err = fmt.Errorf("unsupported source type: %s", sourceType)
	}
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("get metadata failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return
}

func (m *metadataService) BatchGetMetadataBySourceIDs(ctx context.Context, sourceMap map[model.SourceType][]string) (sourceIDToMetadata map[string]interfaces.IMetadataDB, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	sourceIDToMetadata = map[string]interfaces.IMetadataDB{}
	if len(sourceMap) == 0 {
		return
	}
	var wg sync.WaitGroup
	// Add stop sign.
	var stopFlag int32
	// Use thread-safe mapping.
	resultMutex := sync.Mutex{}
	errorsMutex := sync.Mutex{}
	var errList []error
	for sourceType, sourceIDs := range sourceMap {
		if len(sourceIDs) == 0 {
			continue
		}
		wg.Add(1)
		go func(st model.SourceType, sourceIDList []string) {
			defer wg.Done()

			// Check if it needs to be stopped.
			if atomic.LoadInt32(&stopFlag) == 1 {
				return
			}

			var localErr error
			sourceIDList = utils.UniqueStrings(sourceIDList)
			switch st {
			case model.SourceTypeOpenAPI:
				var metadataList []*model.APIMetadataDB
				metadataList, localErr = utils.BatchQueryWithContext(ctx, sourceIDList,
					interfaces.DefaultBatchSize, m.APIMetadataDB.SelectListByVersion)
				if localErr != nil {
					m.Logger.WithContext(ctx).Errorf("batch query api metadata failed, err: %v", localErr)
					localErr = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, localErr.Error())
				} else {
					resultMutex.Lock()
					for _, metadata := range metadataList {
						sourceIDToMetadata[metadata.Version] = metadata
					}
					resultMutex.Unlock()
				}
			case model.SourceTypeFunction:
				var metadataList []*model.FunctionMetadataDB
				metadataList, localErr = utils.BatchQueryWithContext(ctx, sourceIDList,
					interfaces.DefaultBatchSize, m.FuncMetadataDB.SelectListByVersion)
				if localErr != nil {
					m.Logger.WithContext(ctx).Errorf("batch query function metadata failed, err: %v", localErr)
					localErr = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, localErr.Error())
				} else {
					resultMutex.Lock()
					for _, metadata := range metadataList {
						sourceIDToMetadata[metadata.Version] = metadata
					}
					resultMutex.Unlock()
				}
			case model.SourceTypeOperator:
				var operatorList []*model.OperatorRegisterDB
				operatorList, localErr = utils.BatchQueryWithContext(ctx, sourceIDList,
					interfaces.DefaultBatchSize, m.OperatorRegisterDB.SelectByOperatorIDs)
				if localErr != nil {
					m.Logger.WithContext(ctx).Errorf("batch query operator metadata failed, err: %v", localErr)
					localErr = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, localErr.Error())
				} else {
					operatorSourceMap := map[model.SourceType][]string{}
					for _, operator := range operatorList {
						operatorSourceMap[model.SourceType(operator.MetadataType)] = append(operatorSourceMap[model.SourceType(operator.MetadataType)],
							operator.MetadataVersion)
					}
					var operatorSourceIDToMetadata map[string]interfaces.IMetadataDB
					operatorSourceIDToMetadata, localErr = m.BatchGetMetadataBySourceIDs(ctx, operatorSourceMap)
					if localErr != nil {
						m.Logger.WithContext(ctx).Errorf("batch query operator metadata failed, err: %v", localErr)
						localErr = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, localErr.Error())
					} else {
						resultMutex.Lock()
						for _, operatorDB := range operatorList {
							sourceIDToMetadata[operatorDB.OperatorID] = operatorSourceIDToMetadata[operatorDB.MetadataVersion]
						}
						resultMutex.Unlock()
					}
				}
			}
			// handling errors.
			if localErr != nil {
				errorsMutex.Lock()
				errList = append(errList, localErr)
				errorsMutex.Unlock()
				// Set stop flag but don't cancel context.
				atomic.StoreInt32(&stopFlag, 1)
			}
		}(sourceType, sourceIDs)
	}

	wg.Wait()
	// handling errors.
	if len(errList) > 0 {
		// Return the first error as the primary error.
		err = errList[0]
		if len(errList) > 1 {
			m.Logger.WithContext(ctx).Warnf("multiple errors occurred during batch get metadata: %v", errList)
		}
	}
	return sourceIDToMetadata, err
}

// ParseMetadata parses metadata.
func (m *metadataService) ParseMetadata(ctx context.Context, metadataType interfaces.MetadataType, input any) ([]interfaces.IMetadataDB, error) {
	parser, err := m.ParserRegistry.Get(metadataType)
	if err != nil {
		return nil, err
	}
	return parser.Parse(ctx, input)
}

// ParseRawContent gets the parsed original content.
func (m *metadataService) ParseRawContent(ctx context.Context, metadataType interfaces.MetadataType, input any) (content any, err error) {
	parser, err := m.ParserRegistry.Get(metadataType)
	if err != nil {
		return nil, err
	}
	// Parse raw data into target structure.
	content, err = parser.GetAllContent(ctx, input)
	if err != nil {
		return nil, err
	}
	return
}

// RegisterMetadata registers a single metadata.
func (m *metadataService) RegisterMetadata(ctx context.Context, tx *sql.Tx, metadata interfaces.IMetadataDB) (version string, err error) {
	// Verify metadata.
	err = m.ValidateMetadata(ctx, metadata)
	if err != nil {
		return
	}

	// Store in corresponding table according to type.
	switch metadata.GetType() {
	case string(model.SourceTypeOpenAPI):
		apiMetadata, ok := metadata.(*model.APIMetadataDB)
		if !ok {
			err = fmt.Errorf("invalid metadata type for API: %T", metadata)
			return
		}
		if apiMetadata.Version == "" {
			apiMetadata.Version = uuid.New().String()
		}
		version, err = m.APIMetadataDB.InsertAPIMetadata(ctx, tx, apiMetadata)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("insert API metadata failed, err: %v", err)
			return
		}
	case string(model.SourceTypeFunction):
		funcMetadata, ok := metadata.(*model.FunctionMetadataDB)
		if !ok {
			err = fmt.Errorf("invalid metadata type for Function: %T", metadata)
			return
		}
		if funcMetadata.Version == "" {
			funcMetadata.Version = uuid.New().String()
		}
		funcMetadata.Path = interfaces.SetAOIFuncExecPath(funcMetadata.Version)
		version, err = m.FuncMetadataDB.InsertFuncMetadata(ctx, tx, funcMetadata)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("insert Function metadata failed, err: %v", err)
			return
		}
	default:
		err = fmt.Errorf("unsupported metadata type: %s", metadata.GetType())
		return
	}
	return
}

// BatchRegisterMetadata batch registration metadata.
func (m *metadataService) BatchRegisterMetadata(ctx context.Context, tx *sql.Tx, metadatas []interfaces.IMetadataDB) (versions []string, err error) {
	if len(metadatas) == 0 {
		return []string{}, nil
	}

	// Group by type.
	apiMetadatas := make([]*model.APIMetadataDB, 0)
	funcMetadatas := make([]*model.FunctionMetadataDB, 0)

	for _, metadata := range metadatas {
		err = m.ValidateMetadata(ctx, metadata)
		if err != nil {
			return
		}

		switch metadata.GetType() {
		case string(model.SourceTypeOpenAPI):
			apiMetadata, ok := metadata.(*model.APIMetadataDB)
			if !ok {
				err = fmt.Errorf("invalid metadata type for API: %T", metadata)
				return
			}
			if apiMetadata.Version == "" {
				apiMetadata.Version = uuid.New().String()
			}
			apiMetadatas = append(apiMetadatas, apiMetadata)
		case string(model.SourceTypeFunction):
			funcMetadata, ok := metadata.(*model.FunctionMetadataDB)
			if !ok {
				err = fmt.Errorf("invalid metadata type for Function: %T", metadata)
				return
			}
			if funcMetadata.Version == "" {
				funcMetadata.Version = uuid.New().String()
			}
			funcMetadata.Path = interfaces.SetAOIFuncExecPath(funcMetadata.Version)
			funcMetadatas = append(funcMetadatas, funcMetadata)
		default:
			err = fmt.Errorf("unsupported metadata type: %s", metadata.GetType())
			return
		}
	}

	versions = make([]string, 0, len(metadatas))

	// Insert API metadata in batches.
	if len(apiMetadatas) > 0 {
		apiVersions, err := m.APIMetadataDB.InsertAPIMetadatas(ctx, tx, apiMetadatas)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("batch insert API metadata failed, err: %v", err)
			return nil, err
		}
		versions = append(versions, apiVersions...)
	}

	// Insert Function metadata in batches.
	if len(funcMetadatas) > 0 {
		funcVersions, err := m.FuncMetadataDB.InsertFuncMetadatas(ctx, tx, funcMetadatas)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("batch insert Function metadata failed, err: %v", err)
			return nil, err
		}
		versions = append(versions, funcVersions...)
	}

	return versions, nil
}

// CheckMetadataExists checks whether metadata exists.
func (m *metadataService) CheckMetadataExists(ctx context.Context, metadataType interfaces.MetadataType, version string) (exists bool,
	metadata interfaces.IMetadataDB, err error) {
	switch metadataType {
	case interfaces.MetadataTypeAPI:
		exists, metadata, err = m.APIMetadataDB.SelectByVersion(ctx, version)
		if err != nil {
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("select API metadata by version failed, err: %v", err))
			m.Logger.WithContext(ctx).Errorf("select API metadata by version failed, err: %v", err)
			return
		}
	case interfaces.MetadataTypeFunc:
		exists, metadata, err = m.FuncMetadataDB.SelectByVersion(ctx, version)
		if err != nil {
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("select Function metadata by version failed, err: %v", err))
			m.Logger.WithContext(ctx).Errorf("select Function metadata by version failed, err: %v", err)
			return
		}
	default:
		m.Logger.WithContext(ctx).Warnf("unsupported metadata type: %s", metadataType)
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("unsupported metadata type: %s", metadataType))
	}
	return
}

// GetMetadataByVersion Query metadata based on version.
func (m *metadataService) GetMetadataByVersion(ctx context.Context, metadataType interfaces.MetadataType, version string) (interfaces.IMetadataDB, error) {
	switch metadataType {
	case interfaces.MetadataTypeAPI:
		exist, metadata, err := m.APIMetadataDB.SelectByVersion(ctx, version)
		if err != nil {
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("select API metadata by version failed, err: %v", err))
			m.Logger.WithContext(ctx).Errorf("select API metadata by version failed, err: %v", err)
			return nil, err
		}
		if !exist {
			return nil, errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtMetadataNotFound, fmt.Sprintf("API metadata version %s not found", version))
		}
		return metadata, nil
	case interfaces.MetadataTypeFunc:
		exist, metadata, err := m.FuncMetadataDB.SelectByVersion(ctx, version)
		if err != nil {
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("select Function metadata by version failed, err: %v", err))
			m.Logger.WithContext(ctx).Errorf("select Function metadata by version failed, err: %v", err)
			return nil, err
		}
		if !exist {
			return nil, errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtMetadataNotFound, fmt.Sprintf("Function metadata version %s not found", version))
		}
		return metadata, nil
	default:
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("unsupported metadata type: %s", metadataType))
	}
}

// BatchGetMetadata batch query metadata.
func (m *metadataService) BatchGetMetadata(ctx context.Context, apiVersions, funcVersions []string) (result []interfaces.IMetadataDB, err error) {
	// Concurrent query of API metadata.
	var apiMetadatas []*model.APIMetadataDB
	var funcMetadatas []*model.FunctionMetadataDB
	var apiErr, funcErr error
	result = []interfaces.IMetadataDB{}
	var wg sync.WaitGroup

	// Query OpenAPI metadata.
	if len(apiVersions) > 0 {
		apiVersions = utils.UniqueStrings(apiVersions)
		wg.Add(1)
		go func() {
			defer wg.Done()
			apiMetadatas, apiErr = utils.BatchQueryWithContext[*model.APIMetadataDB, string](
				ctx, apiVersions, interfaces.DefaultBatchSize, m.APIMetadataDB.SelectListByVersion)
		}()
	}
	// Query Function metadata.
	if len(funcVersions) > 0 {
		funcVersions = utils.UniqueStrings(funcVersions)
		wg.Add(1)
		go func() {
			defer wg.Done()
			funcMetadatas, funcErr = utils.BatchQueryWithContext[*model.FunctionMetadataDB, string](
				ctx, funcVersions, interfaces.DefaultBatchSize, m.FuncMetadataDB.SelectListByVersion)
		}()
	}

	// Wait for all queries to complete.
	wg.Wait()

	// Error handling.
	if apiErr != nil || funcErr != nil {
		err = fmt.Errorf("batch get metadata failed, apiErr: %v, funcErr: %v", apiErr, funcErr)
		return
	}

	// Merge results.
	for _, metadata := range apiMetadatas {
		result = append(result, metadata)
	}
	for _, metadata := range funcMetadatas {
		result = append(result, metadata)
	}
	return result, nil
}

// UpdateMetadata Update metadata.
func (m *metadataService) UpdateMetadata(ctx context.Context, tx *sql.Tx, metadata interfaces.IMetadataDB) error {
	// Verify metadata.
	err := m.ValidateMetadata(ctx, metadata)
	if err != nil {
		return err
	}

	// Update the corresponding table according to the type.
	switch metadata.GetType() {
	case string(model.SourceTypeOpenAPI):
		apiMetadata, ok := metadata.(*model.APIMetadataDB)
		if !ok {
			return fmt.Errorf("invalid metadata type for API: %T", metadata)
		}
		now := time.Now().UnixNano()
		apiMetadata.UpdateTime = now
		err = m.APIMetadataDB.UpdateByVersion(ctx, tx, apiMetadata.Version, apiMetadata)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("update API metadata failed, err: %v", err)
			return err
		}
	case string(model.SourceTypeFunction):
		funcMetadata, ok := metadata.(*model.FunctionMetadataDB)
		if !ok {
			return fmt.Errorf("invalid metadata type for Function: %T", metadata)
		}
		now := time.Now().UnixNano()
		funcMetadata.UpdateTime = now
		funcMetadata.Path = interfaces.SetAOIFuncExecPath(funcMetadata.Version)
		err = m.FuncMetadataDB.UpdateByVersion(ctx, tx, funcMetadata)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("update Function metadata failed, err: %v", err)
			return err
		}
	default:
		return fmt.Errorf("unsupported metadata type: %s", metadata.GetType())
	}
	return nil
}

// DeleteMetadata Delete metadata.
func (m *metadataService) DeleteMetadata(ctx context.Context, tx *sql.Tx, metadataType interfaces.MetadataType, version string) error {
	switch metadataType {
	case interfaces.MetadataTypeAPI:
		err := m.APIMetadataDB.DeleteByVersion(ctx, tx, version)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("delete API metadata failed, err: %v", err)
			return err
		}
	case interfaces.MetadataTypeFunc:
		err := m.FuncMetadataDB.DeleteByVersion(ctx, tx, version)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("delete Function metadata failed, err: %v", err)
			return err
		}
	default:
		return fmt.Errorf("unsupported metadata type: %s", metadataType)
	}
	return nil
}

// BatchDeleteMetadata Batch delete metadata.
func (m *metadataService) BatchDeleteMetadata(ctx context.Context, tx *sql.Tx, metadataType interfaces.MetadataType, versions []string) error {
	if len(versions) == 0 {
		return nil
	}
	versions = utils.UniqueStrings(versions)
	switch metadataType {
	case interfaces.MetadataTypeAPI:
		err := m.APIMetadataDB.DeleteByVersions(ctx, tx, versions)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("batch delete API metadata failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("batch delete API metadata failed, err: %v", err))
			return err
		}
	case interfaces.MetadataTypeFunc:
		err := m.FuncMetadataDB.DeleteByVersions(ctx, tx, versions)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("batch delete Function metadata failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("batch delete Function metadata failed, err: %v", err))
			return err
		}
	default:
		return errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("unsupported metadata type: %s", metadataType))
	}
	return nil
}

// ValidateMetadata validates metadata format.
func (m *metadataService) ValidateMetadata(ctx context.Context, metadata interfaces.IMetadataDB) error {
	if metadata == nil {
		return fmt.Errorf("metadata is nil")
	}
	return metadata.Validate(ctx)
}
