from fastapi import APIRouter, Body, Header
from typing import Optional, List, Dict, Union
from pydantic import StrictFloat, StrictStr, Field, StrictInt, conint, validator, confloat, conlist, constr, \
    root_validator
from fastapi.exceptions import RequestValidationError
from pydantic import BaseModel


class ModelConf(BaseModel):
    api_model: StrictStr = Field(description="api model")
    api_base: StrictStr = Field(description="api base")
    api_key: StrictStr = Field(description="api key")


class AddModelUsedAudit(BaseModel):
    model_id: StrictStr = Field(description="Model ID", default="")
    user_id: StrictStr = Field(description="User ID", default="")
    input_tokens: StrictInt = Field(description="Input token count", default=0)
    output_tokens: StrictInt = Field(description="Output token count", default=0)
    resourece_type: StrictStr = Field(description="Resource type", default="unknown")
    func_module: StrictStr = Field(description="Calling module", default="unknown")
    total_time: StrictFloat = Field(description="Total invocation time", default=0.0)
    first_time: StrictFloat = Field(description="Time to first token", default=0.0)
    status: StrictStr = Field(description="Invocation status", default="failed")

class ConfIdList(BaseModel):
    conf_id_list: List


class ModelIdList(BaseModel):
    model_id_list: conlist(constr(min_length=19, max_length=19))


class Message(BaseModel):
    role: StrictStr = Field(description="", regex=r'^(user|assistant|system|tool)$')
    # OpenAI permits null content for assistant messages that contain tool_calls.
    content: Optional[Union[StrictStr, List[Dict[str, Union[str, Dict[str, str]]]]]] = Field(default=None)
    tool_calls: List[dict] = Field(default=None)
    tool_call_id: StrictStr = Field(default=None)

    @validator('content', pre=True, always=True, check_fields=False)
    def coerce_null_content(cls, v):
        """Normalize null to an empty string for downstream concatenation and metering.

        Normalizing at validation covers every route and read path. A previous
        controller-only fix missed the messages[-1]["content"] snapshot and
        allowed a TypeError to surface as a 500 response.
        """
        return "" if v is None else v

    @validator('content', check_fields=False)
    def validate_content(cls, v):
        if isinstance(v, list):
            image_url_count = 0
            video_url_count = 0
            for item in v:
                if not isinstance(item, dict):
                    raise ValueError("List items must be dictionaries")
                if 'type' not in item:
                    raise ValueError("Each item must have a 'type' field")

                if item['type'] == 'text' and 'text' not in item:
                    raise ValueError("Text items must have 'text' field")

                if item['type'] == 'image_url':
                    if 'image_url' not in item:
                        raise ValueError("Image items must have 'image_url' field")
                    image_url_count += 1
                    if image_url_count > 5:
                        raise ValueError("Maximum 5 image_url items allowed")

                if item['type'] == 'video_url':
                    if 'video_url' not in item:
                        raise ValueError("Video items must have 'video_url' field")
                    video_url_count += 1
                    if video_url_count > 1:
                        raise ValueError("Maximum 1 video_url item allowed")
        return v


class LLMToolsFunctionParameters(BaseModel):
    type: StrictStr
    properties: Dict
    required: List[str] = Field(default=[])


class LLMToolsFunction(BaseModel):
    name: StrictStr
    description: StrictStr = Field(default="")
    parameters: LLMToolsFunctionParameters


class LLMTool(BaseModel):
    type: StrictStr
    function: LLMToolsFunction


