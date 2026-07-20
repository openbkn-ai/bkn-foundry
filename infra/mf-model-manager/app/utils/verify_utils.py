import json

import aiohttp
from fastapi.responses import JSONResponse
from func_timeout import func_set_timeout
from urllib3.exceptions import MaxRetryError
from app.commons.errors import ModelFactory_ModelController_TestModel_Error_Error, LLMTestError
from app.logs.stand_log import StandLogger
from app.core.config import base_config


def _semantic_model_test_error(detail, fallback):
    upstream_code = ""
    upstream_type = ""
    upstream_message = ""
    try:
        payload = json.loads(detail)
        if isinstance(payload, dict):
            error = payload.get("error")
            if isinstance(error, dict):
                upstream_code = str(error.get("code") or "")
                upstream_type = str(error.get("type") or "")
                upstream_message = str(error.get("message") or "")
            elif isinstance(error, str):
                upstream_message = error
            if not upstream_message:
                for key in ("message", "detail", "error_description"):
                    if payload.get(key):
                        upstream_message = str(payload[key])
                        break
    except Exception:
        pass

    raw = " ".join(
        [upstream_code, upstream_type, upstream_message, str(detail)]
    ).lower()
    if any(token in raw for token in ("auth", "unauthorized", "api key", "ak/sk", "apikey", "401")):
        return "模型服务认证失败，请检查 API Key、AK/SK 或授权配置"
    if any(token in raw for token in ("deploymentnotfound", "model not found", "not found", "404")):
        return "模型或部署不存在，请检查 API Model 和模型服务地址"
    if any(token in raw for token in ("timeout", "timed out")):
        return "模型服务连接超时，请检查服务地址和网络连通性"
    if any(token in raw for token in ("connection", "connect", "dns", "name resolution", "enotfound")):
        return "无法访问模型服务，请检查 API URL 和网络连通性"
    if upstream_message:
        return f"模型服务返回错误：{upstream_message}"
    return fallback


