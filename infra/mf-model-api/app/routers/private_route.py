from fastapi import APIRouter, Body, Request

from app.controller import small_model_controller, llm_controller
from app.controller.llm_controller import *
from app.interfaces import logics
from app.interfaces.logics import LLMUsedOpenAI
from app.utils.common import get_user_info

private_route = APIRouter()


# Internal large-model invocation endpoint.
@private_route.post("/chat/completions")
async def llm_used_openai2(request: LLMUsedOpenAI, head_request: Request):
    '''
    Invoke a large model through an OpenAI-compatible API.
    ---
    operationId: llm_used_openai
    requestBody:
        description: 'request body'
        content:
            application/json:
                schema:
                type: 'object'
                required:
                    - model
                    - messages
                properties:
                    model:
                        type: string
                        format: string
                        description: 'Name of the model to invoke'
                        example: 'deepseek-chat'
                    top_p:
                        type: float
                        format: float
                        description: 'Nucleus sampling value from 0 to 1; defaults to 1'
                        example: 0.7
                    temperature:
                        type: float
                        format: float
                        description: 'Sampling temperature from 0 to 2; defaults to 1'
                        example: 0
                    presence_penalty:
                        type: float
                        format: float
                        description: 'Presence penalty from -2 to 2; defaults to 0'
                        example: 0
                    frequency_penalty:
                        type: float
                        format: float
                        description: 'Frequency penalty from -2 to 2; defaults to 0'
                        example: 0
                    max_tokens:
                        type: integer
                        format: integer
                        description: 'Maximum response tokens, from 10 to the model limit; defaults to 1000'
                        example: 1000
                    messages:
                        type: object
                        properties:
                            role:
                                type: string
                                format: string
                                description: 'Message role: system, assistant, or user'
                                example: 'user'
                            content:
                                type: string
                                format: string
                                description: 'Message content'
                                example: 'Who are you?'
                    stream:
                        type: boolean
                        description: 'Whether to stream the response; defaults to false'
                        example: true
                    top_k:
                        type: integer
                        description: 'Value greater than or equal to 1, or -1; defaults to 1',
                        example: 1

    '''
    userId, language, role = await get_user_info(head_request)
    headers = head_request.headers
    func_module = headers.get('x-func-module', "")
    return await used_model_openai(request.dict(), userId, language, func_module, dict(headers))


# Internal reranker invocation endpoint.
@private_route.post("/small-model/reranker")
async def model_used(request: logics.UsedReranker, head_request: Request):
    userId, language, role = await get_user_info(head_request)
    headers = head_request.headers
    func_module = headers.get('x-func-module', "")
    return await small_model_controller.reranker_model_used(request, userId, language, role, func_module)


# Internal embedding invocation endpoints.
@private_route.post("/small-model/embedding")
async def model_used(request: logics.UsedEmbedding, head_request: Request):
    userId, language, role = await get_user_info(head_request)
    headers = head_request.headers
    func_module = headers.get('x-func-module', "")
    return await small_model_controller.embedding_model_used(request, userId, language, role, func_module)


@private_route.post("/small-model/embeddings")
async def model_used(request: logics.UsedEmbedding, head_request: Request):
    userId, language, role = await get_user_info(head_request)
    headers = head_request.headers
    func_module = headers.get('x-func-module', "")
    return await small_model_controller.embedding_model_used(request, userId, language, role, func_module)
