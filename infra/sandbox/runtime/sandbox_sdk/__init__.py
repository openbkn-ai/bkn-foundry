"""
sandbox_sdk —— 沙箱侧函数 SDK

让用户只写一个带类型注解的普通 Python 函数，不用写 AWS Lambda 的 handler(event)：

    from sandbox_sdk import tool

    @tool
    def add(a: int, b: int) -> int:
        "两数相加"
        return a + b

平台在沙箱里执行时（wrapper 自动生成，用户看不到）：
    1. 用户代码 import 本包（本包【预装进沙箱模板镜像】，不是 pip 每次装）
    2. @tool 登记函数，并从「签名 + 类型注解 + docstring」推导参数 schema
    3. event（业务入参，从 stdin 灌入的 JSON）由本包解包成形参，喂给用户函数
    4. 用户函数返回值 → print(json.dumps(...)) 打到 stdout 末行 → 平台按「末行 JSON」提取

【与真实沙箱契约的对齐点】（都已核对 infra/sandbox 源码）
    - event 来源：沙箱 wrapper 用 `event = json.loads(sys.stdin.read())`，本包 dispatch(event) 接收
    - result 交回：沙箱 subprocess 取 stdout 最后一个合法 JSON 行作为 return_value，
      所以 wrapper 末尾 `print(json.dumps(result))` 即可，本包只负责算出 result
    - context：沙箱把 task_id/user_id 等塞进【子进程环境变量】(env_vars)，
      本包按需从 os.environ 组装 Context，仅当用户函数签名里声明了 `ctx: Context` 才注入

【归属】本包属于沙箱模板镜像 (infra/sandbox)，不属于执行工厂 operator-integration。
"""

from __future__ import annotations

import inspect
import os
import typing
from dataclasses import dataclass, field
from typing import Any, Callable, Optional

try:                                    # py3.8+
    from typing import get_args, get_origin
except ImportError:                     # py3.7 兜底（本地验证用；沙箱是 3.11）
    def get_origin(tp):
        return getattr(tp, "__origin__", None)

    def get_args(tp):
        return getattr(tp, "__args__", ())


# 联合类型的 origin：typing.Union 来自 Optional[X]/Union[...]，
# types.UnionType 来自 PEP 604 的 X | None（Python 3.10+）。
_UNION_ORIGINS = {typing.Union}
try:
    import types as _types
    _UNION_ORIGINS.add(_types.UnionType)   # py3.10+
except AttributeError:
    pass

# pydantic 可选：沙箱镜像已预装。有则复杂/嵌套参数走它，无则纯 inspect 兜底。
try:
    from pydantic import BaseModel
    _HAS_PYDANTIC = True
except ImportError:
    BaseModel = None
    _HAS_PYDANTIC = False


def _is_pydantic_model(anno) -> bool:
    return _HAS_PYDANTIC and isinstance(anno, type) and issubclass(anno, BaseModel)


__all__ = ["tool", "Context", "dispatch", "export_schema", "run"]


# ==================================================================== #
# 1. Context —— 平台上下文（按需注入，数据来自子进程环境变量）
# ==================================================================== #

@dataclass
class Context:
    """
    平台注入的运行时上下文，对齐 AWS Lambda 的 context 参数。
    数据来自沙箱塞进环境变量的 env_vars（source/task_id/user_id 等）。

    仅当用户函数签名里声明了 `ctx: Context` 类型参数时，SDK 才组装并注入。
    不声明就不注入 —— 纯计算函数零负担。
    """
    task_id: str = ""
    user_id: str = ""
    user_name: str = ""
    capability_id: str = ""
    function_version_id: str = ""
    source: str = ""

    @classmethod
    def from_env(cls) -> "Context":
        """从环境变量组装（沙箱把 env_vars 塞进了 os.environ）。"""
        return cls(
            task_id=os.environ.get("task_id", ""),
            user_id=os.environ.get("user_id", ""),
            user_name=os.environ.get("user_name", ""),
            capability_id=os.environ.get("capability_id", ""),
            function_version_id=os.environ.get("function_version_id", ""),
            source=os.environ.get("source", ""),
        )


