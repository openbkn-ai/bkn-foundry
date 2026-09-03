import json
import os

import aiohttp
from app.core.config import base_config, directory_settings
from app.commons.errors import UserManagementError
from app.commons.locale import internal_request_headers


async def _resolve_names_bkn_safe(bkn_safe_url, user_ids):
    """bkn-safe directory name resolution: app accounts are User rows, so a
    single /names call with the ids as both user_ids and app_ids resolves
    everything; merge the two name arrays into {id: name}."""
    out = {}
    async with aiohttp.ClientSession() as session:
        async with session.post(
                f"{bkn_safe_url}/api/safe/v1/directory/names",
                json={"user_ids": user_ids, "app_ids": user_ids},
                headers=internal_request_headers({"Content-Type": "application/json"})) as resp:
            if resp.status != 200:
                raise Exception("bkn-safe directory service error,please check")
            data = json.loads(await resp.text())
            for item in data.get("user_names", []):
                out[item["id"]] = item["name"]
            for item in data.get("app_names", []):
                out[item["id"]] = item["name"]
    return out


async def get_username_by_ids(user_ids):
    if base_config.DEBUG:
        return {}
    if not user_ids:
        return {}
    # bkn-safe directory cutover (revertible): DIRECTORY_PROVIDER=bkn-safe +
    # BKN_SAFE_URL resolves names via bkn-safe instead of ISF. Unset to revert.
    provider, bkn_safe_url = directory_settings()
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
    
    # Accumulate resolved user and application names.
    final_user_infos = {}
    
    for i in range(2):
        async with aiohttp.ClientSession() as session:
            async with session.post(
                    user_management_url,
                    json=payload,
                    headers=internal_request_headers()) as response:
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
                                    user_management_app_url,
                                    json=app_payload,
                                    headers=internal_request_headers()) as app_response:
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
                                                user_management_app_url,
                                                json=app_payload,
                                                headers=internal_request_headers()) as retry_response:
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
