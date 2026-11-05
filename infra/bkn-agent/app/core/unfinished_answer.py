# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Answers a model left unfinished.

A model reply without tool calls ends the run, so a reply cut short reaches the
user as the answer: a sentence broken off mid-word, or a tool call some models
(DeepSeek V4) write into the text as DSML markup while reporting a normal stop.
Such a reply is sent back, up to twice, with a request to finish; each final reply's
finish reason is logged, so a cut-off that matches neither sign can be traced.
"""

import logging

from langchain.agents.middleware import AgentMiddleware
from langchain_core.messages import AIMessage, HumanMessage

logger = logging.getLogger(__name__)

# The start of a tool call DeepSeek writes into the text instead of tool_calls.
DSML_MARKER = "DSML｜"

CONTINUE = (
    "Your previous reply was cut off before it was finished. Call the tools you still need,"
    " or give your complete answer from the beginning: only your next reply is shown."
)

# A final reply this short and ending this way may have been cut off with a normal stop.
_SHORT = 200
_ENDINGS = tuple("。！？.!?）)」』|*`>:：")


def _text(content) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(
            block.get("text", "") if isinstance(block, dict) else str(block) for block in content
        )
    return str(content or "")


def unfinished(message) -> str | None:
    """Why a final reply was cut short, or None for a finished one or a tool call."""
    if not isinstance(message, AIMessage) or message.tool_calls:
        return None
    if (message.response_metadata or {}).get("finish_reason") == "length":
        return "length"
    if DSML_MARKER in _text(message.content):
        return "dsml"
    return None


def _without_markup(message: AIMessage) -> AIMessage:
    """The reply up to a tool call written as text: the markup is not an answer."""
    text = _text(message.content)
    cut = text.find(DSML_MARKER)
    if cut < 0:
        return message
    # The marker opens with a "<" and a run of full-width bars before it.
    start = text.rfind("<", 0, cut)
    return message.model_copy(update={"content": text[: start if start >= 0 else cut].rstrip()})


class UnfinishedAnswer(AgentMiddleware):
    """Asks the model to finish a final reply that was cut short."""

    def __init__(self, retries: int = 2):
        super().__init__()
        self.retries = retries

    async def awrap_model_call(self, request, handler):
        response = await handler(request)
        for attempt in range(self.retries + 1):
            if response.structured_response is not None or not response.result:
                return response
            last = response.result[-1]
            if not isinstance(last, AIMessage) or last.tool_calls:
                return response
            reason = unfinished(last)
            finish = (last.response_metadata or {}).get("finish_reason")
            text = _text(last.content).rstrip()
            # Logged to trace cut-offs that match no sign: an odd finish, or a short reply
            # that does not read as ended. Neither is retried on its own.
            if finish not in ("stop", None) or (len(text) < _SHORT and not text.endswith(_ENDINGS)):
                logger.warning(
                    "[BknAgent] final reply finish_reason=%s chars=%d tail=%r",
                    finish,
                    len(text),
                    text[-40:],
                )
            if reason is None or attempt == self.retries:
                return response
            logger.warning("[BknAgent] final reply cut short (%s); asking the model to finish", reason)
            response = await handler(
                request.override(
                    messages=[*request.messages, _without_markup(last), HumanMessage(CONTINUE)]
                )
            )
        return response
