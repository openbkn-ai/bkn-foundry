from fastapi import APIRouter, Body, Request, Query

from app.controller import small_model_controller, llm_controller
from app.controller.llm_controller import *
from app.controller.prompt_controller import run_prompt_endpoint_stream, source_prompt_item_endpoint, \
    source_prompt_endpoint, prompt_list_endpoint, template_source_prompt_endpoint, check_prompt_endpoint, \
    add_prompt_item_endpoint, edit_prompt_item_endpoint, add_prompt_type_endpoint, edit_prompt_type_endpoint, \
    add_prompt_endpoint, name_edit_prompt_endpoint, edit_prompt_endpoint, edit_template_prompt_endpoint, \
    delete_prompt_endpoint, move_prompt_endpoint, batch_add_prompt_endpoint
from app.interfaces import logics
from app.interfaces.logics import LLMUsedOpenAI
from app.utils.common import get_user_info

private_route = APIRouter()


# Internal large-model invocation endpoint.
# @private_route.post("/chat/completions")
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


# Internal reranker invocation endpoint.
# @private_route.post("/small-model/reranker")
async def model_used(request: logics.UsedReranker, head_request: Request):
    userId, language, role = await get_user_info(head_request)
    headers = head_request.headers
    func_module = headers.get('x-func-module', "")
    return await small_model_controller.reranker_model_used(request, userId, language, role, func_module)


# Internal embedding invocation endpoints.
# @private_route.post("/small-model/embedding")
async def model_used(request: logics.UsedEmbedding, head_request: Request):
    userId, language, role = await get_user_info(head_request)
    headers = head_request.headers
    func_module = headers.get('x-func-module', "")
    return await small_model_controller.embedding_model_used(request, userId, language, role, func_module)


# @private_route.post("/small-model/embeddings")
# async def model_used(request: logics.UsedEmbedding, head_request: Request):
#     userId, language, role = await get_user_info(head_request)
#     headers = head_request.headers
#     func_module = headers.get('x-func-module', "")
#     return await small_model_controller.embedding_model_used(request, userId, language, role, func_module)


@private_route.get("/llm/list")
async def source_llm(request: Request, page, size, order='desc', rule='update_time', series='all', name='',
                     api_model='', model_type='', quota: bool = Query(default=None)):
    userId, language, role = await get_user_info(request)
    return await source_model(userId, language, page, size, name, order, series, rule, api_model, model_type, quota)


@private_route.get("/llm/get")
async def check_llm_(model_id, request: Request, ):
    userId, language, role = await get_user_info(request)
    return await check_model(model_id, userId, language)


@private_route.post("/prompt-run-stream")
async def run_prompt_stream(request: Request, params: dict = Body(...)):
    userId = ""
    security_token = ""
    return await run_prompt_endpoint_stream(userId, params, security_token)


@private_route.get('/prompt-item-source')
async def source_prompt_item(request: Request, prompt_item_name='', prompt_name=''):
    return await source_prompt_item_endpoint(request, prompt_item_name, prompt_name)


@private_route.get('/prompt-source')
async def source_prompt(
        request: Request,
        page, size,
        prompt_item_id='',
        prompt_item_type_id='', prompt_name='',
        order='desc', rule='update_time', deploy='all', prompt_type='all'):
    return await source_prompt_endpoint(
        request,
        prompt_item_id, prompt_item_type_id,
        page, size, prompt_name, order, rule, deploy, prompt_type)


@private_route.get('/prompt-list')
async def prompt_list():
    return await prompt_list_endpoint()


@private_route.get('/prompt-template-source')
async def template_source_prompt(request: Request, prompt_type='', prompt_name=''):
    return await template_source_prompt_endpoint(request, prompt_type, prompt_name)


@private_route.get('/prompt/{prompt_id}')
async def check_prompt(request: Request, prompt_id):
    return await check_prompt_endpoint(request, prompt_id)


