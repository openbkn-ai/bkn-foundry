from typing import Annotated, Any, Literal, Optional

from jsonschema.exceptions import SchemaError
from jsonschema.validators import validator_for
from pydantic import AfterValidator, BaseModel, Field, field_serializer, model_validator
from sqlalchemy import BigInteger, Integer, String, Text
from sqlalchemy.dialects.mysql import JSON
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column


class Base(DeclarativeBase):
    pass


class AgentRow(Base):
    __tablename__ = "t_agent"

    f_agent_id: Mapped[str] = mapped_column(String(50), primary_key=True)
    f_name: Mapped[str] = mapped_column(String(100), unique=True)
    f_mode: Mapped[str] = mapped_column(String(10))
    f_prompt_id: Mapped[Optional[str]] = mapped_column(String(50), nullable=True)
    f_prompt_vars_schema: Mapped[Optional[dict]] = mapped_column(JSON, nullable=True)
    f_model: Mapped[str] = mapped_column(String(100), default="")
    f_tools: Mapped[list] = mapped_column(JSON, default=list)
    f_skills: Mapped[list] = mapped_column(JSON, default=list)
    f_limits: Mapped[Optional[dict]] = mapped_column(JSON, nullable=True)
    f_status: Mapped[str] = mapped_column(String(20), default="draft")
    f_create_user: Mapped[str] = mapped_column(String(50))
    f_update_user: Mapped[str] = mapped_column(String(50))
    f_create_time: Mapped[int] = mapped_column(BigInteger)
    f_update_time: Mapped[int] = mapped_column(BigInteger)


class PromptRow(Base):
    __tablename__ = "t_agent_prompt"

    f_prompt_id: Mapped[str] = mapped_column(String(50), primary_key=True)
    f_name: Mapped[str] = mapped_column(String(100), unique=True)
    f_current_version: Mapped[int] = mapped_column(Integer)
    f_update_user: Mapped[str] = mapped_column(String(50))
    f_update_time: Mapped[int] = mapped_column(BigInteger)


class PromptVersionRow(Base):
    __tablename__ = "t_agent_prompt_version"

    f_prompt_id: Mapped[str] = mapped_column(String(50), primary_key=True)
    f_version: Mapped[int] = mapped_column(Integer, primary_key=True)
    f_content: Mapped[str] = mapped_column(Text)
    f_vars_schema: Mapped[Optional[dict]] = mapped_column(JSON, nullable=True)
    f_create_user: Mapped[str] = mapped_column(String(50))
    f_create_time: Mapped[int] = mapped_column(BigInteger)


class PromptOverrideRow(Base):
    __tablename__ = "t_agent_prompt_override"

    f_agent_id: Mapped[str] = mapped_column(String(50), primary_key=True)
    f_account_id: Mapped[str] = mapped_column(String(50), primary_key=True)
    f_content: Mapped[str] = mapped_column(Text)
    f_update_time: Mapped[int] = mapped_column(BigInteger)


class ThreadRow(Base):
    __tablename__ = "t_agent_thread"

    f_thread_id: Mapped[str] = mapped_column(String(50), primary_key=True)
    f_agent_id: Mapped[str] = mapped_column(String(50))
    f_account_id: Mapped[str] = mapped_column(String(50))
    f_create_time: Mapped[int] = mapped_column(BigInteger)
    f_update_time: Mapped[int] = mapped_column(BigInteger)


class TaskRow(Base):
    __tablename__ = "t_agent_task"

    f_task_id: Mapped[str] = mapped_column(String(50), primary_key=True)
    f_agent_id: Mapped[str] = mapped_column(String(50))
    f_status: Mapped[str] = mapped_column(String(20), default="pending")
    f_input: Mapped[Optional[dict]] = mapped_column(JSON, nullable=True)
    f_output: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    f_failure_detail: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    f_parent_thread_id: Mapped[Optional[str]] = mapped_column(String(50), nullable=True)
    f_account_id: Mapped[str] = mapped_column(String(50))
    f_create_time: Mapped[int] = mapped_column(BigInteger)
    f_update_time: Mapped[int] = mapped_column(BigInteger)


