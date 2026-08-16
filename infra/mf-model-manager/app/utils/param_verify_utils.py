import json
import re
from fractions import Fraction

import func_timeout
from fastapi.exceptions import RequestValidationError
from app.commons.errors import *
from app.dao.llm_model_dao import llm_model_dao
from app.dao.prompt_dao import prompt_dao


# Validate parameters for adding a model.


async def llm_add_verify(schema_para, userId):
    # model_name
    model_name = schema_para.get("model_name", "")
    if not isinstance(model_name, str) or model_name == "":
        LLMAdd2Error['description'] = "model_name is invalid"
        LLMAdd2Error['detail'] = "model_name must be a non-empty string"
        return LLMAdd2Error
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$',
                     schema_para["model_name"]):
        LLMAdd2Error['description'] = "model_name is invalid"
        LLMAdd2Error['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 50 characters"
        return LLMAdd2Error
    model_name_list = llm_model_dao.get_model_by_name(model_name)
    if len(model_name_list) > 0:
        if model_name_list[0]["f_create_by"] == userId:
            LLMAdd2Error['description'] = "The name already exists; choose another name"
            LLMAdd2Error['detail'] = "The name already exists; choose another name"
        else:
            LLMAdd2Error['description'] = "The name is already used by another user; choose another name"
            LLMAdd2Error['detail'] = "The name is already used by another user; choose another name"
        error_dict = LLMAdd2Error.copy()
        error_dict["code"] = "ModelFactory.ConnectController.LLMAdd.NameRepeat"
        return error_dict
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9]{,50}$',
                     schema_para['model_config']['api_model']) or len(
        schema_para['model_config']['api_model'].replace(' ', '')) == 0:
        LLMAdd2Error['description'] = "api_model is invalid"
        LLMAdd2Error['detail'] = "The parameter supports English and keyboard symbols, with a maximum of 50 characters"
        return LLMAdd2Error
    if not isinstance(schema_para['max_model_len'], int) or schema_para['max_model_len'] <= 0:
        LLMAdd2Error['description'] = "max_model_len is invalid"
        LLMAdd2Error['detail'] = "max_model_len must be a positive integer"
        return LLMAdd2Error
    if "model_parameters" in schema_para:
        if not isinstance(schema_para['model_parameters'], int) or schema_para['model_parameters'] <= 0:
            LLMAdd2Error['description'] = "model_parameters is invalid"
            LLMAdd2Error['detail'] = "model_parameters must be a positive integer"
            return LLMAdd2Error
    model_series_list = ["tome", "qwen", "openai", "internlm", "deepseek", "qianxun", "claude",
                         "chatglm", "llama", "others", "baidu", "baidu_tianchen"]
    try:
        if schema_para['model_series'] not in model_series_list:
            LLMAdd2Error['description'] = f"model_series must be one of {model_series_list}"
            LLMAdd2Error['detail'] = f"model_series must be one of {model_series_list}"
            return LLMAdd2Error

        if schema_para['model_series'] == 'openai':
            if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9]+$',
                             schema_para['model_config']['api_key']) or len(
                schema_para['model_config']["api_key"].replace(' ', '')) == 0:
                LLMAdd2Error['description'] = "api_key is invalid"
                LLMAdd2Error['detail'] = "The parameter supports English and keyboard symbols"
                return LLMAdd2Error
        elif schema_para['model_series'].lower() == 'tome':
            if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9]{,400}$',
                             schema_para['model_config']['api_url']) or len(
                schema_para['model_config']["api_url"].replace(' ', '')) == 0:
                LLMAdd2Error['description'] = "api_url is invalid"
                LLMAdd2Error['detail'] = "The parameter supports English and keyboard symbols, with a maximum of 400 characters"
                return LLMAdd2Error
        elif schema_para['model_series'].lower() == "others":
            schema_para['model_config']["api_url"] = schema_para["model_config"]["api_url"]
            if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9]{,400}$',
                             schema_para['model_config']['api_url']) or len(
                schema_para['model_config']["api_url"].replace(' ', '')) == 0:
                LLMAdd2Error['description'] = "api_url is invalid"
                LLMAdd2Error['detail'] = "The parameter supports English and keyboard symbols, with a maximum of 400 characters"
                return LLMAdd2Error
        else:
            schema_para['model_config']["api_url"] = schema_para["model_config"]["api_url"]
            if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9]{,400}$',
                             schema_para['model_config']['api_url']) or len(
                schema_para['model_config']["api_url"].replace(' ', '')) == 0:
                LLMAdd2Error['description'] = "api_url is invalid"
                LLMAdd2Error['detail'] = "The parameter supports English and keyboard symbols, with a maximum of 400 characters"
                return LLMAdd2Error
            if "api_key" in schema_para['model_config']:
                if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9]+$',
                                 schema_para['model_config']['api_key']) or len(
                    schema_para['model_config']["api_key"].replace(' ', '')) == 0:
                    LLMAdd2Error['description'] = "api_key is invalid"
                    LLMAdd2Error['detail'] = "The parameter supports English and keyboard symbols"
                    return LLMAdd2Error
        if "quota" in schema_para.keys() and schema_para["quota"] is not True and schema_para["quota"] is not False:
            raise RequestValidationError([{"loc": ('body', "quota"), "type": "value_error.type_error"}])
        api_key = schema_para['model_config'].get("api_key", None)
        if llm_model_dao.check_model_unique(schema_para['model_config']["api_url"],
                                            schema_para['model_config']["api_model"], userId, api_key):
            error_dict = LLMAdd2Error.copy()
            error_dict["code"] = "ModelFactory.ConnectController.LLMAdd.BaseAndModelRepeat"
            error_dict["description"] = "A model with the same api_url and api_model {} already exists".format(
                "" if api_key in ["", None] else "、api_key")
            error_dict["detail"] = "A model with the same api_url and api_model {} already exists".format("" if api_key in ["", None] else "、api_key")
            return error_dict
    except Exception as e:
        LLMAdd2Error['description'] = "config is invalid"
        LLMAdd2Error['detail'] = str(e)
        return LLMAdd2Error


# Validate parameters for testing a model.
def llm_test_verify(model_param):
    key_list = ["model_series", "model_config", "model_type"]
    for k in key_list:
        if k not in model_param.keys():
            raise RequestValidationError([{"loc": ('body', k), "type": "value_error.missing"}])
    conf_list = list(model_param['model_config'].keys())
    if 'api_model' not in conf_list or not model_param['model_config']['api_model']:
        LLMTestError['description'] = "api_model is invalid"
        LLMTestError['detail'] = "Provide a valid api_model"
        return LLMTestError
    elif 'api_url' not in conf_list or not model_param['model_config']['api_url']:
        LLMTestError['description'] = "api_url is invalid"
        LLMTestError['detail'] = "Provide a valid api_url"
        return LLMTestError

    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9]{,50}$',
                     model_param['model_config']['api_model']):
        LLMTestError['description'] = "api_model is invalid"
        LLMTestError['detail'] = "The parameter supports English and keyboard symbols, with a maximum of 50 characters"
        return LLMTestError
    try:
        if 'api_key' in model_param['model_config']:
            if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9]+$',
                             model_param['model_config']['api_key']):
                LLMTestError['description'] = "api_key is invalid"
                LLMTestError['detail'] = "The parameter supports English and keyboard symbols"
                return LLMTestError
        if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9]{,400}$',
                         model_param['model_config']['api_url']):
            LLMTestError['description'] = "api_url is invalid"
            LLMTestError['detail'] = "The parameter supports English and keyboard symbols, with a maximum of 400 characters"
            return LLMTestError
        if model_param["model_type"] not in ('llm', 'rlm','vu'):
            LLMTestError['description'] = "model_type is invalid"
            LLMTestError['detail'] = "The parameter supports only llm, rlm, or vu"
            return LLMTestError
    except Exception as e:
        print(e)
        LLMTestError['description'] = "config is invalid"
        LLMTestError['detail'] = ""
        return LLMTestError


def llm_edit_verify(model_para):
    key_list = ["model_config", "model_series", "model_name", "model_id", "max_model_len", "model_type"]
    for k in key_list:
        if k not in model_para.keys():
            raise RequestValidationError([{"loc": ('body', k), "type": "value_error.missing"}])
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$',
                     model_para['model_name']):
        LLMEditError['description'] = "model_name is invalid"
        LLMEditError['detail'] = "The parameter supports Chinese, English, and keyboard symbols, with a maximum of 50 characters"
        return LLMEditError
    if not isinstance(model_para['max_model_len'], int) or model_para['max_model_len'] <= 0:
        LLMEditError['description'] = "max_model_len is invalid"
        LLMEditError['detail'] = "max_model_len must be a positive integer"
        return LLMEditError
    if "model_parameters" in model_para:
        if not isinstance(model_para['model_parameters'], int) or model_para['model_parameters'] <= 0:
            LLMEditError['description'] = "model_parameters is invalid"
            LLMEditError['detail'] = "model_parameters must be a positive integer"
            return LLMEditError
    if model_para["model_type"] not in ('llm', 'rlm', 'vu'):
        LLMEditError['description'] = "model_type is invalid"
        LLMEditError['detail'] = "The parameter supports only llm, rlm, or vu"
        return LLMEditError
    return False


