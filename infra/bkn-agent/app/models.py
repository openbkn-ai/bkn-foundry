from typing import Any, Literal, Optional

from pydantic import BaseModel, Field
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

ToolRef = dict  # {"type": "mcp"|"toolbox"|"agent", ...}，M3/M7 扩展 toolbox/agent


class AgentLimits(BaseModel):
    max_turns: Optional[int] = Field(default=None, ge=1, le=200)
    max_tool_calls: Optional[int] = Field(default=None, ge=0, le=500)
    timeout_s: Optional[int] = Field(default=None, ge=1, le=3600)


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


class AgentOut(AgentSpec):
    agent_id: str
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


class ChatRequest(BaseModel):
    agent_id: str
    thread_id: Optional[str] = None
    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
    # 结构化输出：传 JSON Schema（schema 本体，如 {"type":"object","properties":{...}}），
    # 工具循环跑完后再做一次结构化调用，结果经 SSE `structured` 事件返回（正文 token 照常流）。
    # 依赖底层模型支持结构化输出（with_structured_output / function-calling）。
    response_format: Optional[dict[str, Any]] = None


class InvokeRequest(BaseModel):
    """同步一次性执行（agent_id 在路径上；算子工厂 toolbox 工具经此调用）。"""

    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
    # 结构化输出：传 JSON Schema，task output 落序列化后的 JSON（见 ChatRequest.response_format）。
    response_format: Optional[dict[str, Any]] = None


class RunRequest(BaseModel):
    agent_id: str
    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
    # 结构化输出：传 JSON Schema，task output 落序列化后的 JSON（见 ChatRequest.response_format）。
    response_format: Optional[dict[str, Any]] = None


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