# ==================================================================== #
# 2. 参数 schema —— 对齐执行工厂 ParameterDef 的五种类型
#    string / number / boolean / array / object
# ==================================================================== #

_PY_TO_PARAM = {
    str: "string",
    int: "number",
    float: "number",
    bool: "boolean",
    list: "array",
    dict: "object",
}


@dataclass
class ParamDef:
    """对齐后端 interfaces.ParameterDef（字段子集，够 schema 用）。"""
    name: str
    type: str
    description: str = ""
    required: bool = True
    default: Any = None
    sub_parameters: list = field(default_factory=list)  # list[ParamDef]

    def to_dict(self) -> dict:
        d: dict = {"name": self.name, "type": self.type, "required": self.required}
        if self.description:
            d["description"] = self.description
        if self.default is not None:
            d["default"] = self.default
        if self.sub_parameters:
            d["sub_parameters"] = [p.to_dict() for p in self.sub_parameters]
        return d


# JSON Schema type -> 我们的五类型
_JSONSCHEMA_TO_PARAM = {
    "string": "string", "integer": "number", "number": "number",
    "boolean": "boolean", "array": "array", "object": "object",
}


def _jsonschema_prop_to_param(name: str, prop: dict, required: bool) -> ParamDef:
    """把 pydantic model_json_schema() 里的一个 property 转成 ParamDef（递归）。"""
    # pydantic v2 把 Optional[X] 表示成 anyOf: [X, null]，没有顶层 type，
    # 不展开的话所有可选字段都会兜底成 string。
    if "type" not in prop and isinstance(prop.get("anyOf"), list):
        for variant in prop["anyOf"]:
            if isinstance(variant, dict) and variant.get("type") not in (None, "null"):
                merged = dict(variant)
                for k in ("description", "default", "title"):
                    if k in prop:
                        merged.setdefault(k, prop[k])
                return _jsonschema_prop_to_param(name, merged, required)
    js_type = prop.get("type", "string")
    ptype = _JSONSCHEMA_TO_PARAM.get(js_type, "string")
    p = ParamDef(name=name, type=ptype, required=required,
                 description=prop.get("description", ""),
                 default=prop.get("default"))
    # object：展开子字段
    if js_type == "object" and "properties" in prop:
        req = set(prop.get("required", []))
        p.sub_parameters = [
            _jsonschema_prop_to_param(k, v, k in req)
            for k, v in prop["properties"].items()
        ]
    # array：展开元素结构
    elif js_type == "array" and isinstance(prop.get("items"), dict):
        p.sub_parameters = [_jsonschema_prop_to_param("items", prop["items"], True)]
    return p


def _pydantic_to_param(name: str, model, required: bool,
                       default: Any = None, description: str = "") -> ParamDef:
    """pydantic BaseModel -> object 类型 ParamDef，字段进 sub_parameters。"""
    js = model.model_json_schema()
    # pydantic 把嵌套模型放 $defs，这里内联展开
    js = _inline_defs(js)
    req = set(js.get("required", []))
    subs = [_jsonschema_prop_to_param(k, v, k in req)
            for k, v in js.get("properties", {}).items()]
    return ParamDef(name=name, type="object", required=required,
                    default=default, description=description or js.get("description", ""),
                    sub_parameters=subs)


def _inline_defs(schema: dict) -> dict:
    """
    把 pydantic $defs 里的 $ref 内联进来，便于递归展开（简化版）。

    自引用模型（树节点、链表之类）的 $ref 会指回正在展开的定义，
    因此记录展开路径上的 $ref，重复出现时留一个不带结构的 object 占位，
    否则装饰阶段就会 RecursionError。
    """
    defs = schema.get("$defs", {})

    def resolve(node, seen: frozenset):
        if isinstance(node, dict):
            ref = node.get("$ref")
            if ref is not None:
                if ref in seen:
                    # 环：不再展开，只说明它是个对象
                    return {"type": "object"}
                key = ref.split("/")[-1]
                return resolve(dict(defs.get(key, {})), seen | {ref})
            return {k: resolve(v, seen) for k, v in node.items() if k != "$defs"}
        if isinstance(node, list):
            return [resolve(x, seen) for x in node]
        return node

    return resolve(schema, frozenset())


