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


# ---- API schemas ----

ToolRef = dict  # {"type": "mcp"|"toolbox"|"agent", ...}，M3/M7 扩展 toolbox/agent


class AgentLimits(BaseModel):
    max_turns: Optional[int] = Field(default=None, ge=1, le=200)
    max_tool_calls: Optional[int] = Field(default=None, ge=0, le=500)
    timeout_s: Optional[int] = Field(default=None, ge=1, le=3600)


class AgentSpec(BaseModel):
    name: str = Field(min_length=1, max_length=100)
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


class ChatRequest(BaseModel):
    agent_id: str
    thread_id: Optional[str] = None
    message: str = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    prompt_override: Optional[str] = None
    prompt_vars: dict[str, Any] = Field(default_factory=dict)