def llm_source_verify(order, page, size, rule, series, name, model_type):
    if not re.search(r'^[1-9]\d*$', page):
        LLMSourceError['description'] = "page is invalid"
        LLMSourceError['detail'] = "The parameter must be a positive integer"
        return LLMSourceError
    if not re.search(r'^[1-9]\d*$', size):
        LLMSourceError['description'] = "size is invalid"
        LLMSourceError['detail'] = "The parameter must be a positive integer"
        return LLMSourceError
    if not re.search(r'^(asc|desc)$', order):
        LLMSourceError['description'] = "order is invalid"
        LLMSourceError['detail'] = "The parameter supports only asc or desc"
        return LLMSourceError
    if not re.search(r'^(update_time|create_time|model_name|default)$', rule):
        LLMSourceError['description'] = "rule is invalid"
        LLMSourceError['detail'] = "The parameter supports only update_time, create_time, model_name, or default"
        return LLMSourceError
    if not series:
        LLMSourceError['description'] = "series is invalid"
        LLMSourceError['detail'] = "series cannot be empty"
        return LLMSourceError
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$|^$',
                     name):
        LLMSourceError['description'] = "name is invalid"
        LLMSourceError['detail'] = "The parameter supports Chinese, English, and keyboard symbols, with a maximum of 50 characters"
    if model_type and model_type not in ('llm', 'rlm', 'vu'):
        LLMSourceError['description'] = "model_type is invalid"
        LLMSourceError['detail'] = "The parameter supports only llm, rlm, or vu"
        return LLMSourceError
    return False


def verify_icon_color_config(s):
    valid_colors = [
        "icon-color-pz-019688",
        "icon-color-pz-F759AB",
        "icon-color-pz-FADB14",
        "icon-color-pz-FF8501",
        "icon-color-pz-F75959",
        "icon-color-pz-8C8C8C",
        "icon-color-pz-126EE3",
        "icon-color-pz-13C2C2",
        "icon-color-pz-52C41A",
        "icon-color-pz-9254DE"
    ]
    if s in valid_colors:
        return True
    else:
        return False


def verify_icon_color_config_metric(s):
    valid_colors = [
        "icon-color-zbk-FF8501",
        "icon-color-zbk-13C2C2",
        "icon-color-zbk-FADB14",
        "icon-color-zbk-019688",
        "icon-color-zbk-9254DE",
        "icon-color-zbk-8C8C8C",
        "icon-color-zbk-126EE3",
        "icon-color-zbk-52C41A",
        "icon-color-zbk-F759AB",
        "icon-color-zbk-F75959"
    ]
    if s in valid_colors:
        return True
    else:
        return False


def verify_text_field(s, max_len):
    if not isinstance(s, str):
        return False
    if len(s) > max_len:
        return False
    if not re.match(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！·￥…（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]*$', s):
        return False
    return True


def verify_id(s):
    if not isinstance(s, str):
        return False
    if not re.match(r'^[0-9]{18}$', s) and not re.match(r'^[0-9]{19}$', s):
        return False
    return True


def include_dataset_id(dataset_version_id_list, dataset_id):
    try:
        for item in dataset_version_id_list:
            if item.split("/")[0] == dataset_id:
                return True
        return False
    except Exception:
        return False
def prompt_source_item_verify(prompt_item_name):
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$|^$',
                     prompt_item_name):
        PromptItemSourceError['description'] = "The search name is invalid"
        PromptItemSourceError['detail'] = "The parameter supports Chinese, English, and keyboard symbols, with a maximum of 50 characters"
        return PromptItemSourceError

async def prompt_source_verify(prompt_item_id, prompt_item_type_id, page, size, prompt_name, order,
                               rule, deploy, prompt_type):
    if not prompt_item_id and prompt_item_id != '':
        PromptSourceError['description'] = "prompt_item_id is invalid"
        PromptSourceError['detail'] = "prompt_item_id is required"
        return PromptSourceError
    # prompt_item_type_id_list = [cell.f_prompt_item_type_id for cell in await PromptItemList.all()]
    # if prompt_item_type_id not in prompt_item_type_id_list and prompt_item_type_id != '':
    #     PromptSourceError['description'] = "prompt_item_type_id is invalid"
    #     return PromptSourceError
    if not re.search(r'^[1-9]\d*$', page):
        PromptSourceError['description'] = "page is invalid"
        PromptSourceError['detail'] = "The parameter must be a positive integer"
        return PromptSourceError
    if not re.search(r'^[1-9]\d*$', size):
        PromptSourceError['description'] = "size is invalid"
        PromptSourceError['detail'] = "The parameter must be a positive integer"
        return PromptSourceError
    if not re.search(r'^(asc|desc)$', order):
        PromptSourceError['description'] = "order is invalid"
        PromptSourceError['detail'] = "The parameter supports only asc or desc"
        return PromptSourceError
    if not re.search(r'^(update_time|create_time|prompt_name)$', rule):
        PromptSourceError['description'] = "rule is invalid"
        PromptSourceError['detail'] = "The parameter supports only update_time, create_time, or prompt_name"
        return PromptSourceError
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$|^$',
                     prompt_name):
        PromptSourceError['description'] = "The search name is invalid"
        PromptSourceError['detail'] = "The parameter supports Chinese, English, and keyboard symbols, with a maximum of 50 characters"
        return PromptSourceError
    if not re.search(r'^(yes|no|all|^$)$', deploy):
        PromptSourceError['description'] = "deploy is invalid"
        PromptSourceError['detail'] = "The parameter supports only yes, no, or all"
        return PromptSourceError
    if not re.search(r'^(chat|completion|all|^$)$', prompt_type):
        PromptSourceError['description'] = "prompt_type is invalid"
        PromptSourceError['detail'] = "The parameter supports only chat, completion, or all"
        return PromptSourceError
def prompt_llm_source_verify(types):
    if not re.search(r'^(chat|completion|^$)$', types):
        PromptLLMSourceError['description'] = "types is invalid"
        PromptLLMSourceError['detail'] = "The parameter supports chat, completion, or an empty value"
        return PromptLLMSourceError


# Validate prompt-template query parameters.
def prompt_template_verify(prompt_name, prompt_type):
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$|^$',
                     prompt_name):
        PromptTemplateSource['description'] = "The search name is invalid"
        PromptTemplateSource['detail'] = "The parameter supports Chinese, English, and keyboard symbols, with a maximum of 50 characters"
        return PromptTemplateSource
    if not re.search(r'^(chat|completion|^$)$', prompt_type):
        PromptLLMSourceError['description'] = "prompt_type is invalid"
        PromptLLMSourceError['detail'] = "The parameter supports chat, completion, or an empty value"
        return PromptLLMSourceError


# Validate prompt lookup parameters.
async def check_prompt_verify(prompt_id):
    prompt_id_list = [cell["f_prompt_id"] for cell in prompt_dao.get_all_data_from_prompt_list()]
    if prompt_id not in prompt_id_list:
        # PromptCheck['detail'] = "prompt_id must exist in the database"
        return {'res': []}


# Validate prompt_service_id before model invocation.
async def used_prompt_id_verify(prompt_service_id):
    prompt_service_id_list = prompt_dao.get_all_prompt_service_id()
    if prompt_service_id not in prompt_service_id_list:
        PromptUsed['description'] = "prompt_service_id is invalid"
        PromptUsed['detail'] = "prompt_service_id must exist in the database"
        return PromptUsed


# Validate prompt-project creation parameters.
async def item_add_verify(model_para):
    if "prompt_item_name" not in model_para:
        PromptItemAddError1['description'] = "The request parameters are invalid"
        PromptItemAddError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptItemAddError1]
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$',
                     model_para["prompt_item_name"]) or len(model_para["prompt_item_name"].replace(' ', '')) == 0:
        PromptItemAddError1['description'] = "prompt_item_name is invalid"
        PromptItemAddError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 50 characters"
        return [400, PromptItemAddError1]
    name = model_para['prompt_item_name']
    item_name_list = [ids["f_prompt_item_name"] for ids in prompt_dao.get_all_prompt_item_list_distinct(None)]
    if name in item_name_list:
        PromptItemAddError2['description'] = "prompt_item_name is invalid"
        PromptItemAddError2['detail'] = "The prompt project name already exists"
        return [500, PromptItemAddError2]