@private_route.post("/prompt-item-add")
async def add_prompt_item(request: Request, params: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await add_prompt_item_endpoint(userId, params)


@private_route.post("/prompt-item-edit")
async def edit_prompt_item(request: Request, model_para: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await edit_prompt_item_endpoint(userId, model_para)


@private_route.post("/prompt-type-add")
async def add_prompt_type(request: Request, model_para: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await add_prompt_type_endpoint(userId, model_para)


@private_route.post("/prompt-type-edit")
async def edit_prompt_type(request: Request, model_para: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await edit_prompt_type_endpoint(userId, model_para)


@private_route.post("/prompt-add")
async def add_prompt(request: Request, model_para: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await add_prompt_endpoint(userId, model_para)


@private_route.post("/prompt-name-edit")
async def name_edit_prompt(request: Request, model_para: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await name_edit_prompt_endpoint(userId, model_para)


@private_route.post("/prompt-edit")
async def edit_prompt(request: Request, model_para: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await edit_prompt_endpoint(userId, model_para)


@private_route.post("/prompt-template-edit")
async def edit_prompt(request: Request, params: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await edit_template_prompt_endpoint(userId, params)


#
# @private_route .get('/open/prompt_completion/{prompt_id}')
# async def completion_prompt(request: Request, prompt_id, inputs=''):
#     headers = request.headers
#     userId = headers.get("x-account-id")
#     return await completion_prompt_endpoint(userId, prompt_id, inputs)


@private_route.post("/delete-prompt")
async def delete_prompt(request: Request, delete_id: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await delete_prompt_endpoint(userId, delete_id)


@private_route.post("/prompt/move")
async def move_prompt(request: Request, move_param: dict = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await move_prompt_endpoint(userId, move_param)


@private_route.post("/prompt/batch_add")
async def batch_add_prompt(request: Request, model_para: list = Body(...)):
    headers = request.headers
    userId = headers.get("x-account-id")
    return await batch_add_prompt_endpoint(userId, model_para)


@private_route.post("/llm/add")
async def add_llm(request: Request, model_para: dict = Body(...)):
    userId, language, role = await get_user_info(request)
    userId = "266c6a42-6131-4d62-8f39-853e7093701c"
    return await llm_controller.add_model(model_para, userId, language)


@private_route.post("/small-model/add")
async def add_model(head_request: Request, request_data: dict = Body(..., example={"is_private": True})):
    userId, language, role = await get_user_info(head_request)
    userId = "266c6a42-6131-4d62-8f39-853e7093701c"
    if "is_private" not in request_data:
        request_data["is_private"] = True
    request = logics.AddExternalSmallModel(**request_data)
    return await small_model_controller.add_model(request, userId, language, role, private=True)


@private_route.post("/llm/delete_by_name")
async def remove_llm(request: Request, model_names: dict = Body(...)):
    userId, language, role = await get_user_info(request)
    return await remove_model_by_name(model_names, userId, language)


@private_route.post("/small-model/delete_by_name")
async def delete_model(request: Request, model_para: dict = Body(...)):
    userId, language, role = await get_user_info(request)
    return await small_model_controller.delete_model_by_name(model_para, userId, language)


@private_route.get("/small-model/get")
async def get_info(request: Request, model_id):
    userId, language, role = await get_user_info(request)
    return await small_model_controller.get_info(model_id, userId, role)


@private_route.get("/small-model/list")
async def get_info_list(request: Request, order: str = Query(regex=r'^(desc|asc)$', default="desc"),
                        rule: str = Query(regex=r'^(create_time|update_time|model_name)$', default="update_time"),
                        page: int = Query(ge=1, default=1), size: int = Query(ge=1, default=20),
                        model_name: str = Query(default=""), model_type: str = Query(default=""),
                        model_series: str = Query(default="")):
    userId, language, role = await get_user_info(request)
    return await small_model_controller.get_info_list(order, rule, page, size, model_name, model_type,
                                                      model_series, userId, role)


@private_route.get("/small-model/get_by_name")
async def get_info(request: Request, model_name):
    userId, language, role = await get_user_info(request)
    return await small_model_controller.get_info_by_name(model_name)


@private_route.get("/small-model/get_default")
async def get_default(request: Request, model_type: str = Query(default="embedding")):
    return await small_model_controller.get_default_model(model_type)
