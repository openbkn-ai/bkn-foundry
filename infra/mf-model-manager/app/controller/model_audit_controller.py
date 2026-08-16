from app.commons.snow_id import worker
from app.interfaces import logics
from app.logs.stand_log import StandLogger
import json

from app.utils.metering_producer import produce_metering_record


async def add_llm_model_call_log(para: logics.AddModelUsedAudit):
    """
    Write token usage to the metering queue selected by METERING_BACKEND.
    Args:
        para: Usage record to enqueue.
    Returns:
        Whether the record was accepted.
    """
    try:

        message_data = {
            'model_id': para.model_id,
            'user_id': para.user_id,
            'input_tokens': para.input_tokens,
            'output_tokens': para.output_tokens,
            'conf_id': str(worker.get_id()),  # Generate a new configuration ID.
            'total_price': 0.0,  # Calculated by the consumer.
            'currency_type': 0,  # Default updated by the consumer.
            'price_type': ["thousand", "thousand"],    # Default updated by the consumer.
            'referprice_in': 0.0,  # Default updated by the consumer.
            'referprice_out': 0.0  # Default updated by the consumer.
        }

        message_json = json.dumps(message_data, ensure_ascii=False)

        await produce_metering_record(
            value=message_json.encode('utf-8'),
            key=f"{para.model_id}_{para.user_id}_{message_data['conf_id']}".encode('utf-8')  # Include conf_id for diagnostics.
        )
    except Exception as e:
        StandLogger.error(f"将token消费信息写入计量队列时出错: {e}")
