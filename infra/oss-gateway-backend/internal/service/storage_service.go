package service

import (
	"context"
	"fmt"
	"oss-gateway/internal/cache"
	"oss-gateway/internal/config"
	"oss-gateway/internal/model"
	"oss-gateway/internal/repository"
	"oss-gateway/pkg/adapter"
	"oss-gateway/pkg/crypto"
	"oss-gateway/pkg/errors"
	"oss-gateway/pkg/utils"
	"strings"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// StorageValidationError carries a stable public code and localization data.
type StorageValidationError struct {
	Code   string
	Params map[string]interface{}
	Err    error
}

func (e *StorageValidationError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("storage validation failed: code=%s: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("storage validation failed: code=%s", e.Code)
}

func (e *StorageValidationError) Unwrap() error {
	return e.Err
}

type StorageService interface {
	Create(ctx context.Context, req *CreateStorageRequest) (string, error)
	Update(ctx context.Context, storageID string, req *UpdateStorageRequest) error
	Delete(ctx context.Context, storageID string) error
	Get(ctx context.Context, storageID string) (*StorageResponse, error)
	List(ctx context.Context, req *ListStorageRequest) (*ListStorageResponse, error)
	CheckConnection(ctx context.Context, storageID string) error
	GetAdapter(ctx context.Context, storageID string, useInternal bool) (adapter.OSSAdapter, error)
}

// ListStorageRequest follows the Python FastAPI pagination contract.
type ListStorageRequest struct {
	Page       int    `form:"page"`        // Page number, starting at 1; defaults to 1.
	Size       int    `form:"size"`        // Page size; defaults to 10 and is capped at 1000.
	Order      string `form:"order"`       // Sort direction: asc or desc; defaults to desc.
	Rule       string `form:"rule"`        // Sort field; defaults to update_time.
	Name       string `form:"name"`        // Fuzzy storage-name filter.
	VendorType string `form:"vendor_type"` // Storage vendor filter.
	Enabled    *bool  `form:"enabled"`     // Enabled-state filter.
	IsDefault  *bool  `form:"is_default"`  // Default-storage filter.
}

// ListStorageResponse is the paginated storage response.
type ListStorageResponse struct {
	Count int                `json:"count"` // Total number of matching records.
	Data  []*StorageResponse `json:"data"`  // Records in the current page.
}

type storageService struct {
	repo         repository.StorageRepository
	crypto       *crypto.AESCrypto
	storageCache *cache.StorageCache
	config       *config.AppConfig
	log          *logrus.Entry
}

type CreateStorageRequest struct {
	StorageName      string `json:"storage_name" binding:"required"`
	VendorType       string `json:"vendor_type" binding:"required"`
	Endpoint         string `json:"endpoint" binding:"required"`
	BucketName       string `json:"bucket_name" binding:"required"`
	AccessKeyID      string `json:"access_key_id" binding:"required"`
	AccessKeySecret  string `json:"access_key_secret" binding:"required"`
	Region           string `json:"region"` // Required for OSS, OBS, and TOS; optional for ECEPH.
	IsDefault        bool   `json:"is_default"`
	InternalEndpoint string `json:"internal_endpoint"`
	SiteID           string `json:"site_id"` // Used with bucket_name for uniqueness checks.
}

type UpdateStorageRequest struct {
	StorageName      string `json:"storage_name"`
	Endpoint         string `json:"endpoint"`
	BucketName       string `json:"bucket_name"`
	AccessKeyID      string `json:"access_key_id"`
	AccessKeySecret  string `json:"access_key_secret"`
	Region           string `json:"region"`
	IsDefault        *bool  `json:"is_default"`
	IsEnabled        *bool  `json:"is_enabled"`
	InternalEndpoint string `json:"internal_endpoint"`
}

type StorageResponse struct {
	StorageID        string `json:"storage_id"`
	StorageName      string `json:"storage_name"`
	VendorType       string `json:"vendor_type"`
	Endpoint         string `json:"endpoint"`
	BucketName       string `json:"bucket_name"`
	Region           string `json:"region"`
	IsDefault        bool   `json:"is_default"`
	IsEnabled        bool   `json:"is_enabled"`
	InternalEndpoint string `json:"internal_endpoint"`
	SiteID           string `json:"site_id"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

func NewStorageService(repo repository.StorageRepository, crypto *crypto.AESCrypto, storageCache *cache.StorageCache, config *config.AppConfig, log *logrus.Entry) StorageService {
	return &storageService{
		repo:         repo,
		crypto:       crypto,
		storageCache: storageCache,
		config:       config,
		log:          log,
	}
}

func (s *storageService) Create(ctx context.Context, req *CreateStorageRequest) (string, error) {
	if !s.isValidVendorType(req.VendorType) {
		return "", &StorageValidationError{
			Code:   errors.InvalidVendorType.Code,
			Params: map[string]interface{}{"VendorType": req.VendorType},
		}
	}

	// Region is required for OSS, OBS, and TOS, but optional for ECEPH.
	if (req.VendorType == "OSS" || req.VendorType == "OBS" || req.VendorType == "TOS") && req.Region == "" {
		return "", &StorageValidationError{
			Code:   errors.InvalidParam.Code,
			Params: map[string]interface{}{"Parameter": "region"},
		}
	}

	if !strings.HasPrefix(req.Endpoint, "http://") && !strings.HasPrefix(req.Endpoint, "https://") {
		return "", &StorageValidationError{Code: errors.InvalidEndpoint.Code}
	}

	// Uniqueness is enforced by the database; Redis only accelerates lookups.
	// Check storage_name uniqueness first.
	nameExists, err := s.repo.ExistsByStorageName(ctx, req.StorageName)
	if err != nil {
		s.log.WithError(err).Error("failed to check storage name in database")
		return "", fmt.Errorf("failed to check storage name uniqueness")
	}
	if nameExists {
		return "", &StorageValidationError{
			Code:   errors.StorageNameExists.Code,
			Params: map[string]interface{}{"StorageName": req.StorageName},
		}
	}

	// Check bucket_name and endpoint uniqueness.
	bucketEndpointExists, err := s.repo.ExistsByBucketAndEndpoint(ctx, req.BucketName, req.Endpoint)
	if err != nil {
		s.log.WithError(err).Error("failed to check bucket+endpoint in database")
		return "", fmt.Errorf("failed to check bucket and endpoint uniqueness")
	}
	if bucketEndpointExists {
		return "", &StorageValidationError{
			Code: errors.StorageExists.Code,
			Params: map[string]interface{}{
				"Bucket":   req.BucketName,
				"Location": req.Endpoint,
			},
		}
	}

	// Check bucket_name and siteId uniqueness when a site is supplied.
	if req.SiteID != "" {
		bucketSiteExists, err := s.repo.ExistsByBucketAndSiteID(ctx, req.BucketName, req.SiteID)
		if err != nil {
			s.log.WithError(err).Error("failed to check bucket+siteId in database")
			return "", fmt.Errorf("failed to check bucket and siteId uniqueness")
		}
		if bucketSiteExists {
			return "", &StorageValidationError{
				Code: errors.StorageExists.Code,
				Params: map[string]interface{}{
					"Bucket":   req.BucketName,
					"Location": req.SiteID,
				},
			}
		}
	}

	encryptedKeyID, err := s.crypto.Encrypt(req.AccessKeyID)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt access_key_id: %w", err)
	}

	encryptedSecret, err := s.crypto.Encrypt(req.AccessKeySecret)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt access_key_secret: %w", err)
	}

	storageID := utils.GenerateStorageID()

	// Reject a second default storage.
	if req.IsDefault {
		existingDefault, err := s.repo.HasDefaultStorage(ctx, "")
		if err == nil && existingDefault != nil {
			return "", &StorageValidationError{
				Code:   errors.DefaultStorageExists.Code,
				Params: map[string]interface{}{"StorageName": existingDefault.StorageName},
			}
		}
		// gorm.ErrRecordNotFound means no default exists and creation can continue.
	}

	storage := &model.StorageConfig{
		StorageID:        storageID,
		StorageName:      req.StorageName,
		VendorType:       req.VendorType,
		Endpoint:         req.Endpoint,
		BucketName:       req.BucketName,
		AccessKeyID:      encryptedKeyID,
		AccessKey:        encryptedSecret,
		Region:           req.Region,
		IsDefault:        req.IsDefault,
		IsEnabled:        true,
		InternalEndpoint: req.InternalEndpoint,
		SiteID:           req.SiteID,
	}

	if err := s.repo.Create(ctx, storage); err != nil {
		return "", fmt.Errorf("failed to create storage: %w", err)
	}

	// Cache the new record to accelerate subsequent reads.
	if err := s.storageCache.SetStorage(ctx, storage); err != nil {
		s.log.WithError(err).Warn("failed to cache storage config")
	}

	return storageID, nil
}

func (s *storageService) Update(ctx context.Context, storageID string, req *UpdateStorageRequest) error {
	storage, err := s.repo.GetByID(ctx, storageID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return &StorageValidationError{Code: errors.StorageNotFound.Code}
		}
		return err
	}

	if req.StorageName != "" {
		// Ensure the updated name does not conflict with another storage.
		if req.StorageName != storage.StorageName {
			nameExists, err := s.repo.ExistsByStorageName(ctx, req.StorageName)
			if err != nil {
				s.log.WithError(err).Error("failed to check storage name in database")
				return fmt.Errorf("failed to check storage name uniqueness")
			}
			if nameExists {
				return &StorageValidationError{
					Code:   errors.StorageNameExists.Code,
					Params: map[string]interface{}{"StorageName": req.StorageName},
				}
			}
		}
		storage.StorageName = req.StorageName
	}
	if req.Endpoint != "" {
		if !strings.HasPrefix(req.Endpoint, "http://") && !strings.HasPrefix(req.Endpoint, "https://") {
			return &StorageValidationError{Code: errors.InvalidEndpoint.Code}
		}
		storage.Endpoint = req.Endpoint
	}
	if req.BucketName != "" {
		storage.BucketName = req.BucketName
	}
	if req.AccessKeyID != "" {
		encrypted, err := s.crypto.Encrypt(req.AccessKeyID)
		if err != nil {
			return fmt.Errorf("failed to encrypt access_key_id: %w", err)
		}
		storage.AccessKeyID = encrypted
	}
	if req.AccessKeySecret != "" {
		encrypted, err := s.crypto.Encrypt(req.AccessKeySecret)
		if err != nil {
			return fmt.Errorf("failed to encrypt access_key_secret: %w", err)
		}
		storage.AccessKey = encrypted
	}
	if req.Region != "" {
		storage.Region = req.Region
	}
	if req.IsDefault != nil {
		// Reject an update that would create a second default storage.
		if *req.IsDefault {
			existingDefault, err := s.repo.HasDefaultStorage(ctx, storageID)
			if err == nil && existingDefault != nil {
				return &StorageValidationError{
					Code:   errors.DefaultStorageExists.Code,
					Params: map[string]interface{}{"StorageName": existingDefault.StorageName},
				}
			}
			// gorm.ErrRecordNotFound means no other default exists.
		}
		storage.IsDefault = *req.IsDefault
	}
	if req.IsEnabled != nil {
		storage.IsEnabled = *req.IsEnabled
	}
	if req.InternalEndpoint != "" {
		storage.InternalEndpoint = req.InternalEndpoint
	}

	if err := s.repo.Update(ctx, storage); err != nil {
		return err
	}

	if err := s.storageCache.InvalidateStorage(ctx, storageID); err != nil {
		s.log.WithError(err).Warn("failed to invalidate storage cache")
	}

	return nil
}

func (s *storageService) Delete(ctx context.Context, storageID string) error {
	if err := s.repo.Delete(ctx, storageID); err != nil {
		return err
	}

	if err := s.storageCache.InvalidateStorage(ctx, storageID); err != nil {
		s.log.WithError(err).Warn("failed to invalidate storage cache")
	}

	return nil
}

func (s *storageService) Get(ctx context.Context, storageID string) (*StorageResponse, error) {
	storage, err := s.repo.GetByID(ctx, storageID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, &StorageValidationError{Code: errors.StorageNotFound.Code}
		}
		return nil, err
	}

	return s.toResponse(storage), nil
}

func (s *storageService) List(ctx context.Context, req *ListStorageRequest) (*ListStorageResponse, error) {
	// Apply pagination defaults.
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Size <= 0 {
		req.Size = 10
	}
	if req.Size > 1000 {
		req.Size = 1000
	}
	if req.Order == "" {
		req.Order = "desc"
	}
	if req.Rule == "" {
		req.Rule = "update_time"
	}

	// Validate sorting parameters before building a query.
	validOrders := map[string]bool{"asc": true, "desc": true}
	if !validOrders[req.Order] {
		return nil, &StorageValidationError{
			Code:   errors.InvalidParam.Code,
			Params: map[string]interface{}{"Parameter": "order"},
		}
	}

	validRules := map[string]bool{"create_time": true, "update_time": true, "storage_name": true}
	if !validRules[req.Rule] {
		return nil, &StorageValidationError{
			Code:   errors.InvalidParam.Code,
			Params: map[string]interface{}{"Parameter": "rule"},
		}
	}

	// Query the database.
	storages, total, err := s.repo.ListWithPagination(ctx, req.VendorType, req.Enabled, req.IsDefault, req.Name, req.Page, req.Size, req.Order, req.Rule)
	if err != nil {
		return nil, err
	}

	responses := make([]*StorageResponse, 0, len(storages))
	for _, storage := range storages {
		responses = append(responses, s.toResponse(storage))
	}

	return &ListStorageResponse{
		Count: total,
		Data:  responses,
	}, nil
}

func (s *storageService) CheckConnection(ctx context.Context, storageID string) error {
	ossAdapter, err := s.GetAdapter(ctx, storageID, false)
	if err != nil {
		return err
	}

	return ossAdapter.CheckConnection(ctx)
}

func (s *storageService) GetAdapter(ctx context.Context, storageID string, useInternal bool) (adapter.OSSAdapter, error) {
	// Try to get from cache first
	storage, err := s.storageCache.GetStorage(ctx, storageID)
	if err != nil {
		s.log.WithError(err).Warn("failed to get storage from cache")
	}

	// If not in cache, get from data store
	if storage == nil {
		storage, err = s.repo.GetByID(ctx, storageID)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil, &StorageValidationError{Code: errors.StorageNotFound.Code}
			}
			return nil, err
		}

		// Cache the storage config
		if err := s.storageCache.SetStorage(ctx, storage); err != nil {
			s.log.WithError(err).Warn("failed to cache storage config")
		}
	}

	if !storage.IsEnabled {
		return nil, &StorageValidationError{Code: errors.StorageDisabled.Code}
	}

	// Record decryption diagnostics without exposing credentials.
	s.log.Infof("Decrypting storage %s", storageID)
	s.log.Infof("AccessKeyID length from DB: %d", len(storage.AccessKeyID))
	s.log.Infof("AccessKey length from DB: %d", len(storage.AccessKey))

	// Treat values as plaintext when legacy records cannot be decrypted.
	accessKeyID := storage.AccessKeyID
	if decrypted, err := s.crypto.Decrypt(storage.AccessKeyID); err == nil {
		accessKeyID = decrypted
		s.log.Infof("✅ AccessKeyID decrypted successfully, result length: %d", len(accessKeyID))
	} else {
		s.log.WithError(err).Warnf("⚠️ Failed to decrypt access_key_id, using as plaintext")
	}

	accessKeySecret := storage.AccessKey
	if decrypted, err := s.crypto.Decrypt(storage.AccessKey); err == nil {
		accessKeySecret = decrypted
		s.log.Infof("✅ AccessKey decrypted successfully, result length: %d", len(accessKeySecret))
	} else {
		s.log.WithError(err).Warnf("⚠️ Failed to decrypt access_key_secret, using as plaintext")
	}

	endpoint := storage.Endpoint
	if useInternal && storage.InternalEndpoint != "" {
		endpoint = storage.InternalEndpoint
	}

	endpointClean, useSSL := utils.ParseEndpoint(endpoint)

	adapterConfig := adapter.StorageConfig{
		StorageID:       storage.StorageID,
		VendorType:      adapter.VendorType(storage.VendorType),
		Endpoint:        endpointClean,
		BucketName:      storage.BucketName,
		AccessKeyID:     accessKeyID,
		AccessKeySecret: accessKeySecret,
		Region:          storage.Region,
		UseSSL:          useSSL,
	}

	// Note: Adapter instances are created on-demand, not cached
	// This ensures fresh connections and avoids connection pooling issues
	return adapter.NewAdapter(adapterConfig)
}

func (s *storageService) toResponse(storage *model.StorageConfig) *StorageResponse {
	return &StorageResponse{
		StorageID:        storage.StorageID,
		StorageName:      storage.StorageName,
		VendorType:       storage.VendorType,
		Endpoint:         storage.Endpoint,
		BucketName:       storage.BucketName,
		Region:           storage.Region,
		IsDefault:        storage.IsDefault,
		IsEnabled:        storage.IsEnabled,
		InternalEndpoint: storage.InternalEndpoint,
		SiteID:           storage.SiteID,
		CreatedAt:        storage.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:        storage.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *storageService) isValidVendorType(vendorType string) bool {
	switch vendorType {
	case "OSS", "OBS", "ECEPH", "TOS":
		return true
	default:
		return false
	}
}