# Validate prompt-project update parameters.
async def item_edit_verify(model_para):
    if "prompt_item_id" not in model_para or "prompt_item_name" not in model_para:
        PromptItemEditError1['description'] = "The request parameters are invalid"
        PromptItemEditError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptItemEditError1]
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$',
                     model_para["prompt_item_name"]) or len(model_para["prompt_item_name"].replace(' ', '')) == 0:
        PromptItemEditError1['description'] = "prompt_item_name is invalid"
        PromptItemEditError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 50 characters"
        return [400, PromptItemEditError1]
    ids_list = [ids["f_prompt_item_id"] for ids in prompt_dao.get_all_prompt_item_list_distinct(None)]
    # info = await PromptItemList.filter(f_prompt_item_id=model_para['prompt_item_id'])
    info = prompt_dao.get_all_info_from_prompt_item_list_by_item_id(model_para['prompt_item_id'])
    if model_para['prompt_item_id'] not in ids_list or info[0]["f_item_is_delete"] == 1:
        PromptItemEditError1['description'] = "prompt_item_id is invalid"
        PromptItemEditError1['detail'] = "The prompt project does not exist"
        return [400, PromptItemEditError1]
    name_new = model_para['prompt_item_name']
    itemid_list1 = [ids["f_prompt_item_id"] for ids in prompt_dao.get_all_prompt_item_list_distinct(None)]
    itemid_list = [num for num in itemid_list1 if num != model_para['prompt_item_id']]
    item_name_list = []
    for i in itemid_list:
        info = prompt_dao.get_all_info_from_prompt_item_list_by_item_id(i)
        item_name_list.append(info[0]["f_prompt_item_name"])
    if name_new in item_name_list:
        PromptItemEditError2['description'] = "prompt_item_name is invalid"
        PromptItemEditError2['detail'] = "The prompt project name already exists"
        return [500, PromptItemEditError2]


# Validate prompt-group creation parameters.
async def type_add_verify(model_para):
    if "prompt_item_id" not in model_para or "prompt_item_type" not in model_para:
        PromptTypeAddError1['description'] = "The request parameters are invalid"
        PromptTypeAddError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptTypeAddError1]
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$',
                     model_para["prompt_item_type"]) or len(model_para["prompt_item_type"].replace(' ', '')) == 0:
        PromptTypeAddError1['description'] = "prompt_item_type is invalid"
        PromptTypeAddError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 50 characters"
        return [400, PromptTypeAddError1]
    item_type = model_para['prompt_item_type']
    item_type_list = [ids["f_prompt_item_type"] for ids in
                    prompt_dao.get_all_info_from_prompt_item_list_by_item_id(model_para["prompt_item_id"])]
    ids_list = [ids["f_prompt_item_id"] for ids in prompt_dao.get_all_prompt_item_list_distinct(None)]
    info = prompt_dao.get_all_info_from_prompt_item_list_by_item_id(model_para["prompt_item_id"])
    if model_para['prompt_item_id'] not in ids_list or info[0]["f_item_is_delete"] == 1:
        PromptTypeAddError1['description'] = "prompt_item_id is invalid"
        PromptTypeAddError1['detail'] = "The prompt project does not exist"
        return [400, PromptTypeAddError1]
    if item_type in item_type_list:
        PromptTypeAddError2['description'] = "prompt_item_type is invalid"
        PromptTypeAddError2['detail'] = "The prompt group name already exists"
        return [500, PromptTypeAddError2]


# Validate prompt-group update parameters.
async def type_edit_verify(model_para):
    if "prompt_item_type" not in model_para or "prompt_item_type_id" not in model_para:
        PromptTypeEditError1['description'] = "The request parameters are invalid"
        PromptTypeEditError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptTypeEditError1]
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥·…（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$',
                     model_para["prompt_item_type"]) or len(model_para["prompt_item_type"].replace(' ', '')) == 0:
        PromptTypeEditError1['description'] = "prompt_item_type is invalid"
        PromptTypeEditError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 50 characters"
        return [400, PromptTypeEditError1]
    info = prompt_dao.get_data_from_prompt_item_list_by_type_id(model_para["prompt_item_type_id"])
    if info == () or info[0]["f_item_is_delete"] == 1:
        PromptTypeEditError1['description'] = "prompt_item_type_id is invalid"
        PromptTypeEditError1['detail'] = "The prompt project does not exist"
        return [400, PromptTypeEditError1]
    item_id = info[0]["f_prompt_item_id"]  # Owning project ID.
    type_id_list = [ids["f_prompt_item_type_id"] for ids in
                    prompt_dao.get_all_info_from_prompt_item_list_by_item_id(item_id)]  # All group IDs in the project.
    if model_para['prompt_item_type_id'] not in type_id_list or info[0]["f_item_is_delete"] == 1:
        PromptTypeEditError1['description'] = "prompt_item_type_id is invalid"
        PromptTypeEditError1['detail'] = "The prompt group does not exist"
        return [400, PromptTypeEditError1]
    item_type = model_para['prompt_item_type']
    type_id_list1 = [num for num in type_id_list if num != model_para['prompt_item_type_id']]
    type_name_list = []
    for i in type_id_list1:
        info1 = prompt_dao.get_data_from_prompt_item_list_by_type_id(i)
        type_name_list.append(info1[0]["f_prompt_item_type"])
    if item_type in type_name_list:
        PromptTypeEditError2['description'] = "prompt_item_type is invalid"
        PromptTypeEditError2['detail'] = "The prompt group name already exists"
        return [500, PromptTypeEditError2]
    #     PromptTypeEditError1['description'] = "prompt_item_type_id is invalid"
    #     return [400, PromptTypeEditError1]


