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

# Tool references form a discriminated union dispatched on type, validated for
# type and length at the creation boundary. Without that, something like
# {"url":123} would be created successfully and only blow up later, in the MCP
# client, a factory request, or tool registration. extra="allow" preserves extra
# caller fields; once validated everything converts back to dict (see the
# model_validator on AgentSpec), because the execution chain (ref.get in
# tools.py) and the stored JSON column keep consuming dicts. On the way out,
# AgentOut.tools is overridden to a bare dict with no validation, so legacy
# dirty data does not block listing or syncing.


class _ToolRefBase(BaseModel):
    model_config = {"extra": "allow"}
    # Optional display name and description, used by agent-as-tool and as the
    # mcp connection name.
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


class ContextLoaderToolRef(_ToolRefBase):
    """Context Loader knowledge-network retrieval tools, loaded over its MCP surface.

    Deliberately carries no url: the endpoint comes from CONTEXT_LOADER_MCP_URL,
    so an agent definition can be exported and imported across environments
    without embedding an environment address, which a type=mcp reference with a
    hard-coded url cannot do.
    """

    type: Literal["context_loader"]
    # When undeclared, the existing behaviour of loading every Context Loader
    # business tool is preserved. An agent meant for read-only diagnosis must
    # list the permitted MCP tool names explicitly, so it cannot still receive
    # run_sql or execution-class tools beyond what the prompt constrains.
    allowed_tools: Optional[list[str]] = Field(
        default=None,
        max_length=100,
        description="Allowlist of Context Loader MCP tool names this agent may load; omit it to allow all.",
    )


ToolRef = Annotated[
    McpToolRef | ToolboxToolRef | AgentToolRef | ContextLoaderToolRef,
    Field(discriminator="type"),
]


class AgentLimits(BaseModel):
    max_turns: Optional[int] = Field(default=None, ge=1, le=200)
    max_tool_calls: Optional[int] = Field(default=None, ge=0, le=500)
    timeout_s: Optional[int] = Field(default=None, ge=1, le=3600)
    # Output token cap for a single model call. Unset means the provider default,
    # commonly around 4096, which truncates long JSON — raise it for large-input
    # scenarios. It is passed through as the OpenAI-compatible max_tokens and is
    # ultimately bounded by the model's own limit. The lower bound of 10 matches
    # mf-model-api (logics.py max_tokens: conint(ge=10)): accepting 1..9 would
    # let the agent be created while every execution failed downstream with 400.
    max_output_tokens: Optional[int] = Field(default=None, ge=10, le=65536)


_ID_PATTERN = r"^[0-9A-Za-z_.-]+$"  # A preset id allows letters, digits, underscore, dot, and hyphen, for stable cross-environment references


class AgentSpec(BaseModel):
    # Optional preset id, given at creation so a module can reference a fixed id
    # across environments; without it the server generates a uuid. An existing id
    # is a creation conflict rather than an overwrite — cross-environment syncing
    # uses the upsert of import. It applies only on creation and is ignored on
    # update.
    agent_id: Optional[str] = Field(default=None, min_length=1, max_length=50, pattern=_ID_PATTERN)
    # Charset: ASCII letters, digits, underscore, or CJK ideographs from the
    # basic block; spaces and hyphens are rejected. The constraint originally
    # aligned with the tool-name validation of the operator-factory toolbox, and
    # since automatic registration was dropped (see "operator factory
    # registration" in the README) it is bkn-agent's own convention. It stays as
    # it is: relaxing it is a behaviour change that needs its own assessment of
    # existing agents and of import/export.
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
        # Validation (union dispatch plus type and length) already happened
        # during field parsing; this converts everything back to dict, because
        # the execution chain (ref.get) and the stored JSON column know nothing
        # about pydantic models.
        self.tools = [
            t.model_dump(exclude_none=True) if isinstance(t, BaseModel) else t
            for t in self.tools
        ]
        return self

    @field_serializer("tools")
    def _ser_tools(self, v):
        # After _tools_to_dicts the values are dicts, which no longer match the
        # union annotation. An explicit serializer emits them as they are and
        # keeps pydantic from logging PydanticSerializationUnexpectedValue on
        # every serialization.
        return v


class AgentOut(AgentSpec):
    # The read path (DB row -> output object) does not re-validate: if legacy
    # dirty data from before an upgrade failed here, it would take the whole
    # /agents page down with a 500 and make the single-item read unusable for
    # repair. The write model (AgentSpec) validates strictly while the output
    # model passes data through as-is — tools is overridden to a bare dict, and
    # agent_id and name drop their pattern re-validation.
    agent_id: str = Field(min_length=1)
    name: str = Field(min_length=1, max_length=100)
    # list[Any] rather than list[dict]: a scalar element inserted by hand-editing
    # the database is let through too, because listing and the repair path come
    # first; load_tools reports the error at execution time instead.
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
    """Validate response_format as a legal JSON Schema at the request boundary,
    answering 400 outright when it is not. Otherwise an invalid schema travels
    all the way into the structured call, wasting one model call on the native
    path and another on the prompt fallback before the task fails with an error
    that no longer resembles the cause."""
    if v is None:
        return v
    try:
        validator_for(v).check_schema(v)
    except SchemaError as e:
        raise ValueError(f"response_format is not a valid JSON Schema: {e.message}") from e
    except Exception as e:  # Errors from the validator_for stage, such as a malformed $schema field
        raise ValueError(f"response_format is not a valid JSON Schema: {e}") from e
    # The root type must be object: the execution chain assumes the result is a
    # mapping (dict(r) on the native path, extraction between {..} on the
    # fallback path), so an array or scalar root would pass validation and fail
    # at execution. Wrap a list in an object property instead.
    # {"type":"object","properties":{"items":{"type":"array",...}}}。
    if v.get("type") != "object":
        raise ValueError(
            'the root type of response_format must be "type":"object"; '
            'wrap an array or scalar in an object property'
        )
    return v


# Structured output: pass the JSON Schema itself, such as
# {"type":"object","properties":{...}}.
ResponseFormat = Annotated[Optional[dict[str, Any]], AfterValidator(_check_json_schema)]


class ChatRequest(BaseModel):
    agent_id: str
    thread_id: Optional[str] = None
    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
    # Structured output: once the tool loop finishes, one more structured call
    # runs and its result is returned through the SSE `structured` event, while
    # body tokens keep streaming as usual. It depends on support in the
    # underlying model (with_structured_output / function calling) and degrades
    # to the prompt path when that is missing; see core/structured.py.
    response_format: ResponseFormat = None


class InvokeRequest(BaseModel):
    """Synchronous one-shot execution; agent_id is taken from the path."""

    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
    # Structured output: the task output stores the serialized JSON; see
    # ChatRequest.response_format.
    response_format: ResponseFormat = None


class RunRequest(BaseModel):
    agent_id: str
    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
    # Structured output: the task output stores the serialized JSON; see
    # ChatRequest.response_format.
    response_format: ResponseFormat = None


class PromptSpec(BaseModel):
    # Optional preset id, as in AgentSpec.agent_id: without it the server
    # generates a uuid, and a collision is an error.
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
    """Platform error envelope, used by every non-2xx response."""

    code: str
    description: str
    detail: str
    solution: str
    trace_id: str
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


# ---------- Import and export (impex) ----------


class PromptExport(BaseModel):
    prompt_id: str
    name: str
    content: str
    vars_schema: Optional[dict[str, Any]] = None


class AgentExportItem(BaseModel):
    agent_id: str
    spec: AgentSpec
    prompt: Optional[PromptExport] = None  # The currently effective version; the import side publishes a new one when the content changed


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
