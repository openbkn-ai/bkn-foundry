"""Stable business wording for persisted audit snapshots."""
_ACTION_NAMES = {"create": "新建", "update": "修改", "delete": "删除", "set_default": "设置默认"}
_TARGET_NAMES = {"llm_model": "大模型", "small_model": "小模型", "model_quota": "模型配额", "user_model_quota": "用户模型配额"}

SEPARATOR = "、"
BATCH = "，共{count}项"
TRUNCATED = "（名称摘要已截断）"
MODEL_QUOTA = "模型“{name}”的配额"
USER_QUOTA = "用户“{user}”在模型“{model}”的配额"