# Validate prompt creation parameters.
async def prompt_add_verify(para):
    if "prompt_item_id" not in para or "prompt_item_type_id" not in para or "prompt_name" not in para or "prompt_type" not in para or "model_id" not in para or "icon" not in para or "model_para" not in para or "messages" not in para:
        PromptAddError1['description'] = "The request parameters are invalid"
        PromptAddError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptAddError1]
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$',
                     para["prompt_name"]) or len(para["prompt_name"].replace(' ', '')) == 0:
        PromptAddError1['description'] = "prompt_name is invalid"
        PromptAddError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 50 characters"
        return [400, PromptAddError1]
    if not re.search(r'^(chat|completion)$', para["prompt_type"]):
        PromptAddError1['description'] = "prompt_type is invalid"
        PromptAddError1['detail'] = "The parameter supports only chat or completion"
        return [400, PromptAddError1]
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，\na-zA-Z0-9\u4e00-\u9fa5]{,255}$',
                     para["prompt_desc"]):
        PromptAddError1['description'] = "prompt_desc is invalid"
        PromptAddError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 255 characters"
        return [400, PromptAddError1]
    name = para['prompt_name']
    # info1 = await PromptItemList.filter(f_prompt_item_type_id=para['prompt_item_type_id'])
    info1 = prompt_dao.get_data_from_prompt_item_list_by_type_id(para['prompt_item_type_id'])
    if info1 == () or info1[0]["f_item_is_delete"] == 1:
        PromptAddError1['description'] = "prompt_item_type_idThe request parameters are invalid"
        PromptAddError1['detail'] = "The prompt group does not exist"
        return [400, PromptAddError1]
    # info = await PromptItemList.filter(f_prompt_item_type_id=para['prompt_item_type_id'],
    #                                    f_prompt_item_id=para['prompt_item_id'])
    info = prompt_dao.get_data_from_prompt_item_list_by_id_and_type_id(para["prompt_item_id"],
                                                                       para["prompt_item_type_id"])
    if info == () or info[0]["f_item_is_delete"] == 1:
        PromptAddError1['description'] = "prompt_item_idThe request parameters are invalid"
        PromptAddError1['detail'] = "The prompt project does not exist"
        return [500, PromptAddError1]
    prompt_name_list = [ids["f_prompt_name"] for ids in
                        prompt_dao.get_data_from_prompt_list_by_item_type_id(para["prompt_item_type_id"])]
    if name in prompt_name_list:
        PromptAddError2['description'] = "prompt_nameThe request parameters are invalid"
        PromptAddError2['detail'] = "The prompt name already exists"
        return [500, PromptAddError2]

    if para["model_id"] != "":
        ids_list = [ids["f_model_id"] for ids in llm_model_dao.get_all_model_list()]
        info2 = llm_model_dao.get_data_from_model_list_by_id(para["model_id"])
        if para["model_id"] not in ids_list:
            PromptAddError1['description'] = "The request parameters are invalid"
            PromptAddError1['detail'] = "The selected large model does not exist"
            return [400, PromptAddError1]
        model = info2[0]["f_model"]
        if model == 'gpt-35-turbo-16k':
            if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                    para["model_para"][
                        "max_tokens"] > 16384 or para["model_para"]["max_tokens"] < 10:
                PromptAddError1['description'] = "max_tokensThe request parameters are invalid"
                PromptAddError1['detail'] = "This model supports an integer max_tokens value from 10 to 16384"
                return [400, PromptAddError1]
        if model == 'text-davinci-002':
            if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                    para["model_para"][
                        "max_tokens"] > 4097 or para["model_para"]["max_tokens"] < 10:
                PromptAddError1['description'] = "max_tokensThe request parameters are invalid"
                PromptAddError1['detail'] = "This model supports an integer max_tokens value from 10 to 4097"
                return [400, PromptAddError1]
        if model == 'baichuan2':
            if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                    para["model_para"][
                        "max_tokens"] > 4096 or para["model_para"]["max_tokens"] < 10:
                PromptAddError1['description'] = "max_tokensThe request parameters are invalid"
                PromptAddError1['detail'] = "This model supports an integer max_tokens value from 10 to 4096"
                return [400, PromptAddError1]
        else:
            if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int:
                PromptAddError1['description'] = "max_tokensThe request parameters are invalid"
                PromptAddError1['detail'] = "max_tokens must be an integer"
                return [400, PromptAddError1]

    if type(para["icon"]) != str:
        PromptAddError1['description'] = "iconThe request parameters are invalid"
        PromptAddError1['detail'] = "The parameter must be a string"
        return [400, PromptAddError1]
    icon = int(para["icon"])
    if icon not in list(range(0, 10)):
        PromptAddError1['description'] = "iconThe request parameters are invalid"
        PromptAddError1['detail'] = "The color configuration is invalid"
        return [400, PromptAddError1]
    for ceil in para["variables"]:
        if ceil["field_type"] == 'text':
            if "max_len" not in ceil or ceil["max_len"] < 1 or ceil["max_len"] > 256:
                PromptAddError1['description'] = "textThe request parameters are invalid"
                PromptAddError1['detail'] = "The parameter must be greater than 1 and less than 256"
                return [400, PromptAddError1]
        if ceil["field_type"] == 'number':
            if "range" not in ceil or len(ceil["range"]) != 2:
                PromptAddError1['description'] = "numberThe request parameters are invalid"
                PromptAddError1['detail'] = "The parameter must be an integer"
                return [400, PromptAddError1]
            if ceil["value_type"] == 'i':
                if (ceil["range"][0] == None or type(ceil["range"][0]) == int) and (
                        ceil["range"][1] == None or type(ceil["range"][1]) == int):
                    pass
                else:
                    PromptAddError1['description'] = "numberThe request parameters are invalid"
                    PromptAddError1['detail'] = "The parameter must be an integer"
                    return [400, PromptAddError1]
            if ceil["value_type"] == 'f':
                if (ceil["range"][0] == None or isinstance(ceil["range"][0], (int, float))) and (
                        ceil["range"][1] == None or isinstance(ceil["range"][1], (int, float))):
                    pass
                else:
                    PromptAddError1['description'] = "numberThe request parameters are invalid"
                    PromptAddError1['detail'] = "The parameter must be numeric"
                    return [400, PromptAddError1]

    if len(para["model_para"]) > 0:
        if type(para["model_para"]) != dict:
            PromptAddError1['description'] = "model_paraThe request parameters are invalid"
            PromptAddError1['detail'] = "The parameter must be an object"
            return [400, PromptAddError1]
        if "temperature" not in para["model_para"] or not isinstance(para["model_para"]["temperature"],
                                                                     (int, float)) or isinstance(
            para["model_para"]["temperature"], Fraction) or para["model_para"]["temperature"] > 2 or para["model_para"][
            "temperature"] < 0:
            PromptAddError1['description'] = "temperatureThe request parameters are invalid"
            PromptAddError1['detail'] = "The parameter must be a number from 0 to 2"
            return [400, PromptAddError1]
        if "top_p" not in para["model_para"] or not isinstance(para["model_para"]["top_p"], (int, float)) or isinstance(
                para["model_para"]["top_p"], Fraction) or para["model_para"]["top_p"] > 1 or para["model_para"][
            "top_p"] < 0:
            PromptAddError1['description'] = "top_pThe request parameters are invalid"
            PromptAddError1['detail'] = "The parameter must be a number from 0 to 1"
            return [400, PromptAddError1]
        if "presence_penalty" not in para["model_para"] or not isinstance(para["model_para"]["presence_penalty"],
                                                                          (int, float)) or isinstance(
            para["model_para"]["presence_penalty"], Fraction) or para["model_para"]["presence_penalty"] > 2 or \
                para["model_para"]["presence_penalty"] < -2:
            PromptAddError1['description'] = "presence_penaltyThe request parameters are invalid"
            PromptAddError1['detail'] = "The parameter must be a number from -2 to 2"
            return [400, PromptAddError1]
        if "frequency_penalty" not in para["model_para"] or not isinstance(para["model_para"]["frequency_penalty"],
                                                                           (int, float)) or isinstance(
            para["model_para"]["frequency_penalty"], Fraction) or para["model_para"]["frequency_penalty"] > 2 or \
                para["model_para"]["frequency_penalty"] < -2:
            PromptAddError1['description'] = "frequency_penaltyThe request parameters are invalid"
            PromptAddError1['detail'] = "The parameter must be a number from -2 to 2"
            return [400, PromptAddError1]


async def prompt_name_verify(model_para):
    if "prompt_id" not in model_para or "prompt_name" not in model_para or "model_id" not in model_para or "icon" not in model_para:
        PromptNameEditError1['description'] = "The request parameters are invalid"
        PromptNameEditError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptNameEditError1]
    prompt_id_list = [ids["f_prompt_id"] for ids in prompt_dao.get_all_data_from_prompt_list()]
    info = prompt_dao.get_prompt_by_id(model_para["prompt_id"])
    if model_para["prompt_id"] not in prompt_id_list:
        PromptNameEditError1['description'] = "The request parameters are invalid"
        PromptNameEditError1['detail'] = "The prompt does not exist"
        return [400, PromptNameEditError1]
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$',
                     model_para["prompt_name"]) or len(model_para["prompt_name"].replace(' ', '')) == 0:
        PromptNameEditError1['description'] = "prompt_name is invalid"
        PromptNameEditError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 50 characters"
        return [400, PromptNameEditError1]
    prompt_name_list = [ids["f_prompt_name"] for ids in
                        prompt_dao.get_data_from_prompt_list_by_item_type_id(info[0]["f_prompt_item_type_id"])]
    if model_para["prompt_name"] in prompt_name_list and model_para["prompt_name"] != info[0]["f_prompt_name"]:
        PromptNameEditError2['description'] = "The request parameters are invalid"
        PromptNameEditError2['detail'] = "The prompt name already exists"
        return [500, PromptNameEditError2]

    ids_list = [ids["f_model_id"] for ids in llm_model_dao.get_all_model_list()]
    info1 = llm_model_dao.get_data_from_model_list_by_id(model_para['model_id'])
    if model_para["model_id"] != "" and model_para["model_id"] not in ids_list:
        PromptNameEditError1['description'] = "The request parameters are invalid"
        PromptNameEditError1['detail'] = "The selected large model does not exist"
        return [500, PromptNameEditError1]
    if type(model_para["icon"]) != str:
        PromptNameEditError1['description'] = "iconThe request parameters are invalid"
        PromptNameEditError1['detail'] = "The parameter must be a string"
        return [400, PromptNameEditError1]
    elif model_para["icon"] == "":
        PromptNameEditError1['description'] = "iconThe request parameters are invalid"
        PromptNameEditError1['detail'] = "The parameter cannot be empty"
        return [400, PromptNameEditError1]
    icon = int(model_para["icon"])
    if icon not in list(range(0, 10)):
        PromptNameEditError1['description'] = "The request parameters are invalid"
        PromptNameEditError1['detail'] = "The color configuration is invalid"
        return [400, PromptNameEditError1]
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，\na-zA-Z0-9\u4e00-\u9fa5]{,255}$',
                     model_para["prompt_desc"]):
        PromptNameEditError1['description'] = "prompt_desc is invalid"
        PromptNameEditError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 255 characters"
        return [400, PromptNameEditError1]



