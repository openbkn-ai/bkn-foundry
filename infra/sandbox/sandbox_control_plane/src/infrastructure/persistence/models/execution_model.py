"""
Execution ORM model

The SQLAlchemy model used for persistence.
Naming convention: t_{module}_{entity} for tables, f_{field_name} for columns.
"""

import hashlib
from datetime import datetime

from sqlalchemy import Column, String, Integer, BigInteger, Text, Index
from sqlalchemy.dialects.mysql import MEDIUMTEXT
from sqlalchemy.orm import Mapped, mapped_column
from sqlalchemy import func

from src.infrastructure.persistence.database import Base

# f_code is a MariaDB TEXT column: 65535 bytes, not characters.
CODE_COLUMN_MAX_BYTES = 65535


def bounded_code(code: str) -> str:
    """
    Fit the code into f_code.

    The executor receives the code with the request and never reads it back from
    here; the column only feeds the execution history. Rejecting the row would fail
    the execution before it starts (#2294), so oversized code keeps its tail behind
    a marker. The tail is the part worth keeping: callers such as run_code put a
    generated prelude first and the caller's own code last.
    """
    raw = code.encode("utf-8")
    if len(raw) <= CODE_COLUMN_MAX_BYTES:
        return code
    digest = hashlib.sha256(raw).hexdigest()
    marker = f"# [truncated: {len(raw)} bytes, sha256:{digest}, tail kept]\n"
    budget = CODE_COLUMN_MAX_BYTES - len(marker.encode("utf-8"))
    # Cutting inside a multi-byte character leaves continuation bytes at the start;
    # dropping them keeps the result valid UTF-8 and within budget.
    tail = raw[-budget:].decode("utf-8", errors="ignore")
    return marker + tail


class ExecutionModel(Base):
    """
    Execution ORM model, t_sandbox_execution

    An infrastructure-layer detail that maps onto the database table,
    following the table naming convention.
    """

    __tablename__ = "t_sandbox_execution"

    # Primary fields
    f_id: Mapped[str] = mapped_column(String(255), primary_key=True)
    f_session_id: Mapped[str] = mapped_column(String(255), nullable=False)

    # Status
    f_status: Mapped[str] = mapped_column(String(20), nullable=False, default="pending")

    # Code and execution
    f_code = Column(Text, nullable=False, default="")
    f_language: Mapped[str] = mapped_column(String(32), nullable=False)
    f_entrypoint: Mapped[str] = mapped_column(String(255), nullable=False, default="")
    f_event_data = Column(Text, nullable=False, default="")
    f_timeout_sec = Column(Integer, nullable=False)

    # Results
    f_return_value = Column(
        Text().with_variant(MEDIUMTEXT(), "mysql", "mariadb"), nullable=False, default=""
    )
    f_stdout = Column(
        Text().with_variant(MEDIUMTEXT(), "mysql", "mariadb"), nullable=False, default=""
    )
    f_stderr = Column(
        Text().with_variant(MEDIUMTEXT(), "mysql", "mariadb"), nullable=False, default=""
    )
    f_exit_code = Column(Integer, nullable=False, default=0)
    f_metrics = Column(Text, nullable=False, default="")
    f_error_message = Column(Text, nullable=False, default="")

    # Timestamps (BIGINT - millisecond timestamps)
    f_started_at = Column(BigInteger, nullable=False, default=0)
    f_completed_at = Column(BigInteger, nullable=False, default=0)

    # Audit fields
    f_created_at = Column(BigInteger, nullable=False, default=0)
    f_created_by = Column(String(40), nullable=False, default="")
    f_updated_at = Column(BigInteger, nullable=False, default=0)
    f_updated_by = Column(String(40), nullable=False, default="")
    f_deleted_at = Column(BigInteger, nullable=False, default=0)
    f_deleted_by = Column(String(36), nullable=False, default="")

    # Indexes
    __table_args__ = (
        Index("t_sandbox_execution_idx_session_id", "f_session_id"),
        Index("t_sandbox_execution_idx_status", "f_status"),
        Index("t_sandbox_execution_idx_created_at", "f_created_at"),
        Index("t_sandbox_execution_idx_deleted_at", "f_deleted_at"),
        Index("t_sandbox_execution_idx_created_by", "f_created_by"),
    )

    def to_entity(self):
        """Convert to the domain entity"""
        from src.domain.entities.execution import Execution
        from src.domain.value_objects.execution_status import ExecutionStatus, ExecutionState

        return Execution(
            id=self.f_id,
            session_id=self.f_session_id,
            code=self.f_code or "",
            language=self.f_language,
            timeout=self.f_timeout_sec,
            event_data=self._parse_json(self.f_event_data),
            state=ExecutionState(
                status=ExecutionStatus(self.f_status),
                exit_code=self.f_exit_code,
                error_message=self.f_error_message or None,
            ),
            created_at=self._millis_to_datetime(self.f_created_at) or datetime.now(),
            completed_at=self._millis_to_datetime(self.f_completed_at),
            execution_time=None,  # Can be calculated from started_at/completed_at
            stdout=self.f_stdout or "",
            stderr=self.f_stderr or "",
            artifacts=[],  # Loaded separately if needed
            retry_count=0,  # Not in database schema
            last_heartbeat_at=None,  # Not in database schema
            return_value=self._parse_json(self.f_return_value),
            metrics=self._parse_json(self.f_metrics),
        )

    @classmethod
    def from_entity(cls, execution):
        """Build the ORM model from the domain entity"""
        import json

        now_ms = int(datetime.now().timestamp() * 1000)

        return cls(
            f_id=execution.id,
            f_session_id=execution.session_id,
            f_status=execution.state.status.value,
            f_code=bounded_code(execution.code),
            f_language=execution.language,
            f_timeout_sec=execution.timeout,
            f_entrypoint="",
            # The event goes to the executor with the request and nothing reads it back.
            # It can carry the caller's credentials (run_code sends a bearer token), so
            # it is not stored.
            f_event_data="",
            f_return_value=(
                json.dumps(execution.return_value, ensure_ascii=False)
                if execution.return_value
                else ""
            ),
            f_stdout=execution.stdout,
            f_stderr=execution.stderr,
            f_exit_code=execution.state.exit_code or 0,
            f_metrics=(
                json.dumps(execution.metrics, ensure_ascii=False) if execution.metrics else ""
            ),
            f_error_message=execution.state.error_message or "",
            f_started_at=0,
            f_completed_at=(
                int(execution.completed_at.timestamp() * 1000) if execution.completed_at else 0
            ),
            # Audit columns
            f_created_at=(
                int(execution.created_at.timestamp() * 1000) if execution.created_at else now_ms
            ),
            f_created_by="",
            f_updated_at=now_ms,
            f_updated_by="",
            f_deleted_at=0,
            f_deleted_by="",
        )

    def _parse_json(self, value: str):
        """Parse a JSON string safely"""
        if not value or value.strip() == "":
            return None
        try:
            import json

            return json.loads(value)
        except (json.JSONDecodeError, ValueError):
            return None

    def _millis_to_datetime(self, millis: int):
        """Turn a millisecond timestamp into a datetime"""
        if not millis or millis == 0:
            return None
        try:
            return datetime.fromtimestamp(millis / 1000)
        except (ValueError, OSError):
            return None
