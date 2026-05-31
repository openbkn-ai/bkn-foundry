import json
import os

# mq_sdk 是内部包，没在 deps/ 里发车。用 try 包住让 pyinstaller bundle 在缺包时
# 仍能加载；真正调用 Connector 的路径会在运行时报 AttributeError，比启动期
# 直接 panic 阻塞整个 binary 友好。
try:
    from mq_sdk.proton_mq import Connector  # type: ignore
except ImportError:  # pragma: no cover
    Connector = None  # type: ignore

from common.logger import logger
from common.configs import mq_configs

class MQ:
    con = ""
    phost = ""
    pport = ""

    @classmethod
    def initconnect(cls):
        cls.phost = mq_configs.get("host")
        cls.pport = int(mq_configs.get("port"))
        cls.chost = mq_configs.get("lookupd_host")
        cls.cport = int(mq_configs.get("lookupd_port"))
        cls.connector_type = mq_configs.get("connector_type")
        cls.con = Connector.get_connector(cls.phost, cls.pport, cls.chost, cls.cport, cls.connector_type)

    @classmethod
    def init_connector_from_file(cls, config_file_path):
        if Connector is None:
            logger.warning(
                "mq_sdk 不在 bundle 里，跳过 MQ 初始化 (config_file_path=%s)。"
                "依赖 MQ 的运行时路径调用时会失败。",
                config_file_path,
            )
            cls.con = None
            return
        cls.con = Connector.get_connector_from_file(config_file_path)

    @classmethod
    async def create_consumer(cls, topic, channel, handler):
        # 60代表nsq两次查询时间间隔为60s， 16代表一个consumer一次最多处理消息的数量
        return await cls.con.sub(topic, channel, handler, 60, 16)

    @classmethod
    async def create_producer(cls, topic, message):
        if isinstance(message, (dict, list)):
            message = json.dumps(message)
        try:
            await cls.con.pub(topic, message)
            logger.info(f"Send success, topic:{topic}, message:{message}")
        except Exception:
            logger.exception(f"Send failed, topic:{topic}, message:{message}.")

    @classmethod
    async def publish(cls, topic, message):
        """
        更新函数名，后续若有新的发布消息逻辑，请调用此方法
        """
        await cls.create_producer(topic, message)
