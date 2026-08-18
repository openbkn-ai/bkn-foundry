import os


def _env(name: str, default: str) -> str:
    v = os.getenv(name)
    return v if v not in (None, "") else default


class Config:
    HOST = _env("BKN_AGENT_HOST", "0.0.0.0")
    PORT = int(_env("BKN_AGENT_PORT", "30800"))

    # The shared openbkn database, following the platform RDS* convention.
    RDS_HOST = _env("RDSHOST", "127.0.0.1")
    RDS_PORT = int(_env("RDSPORT", "3306"))
    RDS_DBNAME = _env("RDSDBNAME", "openbkn")
    RDS_USER = _env("RDSUSER", "root")
    RDS_PASS = _env("RDSPASS", "password")

    # Model surface: the in-cluster private route of mf-model-api, OpenAI
    # compatible. An empty model means the system default model.
    MF_MODEL_API_PRIVATE_BASE = _env(
        "MF_MODEL_API_PRIVATE_BASE",
        "http://mf-model-api:9898/api/private/mf-model-api/v1",
    )
    DEFAULT_MODEL = _env("BKN_AGENT_DEFAULT_MODEL", "")

    # Operator factory (operator-integration): both the tool surface and the
    # skill surface go through its internal-v1 route (#322 moved the skill
    # surface over from capabilities-lab).
    OPERATOR_INTEGRATION_BASE = _env("OPERATOR_INTEGRATION_BASE", "http://agent-operator-integration:9000/api/agent-operator-integration")

    # The Context Loader MCP surface. A ToolRef with type=context_loader loads
    # from here, so no URL has to be hard-coded in the agent definition; a
    # hard-coded one would stop the definition from moving between
    # environments.
    #
    # This is the *public* surface of Context Loader: it runs
    # middlewareIntrospectVerify and accepts only a real credential (an OAuth
    # access token or a bak_ AppKey), not the /in style x-account-id header
    # identity. The caller must therefore forward the end user's token for this
    # path to work at all.
    CONTEXT_LOADER_MCP_URL = _env(
        "CONTEXT_LOADER_MCP_URL",
        "http://agent-retrieval:30779/api/agent-retrieval/v1/mcp/",
    )
    # The only credential is the token forwarded by the caller; see
    # auth.caller_token. There is deliberately no service-credential fallback:
    # standing in with a service AppKey would show Context Loader the issuer
    # instead of the real caller and collapse per-user authorization on the
    # spot. Without a token the CL tools are simply not loaded.
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
    # Tables are created solely by migrations/bkn-agent/, run by
    # core-data-migrator. Only a development environment lets the saver create
    # its own tables at runtime.
    CHECKPOINTER_ALLOW_RUNTIME_DDL = _env("CHECKPOINTER_ALLOW_RUNTIME_DDL", "false").lower() == "true"

    # Default execution limits; agent.limits may override them.
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