class LLMUsedOpenAI(BaseModel):
    model: Optional[StrictStr] = Field(description="",
                             regex=r'^[=~!@#$&%^*()_+`{}\-\[\];:,.\\?<>\'"|/！￥…·（）—。【 】‘“’”：；、《》？，a-zA-Z0-9\u4e00-\u9fa5]{,50}$')
    top_p: confloat(gt=0, le=1) = Field(default=1)
    top_k: conint(ge=1) = Field(default=1)
    temperature: confloat(ge=0, le=2) = Field(default=1)
    presence_penalty: confloat(ge=-2, le=2) = Field(default=0)
    frequency_penalty: confloat(ge=-2, le=2) = Field(default=0)
    max_tokens: conint(ge=10) = Field(default=1024)
    # OpenAI deprecated max_tokens in favor of max_completion_tokens. New SDKs
    # and langchain-openai >=0.2 send only the new field. Map it here so Pydantic
    # does not discard the caller's output limit and silently use 1024.
    max_completion_tokens: Optional[conint(ge=10)] = Field(default=None)
    messages: List[Message]
    response_format: Dict = Field(default={})
    stream: bool = Field(default=False)
    cache: bool = Field(default=False)
    stop: Union[str, List[str]] = Field(default=None)
    system: List[dict] = Field(default=[])
    tools: List[LLMTool] = Field(default=None)
    tool_choice: Union[str, dict] = Field(default=None)
    model_id: Optional[StrictStr] = Field(description="", default="")

    @root_validator(skip_on_failure=True)
    def map_max_completion_tokens(cls, values):
        # An explicit max_completion_tokens value takes precedence. Downstream
        # providers read max_tokens, so one mapping covers every provider branch.
        mct = values.get("max_completion_tokens")
        if mct:
            values["max_tokens"] = mct
        return values

    @validator('stream', pre=True)
    def check_stream(cls, v, values):
        if not v is True and not v is False:
            raise ValueError("stream must be a Boolean value of true or false")
        return v

    @validator('cache', pre=True)
    def check_cache(cls, v, values):
        if not v is True and not v is False:
            raise ValueError("cache must be a Boolean value of true or false")
        return v

    @validator('max_tokens', pre=True)
    def check_max_tokens(cls, v, values):
        if not isinstance(v, int) or v < 10:
            raise ValueError("max_tokens must be an integer and greater than or equal to 10")
        return v

    @validator('top_p', pre=True)
    def check_top_p(cls, v, values):
        if v <= 0 or v > 1:
            raise ValueError("top_p must be an float and the range of values is 0 < top_p ≤ 1")
        return v

    @validator('top_k', pre=True)
    def validate_top_k(cls, v):
        if not isinstance(v, int) or v < 1:
            raise ValueError("top_k must be an integer and greater than or equal to 1")
        return v

    @validator('presence_penalty', pre=True)
    def check_presence_penalty(cls, v, values):
        if v < -2 or v > 2:
            raise ValueError("presence_penalty must be an float and the range of values is -2 ≤ presence_penalty ≤ 2")
        return v

    @validator('frequency_penalty', pre=True)
    def check_frequency_penalty(cls, v, values):
        if v < -2 or v > 2:
            raise ValueError("frequency_penalty must be an float and the range of values is -2 ≤ frequency_penalty ≤ 2")
        return v

    @validator('temperature', pre=True)
    def check_temperature(cls, v, values):
        if v < 0 or v > 2:
            raise ValueError("temperature must be an float and the range of values is 0 ≤ temperature ≤ 2")
        return v


class ModelPara(BaseModel):
    top_p: confloat(ge=0, le=1)
    top_k: conint(ge=1) = Field(default=1)
    temperature: confloat(ge=0, le=2)
    presence_penalty: confloat(ge=-2, le=2)
    frequency_penalty: confloat(ge=-2, le=2)
    max_tokens: conint(ge=10)


class PromptRunPara(BaseModel):
    model_id: constr(min_length=19, max_length=19)
    model_para: ModelPara
    messages: constr()
    inputs: Dict
    variables: List
    history_dia: List
    type: constr()


# Request body for adding an external small model.
class AddExternalSmallModel(BaseModel):
    model_name: StrictStr = Field(description="Model name")
    model_type: StrictStr = Field(description="Model type", regex=r'^(reranker|embedding)$')
    model_config: Optional[dict] = Field(default={}, description="Third-party model service configuration")
    adapter: Optional[bool] = Field(default=False, description="Whether to enable the adapter service")
    adapter_code: Optional[StrictStr] = Field(default=None, description="Adapter code")

    @root_validator
    def validate_mutually_exclusive_groups(cls, values):
        model_config = values.get('model_config', {})
        adapter = values.get('adapter', False)
        adapter_code = values.get('adapter_code')

        if model_config and (adapter or adapter_code):
            raise ValueError("model_config and adapter/adapter_code cannot be set together")
        if not model_config and not (adapter or adapter_code):
            raise ValueError("set either model_config or adapter/adapter_code")
        return values

    @validator('model_config', pre=False)
    def check_output_tokens(cls, v, values):
        key_list = ["api_url", "api_model"]
        for k in key_list:
            if k not in v.keys():
                raise RequestValidationError([{"loc": ('body', k), "type": "value_error.missing"}])
        return v

    @validator('adapter_code', pre=False)
    def check_adapter_code(cls, v, values):
        if 'adapter' in values and values['adapter'] is True and not v:
            raise RequestValidationError([{"loc": ('body', "adapter_code"), "type": "value_error.missing"}])
        return v