async def prompt_edit_verify(para):
    if "prompt_id" not in para or "model_para" not in para or "messages" not in para or "model_id" not in para:
        PromptEditError1['description'] = "The request parameters are invalid"
        PromptEditError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptEditError1]
    prompt_id_list = [ids["f_prompt_id"] for ids in prompt_dao.get_all_data_from_prompt_list()]
    info = prompt_dao.get_prompt_by_id(para["prompt_id"])
    if para["prompt_id"] not in prompt_id_list:
        PromptEditError1['description'] = "The request parameters are invalid"
        PromptEditError1['detail'] = "The prompt does not exist"
        return [400, PromptEditError1]
    ids_list = [ids["f_model_id"] for ids in llm_model_dao.get_all_model_list()]
    info2 = llm_model_dao.get_data_from_model_list_by_id(para["model_id"])
    if para["model_id"] != "" and (para["model_id"] not in ids_list):
        PromptEditError1['description'] = "The request parameters are invalid"
        PromptEditError1['detail'] = "The selected large model does not exist"
        return [400, PromptEditError1]
    for ceil in para["variables"]:
        if ceil["field_type"] == 'text':
            if "max_len" not in ceil or ceil["max_len"] < 1 or ceil["max_len"] > 256:
                PromptEditError1['description'] = "textThe request parameters are invalid"
                PromptEditError1['detail'] = "The parameter must be greater than 1 and less than 256"
                return [400, PromptEditError1]
        if ceil["field_type"] == 'number':
            if "range" not in ceil or len(ceil["range"]) != 2:
                PromptEditError1['description'] = "numberThe request parameters are invalid"
                PromptEditError1['detail'] = "The parameter must be an integer"
                return [400, PromptEditError1]
            if ceil["value_type"] == 'i':
                if (ceil["range"][0] == None or type(ceil["range"][0]) == int) and (
                        ceil["range"][1] == None or type(ceil["range"][1]) == int):
                    pass
                else:
                    PromptEditError1['description'] = "numberThe request parameters are invalid"
                    PromptEditError1['detail'] = "The parameter must be an integer"
                    return [400, PromptEditError1]
            if ceil["value_type"] == 'f':
                if (ceil["range"][0] == None or isinstance(ceil["range"][0], (int, float))) and (
                        ceil["range"][1] == None or isinstance(ceil["range"][1], (int, float))):
                    pass
                else:
                    PromptEditError1['description'] = "numberThe request parameters are invalid"
                    PromptEditError1['detail'] = "The parameter must be numeric"
                    return [400, PromptEditError1]
    if type(para["model_para"]) != dict:
        PromptEditError1['description'] = "model_paraThe request parameters are invalid"
        PromptEditError1['detail'] = "The parameter must be an object"
        return [400, PromptEditError1]
    if para["model_para"] == {}:
        return
    if "temperature" not in para["model_para"] or not isinstance(para["model_para"]["temperature"],
                                                                 (int, float)) or isinstance(
        para["model_para"]["temperature"], Fraction) or para["model_para"]["temperature"] > 2 or para["model_para"][
        "temperature"] < 0:
        PromptEditError1['description'] = "temperatureThe request parameters are invalid"
        PromptEditError1['detail'] = "The parameter must be a number from 0 to 2"
        return [400, PromptEditError1]
    if "top_p" not in para["model_para"] or not isinstance(para["model_para"]["top_p"], (int, float)) or isinstance(
            para["model_para"]["top_p"], Fraction) or para["model_para"]["top_p"] > 1 or para["model_para"][
        "top_p"] < 0:
        PromptEditError1['description'] = "top_pThe request parameters are invalid"
        PromptEditError1['detail'] = "The parameter must be a number from 0 to 1"
        return [400, PromptEditError1]
    if "presence_penalty" not in para["model_para"] or not isinstance(para["model_para"]["presence_penalty"],
                                                                      (int, float)) or isinstance(
        para["model_para"]["presence_penalty"], Fraction) or para["model_para"]["presence_penalty"] > 2 or \
            para["model_para"]["presence_penalty"] < -2:
        PromptEditError1['description'] = "presence_penaltyThe request parameters are invalid"
        PromptEditError1['detail'] = "The parameter must be a number from -2 to 2"
        return [400, PromptEditError1]
    if "frequency_penalty" not in para["model_para"] or not isinstance(para["model_para"]["frequency_penalty"],
                                                                       (int, float)) or isinstance(
        para["model_para"]["frequency_penalty"], Fraction) or para["model_para"]["frequency_penalty"] > 2 or \
            para["model_para"]["frequency_penalty"] < -2:
        PromptEditError1['description'] = "frequency_penaltyThe request parameters are invalid"
        PromptEditError1['detail'] = "The parameter must be a number from -2 to 2"
        return [400, PromptEditError1]
    try:
        model = info2[0]["f_model"]
    except Exception:
        PromptEditError1["description"] = "model_idThe request parameters are invalid"
        PromptEditError1["detail"] = "model_id is required when model_para is provided"
        return [400, PromptEditError1]
    if model == 'gpt-35-turbo-16k':
        if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                para["model_para"][
                    "max_tokens"] > 16384 or para["model_para"]["max_tokens"] < 10:
            PromptEditError1['description'] = "max_tokensThe request parameters are invalid"
            PromptEditError1['detail'] = "This model supports an integer max_tokens value from 10 to 16384"
            return [400, PromptEditError1]
    if model == 'text-davinci-002':
        if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                para["model_para"][
                    "max_tokens"] > 4097 or para["model_para"]["max_tokens"] < 10:
            PromptEditError1['description'] = "max_tokensThe request parameters are invalid"
            PromptEditError1['detail'] = "This model supports an integer max_tokens value from 10 to 4097"
            return [400, PromptEditError1]
    if model == 'baichuan2':
        if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                para["model_para"][
                    "max_tokens"] > 4096 or para["model_para"]["max_tokens"] < 10:
            PromptEditError1['description'] = "max_tokensThe request parameters are invalid"
            PromptEditError1['detail'] = "This model supports an integer max_tokens value from 10 to 4096"
            return [400, PromptEditError1]


async def prompt_template_edit_verify(para):
    if "prompt_id" not in para or "prompt_name" not in para or "messages" not in para or "variables" not in para or\
            "icon" not in para or "prompt_item_type_id" not in para or "prompt_item_id" not in para:
        PromptTemplateEditError1['description'] = "The request parameters are invalid"
        PromptTemplateEditError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptTemplateEditError1]

    prompt_id = para["prompt_id"]
    if not isinstance(prompt_id, str) or prompt_id == "":
        PromptTemplateEditError1['description'] = "The request parameters are invalid"
        PromptTemplateEditError1['detail'] = "The prompt ID must be a non-empty string"
        return [400, PromptTemplateEditError1]
    prompt_id_list = [ids["f_prompt_id"] for ids in prompt_dao.get_all_data_from_prompt_list()]
    info = prompt_dao.get_prompt_by_id(para["prompt_id"])
    if para["prompt_id"] not in prompt_id_list:
        PromptTemplateEditError1['description'] = "The request parameters are invalid"
        PromptTemplateEditError1['detail'] = "The prompt does not exist"
        return [400, PromptTemplateEditError1]

    if not isinstance(para["variables"], list):
        PromptTemplateEditError1['description'] = "variables is invalid"
        PromptTemplateEditError1['detail'] = "The parameter must be a list"
        return [400, PromptTemplateEditError1]
    for ceil in para["variables"]:
        if ceil["field_type"] == 'text':
            if "max_len" not in ceil or ceil["max_len"] < 1 or ceil["max_len"] > 256:
                PromptEditError1['description'] = "textThe request parameters are invalid"
                PromptEditError1['detail'] = "The parameter must be greater than 1 and less than 256"
                return [400, PromptEditError1]
        if ceil["field_type"] == 'number':
            if "range" not in ceil or len(ceil["range"]) != 2:
                PromptEditError1['description'] = "numberThe request parameters are invalid"
                PromptEditError1['detail'] = "The parameter must be an integer"
                return [400, PromptEditError1]
            if ceil["value_type"] == 'i':
                if (ceil["range"][0] == None or type(ceil["range"][0]) == int) and (
                        ceil["range"][1] == None or type(ceil["range"][1]) == int):
                    pass
                else:
                    PromptEditError1['description'] = "numberThe request parameters are invalid"
                    PromptEditError1['detail'] = "The parameter must be an integer"
                    return [400, PromptEditError1]
            if ceil["value_type"] == 'f':
                if (ceil["range"][0] == None or isinstance(ceil["range"][0], (int, float))) and (
                        ceil["range"][1] == None or isinstance(ceil["range"][1], (int, float))):
                    pass
                else:
                    PromptEditError1['description'] = "numberThe request parameters are invalid"
                    PromptEditError1['detail'] = "The parameter must be numeric"
                    return [400, PromptEditError1]

    if not isinstance(para["prompt_name"], str):
        PromptTemplateEditError1['description'] = "prompt_name is invalid"
        PromptTemplateEditError1['detail'] = "The parameter must be a string"
        return [400, PromptTemplateEditError1]

    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$',
                     para["prompt_name"]) or len(para["prompt_name"].replace(' ', '')) == 0:
        PromptTemplateEditError1['description'] = "prompt_name is invalid"
        PromptTemplateEditError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 50 characters"
        return [400, PromptTemplateEditError1]

    # messages
    if not isinstance(para["messages"], str):
        PromptTemplateEditError1['description'] = "messages is invalid"
        PromptTemplateEditError1['detail'] = "The parameter must be a string"
        return [400, PromptTemplateEditError1]

    # opening_remarks
    if not isinstance(para["opening_remarks"], str):
        PromptTemplateEditError1['description'] = "opening_remarks is invalid"
        PromptTemplateEditError1['detail'] = "The parameter must be a string"
        return [400, PromptTemplateEditError1]

    # icon
    if type(para["icon"]) != str or para["icon"] == "":
        PromptTemplateEditError1['description'] = "iconThe request parameters are invalid"
        PromptTemplateEditError1['detail'] = "The parameter must be a non-empty string"
        return [400, PromptTemplateEditError1]
    icon = int(para["icon"])
    if icon not in list(range(0, 10)):
        PromptTemplateEditError1['description'] = "The request parameters are invalid"
        PromptTemplateEditError1['detail'] = "The color configuration is invalid"
        return [400, PromptTemplateEditError1]

    if not isinstance(para["prompt_desc"], str):
        PromptTemplateEditError1['description'] = "prompt_desc is invalid"
        PromptTemplateEditError1['detail'] = "The parameter must be a string"
        return [400, PromptTemplateEditError1]
    if not re.search(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，\na-zA-Z0-9\u4e00-\u9fa5]{,255}$',
                     para["prompt_desc"]):
        PromptTemplateEditError1['description'] = "prompt_desc is invalid"
        PromptTemplateEditError1['detail'] = "The parameter supports Chinese, English, digits, and keyboard symbols, with a maximum of 255 characters"
        return [400, PromptTemplateEditError1]

    # prompt_item_type_id
    if not isinstance(para["prompt_item_type_id"], str) or para["prompt_item_type_id"] == "":
        PromptTemplateEditError1['description'] = "prompt_item_type_id is invalid"
        PromptTemplateEditError1['detail'] = "The parameter must be a non-empty string"
        return [400, PromptTemplateEditError1]

    # prompt_item_id
    if not isinstance(para["prompt_item_id"], str) or para["prompt_item_id"] == "":
        PromptTemplateEditError1['description'] = "prompt_item_id is invalid"
        PromptTemplateEditError1['detail'] = "The parameter must be a non-empty string"
        return [400, PromptTemplateEditError1]

    if len(prompt_dao.check_item_and_item_type(para["prompt_item_id"], para["prompt_item_type_id"])) == 0:
        PromptTemplateEditError1['description'] = "The request parameters are invalid"
        PromptTemplateEditError1['detail'] = "prompt_item_id or prompt_item_type_id does not exist"
        return [400, PromptTemplateEditError1]


