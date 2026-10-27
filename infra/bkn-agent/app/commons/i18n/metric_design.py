# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Language-specific intent patterns for metric proposal validation."""

import re


NAME_ONLY_ZH = re.compile(
    r"(?:只|仅)\s*(?:(?:把|将|修改|调整|更改|改)\s*)?"
    r"(?:(?:对象)?指标\s*)?(?:的\s*)?(?:名称|名字)\s*"
    r"(?:改|调整|更改|重命名|为|是|叫|$)"
)
NAME_EXPLANATION_ZH = re.compile(r"(?:为什么|为何).*?(?:名称|名字)")
EXPLICIT_NAME_ZH = re.compile(r"(?:名称|名字)\s*(?:为|是|叫|[:：])\s*\S")
DESCRIPTION_SUPPLIED_ZH = re.compile(r"(?:业务定义|业务描述|描述|description)\s*[:：]\s*\S", re.IGNORECASE)
DESCRIPTION_UNCHANGED_ZH = re.compile(
    r"(?:描述|业务定义|description).{0,12}(?:保持原样|不变|不改)"
    r"|(?:不(?:要|需|用)?|无需).{0,12}(?:补全|改写|修改|更改).{0,12}(?:描述|业务定义|description)",
    re.IGNORECASE,
)
