import time
from datetime import datetime, timedelta
from pydantic.types import Decimal
import func_timeout.exceptions
from fastapi.responses import JSONResponse
from fastapi import status, Response
from app.commons.errors.codes import ParamValidationErrors
from app.commons.i18n import get_error_message
from app.commons.locale import error_with_message
from app.commons.snow_id import worker
from app.controller import model_quota_controller
from app.core.config import base_config
from app.dao.model_quota_dao import model_quota_dao
from app.logs.stand_log import StandLogger
from app.mydb.ConnectUtil import redis_util, get_redis_util
from app.utils import llm_utils
from app.utils.common import validate_required_params
from app.utils.config_cache import quota_config_cache_tree
from app.utils.permission_manager import permission_manager
from app.utils.llm_utils import openai_series_stream, OpenAIClientRequest
from app.utils.param_verify_utils import *
from app.utils.reshape_utils import *
from app.utils.str_util import generate_random_string
from app.utils.verify_utils import llm_test
from sse_starlette import EventSourceResponse
import re

# Save a large model configuration.
async def add_model(schema_para, userId, language, role=""):
    required_params = ["max_model_len"]
    missing_params = await validate_required_params(schema_para, required_params)
    if missing_params:
        code = ParamValidationErrors.ParamMissing
        content = await get_error_message(code, language)
        content["detail"] = f"missing parameters: {', '.join(missing_params)}"
        return JSONResponse(status_code=status.HTTP_400_BAD_REQUEST, content=content)
    # Large-model creation uses bkn-safe authorization with the wildcard resource ID.
    permission = await permission_manager.check_single_permission(user_id=userId, resource_id="*",
                                                                  operations="create",
                                                                  resource_type="large_model",
                                                                  role=role)
    if not permission:
        return JSONResponse(status_code=403, content=NotPermissionError)
    quota = schema_para.get("quota", False)
    content = await llm_add_verify(schema_para, userId)
    if content:
        StandLogger.error(content)
        content = content.copy()
        return JSONResponse(status_code=400, content=content)
    else:
        try:
            model_configs = schema_para['model_config']
            config = model_configs
            if schema_para["model_series"] == "baidu":
                config["secret_key"] = model_configs.get("secret_key", "")
            elif schema_para["model_series"] == "baidu_tianchen":
                config["ClientId"] = model_configs.get("ClientId", "")
                config["OperationCode"] = model_configs.get("OperationCode", "")
            model_id = worker.get_id()
            llm_model_dao.add_data_into_model_list(model_id, schema_para["model_series"],
                                                   schema_para.get("model_type", "llm"),
                                                   schema_para["model_name"], model_configs["api_model"],
                                                   userId, json.dumps(config),
                                                   schema_para["max_model_len"],
                                                   schema_para.get("model_parameters", None), quota)
            # Grant the new model instance to its creator; add_permission bypasses administrators.
            if base_config.AUTH_ENABLED:
                user_infos = await get_username_by_ids([userId])
                user_name = user_infos.get(userId, "")
            else:
                user_name = ""
            grant_ok = await permission_manager.add_permission(
                user_id=userId, resource_id=str(model_id), resource_name="大模型",
                resource_type="large_model", user_name=user_name, role=role)
            if not grant_ok:
                raise Exception("add permission failed")
            # Return Snowflake IDs as strings because 19-digit values exceed JavaScript's safe integer range.
            # Numeric JSON would lose precision and make the returned resource impossible to query or delete.
            content = {"status": "ok", "id": str(model_id)}
            if quota is False:
                conf_id = worker.get_id()
                model_quota_dao.add_no_model_quota_config(conf_id, model_id, userId)
                quota_config_cache_tree.add({"f_model_id": model_id})
            return JSONResponse(status_code=200, content=content)
        except Exception as e:
            StandLogger.error(e.args)
            return JSONResponse(status_code=500, content=DataBaseError)


async def remove_model(model_ids, userId, language, role=""):
    try:
        if "model_ids" not in model_ids.keys():
            raise RequestValidationError([{"loc": ('body', "model_ids"), "type": "value_error.missing"}])
        if not isinstance(model_ids["model_ids"], list):
            raise RequestValidationError([{"loc": ('body', "model_ids"), "type": "value_error.type_error"}])
        model_dict = {info["f_model_id"]: info["f_model_name"] for info in llm_model_dao.get_all_model_list()}
        model_names = []
        cache_key_list = []
        for model_id in model_ids["model_ids"]:
            model_name = model_dict[model_id]
            model_names.append(model_name)
            cache_key_list.append(f"dip:model-api:llm:{model_name}:list")
            if model_id not in model_dict:
                StandLogger.error(LLMRemoveError["detail"])
                return JSONResponse(status_code=400, content=LLMRemoveError)
        # Check delete permission for every large model before removal.
        for model_id in model_ids["model_ids"]:
            permission = await permission_manager.check_single_permission(
                user_id=userId, resource_id=str(model_id), operations="delete",
                resource_type="large_model", role=role)
            if not permission:
                error_dict = NotPermissionError.copy()
                error_dict["detail"] = "One or more models cannot be deleted by the current identity."
                return JSONResponse(status_code=403, content=error_dict)
        llm_model_dao.delete_model_by_id(model_ids["model_ids"])
        model_quota_dao.delete_model_quota_by_model_id(model_ids["model_ids"])
        # Remove all bkn-safe authorization policies for the deleted model instances.
        await permission_manager.delete_permission(resource_type="large_model",
                                                   resource_ids=[str(mid) for mid in model_ids["model_ids"]])
        # Lazily initialize the Redis client.
        global redis_util
        if redis_util is None:
            redis_util = await get_redis_util()
        await redis_util.delete_str(cache_key_list)
        quota_config_cache_tree.delete_batch(model_ids['model_ids'])
        content = {"status": "ok", "id": model_ids['model_ids']}
        return JSONResponse(status_code=200, content=content)
    except Exception as e:
        if isinstance(e, RequestValidationError):
            raise e
        StandLogger.error(str(e))
        return JSONResponse(status_code=400, content=LLMRemoveError)


