package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"oss-gateway/internal/model"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	storageConfigPrefix     = "kweaver-core:oss-gateway-backend:storage:config:"
	storageNamePrefix       = "kweaver-core:oss-gateway-backend:storage:name:"        // storage_name uniqueness index.
	storageBucketHostPrefix = "kweaver-core:oss-gateway-backend:storage:bucket:host:" // bucket_name and host uniqueness index.
	storageBucketSitePrefix = "kweaver-core:oss-gateway-backend:storage:bucket:site:" // bucket_name and siteId uniqueness index.
	storageConfigTTL        = 1 * time.Hour
)

type StorageCache struct {
	redis *RedisClient
}

func NewStorageCache(redis *RedisClient) *StorageCache {
	return &StorageCache{redis: redis}
}

func (c *StorageCache) GetStorage(ctx context.Context, storageID string) (*model.StorageConfig, error) {
	key := storageConfigPrefix + storageID

	data, err := c.redis.Get(ctx, key)
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get storage from cache: %w", err)
	}

	var storage model.StorageConfig
	if err := json.Unmarshal([]byte(data), &storage); err != nil {
		return nil, fmt.Errorf("failed to unmarshal storage config: %w", err)
	}

	return &storage, nil
}

func (c *StorageCache) SetStorage(ctx context.Context, storage *model.StorageConfig) error {
	key := storageConfigPrefix + storage.StorageID

	data, err := json.Marshal(storage)
	if err != nil {
		return fmt.Errorf("failed to marshal storage: %w", err)
	}

	return c.redis.Set(ctx, key, data, storageConfigTTL)
}

func (c *StorageCache) DeleteStorage(ctx context.Context, storageID string) error {
	key := storageConfigPrefix + storageID
	return c.redis.Del(ctx, key)
}

func (c *StorageCache) InvalidateStorage(ctx context.Context, storageID string) error {
	return c.DeleteStorage(ctx, storageID)
}

// CheckStorageNameExists reports whether storage_name is indexed.
func (c *StorageCache) CheckStorageNameExists(ctx context.Context, storageName string) (bool, error) {
	key := storageNamePrefix + storageName
	count, err := c.redis.Exists(ctx, key)
	if err != nil {
		return false, fmt.Errorf("failed to check storage name: %w", err)
	}
	return count > 0, nil
}

// SetStorageName creates the storage_name index.
func (c *StorageCache) SetStorageName(ctx context.Context, storageName string, storageID string) error {
	key := storageNamePrefix + storageName
	return c.redis.Set(ctx, key, storageID, storageConfigTTL)
}

// DeleteStorageName removes the storage_name index.
func (c *StorageCache) DeleteStorageName(ctx context.Context, storageName string) error {
	key := storageNamePrefix + storageName
	return c.redis.Del(ctx, key)
}

// CheckBucketHostExists reports whether a bucket_name and host pair is indexed.
func (c *StorageCache) CheckBucketHostExists(ctx context.Context, bucketName string, host string) (bool, error) {
	key := storageBucketHostPrefix + bucketName + ":" + host
	count, err := c.redis.Exists(ctx, key)
	if err != nil {
		return false, fmt.Errorf("failed to check bucket and host: %w", err)
	}
	return count > 0, nil
}

// SetBucketHost creates the bucket_name and host index.
func (c *StorageCache) SetBucketHost(ctx context.Context, bucketName string, host string, storageID string) error {
	key := storageBucketHostPrefix + bucketName + ":" + host
	return c.redis.Set(ctx, key, storageID, storageConfigTTL)
}

// DeleteBucketHost removes the bucket_name and host index.
func (c *StorageCache) DeleteBucketHost(ctx context.Context, bucketName string, host string) error {
	key := storageBucketHostPrefix + bucketName + ":" + host
	return c.redis.Del(ctx, key)
}

// CheckBucketSiteExists reports whether a bucket_name and siteId pair is indexed.
func (c *StorageCache) CheckBucketSiteExists(ctx context.Context, bucketName string, siteID string) (bool, error) {
	key := storageBucketSitePrefix + bucketName + ":" + siteID
	count, err := c.redis.Exists(ctx, key)
	if err != nil {
		return false, fmt.Errorf("failed to check bucket and site: %w", err)
	}
	return count > 0, nil
}

// SetBucketSite creates the bucket_name and siteId index.
func (c *StorageCache) SetBucketSite(ctx context.Context, bucketName string, siteID string, storageID string) error {
	key := storageBucketSitePrefix + bucketName + ":" + siteID
	return c.redis.Set(ctx, key, storageID, storageConfigTTL)
}

// DeleteBucketSite removes the bucket_name and siteId index.
func (c *StorageCache) DeleteBucketSite(ctx context.Context, bucketName string, siteID string) error {
	key := storageBucketSitePrefix + bucketName + ":" + siteID
	return c.redis.Del(ctx, key)
}
