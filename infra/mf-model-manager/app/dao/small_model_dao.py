import datetime
import json

from app.interfaces.dbaccess import AddExternalSmallModelInfo
from app.logs.stand_log import StandLogger
from app.mydb.my_pymysql_pool import (
    advisory_lock_transaction,
    connect_execute_close_db,
    connect_execute_commit_close_db,
)

para_dict = {
    "update_time": "f_update_time",
    "create_time": "f_create_time",
    "name": "f_model_name"
}


class SmallModelDao:
    @connect_execute_close_db
    def find_model_by_duplicate_config(self, config_info: AddExternalSmallModelInfo, connection, cursor):
        """Return only display-safe fields when a small-model configuration exists.

        Model type is part of the identity so embedding and reranker defaults and
        configurations remain fully isolated.
        """
        cursor.execute(
            """select f_model_id, f_model_name, f_model_type, f_model_config, f_adapter, f_adapter_code
               from t_small_model where f_model_type=%s""",
            config_info.model_type,
        )
        expected_config = json.dumps(config_info.model_config, ensure_ascii=False, sort_keys=True)
        for item in cursor.fetchall():
            stored_config = json.dumps(json.loads(item["f_model_config"]), ensure_ascii=False, sort_keys=True)
            stored_adapter = item["f_adapter"] in (1, True, "1")
            if (stored_config == expected_config and stored_adapter == bool(config_info.adapter)
                    and (item["f_adapter_code"] or "") == (config_info.adapter_code or "")):
                return {
                    "id": str(item["f_model_id"]),
                    "name": item["f_model_name"],
                    "type": item["f_model_type"],
                }
        return None

    @connect_execute_commit_close_db
    def add_model_info(self, config_info: AddExternalSmallModelInfo, userId, connection, cursor):
        sql = """insert into t_small_model(f_model_id, f_model_name, f_model_type, f_model_config, f_create_time, f_update_time,f_create_by,f_update_by,
        f_adapter,f_adapter_code,f_batch_size,f_max_tokens,f_embedding_dim) 
                    values(%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s) """
        value_list = [config_info.model_id, config_info.model_name, config_info.model_type,
                      json.dumps(config_info.model_config, ensure_ascii=False), datetime.datetime.today(),
                      datetime.datetime.today(), userId, userId, config_info.adapter, config_info.adapter_code,
                      config_info.batch_size, config_info.max_tokens, config_info.embedding_dim]

        cursor.execute(sql, value_list)

    def add_model_with_default(self, config_info: AddExternalSmallModelInfo, userId, requested_default):
        """Create a small model with one default per model type under a lock."""
        lock_name = f"mf-model-default:small:{config_info.model_type}"
        with advisory_lock_transaction(lock_name) as cursor:
            cursor.execute(
                "select f_model_id from t_small_model where f_default=1 and f_model_type=%s",
                config_info.model_type,
            )
            has_default = bool(cursor.fetchall())
            is_default = requested_default or not has_default

            if requested_default and has_default:
                cursor.execute(
                    "update t_small_model set f_default=0 where f_default=1 and f_model_type=%s",
                    config_info.model_type,
                )

            sql = """insert into t_small_model(
                f_model_id,f_model_name,f_model_type,f_model_config,f_create_time,f_update_time,
                f_create_by,f_update_by,f_adapter,f_adapter_code,f_batch_size,f_max_tokens,
                f_embedding_dim,f_default
            ) values(%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)"""
            cursor.execute(sql, (
                config_info.model_id, config_info.model_name, config_info.model_type,
                json.dumps(config_info.model_config, ensure_ascii=False), datetime.datetime.today(),
                datetime.datetime.today(), userId, userId, config_info.adapter, config_info.adapter_code,
                config_info.batch_size, config_info.max_tokens, config_info.embedding_dim,
                1 if is_default else 0,
            ))
            return is_default

    @connect_execute_commit_close_db
    def edit_model_info(self, config_info: AddExternalSmallModelInfo, userId, connection, cursor):
        sql = """update t_small_model set f_model_name = %s,f_model_type = %s,f_model_config = %s,f_adapter = %s,f_adapter_code = %s,
        f_update_time = %s,f_update_by = %s,f_batch_size = %s,f_max_tokens = %s,f_embedding_dim = %s where f_model_id = %s"""
        value_list = [config_info.model_name, config_info.model_type, json.dumps(config_info.model_config, ensure_ascii=False),
                      config_info.adapter,config_info.adapter_code, datetime.datetime.today(),
                      userId, config_info.batch_size, config_info.max_tokens, config_info.embedding_dim,
                      config_info.model_id]

        cursor.execute(sql, value_list)

    @connect_execute_close_db
    def get_model_info_by_id(self, model_id, connection, cursor):
        sql = f"""select f_model_id, f_model_name, f_model_type, f_model_config, f_create_time, f_update_time,f_adapter, 
        f_adapter_code,f_batch_size,f_max_tokens,f_embedding_dim,f_default
                    from t_small_model where f_model_id = '{model_id}'"""

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_model_info_by_name(self, model_name, connection, cursor):
        sql = """select f_model_id, f_model_name, f_model_type, f_model_config, f_create_time, f_update_time,f_adapter, 
        f_adapter_code,f_batch_size,f_max_tokens,f_embedding_dim,f_default
                    from t_small_model where f_model_name = %s"""

        cursor.execute(sql, model_name)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_model_info_by_ids(self, model_ids, connection, cursor):
        placeholders = ','.join(['%s'] * len(model_ids))
        sql = f"""select f_model_id,f_model_name from t_small_model where f_model_id IN ({placeholders})"""
        cursor.execute(sql, model_ids)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_model_info_by_names(self, model_names, connection, cursor):
        placeholders = ','.join(['%s'] * len(model_names))
        sql = f"""select f_model_id,f_model_name from t_small_model where f_model_name IN ({placeholders})"""
        cursor.execute(sql, model_names)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_model_info_list(self, page, size, order, rule, model_name, model_type, model_series, permission_ids,
                            connection, cursor):
        sql = """select f_model_id, f_model_name, f_model_type, f_model_config, f_create_time, f_update_time, f_create_by,f_update_by,
        f_adapter,f_adapter_code,f_batch_size,f_max_tokens,f_embedding_dim,f_default
                    from t_small_model """
        where_list = []
        value_list = []
        if permission_ids:
            placeholders = ','.join(['%s'] * len(permission_ids))
            where_list.append(f"f_model_id in ({placeholders})")
            value_list.extend(permission_ids)
        if model_name != "":
            where_list.append("f_model_name like %s")
            value_list.append(f"%{model_name}%")
        if model_type != "":
            where_list.append("f_model_type = %s")
            value_list.append(model_type)
        where_sql = f" where {' and '.join(where_list)}" if where_list else ""
        order_sql = f" order by f_{rule} {'desc' if order == 'desc' else 'asc'}"
        limit_sql = f" limit {(int(page) - 1) * int(size)},{size}"
        sql = sql + where_sql + order_sql + limit_sql
        cursor.execute(sql, value_list)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_model_info_total(self, model_name, model_type, model_series, permission_ids, connection, cursor):
        sql = """select f_model_id, f_model_name, f_model_type, f_model_config, f_create_time, f_update_time 
                    from t_small_model """
        where_list = []
        value_list = []
        if model_name != "":
            where_list.append("f_model_name like %s")
            value_list.append(f"%{model_name}%")
        if model_type != "":
            where_list.append("f_model_type = %s")
            value_list.append(model_type)
        if permission_ids:
            placeholders = ','.join(['%s'] * len(permission_ids))
            where_list.append(f"f_model_id in ({placeholders})")
            value_list.extend(permission_ids)
        where_sql = f" where {' and '.join(where_list)}" if where_list else ""
        sql = sql + where_sql
        cursor.execute(sql, value_list)
        res = cursor.fetchall()
        return res

    @connect_execute_commit_close_db
    def delete_model_info_by_ids(self, model_ids, connection, cursor):
        placeholders = ','.join(['%s'] * len(model_ids))
        sql = f"DELETE FROM t_small_model WHERE f_model_id IN ({placeholders})"
        cursor.execute(sql, model_ids)

    @connect_execute_close_db
    def name_check(self, name, connection, cursor):
        sql = """select f_model_id, f_model_name from t_small_model where f_model_name = %s"""
        cursor.execute(sql, name)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_all_ids(self, connection, cursor):
        sql = """select f_model_id from t_small_model limit 1000"""
        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_default_by_type(self, model_type, connection, cursor):
        """Return the system default small model for a model_type."""
        sql = """select f_model_id, f_model_name, f_model_type, f_model_config, f_create_time, f_update_time,f_adapter,
        f_adapter_code,f_batch_size,f_max_tokens,f_embedding_dim,f_default
                    from t_small_model where f_default = 1 and f_model_type = %s"""
        cursor.execute(sql, model_type)
        res = cursor.fetchall()
        return res

    @connect_execute_commit_close_db
    def update_model_default_status(self, model_id, is_default, connection, cursor):
        """Update one small model's default status."""
        sql = """update t_small_model set f_default = %s where f_model_id = %s"""
        cursor.execute(sql, (1 if is_default else 0, model_id))

    def set_default_model(self, model_id, model_type):
        """Atomically make a model the sole default in its small-model type."""
        lock_name = f"mf-model-default:small:{model_type}"
        with advisory_lock_transaction(lock_name) as cursor:
            cursor.execute(
                "update t_small_model set f_default=0 where f_default=1 and f_model_type=%s",
                model_type,
            )
            cursor.execute("update t_small_model set f_default=1 where f_model_id=%s", model_id)


small_model_dao = SmallModelDao()
