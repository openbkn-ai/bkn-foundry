"""
计量数据聚合器：传输无关的聚合 + 定时批量落库。

从 kafka_streams_processor.py 抽出，供 Kafka / Redis Stream 两种消费端共用。
聚合口径与落库行为保持原样：按 model_id+user_id+status 联合键累加，
每 300s 批量 INSERT ON DUPLICATE KEY UPDATE 写入 ModelUsedAuditInfo。
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
        # 存储按model_id和user_id联合主键汇总的数据
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
        self.is_processing = False  # 处理状态标志，防止重复执行
        self._timer_thread = None

    def add_record(self, data: dict):
        """累加一条计量记录（data 为已解析的消息 dict）。

        缺关键字段的消息跳过；quota_config_cache_tree 查价异常由调用方捕获，
        与原 Kafka 消费路径行为一致。
        """
        model_id = data.get('model_id', '')
        user_id = data.get('user_id', '')
        status = data.get('status', '')

        if not model_id or not user_id or not status:
            StandLogger.warn(f"消息缺少model_id或user_id或status: {data}")
            return

        # 使用model_id和user_id作为联合主键
        key = f"{model_id}_{user_id}_{status}"
        # 累加input_tokens和output_tokens
        with self.lock:
            self.aggregated_data[key]['input_tokens'] += data.get('input_tokens', 0)
            self.aggregated_data[key]['output_tokens'] += data.get('output_tokens', 0)
            self.aggregated_data[key]['total_count'] += 1
            if status == "failed":
                self.aggregated_data[key]['failed_count'] += 1
            # 累加总时间和首字时间（仅统计成功请求）
            if status != "failed":
                self.aggregated_data[key]['total_time'] += data.get('total_time', 0.0)
                self.aggregated_data[key]['first_time'] += data.get('first_time', 0.0)
            # 保存其他字段（假设同一种model_id和user_id组合的其他字段是相同的）
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
        """启动定时落库线程（仅启动一次）"""
        if self._timer_thread is None or not self._timer_thread.is_alive():
            StandLogger.info_log("启动定时数据处理任务...")
            self._timer_thread = threading.Thread(target=self._run_periodic_processing, daemon=True)
            self._timer_thread.start()
            StandLogger.info_log("定时数据处理任务已启动")
        else:
            StandLogger.info_log("定时数据处理任务已在运行，跳过重复启动")

    def _run_periodic_processing(self):
        """运行定时数据处理的线程函数"""
        StandLogger.info_log("创建定时汇总数据任务成功")

        while self.running:
            try:
                StandLogger.info_log(f"{self.flush_interval_seconds}秒后开始执行定时汇总任务")

                time.sleep(self.flush_interval_seconds)

                if not self.running:
                    break

                # 处理数据
                self.process_aggregated_data()
            except Exception as e:
                StandLogger.error(f"定期处理数据时出错: {e}")
                time.sleep(1)

    def process_aggregated_data(self):
        """处理汇总数据并存入数据库"""
        # 检查是否正在处理中，防止重复执行
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

            # 使用锁保护复制和清空操作
            with self.lock:
                # 复制当前数据并清空原字典
                data_to_process = dict(self.aggregated_data)
                self.aggregated_data.clear()

            # 将数据分批，每批最多500条
            batch_size = 500
            data_items = list(data_to_process.items())

            # 分批处理数据
            for i in range(0, len(data_items), batch_size):
                batch_items = data_items[i:i + batch_size]
                batch_data = []

                # 收集一批数据
                for key, data in batch_items:
                    try:
                        # 计算平均时间
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

                # 批量保存到数据库（使用INSERT ... ON DUPLICATE KEY UPDATE实现幂等性）
                if batch_data:
                    try:
                        # 使用批量插入或更新，避免重复数据
                        affected = self.model_op_dao.batch_add_model_used_log(batch_data)
                        StandLogger.info_log(f"成功批量保存/更新{affected}条数据, 收集到{len(batch_data)}条")
                    except Exception as e:
                        StandLogger.error(f"批量保存数据到数据库时出错: {e}")

        finally:
            # 重置处理状态标志
            with self.lock:
                self.is_processing = False

    def stop(self):
        """停止聚合器定时线程（与原 Kafka 消费端 stop 行为一致，不做额外落库）"""
        self.running = False
        try:
            if self._timer_thread is not None:
                self._timer_thread.join(timeout=2)
        except Exception:
            pass
