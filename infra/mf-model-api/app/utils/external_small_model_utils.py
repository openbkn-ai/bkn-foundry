import json

import aiohttp

from app.commons.errors import *
import concurrent.futures

from app.core.config import base_config
from app.logs.stand_log import StandLogger


class UpstreamModelError(Exception):
    """Provider error with an HTTP status suitable for gateway mapping."""

    def __init__(self, status, detail):
        super().__init__(detail)
        self.status = status
        self.detail = detail


class BaiduTianchenClient:
    def __init__(self, url, ClientId, OperationCode):
        self.url = url
        self.ClientId = ClientId
        self.OperationCode = OperationCode

    async def embedding(self, texts):
        params = {
            "batch_text": texts
        }
        headers = {
            "ClientId": self.ClientId,
            "OperationCode": self.OperationCode
        }
        conn = aiohttp.TCPConnector(verify_ssl=False)
        async with aiohttp.ClientSession(connector=conn, timeout=base_config.aiohttp_timeout) as session:
            async with session.post(self.url, json=params, headers=headers) as resp:
                res = await resp.text()
                result = json.loads(res)
                if resp.status != 200 or "error_msg" in result.keys():
                    raise Exception(result.get("error_msg", res))
        return result["result"]["batch_embedding"]

    async def reranker(self, query, documents):
        if documents == []:
            return []
        params = {
            "query": query,
            "documents": documents
        }
        headers = {
            "ClientId": self.ClientId,
            "OperationCode": self.OperationCode
        }
        conn = aiohttp.TCPConnector(verify_ssl=False)
        res_list = []
        original_res_list = []
        async with aiohttp.ClientSession(connector=conn, timeout=base_config.aiohttp_timeout) as session:
            async with session.post(self.url, json=params, headers=headers) as resp:
                res = await resp.text()
                result = json.loads(res)
                if resp.status != 200 or "error_msg" in result.keys():
                    raise Exception(result.get("error_msg", res))
                original_res_list = sorted(result["results"], key=lambda x: x["index"])
        for item in original_res_list:
            res_list.append(item["relevance_score"])
        return res_list


