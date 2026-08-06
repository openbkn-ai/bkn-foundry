import os


def _env(name: str, default: str) -> str:
    v = os.getenv(name)
    return v if v not in (None, "") else default


class Config:
    HOST = _env("BKN_AGENT_HOST", "0.0.0.0")
    PORT = int(_env("BKN_AGENT_PORT", "30800"))

    # 共享 openbkn 库（平台 RDS* 约定）
    RDS_HOST = _env("RDSHOST", "127.0.0.1")
    RDS_PORT = int(_env("RDSPORT", "3306"))
    RDS_DBNAME = _env("RDSDBNAME", "openbkn")
    RDS_USER = _env("RDSUSER", "root")
    RDS_PASS = _env("RDSPASS", "password")

    # 模型面：mf-model-api 集群内私有路由（OpenAI 兼容）。model 为空 → 系统默认模型。
    MF_MODEL_API_PRIVATE_BASE = _env(
        "MF_MODEL_API_PRIVATE_BASE",
        "http://mf-model-api:9898/api/private/mf-model-api/v1",
    )
    DEFAULT_MODEL = _env("BKN_AGENT_DEFAULT_MODEL", "")

    # 算子工厂（operator-integration）：工具面与技能面统一走这里的 internal-v1
    # （#322 把技能面从 capabilities-lab 收敛过来）
    OPERATOR_INTEGRATION_BASE = _env("OPERATOR_INTEGRATION_BASE", "http://agent-operator-integration:9000/api/agent-operator-integration")

    # BKN Trace phase-two evidence ingestion. Empty URL = construct evidence facts
    # locally but do not submit them, so bkn-agent can deploy before bkn-trace.
    BKN_TRACE_EVIDENCE_INGEST_URL = _env("BKN_TRACE_EVIDENCE_INGEST_URL", "")
    BKN_TRACE_ARTIFACT_INGEST_URL = _env("BKN_TRACE_ARTIFACT_INGEST_URL", "")
    BKN_TRACE_EVIDENCE_INGEST_TOKEN = _env("BKN_TRACE_EVIDENCE_INGEST_TOKEN", "")
    BKN_TRACE_EVIDENCE_TIMEOUT_S = float(_env("BKN_TRACE_EVIDENCE_TIMEOUT_S", "3"))
    BKN_TRACE_EVIDENCE_MAX_ATTEMPTS = int(_env("BKN_TRACE_EVIDENCE_MAX_ATTEMPTS", "3"))
    BKN_TRACE_EVIDENCE_RETRY_BACKOFF_S = float(
        _env("BKN_TRACE_EVIDENCE_RETRY_BACKOFF_S", "0.1")
    )
    BKN_TRACE_EVIDENCE_DRAIN_TIMEOUT_S = float(
        _env("BKN_TRACE_EVIDENCE_DRAIN_TIMEOUT_S", "5")
    )
    BKN_TRACE_MODEL_SOURCE_LIMIT = int(_env("BKN_TRACE_MODEL_SOURCE_LIMIT", "20"))

    # checkpointer: memory | mysql
    CHECKPOINTER_BACKEND = _env("CHECKPOINTER_BACKEND", "mysql")
    # 建表统一走 migrations/bkn-agent/（core-data-migrator）。仅开发环境
    # 允许 saver 运行时自建表。
    CHECKPOINTER_ALLOW_RUNTIME_DDL = _env("CHECKPOINTER_ALLOW_RUNTIME_DDL", "false").lower() == "true"

    # 执行限额默认值（agent.limits 可覆盖）
    DEFAULT_MAX_TURNS = int(_env("BKN_AGENT_MAX_TURNS", "25"))
    DEFAULT_TIMEOUT_S = int(_env("BKN_AGENT_TIMEOUT_S", "300"))

    SKILL_CACHE_TTL_S = int(_env("BKN_AGENT_SKILL_TTL", "60"))

    @property
    def db_url(self) -> str:
        return (
            f"mysql+aiomysql://{self.RDS_USER}:{self.RDS_PASS}"
            f"@{self.RDS_HOST}:{self.RDS_PORT}/{self.RDS_DBNAME}?charset=utf8mb4"
        )

    @property
    def checkpointer_conn(self) -> str:
        return (
            f"mysql://{self.RDS_USER}:{self.RDS_PASS}"
            f"@{self.RDS_HOST}:{self.RDS_PORT}/{self.RDS_DBNAME}"
        )


config = Config()