async def remove_model_by_name(model_names, userId, language):
    try:
        if "model_names" not in model_names.keys():
            raise RequestValidationError([{"loc": ('body', "model_ids"), "type": "value_error.missing"}])
        if not isinstance(model_names["model_names"], list):
            raise RequestValidationError([{"loc": ('body', "model_ids"), "type": "value_error.type_error"}])
        model_dict = {info["f_model_name"]: info["f_model_id"] for info in llm_model_dao.get_all_model_list()}
        model_ids = []
        cache_key_list = []
        for model_name in model_names["model_names"]:
            model_id = model_dict[model_name]
            model_ids.append(model_id)
            cache_key_list.append(f"dip:model-api:llm:{model_name}:list")
            if model_name not in model_dict:
                StandLogger.error(LLMRemoveError["detail"])
                return JSONResponse(status_code=400, content=LLMRemoveError)
        llm_model_dao.delete_model_by_id(model_ids)

        # Lazily initialize the Redis client.
        global redis_util
        if redis_util is None:
            redis_util = await get_redis_util()
        await redis_util.delete_str(cache_key_list)
        content = {"status": "ok", "id": model_ids}
        return JSONResponse(status_code=200, content=content)
    except Exception as e:
        if isinstance(e, RequestValidationError):
            raise e
        StandLogger.error(str(e))
        return JSONResponse(status_code=400, content=LLMRemoveError)


async def test_model(model_config, userId, language):
    model_id = model_config.get("model_id", "-")
    change = model_config.get("change", False)
    model_config_new = model_config.get("model_config",{})
    if not change:
        try:
            info = llm_model_dao.get_data_from_model_list_by_id(model_id)
            if not info:
                StandLogger.error(LLMTestError["detail"])
                return JSONResponse(status_code=400, content=LLMTestError)
            config_str = info[0]["f_model_config"]
            model_config_old = json.loads(config_str.replace("'", '"'))
            if not model_config_new:
                config = model_config_old
                series = info[0]["f_model_series"]
                model_type = info[0]["f_model_type"]
            else:
                config = model_config['model_config']
                if 'api_key' in config:
                    config['api_key'] = model_config_old.get("api_key","")
                series = model_config['model_series']
                model_type = model_config['model_type']
        except Exception as e:
            StandLogger.error(str(e))
            return JSONResponse(status_code=400, content=DataBaseError)
    else:
        if llm_test_verify(model_config):
            content = llm_test_verify(model_config)
            StandLogger.error(content)
            return JSONResponse(status_code=400, content=content)
        series = model_config['model_series']
        config = model_config['model_config']
        model_type = model_config['model_type']
    try:
        res = await llm_test(series, config, model_config.get("model_id", ""), userId, model_type)
        return res
    except Exception as e:
        StandLogger.error(str(e))
        error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
        error_dict["description"] = "The model service URL is not reachable."
        error_dict["detail"] = str(e)
        return JSONResponse(status_code=400, content=error_dict)


async def edit_model(model_para, userId, language, role=""):
    content = llm_edit_verify(model_para)
    if content:
        StandLogger.error(content)
        return JSONResponse(status_code=400, content=content)
    else:
        try:
            change = model_para.get("change", False)
            ids_list = [ids["f_model_id"] for ids in llm_model_dao.get_all_model_list()]
            if model_para['model_id'] not in ids_list:
                StandLogger.error(LLMEdit2Error["detail"])
                return JSONResponse(status_code=400, content=LLMEdit2Error)
            else:
                # Check bkn-safe modify permission for the large model.
                permission = await permission_manager.check_single_permission(
                    user_id=userId, resource_id=str(model_para["model_id"]), operations="modify",
                    resource_type="large_model", role=role)
                if not permission:
                    return JSONResponse(status_code=403, content=NotPermissionError)
                info = llm_model_dao.get_data_from_model_list_by_id(model_para["model_id"])
                old_quota = info[0]["f_quota"]
                model_name = info[0]["f_model_name"]
                # for key in config.keys():
                #     if key in config_pa.keys() and config[key] != config_pa[key]:
                #         if key in ["api_key", "secret_key", "api_type"]:
                #             continue
                #         StandLogger.error(LLMEdit3Error["detail"])
                #         return JSONResponse(status_code=400, content=LLMEdit3Error)
                # if model_para['model_series'] != info[0]["f_model_series"]:
                #     StandLogger.error(LLMEdit3Error["detail"])
                #     return JSONResponse(status_code=400, content=LLMEdit3Error)
                # else:
                model_name_list = [ids["f_model_name"] for ids in llm_model_dao.get_all_model_list()]
                old_name = info[0]["f_model_name"]
                re_name = model_para['model_name']
                quota = model_para["quota"]
                config_old = json.loads(info[0]["f_model_config"])
                config_new = model_para['model_config']
                if not change:
                    if 'api_key' in config_new:
                        config_new["api_key"] = config_old.get("api_key","")
                model_config = config_new
                model_series = model_para["model_series"]
                model_type = model_para["model_type"]
                if old_quota != quota:
                    model_quota_dao.delete_model_quota_by_model_id([model_para["model_id"]])
                if re_name not in model_name_list or re_name == old_name:
                    llm_model_dao.edit_model(model_para["model_id"], re_name, userId,
                                             model_para["max_model_len"],
                                             model_para.get("model_parameters", None), quota, json.dumps(model_config),
                                             model_series, model_type)
                    global redis_util
                    if redis_util is None:
                        redis_util = await get_redis_util()
                    quota_cache_key = f"dip:model-api:llm:{model_name}:list"
                    llm_cache_key = f"dip:model-api:llm:{model_name}:list"
                    llm_default_key = f"dip:model-api:llm:default_model_3ed523:list"
                    await redis_util.delete_str(quota_cache_key)
                    await redis_util.delete_str(llm_cache_key)
                    await redis_util.delete_str(llm_default_key)
                    content = {"status": "ok", "id": model_para['model_id']}
                    return JSONResponse(status_code=200, content=content)
                else:
                    model_name_list = llm_model_dao.get_model_by_name(re_name)
                    if model_name_list[0]["f_create_by"] == userId:
                        LLMEdit4Error['description'] = "Model name already exists."
                        LLMEdit4Error['detail'] = "Another model already uses this name."
                    else:
                        LLMEdit4Error['description'] = "Model name is already in use."
                        LLMEdit4Error['detail'] = "Another user already uses this model name."
                    StandLogger.error(LLMEdit4Error["detail"])
                    content = LLMEdit4Error.copy()
                    return JSONResponse(status_code=500, content=content)
        except Exception as e:
            StandLogger.error(e.args)
            return JSONResponse(status_code=500, content=DataBaseError)


