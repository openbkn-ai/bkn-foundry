import ipaddress
import logging
import os
import sys

from app.utils.observability.observability_setting import ServerInfo, ObservabilitySetting, LogSetting, TraceSetting
import aiohttp


class BaseConfig(object):
    DEBUGDEFAULT = False
    aiohttp_timeout = aiohttp.ClientTimeout(
        total=1800,  # Overall timeout
        sock_connect=30  # Connection-establishment timeout
    )
    DIPHOSTDEFAULT = "10.4.134.253"
    PORTDEFAULT = 9898
    # Relational database defaults.
    RDSHOSTDEFAULT = DIPHOSTDEFAULT
    RDSPORTDEFAULT = 3330
    RDSDBNAMEDEFAULT = 'openbkn'
    RDSUSERDEFAULT = 'root'
    RDSPASSDEFAULT = 'password'

    # Redis defaults.
    REDISCLUSTERMODEDEFAULT = "master-slave"
    # Redis Sentinel.
    SENTINELMASTERDEFAULT = DIPHOSTDEFAULT
    SENTINELUSERDEFAULT = "root"
    SENTINELPASSDEFAULT = "password"
    REDISREADHOSTDEFAULT = DIPHOSTDEFAULT
    REDISREADPORTDEFAULT = 6379
    REDISREADUSERDEFAULT = 'root'
    REDISREADPASSDEFAULT = 'password'

    REDISWRITEHOSTDEFAULT = DIPHOSTDEFAULT
    REDISWRITEPORTDEFAULT = 6379
    REDISWRITEUSERDEFAULT = 'root'
    REDISWRITEPASSDEFAULT = 'password'
    # Redis primary/replica mode.
    REDISHOSTDEFAULT = DIPHOSTDEFAULT
    REDISPORTDEFAULT = 6379
    REDISUSERDEFAULT = 'root'
    REDISPASSDEFAULT = 'password'
    # Authentication services.
    OAUTHADMINHOSTDEFAULT = DIPHOSTDEFAULT
    OAUTHADMINPORTDEFAULT = 4445
    USERMANAGEMENTPRIVATEHOSTDEFAULT = DIPHOSTDEFAULT
    USERMANAGEMENTPRIVATEPORTDEFAULT = 30980

    # Resource authorization service.
    AUTHORIZATIONPRIVATEHOSTDEFAULT = DIPHOSTDEFAULT
    AUTHORIZATIONPRIVATEPORTDEFAULT = 30920

    # Kafka defaults.
    KAFKAHOSTDEFAULT = DIPHOSTDEFAULT
    KAFKAPORTDEFAULT = 9097
    KAFKAUSERDEFAULT = "anyrobot"
    KAFKAPASSDEFAULT = "password"
    # Application settings.
    APP_PORT = int(os.getenv('PORT', PORTDEFAULT))
    DEBUG = True if os.getenv("DEBUG") else DEBUGDEFAULT
    LOG_LEVEL = logging.debug if DEBUG else logging.info

    # Relational database settings.
    RDSHOST = os.getenv("RDSHOST", RDSHOSTDEFAULT)
    RDSPORT = int(os.getenv("RDSPORT", RDSPORTDEFAULT))
    RDSDBNAME = os.getenv("RDSDBNAME", RDSDBNAMEDEFAULT)
    RDSUSER = os.getenv("RDSUSER", RDSUSERDEFAULT)
    RDSPASS = os.getenv("RDSPASS", RDSPASSDEFAULT)

    # Redis settings.
    REDISCLUSTERMODE = os.getenv("REDISCLUSTERMODE", REDISCLUSTERMODEDEFAULT)
    REDISHOST = os.getenv("REDISHOST", DIPHOSTDEFAULT)
    REDISPORT = os.getenv("REDISPORT", REDISPORTDEFAULT)
    REDISUSER = os.getenv("REDISUSER", REDISUSERDEFAULT)
    REDISPASS = os.getenv("REDISPASS", REDISPASSDEFAULT)
    REDISREADHOST = os.getenv("REDISREADHOST", REDISREADHOSTDEFAULT)
    REDISREADPORT = os.getenv("REDISREADPORT", REDISREADPORTDEFAULT)
    REDISREADUSER = os.getenv("REDISREADUSER", REDISREADUSERDEFAULT)
    REDISREADPASS = os.getenv("REDISREADPASS", REDISREADPASSDEFAULT)
    REDISWRITEHOST = os.getenv("REDISWRITEHOST", REDISWRITEHOSTDEFAULT)
    REDISWRITEPORT = os.getenv("REDISWRITEPORT", REDISWRITEPORTDEFAULT)
    REDISWRITEUSER = os.getenv("REDISWRITEUSER", REDISWRITEUSERDEFAULT)
    REDISWRITEPASS = os.getenv("REDISWRITEPASS", REDISWRITEPASSDEFAULT)
    SENTINELMASTER = os.getenv("SENTINELMASTER", SENTINELMASTERDEFAULT)
    SENTINELUSER = os.getenv("SENTINELUSER", SENTINELUSERDEFAULT)
    SENTINELPASS = os.getenv("SENTINELPASS", SENTINELPASSDEFAULT)
    # Authentication settings.
    OAUTHADMINHOST = os.getenv("OAUTHADMINHOST", OAUTHADMINHOSTDEFAULT)
    OAUTHADMINPORT = os.getenv("OAUTHADMINPORT", OAUTHADMINPORTDEFAULT)
    USERMANAGEMENTPRIVATEHOST = os.getenv("USERMANAGEMENTPRIVATEHOST", USERMANAGEMENTPRIVATEHOSTDEFAULT)
    USERMANAGEMENTPRIVATEPORT = os.getenv("USERMANAGEMENTPRIVATEPORT", USERMANAGEMENTPRIVATEPORTDEFAULT)
    # Resource authorization settings.
    AUTHORIZATIONPRIVATEHOST = os.getenv("AUTHORIZATIONPRIVATEHOST", AUTHORIZATIONPRIVATEHOSTDEFAULT)
    AUTHORIZATIONPRIVATEPORT = os.getenv("AUTHORIZATIONPRIVATEPORT", AUTHORIZATIONPRIVATEPORTDEFAULT)

    # Kafka settings.
    KAFKAHOST = os.getenv('KAFKAHOST', KAFKAHOSTDEFAULT)
    KAFKAPORT = os.getenv('KAFKAPORT', KAFKAPORTDEFAULT)
    KAFKAUSER = os.getenv('KAFKAUSER', KAFKAUSERDEFAULT)
    KAFKAPASS = os.getenv('KAFKAPASS', KAFKAPASSDEFAULT)

    # Metering transport: auto selects Kafka when KAFKAHOST is set, otherwise Redis.
    METERINGBACKEND = os.getenv('METERING_BACKEND', 'auto')
    METERINGREDISDB = int(os.getenv('METERING_REDIS_DB', '1'))
    METERINGSTREAMMAXLEN = int(os.getenv('METERING_STREAM_MAXLEN', '100000'))

    # Authorization switch: true enables authentication and resource filtering.
    AUTH_ENABLED = os.getenv('AUTH_ENABLED', 'false').lower() == 'true'
    # Anonymous identity used for audit correlation when authorization is disabled.
    ANONYMOUS_USER_ID = "anonymous-user"


