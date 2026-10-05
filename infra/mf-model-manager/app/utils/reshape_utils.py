import json
import re

from app.commons.get_user_info import get_userid_by_search, get_username_by_ids
from app.dao.llm_model_dao import llm_model_dao
import hashlib
import hmac
import os


_CREDENTIAL_DIGEST_KEY = os.environ.get("BKN_CREDENTIAL_DIGEST_KEY", "")


def _credential_digest(value):
    """Return a stable keyed digest without exposing a reversible credential hash."""
    if not _CREDENTIAL_DIGEST_KEY:
        return hashlib.sha256(value.encode("utf-8")).hexdigest()
    return hmac.new(
        _CREDENTIAL_DIGEST_KEY.encode("utf-8"), value.encode("utf-8"), hashlib.sha256
    ).hexdigest()


def is_credential_digest(candidate, value):
    """Accept both the new digest and legacy MD5 placeholders during rollout."""
    if not isinstance(candidate, str) or not isinstance(value, str):
        return False
    legacy = hashlib.md5(value.encode("utf-8"), usedforsecurity=False).hexdigest()
    return hmac.compare_digest(candidate, _credential_digest(value)) or hmac.compare_digest(candidate, legacy)


async def reshape_source(result, total):
    user_ids = await get_userid_by_search(result)
    user_infos = await get_username_by_ids(user_ids)
    result = {
        "count": total,
        "data": [
            {
                "model_id": line["f_model_id"],
                "model_name": line["f_model_name"],
                "model_series": line["f_model_series"],
                "model": line["f_model"],
                "create_by": user_infos.get(line["f_create_by"], ""),
                "update_by": user_infos.get(line["f_update_by"], ""),
                "create_time": line["f_create_time"].strftime('%Y-%m-%d %H:%M:%S'),
                "update_time": line["f_update_time"].strftime('%Y-%m-%d %H:%M:%S'),
                "max_model_len": line["f_max_model_len"],
                "model_parameters": line["f_model_parameters"],
                "model_type": line["f_model_type"],
                "quota": line["f_quota"],
                "model_config": json.loads(re.sub(r'"api_key":\s*"[^"]*"', '"api_key": "******************************"', line["f_model_config"])),
                "input_tokens_remain": None,
                "input_tokens": None,
                "input_tokens_used": None,
                "billing_type": None,
                "output_tokens": None,
                "output_tokens_used": None,
                "output_tokens_remain": None,
                "default": True if line["f_default"] else False
            }
            for line in result
        ]
    }
    return result


def reshape_check(result):
    result = {
        "model_id": result[0]["f_model_id"],
        "model_series": result[0]["f_model_series"],
        "model_name": result[0]["f_model_name"],
        "model_config": json.loads(result[0]["f_model_config"].replace("'", '"')),
        "max_model_len": result[0]['f_max_model_len'],
        "model_parameters": result[0]["f_model_parameters"],
        "model_type": result[0]["f_model_type"]
    }
    if result["model_parameters"] is None:
        result.pop("model_parameters")
    if "api_key" in result["model_config"].keys() and result["model_config"]["api_key"] != "":
        result["model_config"]["api_key"] = _credential_digest(result["model_config"]["api_key"])
    if "secret_key" in result["model_config"].keys() and result["model_config"]["secret_key"] != "":
        result["model_config"]["secret_key"] = _credential_digest(result["model_config"]["secret_key"])
    return result


async def reshape_param(param):
    result = list()
    data_list = llm_model_dao.get_all_data_from_model_param()
    for line in param:
        series = {
            # 'model': line.f_model_series_name_us,
            'title': line["f_model_series_name_cn"],
            # 'subTitle': {
            #     'zh-CN': line.f_model_series_desc_cn,
            #     'en-US': line.f_model_series_desc_us
            # },
            'icon': line["f_model_icon"],
            'formData': []
        }
        for param_id in json.loads(line["f_model_param_id"].replace("'", '"')):
            # cell = llm_model_dao.get_data_from_model_param_by_param_id(param_id)
            cell = []
            for item in data_list:
                if item["f_param_id"] == param_id:
                    cell.append(item)
                    break
            if cell == []:
                continue
            if cell[0]["f_param_field"] == "model_name":
                cell[0]["f_pattern"] = '^[-()_a-zA-Z0-9\u4e00-\u9fa5]+$'
            param = {
                'field': f'model_config.{cell[0]["f_param_field"]}',
                'component': cell[0]["f_box_component"],
                'type': cell[0]["f_param_type"],
                'label': {
                    'zh-CN': cell[0]["f_box_lab_cn"],
                    'en-US': cell[0]["f_box_lab_us"],
                },
                'placeholder': {
                    'zh-CN': cell[0]["f_box_mark_cn"],
                    'en-US': cell[0]["f_box_mark_us"],
                },
                'rules': [
                    {
                        'required': cell[0]["f_req"],
                        'message': {
                            'zh-CN': cell[0]["f_req_mes_cn"],
                            'en-US': cell[0]["f_req_mes_us"],
                        }
                    },
                    {
                        'max': cell[0]["f_max"],
                        'message': {
                            'zh-CN': cell[0]["f_max_mes_cn"],
                            'en-US': cell[0]["f_max_mes_us"],
                        }
                    },
                    {
                        'pattern': cell[0]["f_pattern"],
                        'message': {
                            'zh-CN': cell[0]["f_pat_mes_cn"],
                            'en-US': cell[0]["f_pat_mes_us"],
                        }
                    }
                ]
            }
            series['formData'].append(param)
        result.append({f"{line['f_model_series_name_cn']}": series})
    return {'res': result}


def key_value():
    order_list = {
        "asc": "",
        "desc": "-"
    }
    rule_dict = {
        "create_time": "f_create_time",
        "update_time": "f_update_time",
        "prompt_name": "f_prompt_name"
    }
    return order_list, rule_dict


def key_value_model():
    order_list = {
        "asc": "",
        "desc": "-"
    }
    rule_dict = {
        "create_time": "f_create_time",
        "update_time": "f_update_time",
        "model_name": "f_model_name"
    }
    return order_list, rule_dict