async def source_model(userId, language, page, size, name, order, series, rule, api_model, model_type, quota, role=""):
    name = name.strip()
    error = llm_source_verify(order, page, size, rule, series, name, model_type)
    if error:
        StandLogger.error(error)
        return JSONResponse(status_code=400, content=error)
    else:
        try:
            # Return all models when authorization is disabled or the caller is an administrator.
            if not base_config.AUTH_ENABLED or userId == "266c6a42-6131-4d62-8f39-853e7093701c":
                result = llm_model_dao.get_data_from_model_list_by_name_fuzzy(name, page, size, order, rule, api_model,
                                                                              model_type)
                total = len(
                    llm_model_dao.get_data_from_model_list_by_name_fuzzy(name, 1, 1000000, order, rule, api_model,
                                                                         model_type))
                result = await reshape_source(result, total)
                return JSONResponse(status_code=200, content=result)
            else:
                # Authorization filter (#213): regular users see only models with large_model:display permission.
                # Return an empty list without grants; otherwise push authorized IDs into the paginated query.
                candidate_ids = [m['f_model_id'] for m in llm_model_dao.get_all_model_list()]
                permission_ids = await permission_manager.filter_authorized_ids(
                    user_id=userId, role=role, candidate_ids=candidate_ids,
                    resource_type="large_model", resource_name="大模型", operation="display")
                if not permission_ids:
                    return JSONResponse(status_code=200, content={"count": 0, "data": []})
                datas = await model_quota_controller.get_user_quote_model_list(userId, page, size, name, api_model,
                                                                               order, rule, quota, model_type,
                                                                               permission_ids=permission_ids)
                result = {
                    "count": datas["total"],
                    "data": datas["res"]
                }
                return JSONResponse(status_code=200, content=result)
        except Exception as e:
            StandLogger.error(e.args)
            return JSONResponse(status_code=500, content=DataBaseError)


async def check_model(model_id, userId, language, role=""):
    idx_list = [idx["f_model_id"] for idx in llm_model_dao.get_all_model_list()]
    if model_id not in idx_list:
        StandLogger.error(LLMCheckError["detail"])
        return JSONResponse(status_code=400, content=LLMCheckError)
    # Check bkn-safe display permission for the large model.
    permission = await permission_manager.check_single_permission(
        user_id=userId, resource_id=str(model_id), operations="display",
        resource_type="large_model", role=role)
    if not permission:
        return JSONResponse(status_code=403, content=NotPermissionError)
    try:
        result = reshape_check(llm_model_dao.get_data_from_model_list_by_id(model_id))
        return JSONResponse(status_code=200, content=result)
    except Exception as e:
        StandLogger.error(e.args)
        return JSONResponse(status_code=500, content=DataBaseError)
async def check_names_by_ids(model_ids, userId, language, role=""):
    # Resolve model names by ID for authorization UI display; omit unknown IDs.
    try:
        if not isinstance(model_ids, list) or not model_ids:
            return JSONResponse(status_code=200, content={"entries": []})
        ids = [str(model_id) for model_id in model_ids]
        original_res = llm_model_dao.get_model_info_by_ids(ids)
        entries = [{"id": str(item["f_model_id"]), "name": item["f_model_name"]} for item in original_res]
        return JSONResponse(status_code=200, content={"entries": entries})
    except Exception as e:
        StandLogger.error(e.args)
        return JSONResponse(status_code=500, content=DataBaseError)


async def param_model(user):
    try:
        info = llm_model_dao.get_all_data_from_model_series()
        result = await reshape_param(info)
        return JSONResponse(status_code=200, content=result)
    except Exception as e:
        StandLogger.error(e.args)
        return JSONResponse(status_code=500, content=DataBaseError)


async def api_doc_model(llm_id):
    from app.commons.restful_api import get_model_restful_api_document
    return JSONResponse(status_code=200, content={"res": get_model_restful_api_document(llm_id)})


from fastapi import Request


