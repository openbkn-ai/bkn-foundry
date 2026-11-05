# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import asyncio
from types import SimpleNamespace

from langchain_core.messages import AIMessage, HumanMessage

from app.core.unfinished_answer import CONTINUE, UnfinishedAnswer, unfinished


class _Request(SimpleNamespace):
    def override(self, **changes):
        return _Request(**{**self.__dict__, **changes})


def _reply(content: str, finish: str | None = "stop", tool_calls=None) -> AIMessage:
    return AIMessage(
        content=content,
        tool_calls=tool_calls or [],
        response_metadata={"finish_reason": finish} if finish else {},
    )


def _run(replies: list[AIMessage]):
    seen = []

    async def handler(request):
        seen.append(request.messages)
        return SimpleNamespace(result=[replies[len(seen) - 1]], structured_response=None)

    question = [HumanMessage("库存多少")]
    response = asyncio.run(UnfinishedAnswer().awrap_model_call(_Request(messages=question), handler))
    return response.result[-1], seen


def test_a_finished_answer_or_a_tool_call_passes_through():
    assert unfinished(_reply("合计 534。")) is None
    assert unfinished(_reply("", tool_calls=[{"name": "run_cypher", "args": {}, "id": "c1"}])) is None
    last, seen = _run([_reply("合计 534。")])
    assert last.content == "合计 534。" and len(seen) == 1


def test_a_reply_cut_by_length_is_finished():
    last, seen = _run([_reply("Now let me count the inventor", "length"), _reply("合计 534。")])
    assert last.content == "合计 534。"
    # The model sees what it said so far and is asked to go on.
    assert seen[1][-2].content == "Now let me count the inventor"
    assert seen[1][-1].content == CONTINUE


def test_a_tool_call_written_as_text_is_asked_again_without_the_markup():
    written = "Now let me count.<｜｜DSML｜｜invoke name=\"run_cypher\">..."
    assert unfinished(_reply(written)) == "dsml"
    last, seen = _run([_reply(written), _reply("合计 534。")])
    assert last.content == "合计 534。"
    assert seen[1][-2].content == "Now let me count."


def test_it_gives_up_after_the_retries():
    cut = _reply("Now let", "length")
    last, seen = _run([cut, cut, cut, _reply("never asked")])
    assert len(seen) == 3 and last.content == "Now let"


def test_a_short_reply_that_does_not_read_as_ended_is_logged_not_retried(caplog):
    last, seen = _run([_reply("Now let me count the inventor")])
    assert len(seen) == 1 and last.content == "Now let me count the inventor"
    assert "tail='Now let me count the inventor'" in caplog.text
