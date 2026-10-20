"""Unit tests for what an execution record stores."""

import hashlib

from src.domain.entities.execution import Execution
from src.domain.value_objects.execution_status import ExecutionState, ExecutionStatus
from src.infrastructure.persistence.database import Base  # noqa: F401
from src.infrastructure.persistence.models.execution_model import (
    CODE_COLUMN_MAX_BYTES,
    ExecutionModel,
    bounded_code,
)


def make_execution(code: str, event_data: dict | None = None) -> Execution:
    return Execution(
        id="exec-1",
        session_id="sess-1",
        code=code,
        language="python",
        timeout=30,
        event_data=event_data,
        state=ExecutionState(status=ExecutionStatus.PENDING),
    )


class TestBoundedCode:
    def test_code_that_fits_is_stored_unchanged(self):
        code = "print('ok')\n" + "中" * 1000
        assert bounded_code(code) == code

    def test_code_at_the_limit_is_stored_unchanged(self):
        code = "a" * CODE_COLUMN_MAX_BYTES
        assert bounded_code(code) == code

    def test_oversized_code_keeps_its_tail_within_the_column(self):
        # The run_code shape from #2294: a large generated prelude, then the caller's code.
        prelude = '"""文档字符串"""\n' * 6000
        user_code = "result = limit_assessment_asof('PF-046')\nprint(result)\n"
        code = prelude + user_code

        stored = bounded_code(code)

        raw = code.encode("utf-8")
        assert len(raw) > CODE_COLUMN_MAX_BYTES
        assert len(stored.encode("utf-8")) <= CODE_COLUMN_MAX_BYTES
        assert stored.endswith(user_code)
        assert stored.startswith(
            f"# [truncated: {len(raw)} bytes, sha256:{hashlib.sha256(raw).hexdigest()}"
        )

    def test_cut_inside_a_multibyte_character_stays_valid_utf8(self):
        # The kept tail is what varies the cut point, so the ASCII suffix moves it 0, 2
        # and 1 bytes into a three-byte character; those continuation bytes are dropped.
        for suffix, partial_bytes in (("", 0), ("a", 2), ("ab", 1)):
            code = "中" * 30000 + suffix

            stored = bounded_code(code)

            marker, tail = stored.split("\n", 1)
            budget = CODE_COLUMN_MAX_BYTES - len((marker + "\n").encode("utf-8"))
            assert len(tail.encode("utf-8")) == budget - partial_bytes
            assert tail == "中" * ((budget - partial_bytes - len(suffix)) // 3) + suffix


class TestExecutionModelStorage:
    def test_event_data_is_not_stored(self):
        event = {"token": "ory_at_secret", "bkn": {"conversation_id": "conv"}}

        model = ExecutionModel.from_entity(make_execution("print(1)", event))

        assert model.f_event_data == ""
        assert "ory_at_secret" not in model.f_event_data

    def test_oversized_code_is_bounded_on_insert(self):
        code = "x" * (CODE_COLUMN_MAX_BYTES + 10)

        model = ExecutionModel.from_entity(make_execution(code))

        assert len(model.f_code.encode("utf-8")) <= CODE_COLUMN_MAX_BYTES

    def test_entity_loaded_from_a_bounded_row_is_valid(self):
        model = ExecutionModel.from_entity(make_execution("y" * (CODE_COLUMN_MAX_BYTES * 2)))

        execution = model.to_entity()

        assert execution.code == model.f_code
        assert execution.event_data is None
