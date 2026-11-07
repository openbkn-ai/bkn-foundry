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
from langchain_core.messages.ai import add_usage

logger = logging.getLogger(__name__)

# The start of a tool call DeepSeek writes into the text instead of tool_calls:
# "DSML" and a full-width bar (｜), escaped so the source stays ASCII.
DSML_MARKER = "DSML\uff5c"

CONTINUE = (
    "Your previous reply was cut off before it was finished. Call the tools you still need,"
    " or give your complete answer from the beginning: only your next reply is shown."
)
# A reply the output limit stopped would stop there again if written the same way.
SHORTER = " It reached the output limit, so write it shorter, to fit."

# A final reply this short and ending this way may have been cut off with a normal stop.
_SHORT = 200
# Its ends, full-width ones escaped: 。！？ ） 」』 ：
_ENDINGS = tuple("\u3002\uff01\uff1f.!?\uff09)\u300d\u300f|*`>:\uff1a")


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


def _counted(response, spent):
    """The response with the usage of the replies sent back added to its last message.
    Those replies never reach the state, and the run's usage is read from the state."""
    if spent is None or not response.result or not isinstance(response.result[-1], AIMessage):
        return response
    last = response.result[-1]
    response.result[-1] = last.model_copy(update={"usage_metadata": add_usage(spent, last.usage_metadata)})
    return response


class UnfinishedAnswer(AgentMiddleware):
    """Asks the model to finish a final reply that was cut short."""

    def __init__(self, retries: int = 2):
        super().__init__()
        self.retries = retries

    async def awrap_model_call(self, request, handler):
        response = await handler(request)
        spent = None
        for attempt in range(self.retries + 1):
            if response.structured_response is not None or not response.result:
                return _counted(response, spent)
            last = response.result[-1]
            if not isinstance(last, AIMessage) or last.tool_calls:
                return _counted(response, spent)
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
                return _counted(response, spent)
            logger.warning("[BknAgent] final reply cut short (%s); asking the model to finish", reason)
            if last.usage_metadata:
                spent = add_usage(spent, last.usage_metadata)
            ask = CONTINUE + (SHORTER if reason == "length" else "")
            response = await handler(
                request.override(messages=[*request.messages, _without_markup(last), HumanMessage(ask)])
            )
        return _counted(response, spent)
