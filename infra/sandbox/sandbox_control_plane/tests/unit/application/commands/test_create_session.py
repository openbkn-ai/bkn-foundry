"""CreateSessionCommand 校验测试（id / template_id 安全白名单兜底层）。"""

import pytest

from src.application.commands.create_session import CreateSessionCommand


class TestCreateSessionCommandValidation:
    @pytest.mark.parametrize(
        "bad_id",
        [
            "foo; rm -rf /",
            "$(id)",
            "a`id`b",
            "../escape",
            "sess/../../etc",
            "a/b",
            "with space",
            "a|b",
            "a&b",
        ],
    )
    def test_rejects_unsafe_id(self, bad_id):
        # 兜底层：即使不经 request schema，含 shell 元字符 / '..' / '/' 的 id 也必须
        # 被拒——它会落入以 root 运行的 s3fs 挂载脚本，否则造成命令注入 / 前缀逃逸。
        with pytest.raises(ValueError):
            CreateSessionCommand(id=bad_id)
        with pytest.raises(ValueError):
            CreateSessionCommand(template_id=bad_id)

    def test_accepts_safe_id_and_template_id(self):
        cmd = CreateSessionCommand(id="sess_aoi_0", template_id="python-basic")
        assert cmd.id == "sess_aoi_0"
        assert cmd.template_id == "python-basic"

    def test_allows_none(self):
        # id/template_id 可选；None 走自动生成 / 默认模板，不应被校验拦下。
        cmd = CreateSessionCommand()
        assert cmd.id is None
        assert cmd.template_id is None
