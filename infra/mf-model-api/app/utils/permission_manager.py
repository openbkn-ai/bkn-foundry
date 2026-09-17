import aiohttp
from typing import Optional

from app.core.config import bkn_safe_url
from app.dao.small_model_dao import small_model_dao
from app.logs.stand_log import StandLogger
from app.commons.locale import internal_request_headers


class PermissionManager:
    def __init__(self):
        self.bkn_safe_url = bkn_safe_url()
        self.session: Optional[aiohttp.ClientSession] = None

    async def get_session(self) -> aiohttp.ClientSession:
        if self.session is None or self.session.closed:
            self.session = aiohttp.ClientSession(connector=aiohttp.TCPConnector(ssl=False))
        return self.session

    async def add_permission(self, user_id: str, resource_id: str, resource_name: str,
                             resource_type: str, user_name: str, role: str) -> bool:
        """Model access is derived from bkn-safe's baseline roles.

        Creating a model must not recreate the retired per-creator ACL.
        """
        return True

    async def close(self):
        if self.session and not self.session.closed:
            await self.session.close()

    async def check_single_permission(self, user_id: str, resource_id: str, operations: str,
                                      resource_type: str, role: str) -> bool:
        try:
            return await self._bkn_safe_check(user_id, resource_type, resource_id, operations)
        except Exception as exc:
            StandLogger.error(exc.args)
            return False

    async def _bkn_safe_check(self, user_id, resource_type, resource_id, operation) -> bool:
        resource_id = str(resource_id)
        session = await self.get_session()
        async with session.post(
                f"{self.bkn_safe_url}/api/safe/v1/authz/checks",
                json={"accessor_id": user_id,
                      "checks": [{"resource": {"type": resource_type, "id": resource_id},
                                  "operation": operation}],
                      "evaluation_scope": "effective"},
                headers=internal_request_headers({'Content-Type': 'application/json'})) as response:
            if response.status < 200 or response.status >= 300:
                raise RuntimeError(f"bkn-safe check returned status {response.status}")
            data = await response.json()
            if not isinstance(data, dict) or not isinstance(data.get('allowed'), bool):
                raise RuntimeError("bkn-safe check returned an invalid decision")
            results = data.get('results')
            if not isinstance(results, list) or len(results) != 1:
                raise RuntimeError("bkn-safe check returned an invalid result")
            result = results[0]
            if (not isinstance(result, dict)
                    or result.get('resource_type') != resource_type
                    or result.get('resource_id') != resource_id
                    or result.get('operation') != operation
                    or not isinstance(result.get('allowed'), bool)
                    or result['allowed'] != data['allowed']):
                raise RuntimeError("bkn-safe check returned an inconsistent result")
            return result['allowed']

    async def _bkn_safe_filter_ids(self, user_id, operation, resource_type) -> list:
        """Filter model ids through bkn-safe's effective batch PEP."""
        if operation == "*":
            return []
        candidate_ids = [model['f_model_id'] for model in small_model_dao.get_all_ids()]
        if not candidate_ids:
            return []
        normalized_ids = [str(model_id) for model_id in candidate_ids]
        session = await self.get_session()
        payload = {
            "accessor_id": user_id,
            "resources": [{"type": resource_type, "id": model_id} for model_id in normalized_ids],
            "visibility_operations": [operation],
            "include_operations": False,
            "evaluation_scope": "effective",
        }
        async with session.post(
                f"{self.bkn_safe_url}/api/safe/v1/authz/resource-filter",
                json=payload,
                headers=internal_request_headers({'Content-Type': 'application/json'})) as response:
            if response.status < 200 or response.status >= 300:
                raise RuntimeError(f"bkn-safe resource-filter returned status {response.status}")
            data = await response.json()
            if not isinstance(data, dict) or not isinstance(data.get('resources'), list):
                raise RuntimeError("bkn-safe resource-filter returned an invalid result")
            allowed = {item.get('resource_id') for item in data['resources']
                       if isinstance(item, dict) and item.get('resource_type') == resource_type}
            return [model_id for model_id, normalized_id in zip(candidate_ids, normalized_ids)
                    if normalized_id in allowed]

    async def _bkn_safe_delete(self, resource_type, resource_ids) -> bool:
        session = await self.get_session()
        ok = True
        for resource_id in resource_ids:
            try:
                async with session.delete(
                        f"{self.bkn_safe_url}/api/safe/v1/authz/policies",
                        json={"resource": {"type": resource_type, "id": resource_id}},
                        headers=internal_request_headers({'Content-Type': 'application/json'})) as response:
                    if response.status != 204:
                        ok = False
            except Exception as exc:
                StandLogger.error(exc.args)
                ok = False
        return ok

    async def get_permission_ids(self, user_id: str, operation: str,
                                 resource_type: str, resource_name: str, role: str) -> list:
        return await self._bkn_safe_filter_ids(user_id, operation, resource_type)

    async def delete_permission(self, resource_type: str, resource_ids: list) -> bool:
        return await self._bkn_safe_delete(resource_type, resource_ids)


permission_manager = PermissionManager()
