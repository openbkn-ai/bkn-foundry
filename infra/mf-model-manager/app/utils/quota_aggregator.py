"""
Transport-independent metering aggregation with scheduled database writes.

Shared by Kafka and Redis Stream consumers.
Aggregate by model_id, user_id, and status,
then upsert ModelUsedAuditInfo in batches every 300 seconds.
"""
import datetime
import json
import threading
import time
from collections import defaultdict

from app.logs.stand_log import StandLogger
from app.dao.model_used_audit_dao import model_op_dao
from app.interfaces.dbaccess import ModelUsedAuditInfo
from app.utils.config_cache import quota_config_cache_tree


class QuotaAggregator:
    def __init__(self, flush_interval_seconds=300):
        self.flush_interval_seconds = flush_interval_seconds
        self.aggregated_data = defaultdict(lambda: {
            'input_tokens': 0,
            'output_tokens': 0,
            'conf_id': '',
            'model_id': '',
            'user_id': '',
            'total_price': 0.0,
            'currency_type': 0,
            'price_type': [],
            'referprice_in': 0.0,
            'referprice_out': 0.0,
            'total_count': 0,
            'failed_count': 0,
            'average_total_time': 0.0,
            'average_first_time': 0.0,
            'total_time': 0.0,
            'first_time': 0.0
        })
        self.model_op_dao = model_op_dao
        self.lock = threading.Lock()
        self.running = True
        self.is_processing = False  # Prevent concurrent processing.
        self._timer_thread = None

    def add_record(self, data: dict):
        """Aggregate one parsed metering record.

        Skip records missing required fields; callers handle quota lookup failures,
        matching the existing Kafka behavior.
        """
        model_id = data.get('model_id', '')
        user_id = data.get('user_id', '')
        status = data.get('status', '')

        if not model_id or not user_id or not status:
            StandLogger.warn(f"消息缺少model_id或user_id或status: {data}")
            return

        key = f"{model_id}_{user_id}_{status}"
        with self.lock:
            self.aggregated_data[key]['input_tokens'] += data.get('input_tokens', 0)
            self.aggregated_data[key]['output_tokens'] += data.get('output_tokens', 0)
            self.aggregated_data[key]['total_count'] += 1
            if status == "failed":
                self.aggregated_data[key]['failed_count'] += 1
            if status != "failed":
                self.aggregated_data[key]['total_time'] += data.get('total_time', 0.0)
                self.aggregated_data[key]['first_time'] += data.get('first_time', 0.0)
            self.aggregated_data[key]['conf_id'] = data.get('conf_id', '')
            self.aggregated_data[key]['model_id'] = model_id
            self.aggregated_data[key]['user_id'] = user_id
            price_dict = {
                "thousand": 1000,
                "million": 1000000
            }
            price_type = quota_config_cache_tree[model_id].price_type
            if quota_config_cache_tree[model_id].billing_type == 1:
                total_price = data.get('input_tokens', 0) * (
                        quota_config_cache_tree[model_id].referprice_in / price_dict.get(price_type[0],
                                                                                         1000)) + data.get(
                    'output_tokens', 0) * (quota_config_cache_tree[model_id].referprice_out / price_dict.get(
                    price_type[1], 1000))
            else:
                total_price = (data.get('input_tokens', 0) + data.get('output_tokens', 0)) * \
                              quota_config_cache_tree[model_id].referprice_in / price_dict.get(price_type[0], 1000)
            self.aggregated_data[key]['total_price'] += total_price
            self.aggregated_data[key]['currency_type'] = quota_config_cache_tree[model_id].currency_type
            self.aggregated_data[key]['price_type'] = price_type
            self.aggregated_data[key]['referprice_in'] = quota_config_cache_tree[model_id].referprice_in
            self.aggregated_data[key]['referprice_out'] = quota_config_cache_tree[model_id].referprice_out

    def start_periodic_flush(self):
        """Start the scheduled persistence thread once."""
        if self._timer_thread is None or not self._timer_thread.is_alive():
            StandLogger.info_log("启动定时数据处理任务...")
            self._timer_thread = threading.Thread(target=self._run_periodic_processing, daemon=True)
            self._timer_thread.start()
            StandLogger.info_log("定时数据处理任务已启动")
        else:
            StandLogger.info_log("定时数据处理任务已在运行，跳过重复启动")

    def _run_periodic_processing(self):
        """Run the scheduled data-processing loop."""
        StandLogger.info_log("创建定时汇总数据任务成功")

        while self.running:
            try:
                StandLogger.info_log(f"{self.flush_interval_seconds}秒后开始执行定时汇总任务")

                time.sleep(self.flush_interval_seconds)

                if not self.running:
                    break

                self.process_aggregated_data()
            except Exception as e:
                StandLogger.error(f"定期处理数据时出错: {e}")
                time.sleep(1)

    def process_aggregated_data(self):
        """Persist aggregated usage data."""
        with self.lock:
            if self.is_processing:
                StandLogger.info_log("数据正在处理中，跳过本次执行")
                return
            self.is_processing = True

        try:
            if not self.aggregated_data:
                StandLogger.info_log("没有需要处理的汇总数据")
                return

            StandLogger.info_log(f"开始处理{len(self.aggregated_data)}条汇总数据")

            with self.lock:
                data_to_process = dict(self.aggregated_data)
                self.aggregated_data.clear()

            batch_size = 500
            data_items = list(data_to_process.items())

            for i in range(0, len(data_items), batch_size):
                batch_items = data_items[i:i + batch_size]
                batch_data = []

                for key, data in batch_items:
                    try:
                        success_count = data['total_count'] - data['failed_count']
                        average_total_time = data['total_time'] / success_count if success_count > 0 else 0.0
                        average_first_time = data['first_time'] / success_count if success_count > 0 else 0.0

                        audit_info = ModelUsedAuditInfo(
                            conf_id=data['conf_id'],
                            model_id=data['model_id'],
                            user_id=data['user_id'],
                            input_tokens=data['input_tokens'],
                            output_tokens=data['output_tokens'],
                            total_price=data['total_price'],
                            create_time=datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
                            currency_type=data['currency_type'],
                            price_type=json.loads(data['price_type']) if isinstance(data['price_type'],
                                                                                    str) else data['price_type'],
                            referprice_in=data['referprice_in'],
                            referprice_out=data['referprice_out'],
                            total_count=data['total_count'],
                            failed_count=data['failed_count'],
                            average_total_time=average_total_time,
                            average_first_time=average_first_time
                        )
                        batch_data.append(audit_info)
                    except Exception as e:
                        StandLogger.error(f"创建ModelUsedAuditInfo对象时出错: {e}")

                if batch_data:
                    try:
                        affected = self.model_op_dao.batch_add_model_used_log(batch_data)
                        StandLogger.info_log(f"成功批量保存/更新{affected}条数据, 收集到{len(batch_data)}条")
                    except Exception as e:
                        StandLogger.error(f"批量保存数据到数据库时出错: {e}")

        finally:
            with self.lock:
                self.is_processing = False

    def stop(self):
        """Stop the aggregation thread without an additional flush."""
        self.running = False
        try:
            if self._timer_thread is not None:
                self._timer_thread.join(timeout=2)
        except Exception:
            pass
