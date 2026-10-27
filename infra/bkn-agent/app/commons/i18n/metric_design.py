# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Language-specific intent patterns for metric proposal validation."""

import re


NAME_ONLY_ZH = re.compile(
    r"^\s*(?:只|仅)\s*(?:(?:把|将|修改|调整|更改|改)\s*)?"
    r"(?:(?:对象)?指标\s*)?(?:的\s*)?(?:名称|名字)\s*"
    r"(?:改|调整|更改|重命名|为|是|叫)?\s*[。！!？?]?\s*$"
)
NAME_EXPLANATION_ZH = re.compile(
    r"^\s*(?:为什么|为何).{0,40}?(?:名称|名字)\s*[。！!？?]?\s*$"
)
EXPLICIT_NAME_ZH = re.compile(r"(?:名称|名字)\s*(?:为|是|叫|[:：])\s*\S")
DESCRIPTION_SUPPLIED_ZH = re.compile(r"(?:业务定义|业务描述|描述|description)\s*[:：]\s*\S", re.IGNORECASE)
DESCRIPTION_UNCHANGED_ZH = re.compile(
    r"(?:描述|业务定义|description).{0,12}(?:保持原样|不变|不改)"
    r"|(?:不(?:要|需|用)?|无需).{0,12}(?:补全|改写|修改|更改).{0,12}(?:描述|业务定义|description)",
    re.IGNORECASE,
)
DESCRIPTION_UNCHANGED_EN = re.compile(
    r"\b(?:keep|leave|preserve)\b.{0,40}\bdescription\b"
    r"|\bdescription\b.{0,24}\b(?:remain|stay)\b.{0,16}\b(?:unchanged|empty|as\s+is)\b"
    r"|\b(?:do\s+not|don't)\b.{0,24}\b(?:change|modify|rewrite|fill|add)\b.{0,24}\bdescription\b",
    re.IGNORECASE,
)

COMPOSITE_FORMULA_CLARIFICATION_ZH = re.compile(
    r"依赖.{0,12}角色|角色.{0,12}分配|分子.{0,12}分母.{0,12}(?:方向|相反)"
)
COMPOSITE_FORMULA_CLARIFICATION_SUMMARY_ZH = "依赖的业务角色或公式方向仍需确认，本轮未提供可应用的配置变更。"
COMPOSITE_FORMULA_MISSING_SUMMARY_ZH = "公式尚未明确，本轮不应用部分配置。"
METRIC_CORE_MISSING_SUMMARY_ZH = "核心计算配置未能完整校验，因此本轮未应用部分字段。"
METRIC_CORE_MISSING_QUESTION_ZH = "请确认缺失的计算口径后重新生成完整配置。"