async def used_model_stream(request: Request, llm_id, ai_system, ai_user, ai_assistant, ai_history, prompt_type,
                            top_p=0.8,
                            temperature=0.9, max_tokens=512, frequency_penalty=0.1, presence_penalty=0.1, top_k=1,
                            return_info=False, prompt_tokens=0, user_id="", stream=True, model_name="", cache=False,
                            stop=None,
                            system=None, security_token=None):
    if system is None:
        system = []
    ids_list = [ids["f_model_id"] for ids in llm_model_dao.get_all_model_list()]
    if llm_id not in ids_list:
        StandLogger.error(LLMUsedError["detail"])
        return JSONResponse(status_code=400, content=LLMUsedError)
    info = llm_model_dao.get_data_from_model_list_by_id(llm_id)

    if info[0]["f_model_series"] == 'openai':
        try:
            types = info[0]["f_model_type"]
            config = json.loads(info[0]["f_model_config"].replace("'", '"'))
            try:
                result = openai_series_stream(
                    types=types,
                    api_key=config['api_key'],
                    api_model=config['api_model'],
                    ai_system=ai_system,
                    ai_user=ai_user,
                    ai_assistant=ai_assistant,
                    base_url=config['api_url'],
                    ai_history=ai_history,
                    top_p=top_p,
                    top_k=top_k,
                    temperature=temperature,
                    max_tokens=max_tokens,
                    frequency_penalty=frequency_penalty,
                    presence_penalty=presence_penalty,
                    llm_id=llm_id,
                    user_id=user_id,
                    cache=cache,
                    stop=stop
                )
                if stream:
                    try:
                        return EventSourceResponse(result, ping=3600)
                    except Exception as e:
                        StandLogger.error(e.args)
                        return JSONResponse(status_code=500,
                                            content=ModelFactory_ModelController_Model_ConnectError_Error)
                else:
                    end = False
                    message = ""
                    prompt_info = {}
                    for chunk in result:
                        if not end:
                            if len(chunk) <= 8 or chunk[0:8] != "--info--":
                                message += chunk
                            else:
                                chunk = chunk[8:]
                                prompt_info = json.loads(chunk)
                                end = True
                    res_id = "chatcmpl-" + generate_random_string(32)
                    res = {
                        "id": res_id,
                        "object": "chat.completion",
                        "created": int(time.time()),
                        "choices": [
                            {
                                "index": 0,
                                "message": {
                                    "role": "assistant",
                                    "content": message
                                },
                                "finish_reason": None
                            }
                        ],
                        "usage": {
                            "prompt_tokens": prompt_tokens,
                            "total_tokens": prompt_info["token_len"] + prompt_tokens,
                            "completion_tokens": prompt_info["token_len"]
                        },
                        "model": model_name
                    }
                    return JSONResponse(status_code=200, content=res)
            except Exception as e:
                StandLogger.error(e.args)
                return JSONResponse(status_code=500, content=ModelFactory_ModelController_Model_ConnectError_Error)

        except func_timeout.exceptions.FunctionTimedOut as e:
            StandLogger.error(e.args)
            return JSONResponse(status_code=500, content=LLMParamError)
    elif info[0]["f_model_series"].lower() == "claude":
        config = json.loads(info[0]["f_model_config"].replace("'", '"'))
        from app.utils.llm_utils import ClaudeClient
        claude_client = ClaudeClient(
            api_url=config['api_url'],
            api_model=config['api_model'],
            api_key=config['api_key'],
            model_id=llm_id,
            temperature=temperature,
            top_p=top_p,
            frequency_penalty=frequency_penalty,
            presence_penalty=presence_penalty,
            max_tokens=max_tokens,
            top_k=top_k,
            system=system
        )
        messages = []
        for item in ai_history:
            tmp_item = {
                "role": item["role"],
                "content": item["message"]
            }
            if tmp_item["role"] == "ai":
                tmp_item["role"] = "assistant"
            elif tmp_item["role"] == "human":
                tmp_item["role"] = "user"
            messages.append(tmp_item)
        messages.append({
            "role": "system",
            "content": ai_system
        })
        if stream:
            generator = claude_client.chat_completion_stream(messages, user_id, True, cache)
            return EventSourceResponse(generator, ping=3600)
        else:
            res = await claude_client.chat_completion(messages, user_id, '', cache)
            if "detail" in res.keys():
                return JSONResponse(status_code=400, content=res)
            else:
                return JSONResponse(status_code=200, content=res)
    elif info[0]["f_model_series"].lower() == "baidu":
        config = json.loads(info[0]["f_model_config"].replace("'", '"'))
        from app.utils.llm_utils import BaiduClient
        baidu_client = BaiduClient(
            api_url=config['api_url'],
            api_model=config['api_model'],
            api_key=config['api_key'],
            model_id=llm_id,
            temperature=temperature,
            top_p=top_p,
            frequency_penalty=frequency_penalty,
            presence_penalty=presence_penalty,
            max_tokens=max_tokens,
            top_k=top_k,
            secret_key=config['secret_key'],
            stop=stop
        )
        messages = []
        for item in ai_history:
            tmp_item = {
                "role": item["role"],
                "content": item["message"]
            }
            if tmp_item["role"] == "ai":
                tmp_item["role"] = "assistant"
            elif tmp_item["role"] == "human":
                tmp_item["role"] = "user"
            messages.append(tmp_item)
        messages.append({
            "role": "system",
            "content": ai_system
        })
        if stream:
            generator = baidu_client.chat_completion_stream(messages, user_id, True, cache)
            return EventSourceResponse(generator, ping=3600)
        else:
            res = await baidu_client.chat_completion(messages, user_id, '', cache)
            if "detail" in res.keys():
                return JSONResponse(status_code=400, content=res)
            else:
                return JSONResponse(status_code=200, content=res)
    elif info[0]["f_model_series"].lower() == "baidu_tianchen":
        config = json.loads(info[0]["f_model_config"].replace("'", '"'))
        from app.utils.llm_utils import BaiduTianchenClient
        baidu_tianchen_client = BaiduTianchenClient(
            api_url=config['api_url'],
            api_model=config['api_model'],
            model_id=llm_id,
            temperature=temperature,
            top_p=top_p,
            frequency_penalty=frequency_penalty,
            presence_penalty=presence_penalty,
            max_tokens=max_tokens,
            top_k=top_k,
            stop=stop,
            OperationCode=config['OperationCode'],
            ClientId=config['ClientId']
        )
        messages = []
        for item in ai_history:
            tmp_item = {
                "role": item["role"],
                "content": item["message"]
            }
            if tmp_item["role"] == "ai":
                tmp_item["role"] = "assistant"
            elif tmp_item["role"] == "human":
                tmp_item["role"] = "user"
            messages.append(tmp_item)
        messages.append({
            "role": "system",
            "content": ai_system
        })
        if stream:
            generator = baidu_tianchen_client.chat_completion_stream(messages, user_id, True, cache)
            return EventSourceResponse(generator, ping=3600)
        else:
            res = await baidu_tianchen_client.chat_completion(messages, user_id, cache)
            if "detail" in res.keys():
                return JSONResponse(status_code=400, content=res)
            else:
                return JSONResponse(status_code=200, content=res)
    else:
        config = json.loads(info[0]["f_model_config"].replace("'", '"'))
        from app.utils.llm_utils import OtherClient
        other_client = OtherClient(
            api_url=config['api_url'],
            api_model=config['api_model'],
            api_key=config.get('api_key', ''),
            model_id=llm_id,
            temperature=temperature,
            top_p=top_p,
            frequency_penalty=frequency_penalty,
            presence_penalty=presence_penalty,
            max_tokens=max_tokens,
            top_k=top_k,
            stop=stop,
            model_type=config.get("model_type", "llm")
        )
        messages = []
        for item in ai_history:
            tmp_item = {
                "role": item["role"],
                "content": item["message"]
            }
            if tmp_item["role"] == "ai":
                tmp_item["role"] = "assistant"
            elif tmp_item["role"] == "human":
                tmp_item["role"] = "user"
            messages.append(tmp_item)
        messages.append({
            "role": "user",
            "content": ai_system
        })

        if stream:
            try:
                generator = other_client.chat_completion_stream(messages, user_id, return_info, cache)
                return EventSourceResponse(generator, ping=3600)
            except Exception as e:
                raise e
        else:
            try:
                res = await other_client.chat_completion(messages, user_id, '')
                if "detail" in res.keys():
                    return JSONResponse(status_code=400, content=res)
                else:
                    return JSONResponse(status_code=200, content=res)
            except Exception as e:
                raise e


