# OSS Gateway Backend

A unified object storage gateway service supporting multiple cloud providers (Alibaba Cloud OSS, Huawei Cloud OBS, Ceph S3).

## Features

- **Multi-vendor Support**: Alibaba Cloud OSS, Huawei Cloud OBS, Ceph S3
- **Unified API**: Single API for all object storage operations
- **Presigned URLs**: Generate presigned URLs for direct client access
- **Multipart Upload**: Support for large file uploads with multipart
- **Database Compatibility**: MariaDB/MySQL and DM8 support
- **Redis Cache**: Distributed cache for multi-instance deployment
- **High Performance**: Redis cache provides 10-100x performance improvement

## Architecture

```
┌─────────────────┐
│   Client/Frontend    │
└────────┬────────┘
         │ ① Request presigned URL
         ▼
┌─────────────────────────┐
│   Golang OSS Gateway    │
│  ├── Storage Management  │
│  ├── URL Generation      │
│  └── Vendor Adapters     │
└─────────┬───────────────┘
          │ ② Return presigned URL
          ▼
┌─────────────────┐
│   Client/Frontend    │ ③ Direct upload/download
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│          Object Storage Services         │
│  ┌─────────┐ ┌─────────┐ ┌─────────┐   │
│  │Aliyun OSS│ │Huawei OBS│ │  Ceph   │   │
│  └─────────┘ └─────────┘ └─────────┘   │
└─────────────────────────────────────────┘
```

## Quick Start

### Prerequisites

- Go 1.25+
- MariaDB/MySQL database
- Redis 6.0+ (for multi-instance deployment)
- Object storage account (OSS/OBS/Ceph)

### Installation

1. Clone the repository
```bash
cd oss-gateway-backend
```

2. Copy environment file
```bash
cp .env.example .env
```

3. Edit `.env` file with your configuration

4. Install dependencies
```bash
go mod download
```

5. Initialize database
```bash
go run main.go initdb
```

6. Start server
```bash
go run main.go server
```

## Configuration

Configuration precedence: **environment variables > local.go > defaults**.

### Method 1: Modify the local configuration file, recommended for development

Edit the `GetLocalConfig()` function in `internal/config/local.go`:

```go
func GetLocalConfig() *LocalConfig {
    return &LocalConfig{
        RedisClusterMode: "standalone",
        RedisHost:        "localhost",
        RedisPort:        "6379",
        // ... modify other configuration
    }
}
```

### Method 2: Use environment variables

Copy `.env.example` to `.env.debug` and modify it:

```bash
cp .env.example .env.debug
```

### Method 3: Set environment variables directly

```bash
export REDISCLUSTERMODE=standalone
export REDISHOST=localhost
export REDISPORT=6379
go run main.go server
```

### Complete Configuration Reference

See the [environment variable configuration document](./ENVIRONMENT_VARIABLES.md), which covers:

- Redis configuration for three modes: standalone, master-slave, and sentinel
- K8s ConfigMap/Secret examples
- Environment-variable compatibility with Python projects

### Main Configuration Items

#### Server Configuration
- `PORT`: Server port (default: 8080)
- `NAME`: Service name (default: oss-gateway)

#### Database Configuration
- `RDSHOST`: Database address
- `RDSPORT`: Database port
- `RDSUSER`: Database username
- `RDSPASS`: Database password
- `RDSDBNAME`: Database name
- `DB_TYPE`: Database type (MYSQL, DM8, KDB9)

#### Redis Configuration, with environment-variable names aligned with Python projects

**Mode selection:**

- `REDISCLUSTERMODE`: Redis mode (standalone/master-slave/sentinel)

**Standalone mode:**

- `REDISHOST`: Redis address
- `REDISPORT`: Redis port
- `REDISUSER`: Redis username, optional
- `REDISPASS`: Redis password, optional
- `REDIS_DB`: Redis database index

**Master-slave mode:**

- `REDISREADHOST`, `REDISREADPORT`, `REDISREADUSER`, `REDISREADPASS`: Read-node configuration
- `REDISWRITEHOST`, `REDISWRITEPORT`, `REDISWRITEUSER`, `REDISWRITEPASS`: Write-node configuration