@func_set_timeout(30)
async def llm_test(series, config, llm_id, user_id, model_type):
    content = "测试连接失败，请重新检查信息"
    # 区分openai和其他模型
    if series == 'openai':
        try:
            if "api_key" not in config.keys():
                LLMTestError['description'] = "api_key parameter is missing"
                LLMTestError['detail'] = "openai model requires api_key"
                return JSONResponse(status_code=400, content=LLMTestError)
            params = {
                "messages": [
                    {
                        "content": "hello",
                        "role": "user"
                    }
                ],
                "model": config["api_model"],
                "stream": False,
                "max_tokens": 16
            }
            headers = {
                "api-key": config["api_key"]
            }
            async with aiohttp.ClientSession(timeout=base_config.test_llm_timeout) as session:
                url = config["api_url"] + (
                    f"openai/deployments/{config['api_model']}/chat/completions"
                    "?api-version=2023-05-15&api-type=azure"
                )
                async with session.post(url, json=params, headers=headers, ssl=False) as response:
                    response.encoding = 'utf-8'
                    if response.status != 200:
                        error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
                        detail = ""
                        try:
                            detail = await response.text()
                        except Exception as err:
                            StandLogger.error(str(err))
                        error_dict["detail"] = detail
                        description = _semantic_model_test_error(detail, content)
                        error_dict["description"] = error_dict["solution"] = description
                        return JSONResponse(status_code=400, content=error_dict)
            return JSONResponse(status_code=200, content={"status": "ok", "id": llm_id})
        except Exception as e:
            print(e)
            detail = str(e.args[0]) if e.args else str(e)
            if e.args and isinstance(e.args[0], MaxRetryError):
                content = "无法访问该链接，请检查该链接是否可以访问"
            error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
            error_dict["detail"] = detail
            description = _semantic_model_test_error(detail, content)
            error_dict["description"] = error_dict["solution"] = description
            # if error_dict["detail"].strip(" ") != "":
            #     error_dict["description"] = error_dict["detail"]
            # if len(error_dict["description"]) > 500:
            #     error_dict["description"] = error_dict["description"][0:500]
            return JSONResponse(status_code=400, content=error_dict)

    elif series.lower() == "claude":
        try:
            params = {
                "messages": [
                    {
                        "content": "你好",
                        "role": "user"
                    }
                ],
                "model": config["api_model"],
                "stream": False,
                "max_tokens": 1000
            }
            headers = {
                "x-api-key": f"{config['api_key']}",
                "anthropic-version": "2023-06-01",
                "content-type": "application/json"
            }
            async with aiohttp.ClientSession() as session:
                async with session.post(config["api_url"], json=params, headers=headers, ssl=False) as response:
                    response.encoding = 'utf-8'
                    if response.status != 200:
                        error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
                        error_dict["detail"] = await response.text()
                        return JSONResponse(status_code=400, content=error_dict)
            content = {"status": "ok", "id": llm_id}
            return JSONResponse(status_code=200, content=content)
        except Exception as e:
            print(e)
            content = "测试连接失败，请重新检查信息"
            if isinstance(e.args[0], MaxRetryError):
                content = "无法访问该链接，请检查该链接是否可以访问"
            error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
            error_dict["detail"] = str(e.args[0])
            error_dict["description"] = error_dict["solution"] = content
            if not isinstance(e.args[0], MaxRetryError):
                error_dict["description"] = "模型配置错误，请检查模型信息"
            return JSONResponse(status_code=400, content=error_dict)
    elif series.lower() == "baidu":
        headers = {
            'Content-Type': 'application/json',
            'Accept': 'application/json'
        }
        url = f"https://aip.baidubce.com/oauth/2.0/token?grant_type=client_credentials&client_id={config.get('api_key', '')}&client_secret={config.get('secret_key', '')}"
        async with aiohttp.ClientSession() as session:
            async with session.post(url, headers=headers, ssl=False) as response:
                response.encoding = 'utf-8'
                if response.status != 200:
                    error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
                    error_dict["detail"] = await response.text()
                    return JSONResponse(status_code=400, content=error_dict)
                access_res = await response.json()
                access_token = access_res["access_token"]
        params = {
            "messages": [
                {
                    "content": "你好",
                    "role": "user"
                }
            ]
        }
        async with aiohttp.ClientSession() as session:
            url = config["api_url"] + f"?access_token={access_token}"
            async with session.post(url, json=params, headers=headers, ssl=False) as response:
                response.encoding = 'utf-8'
                if response.status != 200:
                    error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
                    error_dict["detail"] = await response.text()
                    return JSONResponse(status_code=400, content=error_dict)
        content = {"status": "ok", "id": llm_id}
        return JSONResponse(status_code=200, content=content)
    elif series.lower() == "baidu_tianchen":
        params = {
            "messages": [
                {
                    "role": "user",
                    "content": "你好"
                }
            ]
        }
        async with aiohttp.ClientSession() as session:
            url = config["api_url"] + f"?api_name="
            async with session.post(url, json=params,  ssl=False) as response:
                response.encoding = 'utf-8'
                if response.status != 200:
                    error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
                    error_dict["detail"] = await response.text()
                    return JSONResponse(status_code=400, content=error_dict)
        content = {"status": "ok", "id": llm_id}
        return JSONResponse(status_code=200, content=content)
    else:
        try:
            params = {
                "messages": [
                    {
                        "content": "你好",
                        "role": "user"
                    }
                ],
                "model": config["api_model"],
                "stream": True,
                "stream_options": {"include_usage": True}
            }
            headers = {
                "Authorization": f"Bearer {config.get('api_key', '')}",
                "Content-Type": "application/json"
            }
            token_len = 0
            prompt_tokens = 0
            completion_tokens = 0
            async with aiohttp.ClientSession(timeout=base_config.test_llm_timeout) as session:
                async with session.post(config["api_url"], json=params, headers=headers, ssl=False) as response:
                    response.encoding = 'utf-8'
                    if response.status != 200:
                        error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
                        detail = ""
                        try:
                            detail = await response.text()
                        except Exception as e:
                            StandLogger.error(str(e))
                        error_dict["detail"] = detail
                        description = _semantic_model_test_error(detail, content)
                        error_dict["description"] = error_dict["solution"] = description
                        return JSONResponse(status_code=400, content=error_dict)
                    async for chunk in response.content:
                        chunk = chunk.decode('utf-8')
                        if chunk.endswith('\n'):
                            chunk = chunk[:-1]
                        elif chunk.endswith('\r\n'):
                            chunk = chunk[:-2]
                        if len(chunk) >= 6 and chunk[0:6] != "data: ":
                            continue
                        if chunk != "data: [DONE]" and chunk != "":
                            if chunk[0:6] == "data: ":
                                chunk = chunk[6:]
                            try:
                                datas = json.loads(chunk)
                            except Exception:
                                continue
                            if "usage" in datas.keys():
                                try:
                                    prompt_tokens = datas["usage"]["prompt_tokens"]
                                    completion_tokens = datas["usage"]["completion_tokens"]
                                except Exception:
                                    pass
                    # if llm_id != "":
                    #     log_info = logics.AddModelUsedAudit(
                    #         model_id=llm_id, user_id=user_id,
                    #         input_tokens=prompt_tokens,
                    #         output_tokens=completion_tokens)
                    #     await add_llm_model_call_log(log_info)
                    content = {"status": "ok", "id": llm_id}
                    return JSONResponse(status_code=200, content=content)
        except Exception as e:
            StandLogger.error(str(e))
            content = "测试连接失败，请重新检查信息"
            detail = str(e.args[0]) if e.args else str(e)
            if e.args and isinstance(e.args[0], MaxRetryError):
                content = "无法访问该链接，请检查该链接是否可以访问"
            error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
            error_dict["detail"] = detail
            description = _semantic_model_test_error(detail, content)
            error_dict["description"] = error_dict["solution"] = description
            return JSONResponse(status_code=400, content=error_dict)
