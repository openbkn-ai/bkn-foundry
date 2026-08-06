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

    # Context Loader MCP 面。ToolRef type=context_loader 由此装载，不需要在 agent
    # 定义里写死 URL（写死会让定义不能跨环境搬）。
    #
    # 这是 Context Loader 的**公开面**：它挂 middlewareIntrospectVerify，只认真实
    # 令牌（OAuth access token 或 bak_ AppKey），不吃 /in 那套 x-account-id 头部身份。
    # 所以下面两个凭据配置是这条路的前提，不是可选优化。
    CONTEXT_LOADER_MCP_URL = _env(
        "CONTEXT_LOADER_MCP_URL",
        "http://agent-retrieval:30779/api/agent-retrieval/v1/mcp/",
    )
    # 兜底服务凭据（bak_ AppKey）。仅在调用方没有透传令牌时启用。
    #
    # ⚠️ 一旦用上，Context Loader 看到的就是这个服务主体而不是真实调用者，
    # per-user 授权在该次调用上塌缩为「AppKey 签发人可见的范围」。主路是透传
    # 调用方令牌（见 auth.caller_token）；留空则没有兜底，无令牌时不挂 CL 工具。
    CONTEXT_LOADER_APPKEY = _env("CONTEXT_LOADER_APPKEY", "")
    CONTEXT_LOADER_MCP_TIMEOUT_S = float(_env("CONTEXT_LOADER_MCP_TIMEOUT_S", "30"))

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