async def prompt_deploy_verify(model_para):
    if "prompt_id" not in model_para:
        PromptDeployError1['description'] = "The request parameters are invalid"
        PromptDeployError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptDeployError1]
    prompt_id_list = [ids["f_prompt_id"] for ids in prompt_dao.get_all_data_from_prompt_list()]
    info = prompt_dao.get_prompt_by_id(model_para["prompt_id"])
    if model_para["prompt_id"] not in prompt_id_list:
        PromptDeployError1['description'] = "The request parameters are invalid"
        PromptDeployError1['detail'] = "The prompt does not exist"
        return [400, PromptDeployError1]
    # if info[0].f_is_deploy is True:
    #     PromptDeployError1['description'] = "The request parameters are invalid"
    #     return [400, PromptDeployError1]


async def prompt_undeploy_verify(model_para):
    if "prompt_id" not in model_para:
        PromptUndeployError1['description'] = "The request parameters are invalid"
        PromptUndeployError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptUndeployError1]
    prompt_id_list = [ids["f_prompt_id"] for ids in prompt_dao.get_all_data_from_prompt_list()]
    info = prompt_dao.get_prompt_by_id(model_para["prompt_id"])
    if model_para["prompt_id"] not in prompt_id_list:
        PromptUndeployError1['description'] = "The request parameters are invalid"
        PromptUndeployError1['detail'] = "The prompt does not exist"
        return [400, PromptUndeployError1]
    if info[0]["f_is_deploy"] == 0:
        PromptDeployError1['description'] = "The request parameters are invalid"
        PromptDeployError1['detail'] = "The prompt is not published"
        return [400, PromptUndeployError1]


async def prompt_run_verify(para):
    if "model_id" not in para or "model_para" not in para:
        PromptRunError1['description'] = "The request parameters are invalid"
        PromptRunError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptRunError1]
    model_list = llm_model_dao.get_all_model_list()
    ids_list = [ids["f_model_id"] for ids in model_list]
    info2 = {}
    for model in model_list:
        if model["f_model_id"] == para["model_id"]:
            info2 = model
            break
    if para["model_id"] not in ids_list:
        PromptRunError1['description'] = "The request parameters are invalid"
        PromptRunError1['detail'] = "The selected large model does not exist"
        return [400, PromptRunError1]
    # if len(para["messages"].replace(' ', '')) == 0 or len(para["messages"]) > 5000:
    #     PromptRunError1['description'] = "messagesThe request parameters are invalid"
    #     return [400, PromptRunError1]

    for var in para["variables"]:  # variables are declared by the prompt; inputs are supplied by the caller.
        var_name = var['var_name']
        # Reject a missing required prompt variable.
        if not var['optional'] and var_name not in para["inputs"].keys():
            PromptRunError1['description'] = "Prompt variable input is invalid"
            PromptRunError1['detail'] = "Required field {} is missing".format(var_name)
            return [500, PromptRunError1]
        # Validate a supplied prompt variable.
        if var_name in para["inputs"].keys():
            var_value = para["inputs"][var_name]
            # Validate a text variable.
            if var['field_type'] == 'text':
                if type(var_value) is not str:
                    PromptRunError1['description'] = "Prompt variable input is invalid"
                    PromptRunError1['detail'] = f"Field {var_name} supports only string values"
                    return [500, PromptRunError1]
                if len(var_value) > var['max_len']:
                    PromptRunError1['description'] = "Prompt variable input is invalid"
                    PromptRunError1['detail'] = f"Field {var_name} has a maximum length of {var['max_len']}"
                    return [500, PromptRunError1]
            # Validate a numeric variable.
            if var['field_type'] == 'number':
                if var['value_type'] == 'i' and type(var_value) is not int:
                    PromptRunError1['description'] = "Prompt variable input is invalid"
                    PromptRunError1['detail'] = f"Field {var_name} supports only integers"
                    return [500, PromptRunError1]
                if var['value_type'] == 'f':
                    if type(var_value) is not float and type(var_value) is not int:
                        PromptRunError1['description'] = "Prompt variable input is invalid"
                        PromptRunError1['detail'] = f"Field {var_name} supports only floating-point numbers"
                        return [500, PromptRunError1]
                if var['range'][0] is not None:
                    if var_value < var['range'][0]:
                        PromptRunError1['description'] = "Prompt variable input is invalid"
                        PromptRunError1['detail'] = f"Field {var_name} must be in the range {var['range']}"
                        return [500, PromptRunError1]
                if var['range'][1] is not None:
                    if var_value > var['range'][1]:
                        PromptRunError1['description'] = "Prompt variable input is invalid"
                        PromptRunError1['detail'] = f"Field {var_name} must be in the range {var['range']}"
                        return [500, PromptRunError1]
            # Validate a selector variable.
            if var['field_type'] == 'selector':
                if var_value not in var['options']:
                    PromptRunError1['description'] = "Prompt variable input is invalid"
                    PromptRunError1['detail'] = f"Field {var_name} supports only {var['options']}"
                    return PromptUsed
    if type(para["history_dia"]) != list:
        PromptRunError1['description'] = "Conversation history input is invalid"
        PromptRunError1['detail'] = "Conversation history format is invalid"
        return [400, PromptRunError1]
    from app.utils.llm_utils import model_config, get_context_size
    if para["history_dia"]:
        from app.utils.llm_utils import get_context_size
        if info2["f_model_series"].lower() == "aishu":
            config = json.loads(info2["f_model_config"].replace("'", '"'))
            try:
                model_config.add_model_context_size(info2["f_model_id"], info2["f_model_name"], get_context_size(info2["f_model_series"], config["api_base"], config["api_model"]))
            except Exception:
                model_config.init_model_config()
                model_config.add_model_context_size(info2["f_model_id"], info2["f_model_name"], get_context_size(info2["f_model_series"], config["api_base"], config["api_model"]))
        if info2["f_model_series"].lower() in ["others", "claude"]:
            try:
                model_config.add_model_context_size(info2["f_model_id"], info2["f_model_name"], get_context_size("others", "", info2["f_model"]))
            except Exception:
                model_config.init_model_config()
                model_config.add_model_context_size(info2["f_model_id"], info2["f_model_name"], get_context_size("others", "", info2["f_model"]))
    if type(para["model_para"]) != dict:
        PromptAddError1['description'] = "model_paraThe request parameters are invalid"
        PromptAddError1['detail'] = "The parameter must be an object"
        return [400, PromptAddError1]
    if "temperature" not in para["model_para"] or not isinstance(para["model_para"]["temperature"],
                                                                 (int, float)) or isinstance(
        para["model_para"]["temperature"], Fraction) or para["model_para"]["temperature"] > 2 or para["model_para"][
        "temperature"] < 0:
        PromptAddError1['description'] = "temperatureThe request parameters are invalid"
        PromptAddError1['detail'] = "The parameter must be a number from 0 to 2"
        return [400, PromptAddError1]
    if "top_p" not in para["model_para"] or not isinstance(para["model_para"]["top_p"], (int, float)) or isinstance(
            para["model_para"]["top_p"], Fraction) or para["model_para"]["top_p"] > 1 or para["model_para"][
        "top_p"] < 0:
        PromptAddError1['description'] = "top_pThe request parameters are invalid"
        PromptAddError1['detail'] = "The parameter must be a number from 0 to 1"
        return [400, PromptAddError1]
    if "presence_penalty" not in para["model_para"] or not isinstance(para["model_para"]["presence_penalty"],
                                                                      (int, float)) or isinstance(
        para["model_para"]["presence_penalty"], Fraction) or para["model_para"]["presence_penalty"] > 2 or \
            para["model_para"]["presence_penalty"] < -2:
        PromptAddError1['description'] = "presence_penaltyThe request parameters are invalid"
        PromptAddError1['detail'] = "The parameter must be a number from -2 to 2"
        return [400, PromptAddError1]
    if "frequency_penalty" not in para["model_para"] or not isinstance(para["model_para"]["frequency_penalty"],
                                                                       (int, float)) or isinstance(
        para["model_para"]["frequency_penalty"], Fraction) or para["model_para"]["frequency_penalty"] > 2 or \
            para["model_para"]["frequency_penalty"] < -2:
        PromptAddError1['description'] = "frequency_penaltyThe request parameters are invalid"
        PromptAddError1['detail'] = "The parameter must be a number from -2 to 2"
        return [400, PromptAddError1]

    if "max_tokens" not in para["model_para"] or not isinstance(para["model_para"]["frequency_penalty"],
                                                                       (int, float)) or isinstance(
        para["model_para"]["frequency_penalty"], Fraction) or para["model_para"]["frequency_penalty"] > 2 or \
            para["model_para"]["frequency_penalty"] < -2:
        PromptAddError1['description'] = "frequency_penaltyThe request parameters are invalid"
        PromptAddError1['detail'] = "The parameter must be a number from -2 to 2"
        return [400, PromptAddError1]
    model = info2["f_model"]
    if model == 'gpt-35-turbo-16k':
        if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                para["model_para"][
                    "max_tokens"] > 16384 or para["model_para"]["max_tokens"] < 10:
            PromptAddError1['description'] = "max_tokensThe request parameters are invalid"
            PromptAddError1['detail'] = "This model supports an integer max_tokens value from 10 to 16384"
            return [400, PromptAddError1]
    if model == 'text-davinci-002':
        if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                para["model_para"][
                    "max_tokens"] > 4097 or para["model_para"]["max_tokens"] < 10:
            PromptAddError1['description'] = "max_tokensThe request parameters are invalid"
            PromptAddError1['detail'] = "This model supports an integer max_tokens value from 10 to 4097"
            return [400, PromptAddError1]
    if model == 'baichuan2':
        if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                para["model_para"][
                    "max_tokens"] > 4096 or para["model_para"]["max_tokens"] < 10:
            PromptAddError1['description'] = "max_tokensThe request parameters are invalid"
            PromptAddError1['detail'] = "This model supports an integer max_tokens value from 10 to 4096"
            return [400, PromptAddError1]


