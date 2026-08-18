"""
Application configuration

Manages the application configuration with Pydantic Settings.
"""

from functools import lru_cache
from urllib.parse import quote_plus

from pydantic import Field, computed_field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Application configuration"""

    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        case_sensitive=False,
        extra="ignore",
    )

    # ============== Application ==============
    app_name: str = Field(default="Sandbox Control Plane")
    app_version: str = Field(default="2.1.0")
    environment: str = Field(default="development")
    debug: bool = Field(default=False)

    # ============== Server ==============
    host: str = Field(default="0.0.0.0")
    port: int = Field(default=8000)
    workers: int = Field(default=4)

    # ============== Database ==============
    database_url: str = Field(default="mysql+aiomysql://sandbox:password@localhost:3308/openbkn")
    db_pool_size: int = Field(default=20)
    db_max_overflow: int = Field(default=40)
    db_pool_recycle: int = Field(default=3600)

    # ============== RDS database, injected from depServices.rds ==============
    # These take precedence over database_url: when set, the connection is built from them.
    db_type: str | None = Field(default=None)  # database type, such as MYSQL or POSTGRESQL
    db_host: str | None = Field(default=None)  # primary host
    db_port: int | None = Field(default=None)  # primary port
    db_host_read: str | None = Field(default=None)  # replica host, for read/write splitting
    db_port_read: int | None = Field(default=None)  # replica port
    db_user: str | None = Field(default=None)  # database user
    db_password: str | None = Field(default=None)  # database password
    db_database: str | None = Field(default=None)  # database name
    db_max_connections: int | None = Field(default=None)  # maximum connections
    db_max_read_connections: int | None = Field(default=None)  # maximum read connections
    db_charset: str | None = Field(default=None)  # charset
    db_timeout: int | None = Field(default=None)  # connect timeout
    db_read_timeout: int | None = Field(default=None)  # read timeout
    db_write_timeout: int | None = Field(default=None)  # write timeout

    @computed_field
    @property
    def effective_database_url(self) -> str:
        """
        Resolve the effective database URL

        The RDS environment variables win; database_url applies when they are unset.
        """
        # Use the RDS configuration once every required field is set
        if all(
            [
                self.db_type,
                self.db_host,
                self.db_port is not None,
                self.db_user,
                self.db_password is not None,
                self.db_database,
            ]
        ):
            # Build the aiomysql connection URL,
            # formatted as mysql+aiomysql://user:password@host:port/database
            user = quote_plus(self.db_user)
            password = quote_plus(self.db_password)
            host = self.db_host
            port = self.db_port
            database = self.db_database

            # Append the connection parameters
            params = []
            if self.db_charset:
                params.append(f"charset={self.db_charset}")

            url = f"mysql+aiomysql://{user}:{password}@{host}:{port}/{database}"
            if params:
                url += "?" + "&".join(params)

            return url

        # Otherwise fall back to database_url
        return self.database_url

    # ============== S3 ==============
    s3_bucket: str = Field(default="sandbox-workspace")
    s3_region: str = Field(default="us-east-1")
    s3_access_key_id: str = Field(default="")
    s3_secret_access_key: str = Field(default="")
    s3_endpoint_url: str = Field(default="")

    # ============== Docker ==============
    docker_host: str = Field(default="unix:///var/run/docker.sock")
    docker_tls_verify: bool = Field(default=False)
    docker_cert_path: str = Field(default="")

    # ============== Kubernetes ==============
    kubernetes_namespace: str = Field(default="sandbox-runtime")
    executor_image_pull_policy: str = Field(default="IfNotPresent")
    executor_image_pull_secrets: str = Field(default="")

    # ============== Execution ==============
    default_timeout: int = Field(default=300)
    max_timeout: int = Field(default=3600)
    default_cpu: str = Field(default="1")
    default_memory: str = Field(default="512Mi")
    default_disk: str = Field(default="1Gi")
    default_template_id: str = Field(default="python-basic")
    default_multi_language_template_image: str = Field(default="")
    max_upload_file_size_mb: int = Field(default=100, ge=1)
    max_extracted_file_count: int = Field(default=10000, ge=1)
    max_extracted_total_size_mb: int = Field(default=512, ge=1)
    disable_bwrap: bool = Field(default=False)  # turn Bubblewrap off, for local development
    # The in-cluster MCP address sandbox_sdk.bkn calls back into BKN with. It is deployment
    # configuration rather than a secret, so the control plane injects it once and no caller
    # has to pass it in the event. Left empty, callers must pass mcp themselves.
    # The token takes the other path: event only, never an environment variable — sessions are
    # pooled and reused, and env would leave the previous caller's credential in the container.
    bkn_sandbox_mcp_url: str = Field(default="")
    control_plane_url: str | None = Field(
        default=None
    )  # Control Plane URL for executor callback (None = auto-generate from namespace)

    # ============== Cleanup ==============
    idle_threshold_minutes: int = Field(
        default=-1, ge=-1, description="Idle timeout in minutes; -1 means never, disabling idle cleanup"
    )
    max_lifetime_hours: int = Field(
        default=-1, ge=-1, description="Maximum lifetime in hours; -1 means never"
    )
    cleanup_interval_seconds: int = Field(default=300, ge=1)
    creating_timeout_seconds: int = Field(
        default=300,
        ge=30,
        description="Session creation timeout in seconds; a session left in creating past this is marked failed",
    )

    # ============== Retry ==============
    max_retry_attempts: int = Field(default=3)
    retry_backoff_base: float = Field(default=1.0)
    retry_backoff_factor: float = Field(default=2.0)
    max_retry_backoff: float = Field(default=10.0)

    # ============== Warm pool ==============
    warm_pool_enabled: bool = Field(default=True)
    warm_pool_default_size: int = Field(default=10)
    warm_pool_min_size: int = Field(default=5)
    warm_pool_max_idle_time: int = Field(default=300)

    # ============== Health check ==============
    health_check_interval_seconds: int = Field(default=10)
    heartbeat_interval_seconds: int = Field(default=5)
    heartbeat_timeout_seconds: int = Field(default=15)

    # ============== Logging ==============
    log_level: str = Field(default="INFO")
    log_format: str = Field(default="text")  # json, text (default: text for human-readable)

    @field_validator("log_level")
    @classmethod
    def validate_log_level(cls, v: str) -> str:
        allowed = {"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}
        if v.upper() not in allowed:
            raise ValueError(f"log_level must be one of {allowed}")
        return v.upper()

    @field_validator("log_format")
    @classmethod
    def validate_log_format(cls, v: str) -> str:
        allowed = {"json", "text"}
        if v not in allowed:
            raise ValueError(f"log_format must be one of {allowed}")
        return v

    # ============== Monitoring ==============
    metrics_enabled: bool = Field(default=True)
    metrics_port: int = Field(default=9090)

    # ============== Security ==============
    secret_key: str = Field(default="change-this-in-production")
    allowed_hosts: list[str] = Field(default=["*"])
    cors_origins: list[str] = Field(default=["http://localhost:3000"])

    # ============== Rate limiting ==============
    rate_limit_enabled: bool = Field(default=True)
    rate_limit_per_minute: int = Field(default=60)

    @field_validator("environment")
    @classmethod
    def validate_environment(cls, v: str) -> str:
        allowed = {"development", "staging", "production"}
        if v not in allowed:
            raise ValueError(f"environment must be one of {allowed}")
        return v


@lru_cache
def get_settings() -> Settings:
    """
    Get the configuration singleton

    lru_cache makes sure it loads only once.
    """
    return Settings()