async def used_model_openai(request, user_id, language, func_module):
    if "stream" not in request.keys():
        stream = True
    else:
        stream = request["stream"]
        if not isinstance(stream, bool):
            error_dict = error_with_message(
                ModelFactory_Router_ParamError_TypeError_Error,
                "ModelFactory.Validation.BooleanParameter",
                parameter="stream")
            StandLogger.error(error_dict["detail"])
            return JSONResponse(status_code=400, content=error_dict)

    model_name = request["model"]
    cache_key = f"dip:model-api:llm:{model_name}:list"
    try:
        # Lazily initialize the Redis client.
        global redis_util
        if redis_util is None:
            redis_util = await get_redis_util()
        res = await redis_util.get_str(cache_key)
        if res is not None and isinstance(res, (str, bytes)):
            try:
                model_info = eval(res)
            except Exception as e:
                StandLogger.warn(f"Failed to parse cache data: {str(e)}, key={cache_key}, value={res}")
                # Fall back to the database when cached data is invalid.
                model_info = llm_model_dao.get_data_from_model_list_by_name(model_name)
                if len(model_info) == 0:
                    return JSONResponse(status_code=404, content=ModelFactory_ExternalSmallModel_Used_NameNotExist)
                # Refresh the quota cache.
                await redis_util.set_str(key=cache_key, value=str(model_info), expire=300)
        else:
            # Query the database when the cache is missing or has an unexpected type.
            model_info = llm_model_dao.get_data_from_model_list_by_name(model_name)
            if len(model_info) == 0:
                return JSONResponse(status_code=404, content=ModelFactory_ExternalSmallModel_Used_NameNotExist)
            # Cache the quota result.
            await redis_util.set_str(key=cache_key, value=str(model_info), expire=300)
    except Exception as e:
        StandLogger.error(e.args)
        DataBaseError["detail"] = str(e)
        return JSONResponse(status_code=500, content=DataBaseError)
    model_data = model_info[0]
    model_series = model_data["f_model_series"]
    context_size = model_data["f_max_model_len"]
    model_id = model_data["f_model_id"]
    quota = model_data["f_quota"]
    if quota:
        quota_cache_key = f"dip:model-api:llm-quota:{model_name}:list"
        res = await redis_util.get_str(quota_cache_key)
        if res is not None and isinstance(res, (str, bytes)):
            try:
                model_quota_info = eval(res)
            except Exception as e:
                StandLogger.warn(f"Failed to parse cache data: {str(e)}, key={quota_cache_key}, value={res}")
                # Fall back to the database when cached data is invalid.
                model_quota_info = llm_model_dao.get_quota_by_user_and_model(user_id, model_id)
                # Refresh the quota cache.
                await redis_util.set_str(key=quota_cache_key, value=str(model_quota_info), expire=300)
        else:
            # Query the database when the cache is missing or has an unexpected type.
            model_quota_info = llm_model_dao.get_quota_by_user_and_model(user_id, model_id)
            # Cache the quota result.
            await redis_util.set_str(key=quota_cache_key, value=str(model_quota_info), expire=300)
        if len(model_quota_info) == 0 or model_quota_info[0]["remaining_input_tokens"] <= 0 or model_quota_info[0][
            "remaining_output_tokens"] <= 0:
            error_dict = ModelQuotaControllerUserModelConfigNoLeftSpaceError.copy()
            return JSONResponse(status_code=400, content=error_dict)

    if request["max_tokens"] > context_size * 1000:
        error_dict = ModelFactory_Router_ParamError_FormatError_Error.copy()
        error_dict["detail"] = f"max_tokens exceeds the maximum value of {context_size}k"
        return JSONResponse(status_code=400, content=error_dict)
    messages = request["messages"]
    message = messages[len(messages) - 1]["content"]
    history_dia = []
    for i in range(0, len(messages)):
        if messages[i]["tool_calls"] is None:
            messages[i].pop("tool_calls")
        if messages[i]["tool_call_id"] is None:
            messages[i].pop("tool_call_id")
        tmp_item = {
            "role": messages[i]["role"],
            "message": messages[i]["content"]
        }
        if tmp_item["role"] == "assistant":
            tmp_item["role"] = "ai"
        elif tmp_item["role"] in ["user", "system"]:
            tmp_item["role"] = "human"
        history_dia.append(tmp_item)

    if model_series == "openai":
        _, mess_str = llm_utils.prompt(message + "", "", "", history_dia.copy())
        config = json.loads(model_data["f_model_config"].replace("'", '"'))
        try:
            openai_client = OpenAIClientRequest(
                api_url=config['api_url'],
                api_model=config['api_model'],
                api_key=config.get("api_key", ""),
                model_id=model_data["f_model_id"],
                temperature=request["temperature"],
                top_p=request["top_p"],
                frequency_penalty=request["frequency_penalty"],
                presence_penalty=request["presence_penalty"],
                max_tokens=request["max_tokens"],
                top_k=request["top_k"],
                response_format=request["response_format"],
                stop=request["stop"],
                tools=request.get("tools", None),
                tool_choice=request.get("tool_choice", None)
            )
            if stream:
                return EventSourceResponse(
                    openai_client.chat_completion_stream_openai(messages, user_id, True, func_module, request["cache"]),
                    ping=3600)
            else:
                res = await openai_client.chat_completion(messages, user_id, func_module, request["cache"])
                if "detail" in res.keys():
                    return JSONResponse(status_code=500, content=res)
                else:
                    return JSONResponse(status_code=200, content=res)
        except Exception as e:
            StandLogger.error(e.args)
            return JSONResponse(status_code=500, content=ModelFactory_ModelController_Model_ConnectError_Error)
    elif model_series.lower() == "claude":
        config = json.loads(model_data["f_model_config"].replace("'", '"'))
        from app.utils.llm_utils import ClaudeClient
        try:
            claude_client = ClaudeClient(
                api_url=config['api_url'],
                api_model=config['api_model'],
                api_key=config['api_key'],
                model_id=model_data["f_model_id"],
                temperature=request["temperature"],
                top_p=request["top_p"],
                frequency_penalty=request["frequency_penalty"],
                presence_penalty=request["presence_penalty"],
                max_tokens=request["max_tokens"],
                top_k=request["top_k"],
                system=request.get("system", []),
                tools=request.get("tools", None),
                tool_choice=request.get("tool_choice", None)
            )
            if stream:
                return EventSourceResponse(
                    claude_client.chat_completion_stream_openai(messages, user_id, func_module, request["cache"]),
                    ping=3600)
            else:
                res = await claude_client.chat_completion(messages, user_id, func_module, request["cache"])
                if "detail" in res.keys():
                    return JSONResponse(status_code=500, content=res)
                else:
                    return JSONResponse(status_code=200, content=res)
        except Exception as e:
            StandLogger.error(e.args)
            return JSONResponse(status_code=500, content=ModelFactory_ModelController_Model_ConnectError_Error)
    elif model_series.lower() == "baidu":
        config = json.loads(model_data["f_model_config"].replace("'", '"'))
        from app.utils.llm_utils import BaiduClient
        baidu_client = BaiduClient(
            api_url=config['api_url'],
            api_model=config['api_model'],
            api_key=config['api_key'],
            model_id=model_data["f_model_id"],
            temperature=request["temperature"],
            top_p=request["top_p"],
            frequency_penalty=request["frequency_penalty"],
            presence_penalty=request["presence_penalty"],
            max_tokens=request["max_tokens"],
            top_k=request["top_k"],
            stop=request["stop"],
            secret_key=config["secret_key"]
        )
        if stream:
            return EventSourceResponse(
                baidu_client.chat_completion_stream_openai(messages, user_id, True, func_module, request["cache"]),
                ping=3600)
        else:
            res = await baidu_client.chat_completion(messages, user_id, func_module, request["cache"])
            if "detail" in res.keys():
                return JSONResponse(status_code=500, content=res)
            else:
                return JSONResponse(status_code=200, content=res)
    elif model_series.lower() == "baidu_tianchen":
        config = json.loads(model_data["f_model_config"].replace("'", '"'))
        from app.utils.llm_utils import BaiduTianchenClient
        try:
            baidu_tianchen_client = BaiduTianchenClient(
                api_url=config['api_url'],
                api_model=config['api_model'],
                model_id=model_data["f_model_id"],
                temperature=request["temperature"],
                top_p=request["top_p"],
                frequency_penalty=request["frequency_penalty"],
                presence_penalty=request["presence_penalty"],
                max_tokens=request["max_tokens"],
                top_k=request["top_k"],
                stop=request["stop"],
                OperationCode=config['OperationCode'],
                ClientId=config['ClientId']
            )
            if stream:
                return EventSourceResponse(
                    baidu_tianchen_client.chat_completion_stream_openai(messages, user_id, True, request["cache"]),
                    ping=3600)
            else:
                res = await baidu_tianchen_client.chat_completion(messages, user_id, request["cache"])
                if "detail" in res.keys():
                    return JSONResponse(status_code=500, content=res)
                else:
                    return JSONResponse(status_code=200, content=res)
        except Exception as e:
            return JSONResponse(status_code=500, content=ModelFactory_ModelController_Model_ConnectError_Error)
    else:
        config = json.loads(model_data["f_model_config"].replace("'", '"'))
        from app.utils.llm_utils import OtherClient
        try:
            other_client = OtherClient(
                api_url=config['api_url'],
                api_model=config['api_model'],
                api_key=config.get("api_key", ""),
                model_id=model_data["f_model_id"],
                temperature=request["temperature"],
                top_p=request["top_p"],
                frequency_penalty=request["frequency_penalty"],
                presence_penalty=request["presence_penalty"],
                max_tokens=request["max_tokens"],
                top_k=request["top_k"],
                response_format=request["response_format"],
                stop=request["stop"],
                model_type=model_data["f_model_type"],
                tools=request.get("tools", None),
                tool_choice=request.get("tool_choice", None)
            )
            if stream:
                return EventSourceResponse(
                    other_client.chat_completion_stream_openai(messages, user_id, True, model_data, func_module),
                    ping=3600)
            else:
                res = await other_client.chat_completion(messages, user_id, func_module)
                if "detail" in res.keys():
                    return JSONResponse(status_code=500, content=res)
                else:
                    return JSONResponse(status_code=200, content=res)
        except Exception as e:
            StandLogger.error(f"call llmModelError {config['api_model']} error params={messages},error={e}")
            return JSONResponse(status_code=500, content=ModelFactory_ModelController_Model_ConnectError_Error)