**Sentinel mode:**

- `REDIS_SENTINEL_ADDRS`: Comma-separated sentinel address list
- `SENTINELMASTER`: Master node name
- `SENTINELUSER`: Sentinel username, optional
- `SENTINELPASS`: Sentinel password, optional

> **Note**: Database and Redis environment-variable names are aligned with the `mf-model-api` Python project for
> unified K8s management.

#### OSS Configuration
- `OSS_DEFAULT_VALID_SECONDS`: Default URL expiration time (default: 3600)
- `OSS_MIN_PART_SIZE`: Minimum part size for multipart upload (default: 5MB)
- `OSS_MAX_PART_SIZE`: Maximum part size for multipart upload (default: 5GB)

## Multi-Instance Deployment

For production multi-instance deployment with Redis:

1. **Redis supports three modes**:
   - **Standalone mode**: suitable for development and small-scale production
   - **Master-slave mode**: supports read/write splitting
   - **Sentinel mode**: supports high availability and automatic failover

2. **Configure the Redis connection**, with environment-variable names aligned with Python projects:
   ```bash
   # Standalone mode
   REDISCLUSTERMODE=standalone
   REDISHOST=your-redis-host
   REDISPORT=6379
   REDISPASS=your-redis-password
   
   # Master-slave mode
   REDISCLUSTERMODE=master-slave
   REDISREADHOST=redis-slave-host
   REDISWRITEHOST=redis-master-host
   
   # Sentinel mode
   REDISCLUSTERMODE=sentinel
   REDIS_SENTINEL_ADDRS=sentinel1:26379,sentinel2:26379
   SENTINELMASTER=mymaster
   ```

3. All instances share the same Redis and database
4. Cache hit rate >95% for high-concurrency scenarios

For detailed configuration, see [ENVIRONMENT_VARIABLES.md](./ENVIRONMENT_VARIABLES.md).  
For the cache architecture, see [REDIS_CACHE_ARCHITECTURE.md](./REDIS_CACHE_ARCHITECTURE.md).

## API Documentation

### Health Check
- `GET /health/ready` - Readiness probe
- `GET /health/alive` - Liveness probe

### Storage Management
- `GET /api/v1/storages` - List all storages
- `GET /api/v1/storages/:id` - Get storage by ID
- `POST /api/v1/storages` - Create new storage
- `PUT /api/v1/storages/:id` - Update storage
- `DELETE /api/v1/storages/:id` - Delete storage
- `POST /api/v1/storages/:id/check` - Check storage connection

### Object Operations
- `GET /api/v1/head/:storageId/:key` - Get object metadata URL
- `POST /api/v1/head/:storageId` - Batch get object metadata URLs
- `GET /api/v1/upload/:storageId/:key` - Get upload URL
- `GET /api/v1/download/:storageId/:key` - Get download URL
- `GET /api/v1/delete/:storageId/:key` - Get delete URL

### Multipart Upload
- `GET /api/v1/initmultiupload/:storageId/:key` - Initialize multipart upload
- `POST /api/v1/uploadpart/:storageId/:key` - Get part upload URLs
- `POST /api/v1/completeupload/:storageId/:key` - Complete multipart upload

## Database Schema

### storage_config
Stores object storage configuration.

### multipart_upload_task
Tracks multipart upload tasks.

### operation_log
Logs all operations for audit.

## Development

### Project Structure

```
oss-gateway-backend/
├── cmd/
│   ├── server/         # Server entry point
│   └── initdb/         # Database initialization
├── internal/
│   ├── config/         # Configuration management
│   ├── database/       # Database connection
│   ├── handler/        # HTTP handlers
│   ├── middleware/     # HTTP middleware
│   ├── model/          # Data models
│   ├── repository/     # Data access layer
│   ├── router/         # Route configuration
│   └── service/        # Business logic
├── pkg/
│   ├── adapter/        # Storage adapters
│   ├── crypto/         # Encryption utilities
│   ├── response/       # Response utilities
│   └── utils/          # Common utilities
└── migrations/         # Database migrations
    ├── mariadb/
    └── dm8/
```

## License

Copyright © 2024
