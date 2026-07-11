from app.commons.snow_id import worker
from app.interfaces import logics
from app.logs.stand_log import StandLogger
import json

from app.utils.metering_producer import produce_metering_record


async def add_llm_model_call_log(para: logics.AddModelUsedAudit):
    """
    将token消费信息写入计量队列（Kafka 或 Redis Stream，按 METERING_BACKEND）
    :param para:
    :return:
    """
    try:
        # 准备消息数据，参考kafka_streams_processor.py中消费者的字段
        message_data = {
            'model_id': para.model_id,
            'user_id': para.user_id,
            'input_tokens': para.input_tokens,
            'output_tokens': para.output_tokens,
            'conf_id': str(worker.get_id()),  # 生成新的配置ID
            'total_price': 0.0,  # 这个值会在消费者端计算
            'currency_type': 0,  # 默认值，会在消费者端更新
            'price_type': ["thousand", "thousand"],  # 默认值，会在消费者端更新
            'referprice_in': 0.0,  # 默认值，会在消费者端更新
            'referprice_out': 0.0,  # 默认值，会在消费者端更新
            'total_time': para.total_time,
            'first_time': para.first_time,
            'status': para.status
        }

        # 将消息数据转换为JSON格式
        message_json = json.dumps(message_data, ensure_ascii=False)

        # 异步非阻塞发送到计量队列
        import time
        t1 = time.time()
        success = await produce_metering_record(
            value=message_json.encode('utf-8'),
            key=f"{para.model_id}_{para.user_id}_{message_data['conf_id']}".encode('utf-8')  # 加入conf_id以便排查追踪
        )
        t2 = time.time()

        if success:
            StandLogger.info_log(f"消息已加入计量队列，耗时：{t2 - t1}s")
        else:
            StandLogger.warn(f"计量队列发送失败，消息已丢弃，耗时：{t2 - t1}s")
    except Exception as e:
        StandLogger.error(f"将token消费信息写入计量队列时出错: {e}")


