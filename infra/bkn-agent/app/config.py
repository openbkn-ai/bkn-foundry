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

    # 工具面：执行工厂 toolbox（统一工具平面）。默认给每个 agent 挂载的 box
    # 列表（逗号分隔），默认 = contextloader 内置工具集；置空则不默认挂载。
    # 默认 box 拉取失败降级告警不击穿对话；显式 type=toolbox 引用失败则报错。
    DEFAULT_TOOLBOXES = _env(
        "BKN_AGENT_DEFAULT_TOOLBOXES",
        "e521d454-4a0b-4dc9-8a28-d0986de1cef9",
    )

    # 算子工厂（operator-integration）：published agent 注册为 toolbox 工具（#212）；
    # 工具面与技能面统一走这里的 internal-v1（#322 把技能面从 capabilities-lab 收敛过来）
    OPERATOR_INTEGRATION_BASE = _env("OPERATOR_INTEGRATION_BASE", "http://agent-operator-integration:9000/api/agent-operator-integration")
    TOOLBOX_SYNC_ENABLED = _env("BKN_AGENT_TOOLBOX_SYNC", "true").lower() == "true"
    TOOLBOX_SYNC_RETRY_INITIAL_S = int(_env("BKN_AGENT_TOOLBOX_RETRY_INITIAL_S", "5"))
    TOOLBOX_SYNC_RETRY_MAX_S = int(_env("BKN_AGENT_TOOLBOX_RETRY_MAX_S", "60"))
    # toolbox 工具回调本服务的地址（box_svc_url）
    SELF_BASE_URL = _env("BKN_AGENT_SELF_BASE_URL", "http://bkn-agent:30800")

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
    def default_toolboxes(self) -> list[str]:
        return [b.strip() for b in self.DEFAULT_TOOLBOXES.split(",") if b.strip()]

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