def _is_optional_annotation(anno: Any) -> bool:
    """注解是否允许 None（Optional[X] / X | None）。"""
    if anno is None:
        return False
    return get_origin(anno) in _UNION_ORIGINS and type(None) in get_args(anno)


def _annotation_to_param(name: str, anno: Any, required: bool,
                         default: Any = None, description: str = "") -> ParamDef:
    """把一个类型注解翻成 ParamDef，支持 List[X] / Dict / Optional[X] / pydantic 模型 / 嵌套。"""
    # pydantic 模型：展开成 object + 完整字段结构（inspect 做不到的）
    if _is_pydantic_model(anno):
        return _pydantic_to_param(name, anno, required, default, description)

    origin = get_origin(anno)

    # Optional[X] / Union[X, None] / X | None → 取 X，转为非必填。
    # PEP 604 的 `int | None` 求出的 origin 是 types.UnionType，与 typing.Union 不是同一个对象，
    # 只判后者会让两种同义写法推出不同的类型。
    #
    # 不含 None 的联合（Union[int, str]）只是"类型可以是其中之一"，与必填无关，
    # 因此沿用调用方给的 required，只取第一个分支来描述类型。
    if origin in _UNION_ORIGINS:
        args = get_args(anno)
        non_none = [a for a in args if a is not type(None)]
        if non_none:
            optional = type(None) in args
            return _annotation_to_param(
                name, non_none[0], required and not optional, default, description
            )

    # List[X] → array，元素结构进 sub_parameters（约定名 items）
    if origin in (list, getattr(typing, "List", list)):
        args = get_args(anno)
        sub = [_annotation_to_param("items", args[0], True)] if args else []
        return ParamDef(name, "array", description, required, default, sub)

    # Dict → object（不深挖 key/value，保持宽松）
    if origin in (dict, getattr(typing, "Dict", dict)) or anno is dict:
        return ParamDef(name, "object", description, required, default)

    # 基础类型，未知注解兜底 string
    return ParamDef(name, _PY_TO_PARAM.get(anno, "string"), description, required, default)


def build_schema(func: Callable) -> tuple:
    """从函数签名 + 注解 + 返回注解推导 (inputs, output)。跳过 ctx: Context 参数。"""
    sig = inspect.signature(func)
    try:
        hints = typing.get_type_hints(func)
    except Exception:
        hints = getattr(func, "__annotations__", {})

    inputs = []
    for pname, p in sig.parameters.items():
        # *args / **kwargs 没有对应的 event key，按种类判定而不是按名字，
        # 否则名叫 args 的普通参数会被漏掉，而真正的变参会被当成必填参数。
        if p.kind in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD):
            continue
        anno = hints.get(pname, str)
        if anno is Context:              # context 参数不算业务入参，不进 schema
            continue
        has_default = p.default is not inspect.Parameter.empty
        inputs.append(_annotation_to_param(
            name=pname, anno=anno,
            required=not has_default,
            default=p.default if has_default else None,
        ))

    output = None
    ret_anno = hints.get("return")
    if ret_anno is not None and ret_anno is not type(None):
        output = _annotation_to_param("result", ret_anno, True)

    return inputs, output


# ==================================================================== #
# 3. @tool 装饰器 + 注册表
# ==================================================================== #

@dataclass
class RegisteredTool:
    func: Callable
    name: str
    description: str
    inputs: list          # list[ParamDef]
    output: Optional[ParamDef]
    ctx_param: Optional[str] = None      # 若签名里有 ctx: Context，记下参数名

    def schema(self) -> dict:
        return {
            "name": self.name,
            "description": self.description,
            "inputs": [p.to_dict() for p in self.inputs],
            "outputs": [self.output.to_dict()] if self.output else [],
            "script_type": "python",
        }


# 单函数模型：一段用户代码注册一个工具（与「一个工具 = 一个函数」的现状一致）
_REGISTRY: dict = {}      # name -> RegisteredTool
_ENTRY: Optional[str] = None


def _find_ctx_param(func: Callable) -> Optional[str]:
    """找签名里类型为 Context 的参数名；没有返回 None。"""
    try:
        hints = typing.get_type_hints(func)
    except Exception:
        hints = getattr(func, "__annotations__", {})
    for pname in inspect.signature(func).parameters:
        if hints.get(pname) is Context:
            return pname
    return None