def prompt_template_run_verify(para):
    from app.utils.llm_utils import model_config, get_context_size
    if "model_name" not in para or "model_para" not in para or "prompt_id" not in para:
        PromptTemplateRunError1['description'] = "The request parameters are invalid"
        PromptTemplateRunError1['detail'] = "The request contains invalid parameter names or an invalid parameter count"
        return [400, PromptTemplateRunError1]
    prompt_id = para["prompt_id"]
    if not isinstance(prompt_id, str) or prompt_id == "":
        PromptTemplateRunError1['description'] = "The request parameters are invalid"
        PromptTemplateRunError1['detail'] = "The prompt ID must be a non-empty string"
        return [400, PromptTemplateRunError1]

    prompt = prompt_dao.get_prompt_by_id(para["prompt_id"])
    if len(prompt) == 0:
        PromptTemplateRunError1['description'] = "The request parameters are invalid"
        PromptTemplateRunError1['detail'] = "The prompt ID does not exist"
        return [400, PromptTemplateRunError1]

    model_list = llm_model_dao.get_all_model_list()
    info2 = {}
    for model in model_list:
        if model["f_model_name"] == para["model_name"]:
            info2 = model
            break

    if type(para["history_dia"]) != list:
        PromptTemplateRunError1['description'] = "Conversation history input is invalid"
        PromptTemplateRunError1['detail'] = "Conversation history format is invalid"
        return [400, PromptTemplateRunError1]
    if "context_size" not in model_config.get_model_config("id", model["f_model_id"]).keys():
        config = json.loads(model["f_model_config"].replace("'", '"'))
        if "api_url" in config.keys():
            config["api_base"] = config["api_url"]
        try:
            try:
                context_size = get_context_size(model["f_model_series"], config["api_base"], config["api_model"])
            except func_timeout.exceptions.FunctionTimedOut:
                context_size = 4096
        except Exception as e:
            context_size = 4096
        try:
            model_config.add_model_context_size(model["f_model_id"], model["f_model_name"], context_size)
        except Exception:
            model_config.init_model_config()
            model_config.add_model_context_size(model["f_model_id"], model["f_model_name"], context_size)
    max_tokens = model_config.get_model_config("name", para["model_name"])["context_size"]
    if para["history_dia"]:

        history_str = ''
        for cell in para["history_dia"]:
            history_str += cell['message']
        if len(history_str) > max_tokens:
            PromptTemplateRunError1['description'] = "Conversation history exceeds the length limit"
            PromptTemplateRunError1['detail'] = "Conversation history must not exceed {} characters".format(max_tokens)
            return [400, PromptTemplateRunError1]
    if type(para["model_para"]) != dict:
        PromptTemplateRunError1['description'] = "model_paraThe request parameters are invalid"
        PromptTemplateRunError1['detail'] = "The parameter must be an object"
        return [400, PromptTemplateRunError1]
    if "temperature" not in para["model_para"] or not isinstance(para["model_para"]["temperature"],
                                                                 (int, float)) or isinstance(
        para["model_para"]["temperature"], Fraction) or para["model_para"]["temperature"] > 2 or para["model_para"][
        "temperature"] < 0:
        PromptTemplateRunError1['description'] = "temperatureThe request parameters are invalid"
        PromptTemplateRunError1['detail'] = "The parameter must be a number from 0 to 2"
        return [400, PromptTemplateRunError1]
    if "top_p" not in para["model_para"] or not isinstance(para["model_para"]["top_p"], (int, float)) or isinstance(
            para["model_para"]["top_p"], Fraction) or para["model_para"]["top_p"] > 1 or para["model_para"][
        "top_p"] < 0:
        PromptTemplateRunError1['description'] = "top_pThe request parameters are invalid"
        PromptTemplateRunError1['detail'] = "The parameter must be a number from 0 to 1"
        return [400, PromptTemplateRunError1]
    if "top_k" in para["model_para"] and (not isinstance(para["model_para"]["top_k"], int) or isinstance(
            para["model_para"]["top_p"], Fraction) or para["model_para"]["top_k"] < 1):
        PromptTemplateRunError1['description'] = "top_kThe request parameters are invalid"
        PromptTemplateRunError1['detail'] = "The parameter must be an integer greater than or equal to 1"
        return [400, PromptTemplateRunError1]
    if "presence_penalty" not in para["model_para"] or not isinstance(para["model_para"]["presence_penalty"],
                                                                      (int, float)) or isinstance(
        para["model_para"]["presence_penalty"], Fraction) or para["model_para"]["presence_penalty"] > 2 or \
            para["model_para"]["presence_penalty"] < -2:
        PromptTemplateRunError1['description'] = "presence_penaltyThe request parameters are invalid"
        PromptTemplateRunError1['detail'] = "The parameter must be a number from -2 to 2"
        return [400, PromptTemplateRunError1]
    if "frequency_penalty" not in para["model_para"] or not isinstance(para["model_para"]["frequency_penalty"],
                                                                       (int, float)) or isinstance(
        para["model_para"]["frequency_penalty"], Fraction) or para["model_para"]["frequency_penalty"] > 2 or \
            para["model_para"]["frequency_penalty"] < -2:
        PromptTemplateRunError1['description'] = "frequency_penaltyThe request parameters are invalid"
        PromptTemplateRunError1['detail'] = "The parameter must be a number from -2 to 2"
        return [400, PromptTemplateRunError1]

    model = info2.get("f_model", "")
    if model == 'gpt-35-turbo-16k':
        if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                para["model_para"][
                    "max_tokens"] > 16384 or para["model_para"]["max_tokens"] < 10:
            PromptAddError1['description'] = "max_tokensThe request parameters are invalid"
            PromptAddError1['detail'] = "This model supports an integer max_tokens value from 10 to 16384"
            return [400, PromptAddError1]
    else:
        if "max_tokens" not in para["model_para"] or type(para["model_para"]["max_tokens"]) != int or \
                para["model_para"][
                    "max_tokens"] > max_tokens or para["model_para"]["max_tokens"] < 10:
            PromptAddError1['description'] = "max_tokensThe request parameters are invalid"
            PromptAddError1['detail'] = "This model supports an integer max_tokens value from 10 to " + str(max_tokens) + ""
            return [400, PromptAddError1]