async def encode_endpoint(params_json, userId, language):
    return JSONResponse(status_code=200, content={"count": 100})
    # try:
    #     if "text" not in params_json:
    #         return JSONResponse(status_code=400, content=EncodeParaError)
    #     elif not isinstance(params_json["text"], str):
    #         return JSONResponse(status_code=400, content=EncodeParaError)
    #
    #     if "model_name" not in params_json:
    #         return JSONResponse(status_code=400, content=EncodeParaError)
    #     elif not isinstance(params_json["model_name"], str):
    #         return JSONResponse(status_code=400, content=EncodeParaError)
    #     elif params_json["model_name"] == "":
    #         return JSONResponse(status_code=400, content=EncodeParaError)
    #
    #     model_name = params_json["model_name"]
    #     text = params_json["text"]
    #     model_info = await llm_utils.model_config.get_model_config("name", model_name)
    #     if len(model_info) <= 0:
    #         model_info = llm_model_dao.get_model_by_name(model_name)
    #         if len(model_info) <= 0:
    #             return JSONResponse(status_code=500, content=EncodeParaError)
    #         model_info = model_info[0]
    #         await llm_utils.model_config.add_model_config(model_info["f_model_id"], model_info["f_model_series"],
    #                                                       model_info["f_model_type"], model_info["f_model_name"],
    #                                                       model_info["f_model"],
    #                                                       model_info["f_model_config"])
    #     config = json.loads(model_info["f_model_config"])
    #     token_ids, token_count = await llm_utils.encode(model_info["f_model_series"], text, model_info["f_model"],
    #                                                     config.get("api_key", ""), config.get("secret_key", ""))
    #     return JSONResponse(status_code=200, content={"count": token_count})
    # except Exception as e:
    #     StandLogger.error(repr(e))
    #     UnknownError["description"] = UnknownError["detail"] = repr(e)
    #     return JSONResponse(status_code=500, content=UnknownError)