def tool(_func: Callable = None, *, name: str = None, description: str = None):
    """
    把普通函数登记为工具。
    用法：@tool  或  @tool(name="add", description="两数相加")
    """
    def wrap(func: Callable) -> Callable:
        global _ENTRY
        inputs, output = build_schema(func)
        t = RegisteredTool(
            func=func,
            name=name or func.__name__,
            description=(description or inspect.getdoc(func) or "").strip(),
            inputs=inputs,
            output=output,
            ctx_param=_find_ctx_param(func),
        )
        if _ENTRY is not None and _ENTRY != t.name:
            # 一段代码只有一个入口。静默改写会让执行的函数和用户以为的不是同一个。
            raise RuntimeError(
                "一段代码只能有一个 @tool 函数，已注册 %r，又遇到 %r" % (_ENTRY, t.name)
            )
        _REGISTRY[t.name] = t
        _ENTRY = t.name
        # 把提取结果直接挂到函数上，用户可 demo.inputs / demo.outputs / demo.schema() 访问
        func.__sandbox_tool__ = t
        func.inputs = [p.to_dict() for p in t.inputs]
        func.outputs = [t.output.to_dict()] if t.output else []
        func.schema = t.schema
        return func                       # 原样返回，函数照常可直接调用

    return wrap(_func) if callable(_func) else wrap


# ==================================================================== #
# 4. dispatch —— 沙箱侧运行时入口：event 解包 → 调用 → 返回值
#    沙箱 wrapper 生成 `result = sandbox_sdk.dispatch(event)` 再 print(json.dumps(result))
# ==================================================================== #

def dispatch(event: dict = None, entry: str = None) -> Any:
    """
    把 event（业务入参 map）解包成形参，调用用户 @tool 函数，返回其结果。

    - event 来自沙箱 wrapper 的 `json.loads(sys.stdin.read())`
    - 若函数声明了 ctx: Context，按需从环境变量注入
    - 缺失的必填参数在此暴露，报清是哪个参数
    """
    event = event or {}
    key = entry or _ENTRY
    if key is None:
        raise RuntimeError(
            "没有找到 @tool 注册的函数。请给你的函数加 @tool 装饰器。"
        )
    if key not in _REGISTRY:
        raise RuntimeError(
            "未注册的函数 %r。已注册: %s" % (key, ", ".join(sorted(_REGISTRY)) or "（无）")
        )
    t = _REGISTRY[key]

    sig = inspect.signature(t.func)
    try:
        hints = typing.get_type_hints(t.func)
    except Exception:
        hints = getattr(t.func, "__annotations__", {})

    kwargs: dict = {}
    missing = []
    for pname, p in sig.parameters.items():
        # *args / **kwargs 不从 event 取，判定与 build_schema 保持一致
        if p.kind in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD):
            continue
        # context 参数：按需从环境变量注入，不从 event 取
        if pname == t.ctx_param:
            kwargs[pname] = Context.from_env()
            continue
        if pname in event:
            val = event[pname]
            anno = hints.get(pname)
            # 参数是 pydantic 模型：用 event 的 dict 构造实例（带校验）
            if _is_pydantic_model(anno) and isinstance(val, dict):
                val = anno(**val)         # 校验失败会抛 pydantic ValidationError
            kwargs[pname] = val
        elif p.default is inspect.Parameter.empty:
            # Optional[X] 没有默认值时 schema 记的是非必填,这里得一致地放行,
            # 否则调用方按 schema 省略该参数就会被拦下。
            if _is_optional_annotation(hints.get(pname)):
                kwargs[pname] = None
            else:
                missing.append(pname)     # 必填但 event 没给

    if missing:
        raise ValueError("缺少必填参数: %s（event 里没有这些 key）" % ", ".join(missing))

    return t.func(**kwargs)


def export_schema() -> dict:
    """把推导出的 schema 吐出来，供执行工厂注册时自动填 inputs/outputs。"""
    return _REGISTRY[_ENTRY].schema() if _ENTRY else {}


# 别名：语义上 dispatch 就是「运行这次调用」
run = dispatch