class BaiduClient:
    def __init__(self, url, api_key, secret_key):
        self.url = url
        self.api_key = api_key
        self.secret_key = secret_key
        self.access_token = ""

    async def get_access_token(self):
        headers = {
            'Content-Type': 'application/json',
            'Accept': 'application/json'
        }
        conn = aiohttp.TCPConnector(verify_ssl=False)
        async with aiohttp.ClientSession(connector=conn, timeout=base_config.aiohttp_timeout) as session:
            async with session.post(
                    f"https://aip.baidubce.com/oauth/2.0/token?grant_type=client_credentials&client_id={self.api_key}&client_secret={self.secret_key}",
                    headers=headers) as resp:
                res = await resp.text()
                result = json.loads(res)
                if resp.status != 200:
                    tmp_map = result
                    error_dict = ModelFactory_ModelController_Model_Error_Error.copy()
                    if "detail" in tmp_map.keys():
                        error_dict["detail"] = tmp_map["detail"]
                    if "errors" in tmp_map.keys() and "message" in tmp_map["errors"].keys():
                        error_dict["detail"] = tmp_map["errors"]["message"]
                    return error_dict
                access_token = result["access_token"]
        return access_token

    async def embedding_thread(self, texts_slice, index):
        params = {
            "input": texts_slice
        }

        conn = aiohttp.TCPConnector(verify_ssl=False)
        res_list = []

        async with aiohttp.ClientSession(connector=conn, timeout=base_config.aiohttp_timeout) as session:
            async with session.post(
                    self.url + f"?access_token={self.access_token}",
                    json=params) as resp:
                res = await resp.text()
                result = json.loads(res)
                if resp.status != 200 or "error_msg" in result.keys():
                    raise Exception(result.get("error_msg", res))
                for item in result["data"]:
                    res_list.append(item["embedding"])
        return {index: res_list}

    async def embedding(self, texts):
        self.access_token = await self.get_access_token()

        for i in range(0, len(texts)):
            if texts[i] == "":
                texts[i] = " "
            if len(texts[i]) > 380:
                texts[i] = texts[i][:380]
        slice_list = [texts[i:i + 16] for i in range(0, len(texts), 16)]
        index = 0
        request_dict = {}
        with concurrent.futures.ThreadPoolExecutor(max_workers=10) as executor:
            futures = []
            for texts_slice in slice_list:
                request_dict[index] = texts_slice  # 存储顺序信息，便于与返回内容匹配
                future = executor.submit(self.embedding_thread, texts_slice, index)
                index += 1
                futures.append(future)
        original_result_list = [await future.result() for future in futures]
        original_result_dict = {}
        for item in original_result_list:
            original_result_dict = original_result_dict | item
        res_list = []

        for i in range(0, index):
            for item in original_result_dict[i]:
                res_list.append(item)
        return res_list

    async def reranker_thread(self, query, documents, index):
        params = {
            "query": query,
            "documents": documents
        }
        conn = aiohttp.TCPConnector(verify_ssl=False)
        res_list = []
        original_res_list = []
        async with aiohttp.ClientSession(connector=conn, timeout=base_config.aiohttp_timeout) as session:
            async with session.post(
                    self.url + f"?access_token={self.access_token}",
                    json=params) as resp:
                res = await resp.text()
                result = json.loads(res)
                if resp.status != 200 or "error_msg" in result.keys():
                    raise Exception(result.get("error_msg", res))
                original_res_list = sorted(result["results"], key=lambda x: x["index"])
        for item in original_res_list:
            res_list.append(item["relevance_score"])
        return {index: res_list}

    async def reranker(self, query, documents):
        self.access_token = await self.get_access_token()
        if len(query) > 1590:
            query = query[:1590]
        for i in range(0, len(documents)):
            if documents[i] == "":
                documents[i] = " "
            if len(documents[i]) > 4000:
                documents[i] = documents[i][:4000]
        slice_list = [documents[i:i + 64] for i in range(0, len(documents), 64)]
        index = 0
        request_dict = {}
        with concurrent.futures.ThreadPoolExecutor(max_workers=10) as executor:
            futures = []
            for texts_slice in slice_list:
                request_dict[index] = texts_slice  # 存储顺序信息，便于与返回内容匹配
                future = executor.submit(self.reranker_thread, query, texts_slice, index)
                index += 1
                futures.append(future)
        original_result_list = [await future.result() for future in futures]
        original_result_dict = {}
        for item in original_result_list:
            original_result_dict = original_result_dict | item
        res_list = []
        for i in range(0, index):
            for item in original_result_dict[i]:
                res_list.append(item)
        return res_list


class BaishengClient:
    def __init__(self, url, api_key, model):
        self.url = url
        self.api_key = api_key
        self.model = model

    async def embedding(self, texts):
        params = {
            "model": self.model,
            "input": texts
        }
        headers = {
            "Authorization": f"Bearer {self.api_key}"
        }
        conn = aiohttp.TCPConnector(verify_ssl=False)
        async with aiohttp.ClientSession(connector=conn, timeout=base_config.aiohttp_timeout) as session:
            async with session.post(
                    self.url,
                    json=params, headers=headers) as resp:
                res = await resp.text()
                result = json.loads(res)
                if resp.status != 200 or "error_msg" in result.keys():
                    raise Exception(result.get("error_msg", res))
        res_list = []
        for item in result["data"]:
            res_list.append(item["embedding"])
        return res_list

    async def reranker(self, query, documents):
        params = {
            "model": self.model,
            "query": query,
            "sentences": documents
        }
        headers = {
            "Authorization": f"Bearer {self.api_key}"
        }
        conn = aiohttp.TCPConnector(verify_ssl=False)
        async with aiohttp.ClientSession(connector=conn, timeout=base_config.aiohttp_timeout) as session:
            async with session.post(
                    self.url,
                    json=params, headers=headers) as resp:
                res = await resp.text()
                result = json.loads(res)
                if resp.status != 200 or "error_msg" in result.keys():
                    raise Exception(result.get("error_msg", res))
        return result["scores"]


