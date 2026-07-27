"""
创建会话命令

定义创建会话的命令对象。
扩展支持依赖安装，按照 sandbox-design-v2.1.md 章节 5 设计。
"""

import re
from dataclasses import dataclass, field
from typing import Dict, List, Optional

from src.domain.value_objects.resource_limit import ResourceLimit
from src.shared.utils.dependencies import DEFAULT_PYTHON_PACKAGE_INDEX_URL


@dataclass
class CreateSessionCommand:
    """
    创建会话命令

    扩展支持 Python 依赖安装功能。
    """

    template_id: Optional[str] = None
    timeout: int = 300
    resource_limit: ResourceLimit | None = None
    env_vars: Dict[str, str] | None = None
    id: Optional[str] = None  # 手动指定会话 ID（可选）

    # 依赖安装相关字段（新增）
    dependencies: List[str] = field(default_factory=list)
    install_timeout: int = 300
    fail_on_dependency_error: bool = True
    allow_version_conflicts: bool = False
    python_package_index_url: str = DEFAULT_PYTHON_PACKAGE_INDEX_URL

    def __post_init__(self):
        """初始化后验证"""
        if self.timeout <= 0:
            raise ValueError("timeout must be positive")

        # 安全关键：id / template_id 会经 workspace_path 落入以 root 运行的 s3fs
        # 挂载脚本（k8s/docker scheduler）。严格白名单兜底，防 shell 命令注入与
        # 前缀逃逸；request schema 已在入口校验，此处覆盖不经 schema 的调用路径。
        for _name, _val in (("id", self.id), ("template_id", self.template_id)):
            if _val is not None and not re.match(r"^[A-Za-z0-9_-]+$", _val):
                raise ValueError(
                    f"{_name} may only contain letters, digits, '_' and '-'"
                )

        # 设置默认值
        if self.resource_limit is None:
            self.resource_limit = ResourceLimit.default()
        if self.env_vars is None:
            self.env_vars = {}

        # 验证安装超时
        if self.install_timeout < 30 or self.install_timeout > 1800:
            raise ValueError("install_timeout must be between 30 and 1800 seconds")