class TestSmallModel(BaseModel):
    model_id: Optional[StrictStr] = Field(None, description="Configuration ID", min_length=19, max_length=19)
    model_name: Optional[StrictStr] = Field(default="", description="Model name")
    model_type: Optional[StrictStr] = Field(default="", description="Model type", regex=r'^(reranker|embedding)$')
    model_config: Optional[dict] = Field(default={}, description="Third-party model service configuration")
    adapter: Optional[bool] = Field(default=False, description="Whether to enable the adapter service")
    adapter_code: Optional[StrictStr] = Field(default=None, description="Adapter code")

    @root_validator
    def check_fields(cls, values):
        model_id = values.get('model_id')
        if model_id is None:
            model_config = values.get('model_config', {})
            adapter = values.get('adapter', False)
            adapter_code = values.get('adapter_code')
            if model_config and (adapter or adapter_code):
                raise ValueError("model_config and adapter/adapter_code cannot be set together")
            if not model_config and not (adapter or adapter_code):
                raise ValueError("set either model_config or adapter/adapter_code")
            # Without model_id, the identifying model fields are required.
            required_fields = ['model_name', 'model_type']
            for field in required_fields:
                if values.get(field) is None:
                    raise RequestValidationError([{"loc": ('body', field), "type": "value_error.missing"}])
        else:
            # With model_id, the other model fields may be omitted.
            pass
        return values

    @validator('model_config', pre=False)
    def check_output_tokens(cls, v, values):
        key_list = ["api_url", "api_model"]
        for k in key_list:
            if k not in v.keys():
                raise RequestValidationError([{"loc": ('body', k), "type": "value_error.missing"}])
        return v


class EditExternalSmallModel(BaseModel):
    model_id: StrictStr = Field(description="Configuration ID", min_length=19, max_length=19)
    model_name: StrictStr = Field(description="Model name")
    model_type: StrictStr = Field(description="Model type", regex=r'^(reranker|embedding)$')
    model_config: Optional[dict] = Field(default={}, description="Third-party model service configuration")
    adapter: Optional[bool] = Field(default=False, description="Whether to enable the adapter service")
    adapter_code: Optional[StrictStr] = Field(default=None, description="Adapter code")

    @root_validator
    def validate_mutually_exclusive_groups(cls, values):
        model_config = values.get('model_config', {})
        adapter = values.get('adapter', False)
        adapter_code = values.get('adapter_code')

        if model_config and (adapter or adapter_code):
            raise ValueError("model_config and adapter/adapter_code cannot be set together")
        if not model_config and not (adapter or adapter_code):
            raise ValueError("set either model_config or adapter/adapter_code")
        return values

    @validator('model_config', pre=False)
    def check_output_tokens(cls, v, values):
        key_list = ["api_url", "api_model"]
        for k in key_list:
            if k not in v.keys():
                raise RequestValidationError([{"loc": ('body', k), "type": "value_error.missing"}])
        return v


class UsedReranker(BaseModel):
    model: Optional[StrictStr] = Field(description="Model name")
    query: StrictStr = Field(description="Query to rank against")
    documents: list = Field(description="Documents to rank")
    model_id: Optional[StrictStr] = Field(description="Model ID", default="")


class UsedEmbedding(BaseModel):
    model: Optional[StrictStr] = Field(description="Model name")
    input: list = Field(description="Content to embed")
    model_id: Optional[StrictStr] = Field(description="Model ID", default="")

class AuthInfo(BaseModel):
    userid: Optional[str]
    appid: Optional[str]
    authorization: Optional[str]
    token: Optional[str]


def get_auth(userid: Optional[str] = Header(None),
             token: Optional[str] = Header(None), appid: Optional[str] = Header(None),
             authorization: Optional[str] = Header(None)):
    return AuthInfo(userid=userid, token=token, appid=appid, authorization=authorization)


class LLMGenerateReq(BaseModel):
    # model_name: str = Body("",description="")
    input: Optional[str] = Body(...)
    model_name: Optional[str] = Body(...)
    type: Optional[str] = Body(...)
    retry: Optional[bool] = Body(False)