async def get_monitor_data(userId, language, model_id, role=""):
    try:
        if not model_id:
            error_dict = error_with_message(
                ModelFactory_Router_ParamError_ParamMissing_Error,
                "ModelFactory.Validation.RequiredParameter",
                parameters="model_id")
            return JSONResponse(status_code=400, content=error_dict)
        # Authorization (#213): callers without display permission cannot view model statistics.
        # check_display bypasses administrators and disabled authorization.
        if userId != "266c6a42-6131-4d62-8f39-853e7093701c" and \
                not await permission_manager.check_display(userId, role, "large_model", model_id):
            return JSONResponse(status_code=403, content=NotPermissionError)
        result = llm_model_dao.get_monitor_data(model_id)
        output_token_speed_list = []
        total_token_speed_list = []
        average_first_token_time_list = []
        for line in result:
            create_time = line["f_create_time"].strftime("%m/%d %H:%M")
            model_id = line["f_model_id"]
            average_first_token_time = float(line["f_average_first_token_time"])
            output_token_speed = float(line["f_generation_token_speed"])
            total_token_speed = float(line["f_total_token_speed"])
            output_token_speed_list.append({"time": create_time, "value": output_token_speed})
            total_token_speed_list.append({"time": create_time, "value": total_token_speed})
            average_first_token_time_list.append({"time": create_time, "value": average_first_token_time})
        data = {"output_token_speed": output_token_speed_list, "total_token_speed": total_token_speed_list,
                "average_first_token_time": average_first_token_time_list}
        return JSONResponse(status_code=200, content=data)
    except Exception as e:
        StandLogger.error(e.args)
        return JSONResponse(status_code=500, content=DataBaseError)


async def edit_default_model(model_para, userId, language):
    global redis_util
    if base_config.AUTH_ENABLED and userId != "266c6a42-6131-4d62-8f39-853e7093701c":
        error_dict = error_with_message(
            ModelFactory_Router_ParamError_TypeError_Error,
            "ModelFactory.ModelController.EditDefaultModel.PermissionDenied")
        return JSONResponse(status_code=403, content=error_dict)
    key_list = ["model_id", "default"]
    for k in key_list:
        if k not in model_para:
            raise RequestValidationError([{"loc": ('body', k), "type": "value_error.missing"}])
    if not isinstance(model_para["default"], bool):
        error_dict = error_with_message(
            ModelFactory_Router_ParamError_TypeError_Error,
            "ModelFactory.Validation.BooleanParameter",
            parameter="default")
        return JSONResponse(status_code=400, content=error_dict)

    try:
        model_id = model_para["model_id"]

        # Check whether the model exists.
        model_exists = llm_model_dao.check_model_is_exist(model_id)
        if not model_exists:
            error_dict = ModelFactory_ExternalSmallModel_EditModel_IdNotExist_Error.copy()
            error_dict['description'] = "Model not found."
            error_dict['detail'] = "The specified model does not exist."
            return JSONResponse(status_code=400, content=error_dict)

        # Read the current default model.
        set_default = model_para["default"]
        if not set_default:
            llm_model_dao.update_model_default_status(model_id, False)
            default_cache_name = "default_model_3ed523"
            default_cache_key = f"dip:model-api:llm:{default_cache_name}:list"
            if redis_util is None:
                redis_util = await get_redis_util()
            await redis_util.delete_str(default_cache_key)
            return JSONResponse(status_code=200, content={"status": "ok", "id": model_id, "default": False})

        old_default_data = llm_model_dao.get_default_model()
        old_model_id = ""
        if old_default_data:
            old_model_id = old_default_data[0]["f_model_id"]
        # Reject a redundant default-model update.
        if old_model_id == model_id:
            error_dict = error_with_message(
                ModelFactory_Router_ParamError_TypeError_Error,
                "ModelFactory.ModelController.EditDefaultModel.AlreadyDefault")
            return JSONResponse(status_code=400, content=error_dict)

        # Clear the old default before setting the new one.
        if old_model_id:
            llm_model_dao.update_model_default_status(old_model_id, False)
        # Set the new default model.
        llm_model_dao.update_model_default_status(model_id, True)
        default_cache_name = "default_model_3ed523"
        default_cache_key = f"dip:model-api:llm:{default_cache_name}:list"
        if redis_util is None:
            redis_util = await get_redis_util()
        await redis_util.delete_str(default_cache_key)
        content = {"status": "ok", "id": model_id, "default": True}
        return JSONResponse(status_code=200, content=content)

    except Exception as e:
        StandLogger.error(e.args)
        return JSONResponse(status_code=500, content=DataBaseError)