class InnerClient:
    def __init__(self, url, model_name, api_key="", adapter=False, adapter_code=None):
        if not url.startswith(('http://', 'https://')):
            url = f"http://{url}"
        self.url = url
        self.model_name = model_name
        self.api_key = api_key
        self.headers = {
            "Authorization": f"Bearer {self.api_key}"
        }
        self.adapter = adapter
        self.adapter_code = adapter_code

    def _is_volcengine_embedding(self):
        # Only the vision family uses Ark's multimodal embeddings endpoint.
        # Other Doubao embedding models retain the OpenAI-compatible text body.
        return self.model_name.startswith("doubao-embedding-vision-")

    def _embedding_params(self, texts):
        """Build the provider-specific embedding request body."""
        if self._is_volcengine_embedding():
            return {
                "model": self.model_name,
                "input": [
                    text if isinstance(text, dict) else {"type": "text", "text": text}
                    for text in texts
                ],
                "dimensions": 1024,
            }
        return {
            "model": self.model_name,
            "input": texts,
        }

    def _normalize_volcengine_embedding_response(self, result):
        """Normalize Ark's single-object data field to the OpenAI-style list."""
        if self._is_volcengine_embedding() and isinstance(result.get("data"), dict):
            result["data"] = [result["data"]]
        return result

    def _validate_volcengine_embedding_response(self, result, expected_count=1):
        """Validate the response shape consumed by mf-model-api callers."""
        result = self._normalize_volcengine_embedding_response(result)
        data = result.get("data") if isinstance(result, dict) else None
        if not isinstance(data, list) or len(data) != expected_count:
            raise ValueError(
                f"Invalid Volcengine embedding response: expected {expected_count} vectors")
        if not all(
                isinstance(item, dict) and isinstance(item.get("embedding"), list) and
                len(item["embedding"]) > 0
                for item in data):
            raise ValueError("Invalid Volcengine embedding response: invalid embedding data")
        return result

    async def _post_embedding(self, session, texts):
        async with session.post(
                self.url,
                json=self._embedding_params(texts), headers=self.headers, ssl=False) as resp:
            res = await resp.text()
            if resp.status != 200:
                StandLogger.error(
                    f"call embeddingError,model_name={self.model_name},status={resp.status}")
                raise UpstreamModelError(resp.status, res)
            return json.loads(res)

    async def _volcengine_text_embeddings(self, texts):
        """Ark permits one text item per multimodal request; preserve batch semantics locally."""
        if not texts:
            raise ValueError("Volcengine embedding input must not be empty")

        async with aiohttp.ClientSession(timeout=base_config.aiohttp_timeout) as session:
            results = []
            for index, text in enumerate(texts):
                result = self._validate_volcengine_embedding_response(
                    await self._post_embedding(session, [text]))
                item = result["data"][0]
                item["index"] = index
                results.append((result, item))

        response = results[0][0]
        response["data"] = [item for _, item in results]
        usage = response.get("usage")
        if isinstance(usage, dict):
            response["usage"] = {
                key: sum(
                    result.get("usage", {}).get(key, 0)
                    for result, _ in results
                    if isinstance(result.get("usage"), dict) and
                    isinstance(result["usage"].get(key, 0), (int, float))
                ) if isinstance(value, (int, float)) else value
                for key, value in usage.items()
            }
        return response

    async def embedding(self, texts):
        if self.adapter and self.adapter_code:
            try:
                global_namespace = {'__builtins__': __builtins__}
                exec(self.adapter_code, global_namespace, global_namespace)
                adapter_func = global_namespace.get('main')
                if not adapter_func or not callable(adapter_func):
                    raise ValueError("Adapter code must define an async function named 'main'")

                result = await adapter_func(texts)
                return result
            except Exception as e:
                raise Exception(f"Adapter execution failed: {str(e)}")

        if self._is_volcengine_embedding() and all(isinstance(text, str) for text in texts):
            return await self._volcengine_text_embeddings(texts)

        async with aiohttp.ClientSession(timeout=base_config.aiohttp_timeout) as session:
            result = await self._post_embedding(session, texts)
        if self._is_volcengine_embedding():
            return self._validate_volcengine_embedding_response(result)
        return result

    async def reranker(self, query, documents):
        if self.adapter and self.adapter_code:
            try:
                global_namespace = {'__builtins__': __builtins__}
                exec(self.adapter_code, global_namespace, global_namespace)
                reranker_func = global_namespace.get('main')
                if not reranker_func or not callable(reranker_func):
                    raise ValueError("Adapter code must define an async function named 'my_reranker'")
                result = await reranker_func(query, documents)
                return result
            except Exception as e:
                raise Exception(f"Adapter execution failed: {str(e)}")

        # 原有逻辑
        params = {
            "model": self.model_name,
            "query": query,
            "documents": documents
        }
        async with aiohttp.ClientSession(timeout=base_config.aiohttp_timeout) as session:
            async with session.post(
                    self.url,
                    json=params, headers=self.headers, ssl=False) as resp:
                if resp.status != 200:
                    error_msg = await resp.text()
                    StandLogger.error(
                        f"call reranker error,model_name={self.model_name},error_detail={error_msg},query={query}，documents={documents},status={resp.status}")
                    raise Exception(error_msg)
                res = await resp.text()
                result = json.loads(res)
        return result

    async def test_embedding(self, texts):
        if self.adapter and self.adapter_code:
            try:
                global_namespace = {'__builtins__': __builtins__}
                exec(self.adapter_code, global_namespace, global_namespace)
                adapter_func = global_namespace.get('main')
                if not adapter_func or not callable(adapter_func):
                    raise ValueError("Adapter code must define an async function named 'main'")

                result = await adapter_func(texts)

                return result
            except Exception as e:
                raise Exception(f"Adapter execution failed: {str(e)}")

        else:
            params = self._embedding_params(texts)
            async with aiohttp.ClientSession(timeout=base_config.aiohttp_timeout) as session:
                async with session.post(
                        self.url,
                        json=params, headers=self.headers, ssl=False) as resp:
                    if resp.status == 422:
                        raise Exception("string_too_long,String should have at most 122880 characters")
                    if resp.status != 200:
                        raise Exception(resp.content)
                    res = await resp.text()
                    result = self._normalize_volcengine_embedding_response(json.loads(res))
        required_keys = ["object", "data", "model", "usage"]
        if not all(key in result for key in required_keys):
            raise ValueError(f"Invalid adapter response format, missing one of: {required_keys}")

        if not isinstance(result["data"], list) or not all(
                isinstance(item, dict) and "embedding" in item and
                isinstance(item["embedding"], list) and len(item["embedding"]) > 0
                for item in result["data"]
        ):
            raise ValueError("Invalid data format in adapter response")
        return result

    async def test_reranker(self, query, documents):
        if self.adapter and self.adapter_code:
            try:
                global_namespace = {'__builtins__': __builtins__}
                exec(self.adapter_code, global_namespace, global_namespace)
                reranker_func = global_namespace.get('main')
                if not reranker_func or not callable(reranker_func):
                    raise ValueError("Adapter code must define an async function named 'my_reranker'")
                result = await reranker_func(query, documents)
            except Exception as e:
                raise Exception(f"Adapter execution failed: {str(e)}")

        else:
            params = {
                "model": self.model_name,
                "query": query,
                "documents": documents
            }
            async with aiohttp.ClientSession(timeout=base_config.aiohttp_timeout) as session:
                async with session.post(
                        self.url,
                        json=params, headers=self.headers, ssl=False) as resp:
                    if resp.status != 200:
                        error_msg = await resp.text()
                        StandLogger.error(
                            f"call reranker error,model_name={self.model_name},error_detail={error_msg},query={query}，documents={documents},status={resp.status}")
                        raise Exception(error_msg)
                    res = await resp.text()
                    result = json.loads(res)
        required_keys = ["object", "results", "model", "usage"]
        if not all(key in result for key in required_keys):
            raise ValueError(f"Invalid adapter response format, missing one of: {required_keys}")

        if not isinstance(result["results"], list) or len(result["results"]) == 0 or not all(
                isinstance(item, dict) and "relevance_score" in item
                for item in result["results"]
        ):
            raise ValueError("Invalid results format in adapter response")
        return result