# ---- API schemas ----

# 工具引用：discriminated union（按 type 分派），创建边界即校验类型/长度——
# 否则 {"url":123} 之类会建成功、到 MCP 客户端/工厂请求/工具注册阶段才炸。
# extra="allow" 保留调用方附加字段；校验通过后统一转回 dict（见 AgentSpec 的
# model_validator）——执行链（tools.py 的 ref.get）与入库 JSON 列继续吃 dict，
# 出库 AgentOut.tools 覆写为裸 dict 不校验（存量脏数据不阻断列表/同步）。


class _ToolRefBase(BaseModel):
    model_config = {"extra": "allow"}
    # 工具展示名/说明（可选）：agent-as-tool 与 mcp 连接名会用到
    name: Optional[str] = Field(default=None, max_length=100)
    description: Optional[str] = Field(default=None, max_length=500)


class McpToolRef(_ToolRefBase):
    type: Literal["mcp"]
    url: str = Field(min_length=1, max_length=2048, pattern=r"^https?://")


class ToolboxToolRef(_ToolRefBase):
    type: Literal["toolbox"]
    box_id: str = Field(min_length=1, max_length=100)


class AgentToolRef(_ToolRefBase):
    type: Literal["agent"]
    agent_id: str = Field(min_length=1, max_length=100)
    # 显式工具名直接当 OpenAI function name：只收 ASCII 字母数字 _ -，≤64
    # （中文/超长会让引用方每次模型请求稳定 400；中文语义放 description）。
    # 运行时侧 tools.py 对派生名（agent_{中文名}）同样清洗兜底。
    name: Optional[str] = Field(default=None, max_length=64, pattern=r"^[a-zA-Z0-9_-]+$")


ToolRef = Annotated[
    McpToolRef | ToolboxToolRef | AgentToolRef, Field(discriminator="type")
]


class AgentLimits(BaseModel):
    max_turns: Optional[int] = Field(default=None, ge=1, le=200)
    max_tool_calls: Optional[int] = Field(default=None, ge=0, le=500)
    timeout_s: Optional[int] = Field(default=None, ge=1, le=3600)
    # 单次模型调用的输出 token 上限。不设则用 provider 默认（常见 ~4096，长 JSON 会被
    # 截断——大输入场景配大此值）。透传 OpenAI 兼容 max_tokens，最终受模型自身上限约束。
    # 下限 10 对齐 mf-model-api（logics.py max_tokens: conint(ge=10)）——收 1..9 会让
    # agent 建成功但每次执行必被下游 400。
    max_output_tokens: Optional[int] = Field(default=None, ge=10, le=65536)


_ID_PATTERN = r"^[0-9A-Za-z_.-]+$"  # 预设 id 允许字母数字下划线点连字符（跨环境稳定引用用）


