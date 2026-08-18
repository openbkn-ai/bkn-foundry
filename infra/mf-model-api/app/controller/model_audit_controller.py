from app.commons.snow_id import worker
from app.interfaces import logics
from app.logs.stand_log import StandLogger
import json

from app.utils.metering_producer import produce_metering_record


async def add_llm_model_call_log(para: logics.AddModelUsedAudit):
    """
    Write token usage information to the metering queue, using Kafka or Redis Stream according to METERING_BACKEND.
    :param para:
    :return:
    """
    try:
        # Prepare message data using the consumer fields from kafka_streams_processor.py.
        message_data = {
            'model_id': para.model_id,
            'user_id': para.user_id,
            'input_tokens': para.input_tokens,
            'output_tokens': para.output_tokens,
            'conf_id': str(worker.get_id()),  # Generate a new configuration ID.
            'total_price': 0.0,  # This value is calculated by the consumer.
            'currency_type': 0,  # Default value, updated by the consumer.
            'price_type': ["thousand", "thousand"],  # Default value, updated by the consumer.
            'referprice_in': 0.0,  # Default value, updated by the consumer.
            'referprice_out': 0.0,  # Default value, updated by the consumer.
            'total_time': para.total_time,
            'first_time': para.first_time,
            'status': para.status
        }

        # Convert message data to JSON.
        message_json = json.dumps(message_data, ensure_ascii=False)

        # Send to the metering queue asynchronously without blocking.
        import time
        t1 = time.time()
        success = await produce_metering_record(
            value=message_json.encode('utf-8'),
            key=f"{para.model_id}_{para.user_id}_{message_data['conf_id']}".encode('utf-8')  # Include conf_id for troubleshooting and tracing.
        )
        t2 = time.time()

        if success:
            StandLogger.info_log(f"消息已加入计量队列，耗时：{t2 - t1}s")
        else:
            StandLogger.warn(f"计量队列发送失败，消息已丢弃，耗时：{t2 - t1}s")
    except Exception as e:
        StandLogger.error(f"将token消费信息写入计量队列时出错: {e}")