def resolve_metering_backend():
    """Resolve the metering transport to Kafka or Redis.

    In auto mode, use the presence of the original KAFKAHOST environment
    variable. KAFKAHOSTDEFAULT is always populated and therefore cannot decide
    whether Kafka was configured explicitly. An explicit backend is used
    without a health probe.
    """
    backend = (BaseConfig.METERINGBACKEND or 'auto').strip().lower()
    if backend in ('kafka', 'redis'):
        return backend
    return 'kafka' if os.getenv('KAFKAHOST') else 'redis'


base_config = BaseConfig()
server_info = ServerInfo(
    server_name="agent-executor",
    server_version="1.0.0",
    language="python",
    python_version=sys.version,
)

observability_config = ObservabilitySetting(
    log=LogSetting(
        log_enabled=os.getenv("LOG_ENABLED", "false") == "true",
        log_exporter=os.getenv("LOG_EXPORTER", "console"),
        log_load_interval=int(os.getenv("LOG_LOAD_INTERNAL", "10")),
        log_load_max_log=int(os.getenv("LOG_LOAD_MAX_LOG", "1000")),
        http_log_feed_ingester_url=os.getenv("httpLogFeedIngesterUrl",
                                             "http://feed-ingester-service:13031/api/feed_ingester/v1/jobs/dip-o11y-log/events"),
    )
    # trace=TraceSetting(
    #     trace_enabled=os.getenv("O11Y_TRACE_ENABLED", "false") == "true",
    #     trace_provider=os.getenv("O11Y_TRACE_PROVIDER", "http"),
    #     trace_max_queue_size=int(os.getenv("O11Y_TRACE_MAX_QUEUE_SIZE", "512")),
    #     max_export_batch_size=int(os.getenv("O11Y_TRACE_MAX_EXPORT_BATCH_SIZE", "512")),
    #     http_trace_feed_ingester_url=os.getenv("O11Y_HTTP_TRACE_FEED_INGESTER_URL",
    #                                            "http://feed-ingester-service:13031/api/feed_ingester/v1/jobs/dip-o11y-trace/events"),
    # )
)