class AgentSpec(BaseModel):
    # 预设 id（可选）：创建时指定，便于模块用固定 id 跨环境引用；不传则服务端生成 uuid。
    # 已存在则创建冲突（不覆盖，跨环境同步用 import 的 upsert）。仅创建生效，更新忽略。
    agent_id: Optional[str] = Field(default=None, min_length=1, max_length=50, pattern=_ID_PATTERN)
    # 名字直接用作算子工厂 toolbox 的工具名，字符集须与工厂校验一致
    # （operator-integration validator: ^[[:word:]\p{Han}]+$ —— ASCII 字母数字下划线
    # 或汉字；空格、连字符都不收）。这里前置拦住：否则一个非法名会让整包注册 400、
    # 无限重试，连带堵死所有 published agent 的上下架。
    # 刻意比 Go 侧略严（汉字取基本区），宁可这里先拒，不让工厂 400。
    name: str = Field(min_length=1, max_length=100, pattern=r"^[0-9A-Za-z_一-鿿]+$")
    mode: Literal["chat", "task"] = "chat"
    prompt_id: Optional[str] = None
    prompt_vars_schema: Optional[dict[str, Any]] = None
    model: str = ""
    tools: list[ToolRef] = Field(default_factory=list)
    skills: list[str] = Field(default_factory=list)
    limits: Optional[AgentLimits] = None
    status: Literal["draft", "published"] = "draft"

    @model_validator(mode="after")
    def _tools_to_dicts(self):
        # 校验（union 分派+类型/长度）在字段解析时已完成；这里统一转回 dict，
        # 执行链（ref.get）与入库 JSON 列不感知 pydantic 模型。
        self.tools = [
            t.model_dump(exclude_none=True) if isinstance(t, BaseModel) else t
            for t in self.tools
        ]
        return self

    @field_serializer("tools")
    def _ser_tools(self, v):
        # 值在 _tools_to_dicts 已是 dict，与 union 注解不符——显式序列化器原样输出，
        # 避免 pydantic 每次序列化刷 PydanticSerializationUnexpectedValue 警告。
        return v


class AgentOut(AgentSpec):
    # 出库（DB 行 → 输出对象）不复验：升级前的存量脏数据若在这里炸，会连坐
    # /agents 整页 500、单查无法读取修复、list_published_agents 中断导致启动同步
    # 永久重试。写入模型（AgentSpec）严校验，输出模型放行原样数据——
    # tools 覆写为裸 dict，agent_id/name 去掉 pattern 复验。
    agent_id: str = Field(min_length=1)
    name: str = Field(min_length=1, max_length=100)
    # list[Any] 而非 list[dict]：手改 DB 塞进的标量元素也放行（列表/修复通道优先，
    # 执行时才在 load_tools 报错）
    tools: list[Any] = Field(default_factory=list)
    create_user: str
    update_user: str
    create_time: int
    update_time: int


class AgentList(BaseModel):
    items: list["AgentOut"]
    total: int
    page: int
    size: int


class AgentDeleted(BaseModel):
    deleted: str


def _check_json_schema(v: Optional[dict[str, Any]]) -> Optional[dict[str, Any]]:
    """response_format 在请求边界即校验为合法 JSON Schema（非法直接 400）。
    否则非法 schema 会一路进到结构化调用：原生+提示词降级各白费一次模型调用，
    最后以任务执行失败收场，错误面目全非。"""
    if v is None:
        return v
    try:
        validator_for(v).check_schema(v)
    except SchemaError as e:
        raise ValueError(f"response_format 不是合法 JSON Schema：{e.message}") from e
    except Exception as e:  # $schema 字段本身畸形等 validator_for 阶段的错
        raise ValueError(f"response_format 不是合法 JSON Schema：{e}") from e
    # 根类型必须是 object：执行链假设结果是 mapping（原生路径 dict(r)、降级路径按
    # {..} 区间抽取），array/标量根会过校验但执行必挂。需要列表就包一层
    # {"type":"object","properties":{"items":{"type":"array",...}}}。
    if v.get("type") != "object":
        raise ValueError(
            'response_format 根类型必须是 "type":"object"（数组/标量请包进 object 属性）'
        )
    return v


# 结构化输出：传 JSON Schema 本体（如 {"type":"object","properties":{...}}）。
ResponseFormat = Annotated[Optional[dict[str, Any]], AfterValidator(_check_json_schema)]


class ChatRequest(BaseModel):
    agent_id: str
    thread_id: Optional[str] = None
    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
    # 结构化输出：工具循环跑完后再做一次结构化调用，结果经 SSE `structured` 事件
    # 返回（正文 token 照常流）。依赖底层模型支持（with_structured_output /
    # function-calling），不支持时提示词降级（见 core/structured.py）。
    response_format: ResponseFormat = None