async def get_overview_data(userId, language, model_id, start_time, end_time, role=""):
    try:
        # Authorization (#213): check display permission for a selected model.
        # Aggregate views require permission for at least one large model.
        # Administrators and disabled authorization bypass this check.
        if base_config.AUTH_ENABLED and userId != "266c6a42-6131-4d62-8f39-853e7093701c":
            if model_id:
                if not await permission_manager.check_display(userId, role, "large_model", model_id):
                    return JSONResponse(status_code=403, content=NotPermissionError)
            else:
                # authorized is an admission check for at least one model, preventing a misleading zero overview.
                # The aggregate is not narrowed to the authorized model set; the DAO filters by caller.
                # Non-admin queries add d.f_user_id='{userId}' in llm_model_dao.
                # The overview aggregates the caller's own cross-model usage and does not expose other users.
                # Lists are grant-filtered, while overviews are caller-filtered.
                candidate_ids = [m['f_model_id'] for m in llm_model_dao.get_all_model_list()]
                authorized = await permission_manager.filter_authorized_ids(
                    user_id=userId, role=role, candidate_ids=candidate_ids,
                    resource_type="large_model", resource_name="大模型", operation="display")
                if not authorized:
                    return JSONResponse(status_code=403, content=NotPermissionError)
        if not start_time:
            error_dict = error_with_message(
                ModelFactory_Router_ParamError_ParamMissing_Error,
                "ModelFactory.Validation.RequiredParameter",
                parameters="start_time")
            return JSONResponse(status_code=400, content=error_dict)
        if not end_time:
            error_dict = error_with_message(
                ModelFactory_Router_ParamError_ParamMissing_Error,
                "ModelFactory.Validation.RequiredParameter",
                parameters="end_time")
            return JSONResponse(status_code=400, content=error_dict)

        date_pattern = r'^\d{4}-\d{2}-\d{2}$'
        if not re.match(date_pattern, start_time):
            error_dict = error_with_message(
                ModelFactory_Router_ParamError_TypeError_Error,
                "ModelFactory.Validation.DateFormat",
                parameter="start_time")
            return JSONResponse(status_code=400, content=error_dict)
        if not re.match(date_pattern, end_time):
            error_dict = error_with_message(
                ModelFactory_Router_ParamError_TypeError_Error,
                "ModelFactory.Validation.DateFormat",
                parameter="end_time")
            return JSONResponse(status_code=400, content=error_dict)
        start_date = datetime.strptime(start_time, "%Y-%m-%d")
        end_date = datetime.strptime(end_time, "%Y-%m-%d")
        if start_date > end_date:
            error_dict = error_with_message(
                ModelFactory_Router_ParamError_TypeError_Error,
                "ModelFactory.Validation.DateRange")
            return JSONResponse(status_code=400, content=error_dict)

        core_metrics, trend_analysis, qps_analysis = llm_model_dao.get_overview_data(model_id, start_time, end_time,
                                                                                     userId)

        summary = {}
        if core_metrics and len(core_metrics) > 0:
            metric = core_metrics[0]
            summary = {
                "avg_response_time": float(metric["avg_response_time"]) if metric[
                                                                               "avg_response_time"] is not None else 0,
                "error_rate": float(metric["error_rate"]) if metric["error_rate"] is not None else 0,
                "input_tokens": int(metric["input_tokens"]) if metric["input_tokens"] is not None else 0,
                "output_tokens": int(metric["output_tokens"]) if metric["output_tokens"] is not None else 0,
                "total_tokens": int(metric["total_tokens"]) if metric["total_tokens"] is not None else 0,
                "total_usage": int(metric["total_usage"]) if metric["total_usage"] is not None else 0
            }

        trends = []
        for trend in trend_analysis:
            trends.append({
                "date": str(trend["date_group"]),  # Convert to a string.
                "input_tokens": int(trend["input_tokens"]) if trend["input_tokens"] is not None else 0,
                "output_tokens": int(trend["output_tokens"]) if trend["output_tokens"] is not None else 0,
                "avg_total_time": float(trend["avg_total_time"]) if trend["avg_total_time"] is not None else 0,
                "avg_first_time": float(trend["avg_first_time"]) if trend["avg_first_time"] is not None else 0,
                "avg_rate": float(trend["avg_rate"]) if trend["avg_rate"] is not None else 0
            })
        
        if not trends:
            start_date = datetime.strptime(start_time, "%Y-%m-%d")
            end_date = datetime.strptime(end_time, "%Y-%m-%d")
            current_date = start_date
            while current_date <= end_date:
                trends.append({
                    "date": current_date.strftime("%Y-%m-%d"),
                    "input_tokens": 0,
                    "output_tokens": 0,
                    "avg_total_time": 0.0,
                    "avg_first_time": 0.0,
                    "avg_rate": 0.0
                })
                current_date += timedelta(days=1)
        else:
            expected_dates = set()
            start_date = datetime.strptime(start_time, "%Y-%m-%d")
            end_date = datetime.strptime(end_time, "%Y-%m-%d")
            current_date = start_date
            while current_date <= end_date:
                expected_dates.add(current_date.strftime("%Y-%m-%d"))
                current_date += timedelta(days=1)
            
            existing_dates = set(item["date"] for item in trends)
            missing_dates = expected_dates - existing_dates
            
            for missing_date in missing_dates:
                trends.append({
                    "date": missing_date,
                    "input_tokens": 0,
                    "output_tokens": 0,
                    "avg_total_time": 0.0,
                    "avg_first_time": 0.0,
                    "avg_rate": 0.0
                })
            
            trends.sort(key=lambda x: datetime.strptime(x["date"], "%Y-%m-%d"))
        qps_data = []
        for qps in qps_analysis:
            qps_data.append(({
                "date": qps['date_group'],
                "avg_qps": f"{float(qps['avg_qps']):.6f}" if qps["avg_qps"] is not None else "0.000000"
            }))
        
        # Fill missing points for the latest eight hours at five-minute intervals.
        if not qps_data:
            end_date = datetime.strptime(end_time, "%Y-%m-%d")
            today = datetime.now().date()
            if end_date.date() == today:
                end_time_dt = datetime.now()
            else:
                end_time_dt = end_date.replace(hour=23, minute=59, second=59)
            # Generate five-minute time points for the latest eight hours.
            for i in range(96):
                time_point = end_time_dt - timedelta(minutes=i * 5)
                qps_data.append({
                    "date": time_point.strftime("%Y-%m-%d %H:%M:%S"),
                    "avg_qps": "0.000000"
                })
        else:
            end_time_dt = datetime.strptime(qps_data[0]["date"], "%Y-%m-%d %H:%M:%S")
            # Generate five-minute time points for the latest eight hours.
            for i in range(96):
                num = i+1
                time_point = end_time_dt - timedelta(minutes=num * 5)
                qps_data.append({
                    "date": time_point.strftime("%Y-%m-%d %H:%M:%S"),
                    "avg_qps": "0.000000"
                })
        qps_data.sort(key=lambda x: datetime.strptime(x["date"], "%Y-%m-%d %H:%M:%S"))
        
        # Keep at most 96 data points.
        if len(qps_data) > 96:
            qps_data = qps_data[-96:]
        
        data = {"summary": summary, "trends": trends, "qps_data": qps_data}
        return JSONResponse(status_code=200, content=data)
    except Exception as e:
        StandLogger.error(e.args)
        return JSONResponse(status_code=500, content=DataBaseError)
