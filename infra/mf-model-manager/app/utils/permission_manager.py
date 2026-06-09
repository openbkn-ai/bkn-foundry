import os

import aiohttp
from typing import List, Dict, Optional

from app.core.config import base_config
from app.dao.small_model_dao import small_model_dao
from app.logs.stand_log import StandLogger


class PermissionManager:
    def __init__(self):
        self.base_url = f"http://{base_config.AUTHORIZATIONPRIVATEHOST}:{base_config.AUTHORIZATIONPRIVATEPORT}"
        self.auth_url = f"{self.base_url}/api/authorization/v1/policy"
        self.check_single_auth_url = f"{self.base_url}/api/authorization/v1/operation-check"
        # self.check_resource_list_url = f"{self.base_url}/api/authorization/v1/resource-list"
        self.resource_filter_url = f"{self.base_url}/api/authorization/v1/resource-filter"
        self.delete_resource_url = f"{self.base_url}/api/authorization/v1/policy-delete"
        self.session: Optional[aiohttp.ClientSession] = None
        # bkn-safe authz cutover (revertible, env-gated):
        #   AUTHZ_PROVIDER=shadow   -> ISF authoritative, bkn-safe queried in
        #                              parallel, diffs logged (decision path only)
        #   AUTHZ_PROVIDER=bkn-safe -> bkn-safe AUTHORITATIVE for all methods
        #                              (ISF not consulted)
        # Unset to revert (default = pure ISF). BKN_SAFE_URL points at bkn-safe.
        self.authz_provider = os.getenv("AUTHZ_PROVIDER", "")
        self.bkn_safe_url = os.getenv("BKN_SAFE_URL", "")

    def _bkn_safe_authoritative(self) -> bool:
        return self.authz_provider == "bkn-safe" and bool(self.bkn_safe_url)

    async def get_session(self) -> aiohttp.ClientSession:
        if self.session is None or self.session.closed:
            self.session = aiohttp.ClientSession(connector=aiohttp.TCPConnector(ssl=False))
        return self.session

    async def add_permission(self, user_id: str, resource_id: str, resource_name: str, resource_type: str,
                             user_name: str, role: str) -> bool:
        if not base_config.AUTH_ENABLED:
            return True
        # admin用户无需授权
        if user_id == "266c6a42-6131-4d62-8f39-853e7093701c":
            return True
        # bkn-safe authoritative: grant the four instance ops directly.
        if self._bkn_safe_authoritative():
            return await self._bkn_safe_add(user_id, resource_type, resource_id,
                                            ["display", "modify", "delete", "execute"])
        """添加权限"""
        payload = [{
            "accessor": {
                "id": user_id,
                "type": role,
                "name": user_name
            },
            "resource": {
                "id": resource_id,
                "type": resource_type,
                "name": resource_name
            },
            "operation": {
                "allow": [
                    {"id": "display"},
                    {"id": "modify"},
                    {"id": "delete"},
                    {"id": "execute"}
                ],
                "deny": []
            },
            "condition": "{}",
            "expires_at": "1970-01-01T08:00:00+08:00"
        }]
        # 使用filter接口过滤权限不再需要手动添加
        # admin_user_id = "266c6a42-6131-4d62-8f39-853e7093701c"
        # if user_id != admin_user_id:
        #     payload.append({
        #         "accessor": {
        #             "id": admin_user_id,
        #             "type": "user",
        #             "name": "admin"
        #         },
        #         "resource": {
        #             "id": resource_id,
        #             "type": resource_type,
        #             "name": resource_name
        #         },
        #         "operation": {
        #             "allow": [
        #                 {"id": "display"},
        #                 {"id": "modify"},
        #                 {"id": "delete"},
        #                 {"id": "execute"},
        #                 {"id": "authorize"}
        #             ],
        #             "deny": []
        #         },
        #         "condition": "{}",
        #         "expires_at": "1970-01-01T08:00:00+08:00"
        #     })
        try:
            session = await self.get_session()
            async with session.post(
                    self.auth_url,
                    json=payload,
                    headers={'Content-Type': 'application/json'}
            ) as response:
                if response.status == 204:
                    return True
        except Exception as e:
            StandLogger.error(e.args)
        return False

    async def close(self):
        if self.session and not self.session.closed:
            await self.session.close()

    async def check_single_permission(self, user_id: str, resource_id: str, operations: str,
                                      resource_type: str, role: str) -> bool:
        if not base_config.AUTH_ENABLED:
            return True
        # bkn-safe authoritative: return its decision directly.
        if self._bkn_safe_authoritative():
            try:
                return await self._bkn_safe_check(user_id, resource_type, resource_id, operations)
            except Exception as e:
                StandLogger.error(e.args)
                return False
        """校验用户对资源的权限"""
        payload = {
            "method": "GET",
            "accessor": {
                "id": user_id,
                "type": role
            },
            "resource": {
                "id": resource_id,
                "type": resource_type
            },
            "operation": [operations]
        }

        isf_allowed = False
        try:
            session = await self.get_session()
            async with session.post(
                    self.check_single_auth_url,
                    json=payload,
                    headers={'Content-Type': 'application/json'}
            ) as response:
                if response.status == 200:
                    result = await response.json()
                    isf_allowed = result.get('result', False)
        except Exception as e:
            StandLogger.error(e.args)
            isf_allowed = False

        # Shadow: also query bkn-safe and log any divergence; ISF authoritative.
        await self._maybe_shadow(user_id, role, resource_id, resource_type, operations, isf_allowed)
        return isf_allowed

    async def _maybe_shadow(self, user_id, role, resource_id, resource_type, operations, isf_allowed):
        if self.authz_provider != "shadow" or not self.bkn_safe_url:
            return
        try:
            safe_allowed = await self._bkn_safe_check(user_id, resource_type, resource_id, operations)
            if safe_allowed != isf_allowed:
                StandLogger.warn(
                    f"[authz-shadow] DIFF: accessor={user_id} {resource_type}:{resource_id} "
                    f"op={operations} isf={isf_allowed} bkn-safe={safe_allowed}")
        except Exception as e:
            StandLogger.warn(f"[authz-shadow] bkn-safe error (ISF authoritative): {e}")

    async def _bkn_safe_check(self, user_id, resource_type, resource_id, operation) -> bool:
        session = await self.get_session()
        async with session.post(
                f"{self.bkn_safe_url}/api/safe/v1/authz/check",
                json={"accessor_id": user_id,
                      "resource": {"type": resource_type, "id": resource_id},
                      "operation": operation},
                headers={'Content-Type': 'application/json'}) as resp:
            data = await resp.json()
            return bool(data.get('allowed', False))

    async def _bkn_safe_add(self, user_id, resource_type, resource_id, operations) -> bool:
        try:
            session = await self.get_session()
            async with session.post(
                    f"{self.bkn_safe_url}/api/safe/v1/authz/policies",
                    json={"accessor_id": user_id,
                          "resource": {"type": resource_type, "id": resource_id},
                          "operations": operations},
                    headers={'Content-Type': 'application/json'}) as resp:
                return resp.status == 204
        except Exception as e:
            StandLogger.error(e.args)
            return False

    async def _bkn_safe_filter_ids(self, user_id, operation, resource_type) -> list:
        # Match ISF: a "*" operation yields no ids (the ISF filter drops them).
        if operation == "*":
            return []
        model_ids = small_model_dao.get_all_ids()
        allowed = []
        for m in model_ids:
            mid = m['f_model_id']
            try:
                if await self._bkn_safe_check(user_id, resource_type, mid, operation):
                    allowed.append(mid)
            except Exception as e:
                StandLogger.error(e.args)
        return allowed

    async def _bkn_safe_delete(self, resource_type, resource_ids) -> bool:
        session = await self.get_session()
        ok = True
        for resource_id in resource_ids:
            try:
                async with session.delete(
                        f"{self.bkn_safe_url}/api/safe/v1/authz/policies",
                        json={"resource": {"type": resource_type, "id": resource_id}},
                        headers={'Content-Type': 'application/json'}) as resp:
                    if resp.status != 204:
                        ok = False
            except Exception as e:
                StandLogger.error(e.args)
                ok = False
        return ok

    async def get_permission_ids(self, user_id: str, operation: str,
                                 resource_type: str, resource_name: str, role: str) -> list:
        if not base_config.AUTH_ENABLED:
            all_ids = small_model_dao.get_all_ids()
            return [m['f_model_id'] for m in all_ids]
        # bkn-safe authoritative: filter the model set by per-resource checks.
        if self._bkn_safe_authoritative():
            return await self._bkn_safe_filter_ids(user_id, operation, resource_type)
        """获取资源列表"""
        payload = {
            "method": "GET",
            "accessor": {
                "id": user_id,
                "type": role
            },
            "resource":
                {
                    "type": resource_type,
                    "name": resource_name
                }
            ,
            "operation": [
                operation
            ]
        }
        model_ids = small_model_dao.get_all_ids()
        resources = []
        for model_id in model_ids:
            resources.append({
                "id": model_id['f_model_id'],
                "type": "small_model",
                "name": "小模型"
            })
        payload = {
            "method": "GET",
            "accessor": {
                "id": user_id,
                "type": role
            },
            "resources": resources,
            "operation": [
                operation
            ]
        }
        operation_ids = []
        try:
            session = await self.get_session()
            async with session.post(
                    self.resource_filter_url,
                    json=payload,
                    headers={'Content-Type': 'application/json'}
            ) as response:
                if response.status == 200:
                    result = await response.json()
                    operation_ids = [item['id'] for item in result if operation != "*"]
                    return operation_ids
        except Exception as e:
            StandLogger.error(e.args)
        return operation_ids

    async def delete_permission(self, resource_type: str, resource_ids: list) -> bool:
        if not base_config.AUTH_ENABLED:
            return True
        # bkn-safe authoritative: drop each resource's policies directly.
        if self._bkn_safe_authoritative():
            return await self._bkn_safe_delete(resource_type, resource_ids)
        """删除权限"""
        session = await self.get_session()
        resources = [{"id": resource_id, "type": resource_type} for resource_id in resource_ids]
        payload = {"resources": resources,
                   "method": "DELETE"}
        try:
            async with session.post(
                    self.delete_resource_url,
                    json=payload,
                    headers={"Content-Type": "application/json"}
            ) as response:
                if response.status == 204:
                    return True
        except Exception as e:
            StandLogger.error(e.args)
        return False


permission_manager = PermissionManager()
