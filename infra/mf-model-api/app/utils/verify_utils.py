import json

import aiohttp
from fastapi.responses import JSONResponse
from func_timeout import func_set_timeout
from llmadapter.llms.llm_factory import llm_factory
from llmadapter.schema import AIMessage
from urllib3.exceptions import MaxRetryError
from app.commons.errors import ModelFactory_ModelController_TestModel_Error_Error, LLMTestError
from app.utils.http_client import proxy_aware_aiohttp


# Connection tests must use the deployment's standard proxy environment variables.
aiohttp = proxy_aware_aiohttp(aiohttp)


def _connection_test_error(detail, fallback):
    raw = str(detail).lower()
    if "504" in raw or "gateway timeout" in raw or "upstream request timeout" in raw:
        return "The proxy gateway timed out while waiting for the model service; check the proxy's upstream timeout and streaming policy."
    if "timeout" in raw or "timed out" in raw:
        return "Model service connection timed out; check the proxy and network connectivity."
    return fallback


@func_set_timeout(30)
async def llm_test(series, config, llm_id, user_id, model_type):
    message = [AIMessage(content="Hello")]
    content = "Connection test failed. Check the configuration and try again."
    prompt = "Hello"
    # Handle OpenAI and other providers separately.
    if series == 'openai':
        try:
            try:
                if "api_key" not in config.keys():
                    LLMTestError['description'] = "api_key is missing."
                    LLMTestError['detail'] = "OpenAI-compatible models require api_key."
                    return JSONResponse(status_code=500, content=LLMTestError)
                llm = llm_factory.create_llm(llm_type="openai",
                                             api_type="azure",
                                             api_version="2023-03-15-preview",
                                             openai_api_base=config['api_url'],
                                             openai_api_key=config['api_key'],
                                             engine=config['api_model'],
                                             temperature=0.2,
                                             max_tokens=400)
                # if llm_id != "":
                #     log_info = logics.AddModelUsedAudit(
                #         model_id=llm_id, user_id=user_id, input_tokens=5,
                #         output_tokens=10)
                #     await add_llm_model_call_log(log_info)
                return JSONResponse(status_code=200, content={"res": {"status": True, "model_type": "chat"}})

            except Exception as e:
                print(e)
                # if llm_id != "":
                #     log_info = logics.AddModelUsedAudit(
                #         model_id=llm_id, user_id=user_id, input_tokens=5,
                #         output_tokens=10)
                #     await add_llm_model_call_log(log_info)
                return JSONResponse(status_code=200, content={"res": {"status": True, "model_type": "chat"}})
        except Exception as e:
            print(e)
            if isinstance(e.args[0], MaxRetryError):
                content = "The configured URL is not reachable."
            error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
            error_dict["detail"] = str(e.args[0])
            error_dict["description"] = error_dict["solution"] = content
            if not isinstance(e.args[0], MaxRetryError):
                error_dict["description"] = "Model configuration is invalid."
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
                        "content": "Hello",
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
            content = "Connection test failed. Check the configuration and try again."
            if isinstance(e.args[0], MaxRetryError):
                content = "The configured URL is not reachable."
            error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
            error_dict["detail"] = str(e.args[0])
            error_dict["description"] = error_dict["solution"] = content
            if not isinstance(e.args[0], MaxRetryError):
                error_dict["description"] = "Model configuration is invalid."
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
                    "content": "Hello",
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
                    "content": "Hello"
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
                        "content": "Hello",
                        "role": "user"
                    }
                ],
                "model": config["api_model"],
                # A connectivity test should receive one JSON response.  Streaming
                # SSE is commonly buffered or held open by enterprise proxies.
                "stream": False,
                "max_tokens": 16,
            }
            headers = {
                "Authorization": f"Bearer {config.get('api_key', '')}",
                "Content-Type": "application/json"
            }
            async with aiohttp.ClientSession() as session:
                async with session.post(config["api_url"], json=params, headers=headers, ssl=False) as response:
                    response.encoding = 'utf-8'
                    if response.status != 200:
                        error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
                        detail = ""
                        try:
                            detail = await response.text()
                        except Exception as e:
                            StandLogger.error(str(e))
                        error_detail = f"HTTP {response.status}: {detail}"
                        error_dict["detail"] = error_detail
                        error_dict["description"] = error_dict["solution"] = _connection_test_error(
                            error_detail, content)
                        return JSONResponse(status_code=400, content=error_dict)
                    body = await response.text()
                    try:
                        json.loads(body)
                    except json.JSONDecodeError:
                        error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
                        error_dict["detail"] = "The model service returned an invalid JSON response."
                        error_dict["description"] = error_dict["solution"] = content
                        return JSONResponse(status_code=400, content=error_dict)
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
            content = "Connection test failed. Check the configuration and try again."
            if isinstance(e.args[0], MaxRetryError):
                content = "The configured URL is not reachable."
            error_dict = ModelFactory_ModelController_TestModel_Error_Error.copy()
            error_dict["detail"] = str(e.args[0])
            error_dict["description"] = error_dict["solution"] = content
            if not isinstance(e.args[0], MaxRetryError):
                error_dict["description"] = "Model configuration is invalid."
            return JSONResponse(status_code=400, content=error_dict)
