# Route declarations only.
import datetime

from fastapi import APIRouter, Body, Request, Query
from app.controller.llm_controller import *
from app.interfaces.logics import LLMUsedOpenAI
from app.utils.common import get_user_info
from app.utils.observability.observability_log import get_logger

llm_route = APIRouter()
health_route = APIRouter()
from fastapi.security import HTTPBearer

bearer_scheme = HTTPBearer()


@health_route.get("/health/ready", include_in_schema=False)
async def health_ready():
    return {"res": 0}


@health_route.get("/health/alive", include_in_schema=False)
async def health_alive():
    return {"res": 0}


@llm_route.post("/llm/add")
async def add_llm(request: Request, model_para: dict = Body(...)):
    userId, language, role = await get_user_info(request)
    return await add_model(model_para, userId, language, role)


@llm_route.post("/llm/delete")
async def remove_llm(request: Request, model_ids: dict = Body(...)):
    userId, language, role = await get_user_info(request)
    return await remove_model(model_ids, userId, language, role)


@llm_route.post("/llm/test")
async def test_llm_(request: Request, model_config: dict = Body(...)):
    userId, language, role = await get_user_info(request)
    return await test_model(model_config, userId, language)


@llm_route.post("/llm/edit")
async def edit_llm(request: Request, model_para: dict = Body(...)):
    userId, language, role = await get_user_info(request)
    return await edit_model(model_para, userId, language, role)


@llm_route.get("/llm/list")
async def source_llm(request: Request, page, size, order='desc', rule='update_time', series='all', name='',
                     api_model='', model_type='', quota: bool = Query(default=None)):
    userId, language, role = await get_user_info(request)
    return await source_model(userId, language, page, size, name, order, series, rule, api_model, model_type, quota, role)


@llm_route.post("/llm/names")
async def names_llm(request: Request, model_para: dict = Body(...)):
    userId, language, role = await get_user_info(request)
    return await check_names_by_ids(model_para.get("ids", []), userId, language, role)


@llm_route.get("/llm/get")
async def check_llm_(model_id, request: Request, ):
    userId, language, role = await get_user_info(request)
    return await check_model(model_id, userId, language, role)


@llm_route.get("/llm-api-doc")
async def api_doc_llm(request: Request, llm_id):
    return await api_doc_model(llm_id)


# @llm_route.post("/chat/completions")
async def llm_used_openai2(request: LLMUsedOpenAI, head_request: Request):
    '''
    OpenAI-compatible large model invocation endpoint
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
                        description: 'Nucleus sampling value from 0 to 1; default 1'
                        example: 0.7
                    temperature:
                        type: float
                        format: float
                        description: 'Sampling randomness from 0 to 2; default 1'
                        example: 0
                    presence_penalty:
                        type: float
                        format: float
                        description: 'Presence penalty from -2 to 2; default 0'
                        example: 0
                    frequency_penalty:
                        type: float
                        format: float
                        description: 'Frequency penalty from -2 to 2; default 0'
                        example: 0
                    max_tokens:
                        type: integer
                        format: integer
                        description: 'Maximum response tokens from 10 to the model limit; default 1000'
                        example: 1000
                    messages:
                        type: object
                        properties:
                            role:
                                type: string
                                format: string
                                description: 'Role: system, assistant, or user'
                                example: 'user'
                            content:
                                type: string
                                format: string
                                description: 'Message content'
                                example: 'Who are you?'
                    stream:
                        type: boolean
                        description: 'Whether to stream the response; default false'
                        example: true
                    top_k:
                        type: integer
                        description: 'Value greater than or equal to 1, or -1; default 1',
                        example: 1

    '''
    userId, language, role = await get_user_info(head_request)
    headers = head_request.headers
    func_module = headers.get('x-func-module', "")
    return await used_model_openai(request.dict(), userId, language, func_module)


# @llm_route.post("/encode")
# async def encode(request: Request, params_json: dict = Body(...)):
#     userId, language, role = await get_user_info(request)
#     return await encode_endpoint(params_json, userId, language)


@llm_route.get("/llm/monitor/list")
async def monitor_llm(request: Request, model_id):
    userId, language, role = await get_user_info(request)
    return await get_monitor_data(userId, language, model_id, role)


@llm_route.post("/llm/default/edit")
async def edit_default_llm(request: Request, model_para: dict = Body(...)):
    userId, language, role = await get_user_info(request)
    return await edit_default_model(model_para, userId, language, role)


@llm_route.get("/llm/monitor/overview")
async def get_performance_analysis(request: Request, start_time="", end_time="", model_id=""):
    userId, language, role = await get_user_info(request)
    return await get_overview_data(userId, language, model_id, start_time, end_time, role)