async def completion_prompt_verify(prompt_id, inputs):
    prompt_id_list = [cell["f_prompt_id"] for cell in prompt_dao.get_all_data_from_prompt_list()]
    if prompt_id not in prompt_id_list:
        PromptConpletionError1['description'] = "prompt_id is invalid"
        PromptConpletionError1['detail'] = "prompt_id must exist in the database"
        return PromptConpletionError1
    info = prompt_dao.get_prompt_by_id(prompt_id)
    messages = info[0]["f_messages"]
    var = re.findall(r'\{\{(.*?)\}\}', messages)  # Variables declared in the prompt.
    inputs_key = inputs.keys()
    if set(var) != set(inputs_key):
        PromptConpletionError1['description'] = "Prompt variable input is invalid"
        PromptConpletionError1['detail'] = "The input variables do not match the prompt variables"
        return PromptConpletionError1


async def prompt_code_verify(model_id, prompt_id):
    ids_list = [ids["f_model_id"] for ids in llm_model_dao.get_all_model_list()]
    is_delete = llm_model_dao.get_data_from_model_list_by_id(model_id)
    if model_id not in ids_list or len(model_id.replace(' ', '')) == 0:
        PromptCodeError1['description'] = "The request parameters are invalid"
        PromptCodeError1['detail'] = "The selected large model does not exist"
        return PromptCodeError1
    if len(prompt_id.replace(' ', '')) != 0:
        prompt_id_list = [ids["f_prompt_id"] for ids in prompt_dao.get_all_data_from_prompt_list()]
        info = prompt_dao.get_prompt_by_id(prompt_id)
        if prompt_id not in prompt_id_list:
            PromptCodeError1['description'] = "The request parameters are invalid"
            PromptCodeError1['detail'] = "The prompt does not exist"
            return PromptCodeError1


async def prompt_delete_verify(delete_id):
    if len(delete_id.items()) != 1:
        PromptDeleteError1['description'] = "The request parameters are invalid"
        PromptDeleteError1['detail'] = "Exactly one selector parameter is supported"
        return [500, PromptDeleteError1]
    key_list = list(delete_id.keys())
    if not re.search(r'^(prompt_id|type_id|item_id|prompt_id_list)$', key_list[0]):
        PromptDeleteError1['description'] = "The request parameters are invalid"
        PromptDeleteError1['detail'] = "The request supports prompt_id, type_id, item_id, or prompt_id_list"
        return [500, PromptDeleteError1]
    if "prompt_id" in delete_id:
        prompt_id_list = [cell["f_prompt_id"] for cell in prompt_dao.get_all_data_from_prompt_list()]
        if delete_id["prompt_id"] not in prompt_id_list:
            PromptDeleteError1['description'] = "prompt_id is invalid"
            PromptDeleteError1['detail'] = "The prompt does not exist"
            return [500, PromptDeleteError1]
    if "type_id" in delete_id:
        prompt_id_list = [cell["f_prompt_item_type_id"] for cell in prompt_dao.get_all_from_prompt_item_list(None)]
        if delete_id["type_id"] not in prompt_id_list:
            PromptDeleteError1['description'] = "type_id is invalid"
            PromptDeleteError1['detail'] = "The prompt group does not exist"
            return [500, PromptDeleteError1]
    if "item_id" in delete_id:
        prompt_id_list = [cell["f_prompt_item_id"] for cell in prompt_dao.get_all_from_prompt_item_list(None)]
        if delete_id["item_id"] not in prompt_id_list:
            PromptDeleteError1['description'] = "item_id is invalid"
            PromptDeleteError1['detail'] = "The prompt project does not exist"
            return [500, PromptDeleteError1]
    if "prompt_id_list" in delete_id:
        if len(delete_id["prompt_id_list"]) == 0:
            PromptDeleteError1['description'] = "prompt_id_list is invalid"
            PromptDeleteError1['detail'] = "The list cannot be empty"
            return [500, PromptDeleteError1]


# Validate prompt move parameters.
async def prompt_move_verify(move_param):
    key_list = list(move_param.keys())
    if 'prompt_id' not in key_list:
        PromptMoveError['description'] = "prompt_id is invalid"
        PromptMoveError['detail'] = "prompt_id is required"
        return [400, PromptMoveError]
    if 'prompt_item_id' not in key_list:
        PromptMoveError['description'] = "prompt_item_id is invalid"
        PromptMoveError['detail'] = "prompt_item_id is required"
        return [400, PromptMoveError]
    if 'prompt_item_type_id' not in key_list:
        PromptMoveError['description'] = "prompt_item_type_id is invalid"
        PromptMoveError['detail'] = "prompt_item_type_id is required"
        return [400, PromptMoveError]
    prompt_id_list = [cell["f_prompt_id"] for cell in prompt_dao.get_all_data_from_prompt_list()]
    if move_param["prompt_id"] not in prompt_id_list:
        PromptMoveError['description'] = "prompt_id is invalid"
        PromptMoveError['detail'] = "The prompt does not exist"
        return [400, PromptMoveError]
    prompt_id_list = [cell["f_prompt_item_type_id"] for cell in prompt_dao.get_all_from_prompt_item_list(None)]
    if move_param["prompt_item_type_id"] not in prompt_id_list:
        PromptMoveError['description'] = "prompt_item_type_id is invalid"
        PromptMoveError['detail'] = "The prompt group does not exist"
        return [400, PromptMoveError]
    prompt_id_list = [cell["f_prompt_item_id"] for cell in prompt_dao.get_all_from_prompt_item_list(None)]
    if move_param["prompt_item_id"] not in prompt_id_list:
        PromptMoveError['description'] = "prompt_item_id is invalid"
        PromptMoveError['detail'] = "The prompt project does not exist"
        return [400, PromptMoveError]


async def batch_add_prompt_endpoint_verify(params_list):
    if not isinstance(params_list, list):
        BatchAddPromptError['description'] = "The parameter must be a list"
        BatchAddPromptError['detail'] = "The parameter must be a list"
        return [400, BatchAddPromptError]

    for params in params_list:
        if "prompt_item_name" not in params:
            BatchAddPromptError['description'] = "prompt_item_name is required"
            BatchAddPromptError['detail'] = "prompt_item_name is required"
            return [400, BatchAddPromptError]

        if "prompt_item_type_name" not in params:
            BatchAddPromptError['description'] = "prompt_item_type_name is required"
            BatchAddPromptError['detail'] = "prompt_item_type_name is required"
            return [400, BatchAddPromptError]

        if "prompt_list" not in params:
            BatchAddPromptError['description'] = "prompt_list is required"
            BatchAddPromptError['detail'] = "prompt_list is required"
            return [400, BatchAddPromptError]

        if not isinstance(params["prompt_list"], list):
            BatchAddPromptError['description'] = "prompt_list must be a list"
            BatchAddPromptError['detail'] = "prompt_list must be a list"
            return [400, BatchAddPromptError]


def verify_icon_color_config(s):
    valid_colors = [
        "icon-color-pz-019688",
        "icon-color-pz-F759AB",
        "icon-color-pz-FADB14",
        "icon-color-pz-FF8501",
        "icon-color-pz-F75959",
        "icon-color-pz-8C8C8C",
        "icon-color-pz-126EE3",
        "icon-color-pz-13C2C2",
        "icon-color-pz-52C41A",
        "icon-color-pz-9254DE"
    ]
    if s in valid_colors:
        return True
    else:
        return False

def verify_icon_color_config_metric(s):
    valid_colors = [
        "icon-color-zbk-FF8501",
        "icon-color-zbk-13C2C2",
        "icon-color-zbk-FADB14",
        "icon-color-zbk-019688",
        "icon-color-zbk-9254DE",
        "icon-color-zbk-8C8C8C",
        "icon-color-zbk-126EE3",
        "icon-color-zbk-52C41A",
        "icon-color-zbk-F759AB",
        "icon-color-zbk-F75959"
    ]
    if s in valid_colors:
        return True
    else:
        return False

def verify_text_field(s, max_len):
    if not isinstance(s, str):
        return False
    if len(s) > max_len:
        return False
    if not re.match(r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！·￥…（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]*$', s):
        return False
    return True

def verify_id(s):
    if not isinstance(s, str):
        return False
    if not re.match(r'^[0-9]{18}$', s) and not re.match(r'^[0-9]{19}$', s):
        return False
    return True

def include_dataset_id(dataset_version_id_list, dataset_id):
    try:
        for item in dataset_version_id_list:
            if item.split("/")[0] == dataset_id:
                return True
        return False
    except Exception:
        return False
