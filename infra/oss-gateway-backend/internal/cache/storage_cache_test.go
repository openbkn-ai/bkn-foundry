package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"oss-gateway/internal/model"
)

type storageCacheRedis struct {
	redis.UniversalClient
	key   string
	value string
	ttl   time.Duration
}

func (r *storageCacheRedis) Get(_ context.Context, key string) *redis.StringCmd {
	if r.key != key {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(r.value, nil)
}

func (r *storageCacheRedis) Set(_ context.Context, key string, value any, ttl time.Duration) *redis.StatusCmd {
	r.key = key
	r.value = string(value.([]byte))
	r.ttl = ttl
	return redis.NewStatusResult("OK", nil)
}

func TestStorageCacheReadsExistingJSONAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	client := &storageCacheRedis{
		key:   storageConfigPrefix + "123",
		value: `{"storage_id":"123","storage_name":"Storage \u003ctest\u003e","vendor_type":"S3","endpoint":"storage.example","bucket_name":"test-bucket","access_key_id":"test-id","access_key":"test-key","region":"","is_default":false,"is_enabled":true,"internal_endpoint":"","site_id":"","created_at":"2026-01-02T03:04:05.123456Z","updated_at":"2026-01-02T03:04:05.123456Z"}`,
	}
	storageCache := NewStorageCache(&RedisClient{client: client})
	created := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	expected := &model.StorageConfig{
		StorageID:   "123",
		StorageName: "Storage <test>",
		VendorType:  "S3",
		Endpoint:    "storage.example",
		BucketName:  "test-bucket",
		AccessKeyID: "test-id",
		AccessKey:   "test-key",
		IsEnabled:   true,
		CreatedAt:   created,
		UpdatedAt:   created,
	}

	storage, err := storageCache.GetStorage(ctx, expected.StorageID)
	require.NoError(t, err)
	require.Equal(t, expected, storage)

	storage.IsDefault = true
	require.NoError(t, storageCache.SetStorage(ctx, storage))
	require.Equal(t, storageConfigTTL, client.ttl)

	reloaded, err := storageCache.GetStorage(ctx, expected.StorageID)
	require.NoError(t, err)
	require.Equal(t, storage, reloaded)
}
