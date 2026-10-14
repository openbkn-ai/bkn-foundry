# 对象指标 V1 历史数据迁移

脚本默认只扫描并生成 JSON 报告，不写数据库。它只自动迁移能够无损映射的“对象类范围、无业务条件、无固定分组”的历史原子指标；历史衍生/复合指标及混入业务限定的原子指标均标记为 `manual_review`，避免用技术字段猜测业务语义。

```bash
python3 migrate_object_metrics.py --host 127.0.0.1 --database bkn_backend --report report.json
python3 migrate_object_metrics.py --host 127.0.0.1 --database bkn_backend --report report.json --apply
```

连接参数也可通过 `BKN_DB_HOST`、`BKN_DB_PORT`、`BKN_DB_USER`、`BKN_DB_PASSWORD`、`BKN_DB_NAME` 提供。执行 `--apply` 前必须评审报告。脚本会在写入前显式检查目标版本和稳定编码：已存在的版本记为 `already_exists`，编码冲突记为 `manual_review`，不会静默忽略冲突；因此可重复运行，且不会删除或修改原表。