class InvokeRequest(BaseModel):
    """同步一次性执行（agent_id 在路径上；算子工厂 toolbox 工具经此调用）。"""

    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
    # 结构化输出：task output 落序列化后的 JSON（见 ChatRequest.response_format）。
    response_format: ResponseFormat = None


class RunRequest(BaseModel):
    agent_id: str
    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
    # 结构化输出：task output 落序列化后的 JSON（见 ChatRequest.response_format）。
    response_format: ResponseFormat = None


class PromptSpec(BaseModel):
    # 预设 id（可选）：同 AgentSpec.agent_id；不传则服务端生成 uuid，冲突即报错。
    prompt_id: Optional[str] = Field(default=None, min_length=1, max_length=50, pattern=_ID_PATTERN)
    name: str = Field(min_length=1, max_length=100)
    content: str = Field(min_length=1)
    vars_schema: Optional[dict[str, Any]] = None


class PromptPublish(BaseModel):
    content: str = Field(min_length=1)
    vars_schema: Optional[dict[str, Any]] = None


class PromptRollback(BaseModel):
    version: int = Field(ge=1)


class PromptOut(BaseModel):
    prompt_id: str
    name: str
    current_version: int
    content: str
    vars_schema: Optional[dict] = None
    update_user: str
    update_time: int


class PromptVersionOut(BaseModel):
    version: int
    content: str
    vars_schema: Optional[dict] = None
    create_user: str
    create_time: int


class PromptList(BaseModel):
    items: list["PromptOut"]
    total: int
    page: int
    size: int


class PromptVersionList(BaseModel):
    items: list["PromptVersionOut"]


class OverridePut(BaseModel):
    content: str = Field(min_length=1)


class OverrideState(BaseModel):
    agent_id: str
    account_id: str
    source: Literal["override"]


class OverrideDeleted(BaseModel):
    deleted: bool
    fallback: Literal["default"]


class ErrorEnvelope(BaseModel):
    """平台统一错误封套（所有非 2xx 响应）。"""

    code: str
    description: str
    detail: str
    solution: str
    link: str = ""


class EffectivePromptOut(BaseModel):
    source: Literal["override", "default"]
    content: str
    prompt_id: Optional[str] = None
    version: Optional[int] = None


class ThreadMessage(BaseModel):
    role: Literal["user", "assistant", "tool"]
    content: str
    tool_calls: list[str] = Field(default_factory=list)


class ThreadOut(BaseModel):
    thread_id: str
    agent_id: str
    create_time: int
    update_time: int
    messages: list[ThreadMessage]


class TaskOut(BaseModel):
    task_id: str
    agent_id: str
    status: Literal["pending", "running", "succeeded", "failed"]
    input: Optional[dict] = None
    output: Optional[str] = None
    failure_detail: Optional[str] = None
    parent_thread_id: Optional[str] = None
    create_time: int
    update_time: int


# ---------- 导入导出（impex） ----------


class PromptExport(BaseModel):
    prompt_id: str
    name: str
    content: str
    vars_schema: Optional[dict[str, Any]] = None


class AgentExportItem(BaseModel):
    agent_id: str
    spec: AgentSpec
    prompt: Optional[PromptExport] = None  # 当前生效版本；导入侧内容有变则发布新版本


class ExportRequest(BaseModel):
    agent_ids: list[str] = Field(min_length=1)


class ExportPackage(BaseModel):
    format: Literal["bkn-agent/v1"] = "bkn-agent/v1"
    exported_at: int
    items: list[AgentExportItem]


class ImportRequest(BaseModel):
    package: ExportPackage


class ImportItemResult(BaseModel):
    agent_id: str
    name: str
    action: Literal["created", "updated", "failed"]
    prompt_action: Literal["created", "version_published", "unchanged", "none"] = "none"
    error: Optional[str] = None


class ImportResult(BaseModel):
    results: list[ImportItemResult]
    warnings: list[str] = Field(default_factory=list)
