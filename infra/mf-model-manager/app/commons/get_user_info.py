import json
import logging
import os
import time

import aiohttp
from app.core.config import base_config


_DIRECTORY_TIMEOUT = aiohttp.ClientTimeout(total=3, sock_connect=1, sock_read=2)
_DIRECTORY_CACHE_TTL_SECONDS = 60
_directory_session = None
_directory_name_cache = {}
_logger = logging.getLogger(__name__)


async def _get_directory_session():
    global _directory_session
    if _directory_session is None or _directory_session.closed:
        _directory_session = aiohttp.ClientSession(timeout=_DIRECTORY_TIMEOUT)
    return _directory_session


async def close_directory_session():
    """Close the reusable bkn-safe directory client at application shutdown."""
    global _directory_session
    if _directory_session is not None and not _directory_session.closed:
        await _directory_session.close()
    _directory_session = None


def clear_directory_name_cache():
    """Test and lifecycle helper; names are never authorization decisions."""
    _directory_name_cache.clear()
from app.commons.errors import UserManagementError
from app.commons.locale import internal_request_headers
from app.core.config import base_config


async def _resolve_names_bkn_safe(bkn_safe_url, user_ids):
    """bkn-safe directory name resolution: app accounts are User rows, so a
    single /names call with the ids as both user_ids and app_ids resolves
    everything; merge the two name arrays into {id: name}."""
    now = time.monotonic()
    out = {user_id: cached[1] for user_id in user_ids
           if (cached := _directory_name_cache.get(user_id)) and cached[0] > now}
    missing_ids = [user_id for user_id in user_ids if user_id not in out]
    if not missing_ids:
        return out

    session = await _get_directory_session()
    lookup_started_at = time.perf_counter()
    async with session.post(
            f"{bkn_safe_url}/api/safe/v1/directory/names",
            json={"user_ids": missing_ids, "app_ids": missing_ids},
            headers=internal_request_headers({"Content-Type": "application/json"})) as resp:
        if resp.status != 200:
            raise Exception("bkn-safe directory service error,please check")
        data = json.loads(await resp.text())
        for item in data.get("user_names", []):
            out[item["id"]] = item["name"]
        for item in data.get("app_names", []):
            out[item["id"]] = item["name"]
    _logger.info("bkn-safe directory name lookup completed in %.2f ms for %d uncached IDs",
                 (time.perf_counter() - lookup_started_at) * 1000, len(missing_ids))
    expires_at = now + _DIRECTORY_CACHE_TTL_SECONDS
    _directory_name_cache.update({user_id: (expires_at, name) for user_id, name in out.items()})
    return out


async def get_username_by_ids(user_ids):
    if base_config.DEBUG:
        return {}
    if not user_ids:
        return {}
    # bkn-safe directory cutover (revertible): DIRECTORY_PROVIDER=bkn-safe +
    # BKN_SAFE_URL resolves names via bkn-safe instead of ISF. Unset to revert.
    provider = os.getenv("DIRECTORY_PROVIDER", "")
    bkn_safe_url = os.getenv("BKN_SAFE_URL", "")
    if provider == "bkn-safe" and bkn_safe_url:
        return await _resolve_names_bkn_safe(bkn_safe_url, user_ids)
    user_management_url = f"http://{base_config.USERMANAGEMENTPRIVATEHOST}:{base_config.USERMANAGEMENTPRIVATEPORT}/api/user-management/v1/batch-get-user-info"
    user_management_app_url = f"http://{base_config.USERMANAGEMENTPRIVATEHOST}:{base_config.USERMANAGEMENTPRIVATEPORT}/api/user-management/v2/names"
    payload = {
        "user_ids": user_ids,
        "method": "GET",
        "fields": [
            "name"
        ]
    }
    headers = internal_request_headers()
    
    # Accumulate resolved user and application names.
    final_user_infos = {}
    
    for i in range(2):
        async with aiohttp.ClientSession() as session:
            async with session.post(user_management_url, json=payload, headers=headers) as response:
                if response.status != 200:
                    if response.status == 404:
                        res = await response.text()
                        result = json.loads(res)
                        invalid_ids = result.get("detail", {}).get("ids", [])
                        effective_ids = [user_id for user_id in user_ids if user_id not in invalid_ids]
                        payload["user_ids"] = effective_ids
                        
                        # Resolve invalid user IDs through the application-name endpoint.
                        if invalid_ids:
                            app_payload = {
                                "method": "GET",
                                "app_ids": invalid_ids
                            }
                            
                            # Query application names.
                            async with session.post(
                                    user_management_app_url, json=app_payload, headers=headers) as app_response:
                                if app_response.status == 200:
                                    app_res = await app_response.text()
                                    app_result = json.loads(app_res)
                                    # Merge application names into the result.
                                    app_names = app_result.get("app_names", [])
                                    for app_info in app_names:
                                        final_user_infos[app_info['id']] = app_info['name']
                                elif app_response.status == 400:
                                    app_res = await app_response.text()
                                    app_result = json.loads(app_res)
                                    # Exclude invalid application IDs reported by a 400 response.
                                    invalid_app_ids = app_result.get("detail", {}).get("ids", [])
                                    valid_app_ids = [app_id for app_id in invalid_ids if app_id not in invalid_app_ids]
                                    
                                    if valid_app_ids:
                                        # Retry the application-name request with valid IDs only.
                                        app_payload["app_ids"] = valid_app_ids
                                        async with session.post(
                                                user_management_app_url, json=app_payload,
                                                headers=headers) as retry_response:
                                            if retry_response.status == 200:
                                                retry_res = await retry_response.text()
                                                retry_result = json.loads(retry_res)
                                                app_names = retry_result.get("app_names", [])
                                                for app_info in app_names:
                                                    final_user_infos[app_info['id']] = app_info['name']
                                else:
                                    # Treat all other statuses as dependency failures.
                                    raise Exception("user-management app service error,please check")
                        
                        continue
                    raise Exception("user-management service error,please check")
                else:
                    res = await response.text()
                    result = json.loads(res)
                    user_infos = {info['id']: info['name'] for info in result}
                    # Merge user and application identity results.
                    final_user_infos.update(user_infos)
                    return final_user_infos


async def get_userid_by_search(result):
    user_ids = []
    if base_config.DEBUG:
        return user_ids
    for line in result:
        create_by = line["f_create_by"]
        update_by = line["f_update_by"]
        user_ids.append(create_by) if create_by else user_ids
        user_ids.append(update_by) if update_by else user_ids
    user_ids = list(set(user_ids))
    return user_ids
