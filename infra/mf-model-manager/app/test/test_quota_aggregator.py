"""测试 utils/quota_aggregator.py：传输无关的计量聚合与落库"""
from unittest.mock import MagicMock, patch

from app.utils.quota_aggregator import QuotaAggregator


def _mock_price_config(billing_type=1, referprice_in=2.0, referprice_out=4.0,
                       price_type=None, currency_type=1):
    cfg = MagicMock()
    cfg.billing_type = billing_type
    cfg.referprice_in = referprice_in
    cfg.referprice_out = referprice_out
    cfg.price_type = price_type or ["thousand", "thousand"]
    cfg.currency_type = currency_type
    return cfg


class TestAddRecord:
    def test_success_record_aggregated_with_price(self):
        agg = QuotaAggregator()
        cache = {"m1": _mock_price_config()}
        with patch('app.utils.quota_aggregator.quota_config_cache_tree', cache):
            agg.add_record({
                'model_id': 'm1', 'user_id': 'u1', 'status': 'success',
                'input_tokens': 1000, 'output_tokens': 500,
                'total_time': 2.0, 'first_time': 0.5, 'conf_id': 'c1',
            })

        key = 'm1_u1_success'
        data = agg.aggregated_data[key]
        assert data['input_tokens'] == 1000
        assert data['output_tokens'] == 500
        assert data['total_count'] == 1
        assert data['failed_count'] == 0
        assert data['total_time'] == 2.0
        # billing_type=1: 1000*(2.0/1000) + 500*(4.0/1000) = 4.0
        assert data['total_price'] == 4.0
        assert data['currency_type'] == 1

    def test_failed_record_counts_but_no_time(self):
        agg = QuotaAggregator()
        cache = {"m1": _mock_price_config()}
        with patch('app.utils.quota_aggregator.quota_config_cache_tree', cache):
            agg.add_record({
                'model_id': 'm1', 'user_id': 'u1', 'status': 'failed',
                'input_tokens': 10, 'output_tokens': 0,
                'total_time': 9.9, 'first_time': 9.9, 'conf_id': 'c1',
            })

        data = agg.aggregated_data['m1_u1_failed']
        assert data['failed_count'] == 1
        assert data['total_time'] == 0.0  # 失败请求不计时间
        assert data['first_time'] == 0.0

    def test_missing_key_fields_skipped(self):
        agg = QuotaAggregator()
        with patch('app.utils.quota_aggregator.quota_config_cache_tree', {}):
            agg.add_record({'model_id': '', 'user_id': 'u1', 'status': 'success'})
            agg.add_record({'model_id': 'm1', 'user_id': 'u1'})  # 缺 status

        assert len(agg.aggregated_data) == 0

    def test_same_key_accumulates(self):
        agg = QuotaAggregator()
        cache = {"m1": _mock_price_config(billing_type=0, referprice_in=1.0)}
        record = {
            'model_id': 'm1', 'user_id': 'u1', 'status': 'success',
            'input_tokens': 100, 'output_tokens': 100,
            'total_time': 1.0, 'first_time': 0.1, 'conf_id': 'c1',
        }
        with patch('app.utils.quota_aggregator.quota_config_cache_tree', cache):
            agg.add_record(dict(record))
            agg.add_record(dict(record))

        data = agg.aggregated_data['m1_u1_success']
        assert data['input_tokens'] == 200
        assert data['total_count'] == 2
        # billing_type!=1: (100+100)*1.0/1000 每条 0.2，两条 0.4
        assert abs(data['total_price'] - 0.4) < 1e-9


class TestProcessAggregatedData:
    def test_flush_writes_batch_and_clears(self):
        agg = QuotaAggregator()
        agg.model_op_dao = MagicMock()
        agg.model_op_dao.batch_add_model_used_log.return_value = 1
        cache = {"m1": _mock_price_config()}
        with patch('app.utils.quota_aggregator.quota_config_cache_tree', cache):
            agg.add_record({
                'model_id': 'm1', 'user_id': 'u1', 'status': 'success',
                'input_tokens': 1000, 'output_tokens': 500,
                'total_time': 2.0, 'first_time': 0.5, 'conf_id': 'c1',
            })

        agg.process_aggregated_data()

        agg.model_op_dao.batch_add_model_used_log.assert_called_once()
        batch = agg.model_op_dao.batch_add_model_used_log.call_args[0][0]
        assert len(batch) == 1
        # 聚合字典已清空，处理标志复位
        assert len(agg.aggregated_data) == 0
        assert agg.is_processing is False

    def test_flush_empty_noop(self):
        agg = QuotaAggregator()
        agg.model_op_dao = MagicMock()

        agg.process_aggregated_data()

        agg.model_op_dao.batch_add_model_used_log.assert_not_called()
        assert agg.is_processing is False

    def test_dao_error_does_not_raise(self):
        agg = QuotaAggregator()
        agg.model_op_dao = MagicMock()
        agg.model_op_dao.batch_add_model_used_log.side_effect = Exception("db down")
        cache = {"m1": _mock_price_config()}
        with patch('app.utils.quota_aggregator.quota_config_cache_tree', cache):
            agg.add_record({
                'model_id': 'm1', 'user_id': 'u1', 'status': 'success',
                'input_tokens': 1, 'output_tokens': 1,
                'total_time': 1.0, 'first_time': 0.1, 'conf_id': 'c1',
            })

        # 不应抛出
        agg.process_aggregated_data()
        assert agg.is_processing is False
