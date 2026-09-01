import datetime
import json

from app.commons.snow_id import worker
from app.dao import user_info
from app.interfaces import dbaccess
from app.logs.stand_log import StandLogger
from app.mydb.my_pymysql_pool import (
    advisory_lock_transaction,
    connect_execute_close_db,
    connect_execute_commit_close_db,
)


class ModelDao():
    # Resolve a model name by model ID.
    @connect_execute_close_db
    def get_model_name_by_id(self, model_id, connection, cursor):
        sql = "select f_model_name from t_llm_model where f_model_id={}".format(model_id)

        cursor.execute(sql)
        res = cursor.fetchall()
        return res[0]["f_model_name"]

    @connect_execute_close_db
    def get_model_id_by_name(self, model_name, connection, cursor):
        sql = "select f_model_id from t_llm_model where f_model_name=%s"
        cursor.execute(sql, model_name)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_model_series_by_id(self, model_id, connection, cursor):
        sql = "select f_model_series from t_llm_model where f_model_id={}".format(model_id)

        cursor.execute(sql)
        res = cursor.fetchall()
        return res[0]["f_model_series"]

    @connect_execute_close_db
    def get_model_model_from_model_list_by_id(self, model_id, connection, cursor):
        sql = "select f_model from t_llm_model where f_model_id={}".format(model_id)

        cursor.execute(sql)
        res = cursor.fetchall()
        return res[0]["f_model"]

    @connect_execute_close_db
    def get_all_model_list(self, connection, cursor):
        sql = """select f_create_by,f_create_time,f_model,
                       f_model_config,f_model_id,f_model_name,f_model_series,f_model_type,
                       f_update_by,f_update_time from t_llm_model"""

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_model_info_by_ids(self, model_ids, connection, cursor):
        placeholders = ','.join(['%s'] * len(model_ids))
        sql = f"""select f_model_id,f_model_name from t_llm_model where f_model_id IN ({placeholders})"""
        cursor.execute(sql, model_ids)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_data_from_model_list_by_id(self, model_id, connection, cursor):
        sql = """select f_create_by,f_create_time,f_model,f_model_config,f_model_id,
                            f_model_name,f_model_series,f_model_type,f_update_by,f_update_time,
                            f_max_model_len, f_model_parameters,f_model_type,f_quota 
                            from t_llm_model where f_model_id='{}'""".format(model_id)

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_data_from_model_list_by_name(self, model_name, connection, cursor):
        sql = """select f_create_by,f_model,f_model_config,f_model_id,f_quota,
                                f_model_name,f_model_series,f_model_type,f_update_by,f_max_model_len, f_model_parameters 
                                from t_llm_model where f_model_name='{}'""".format(model_name)

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_commit_close_db
    def add_data_into_model_list(self, model_id, model_series, model_type, model_name, model, userId, model_config,
                                 max_model_len, model_parameters, quota, connection, cursor):
        if quota is True:
            quota = 1
        else:
            quota = 0
        if model_parameters != None:
            sql = """insert into t_llm_model(f_model_id,f_model_series,f_model_type,f_model_name,f_model,
                                        f_create_by,f_update_by,f_model_config,f_update_time,f_create_time,
                                         f_max_model_len, f_model_parameters, f_quota) values(
                                        '{}','{}','{}','{}','{}','{}','{}','{}','{}','{}', '{}', '{}', '{}')""".format(
                model_id, model_series, model_type, model_name,
                model, userId, userId, model_config,
                datetime.datetime.today(),
                datetime.datetime.today(),
                max_model_len, model_parameters, quota)
        else:
            sql = """insert into t_llm_model(f_model_id,f_model_series,f_model_type,f_model_name,f_model,
                                                    f_create_by,f_update_by,f_model_config,f_update_time,f_create_time,
                                                      f_max_model_len, f_quota) values(
                                                    '{}','{}','{}','{}','{}','{}','{}','{}','{}','{}', '{}', '{}'
                                                    )""".format(model_id, model_series, model_type,
                                                                model_name, model, userId, userId,
                                                                model_config,
                                                                datetime.datetime.today(),
                                                                datetime.datetime.today(),
                                                                max_model_len, quota)

        cursor.execute(sql)

    def add_data_with_default(self, model_id, model_series, model_type, model_name, model, userId, model_config,
                              max_model_len, model_parameters, quota, requested_default):
        """Create a model and select its effective default in one transaction."""
        with advisory_lock_transaction("mf-model-default:llm") as cursor:
            cursor.execute("select f_model_id from t_llm_model where f_default=1")
            has_default = bool(cursor.fetchall())
            is_default = requested_default or not has_default

            if requested_default and has_default:
                cursor.execute("update t_llm_model set f_default=0 where f_default=1")

            sql = """insert into t_llm_model(
                f_model_id,f_model_series,f_model_type,f_model_name,f_model,
                f_create_by,f_update_by,f_model_config,f_update_time,f_create_time,
                f_max_model_len,f_model_parameters,f_quota,f_default
            ) values(%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)"""
            cursor.execute(sql, (
                model_id, model_series, model_type, model_name, model, userId, userId, model_config,
                datetime.datetime.today(), datetime.datetime.today(), max_model_len, model_parameters,
                1 if quota else 0, 1 if is_default else 0,
            ))
            return is_default

    @connect_execute_commit_close_db
    def delete_model_by_id(self, model_ids, connection, cursor):
        # Use a parameterized query.
        placeholders = ','.join(['%s'] * len(model_ids))
        sql = f"DELETE FROM t_llm_model WHERE f_model_id IN ({placeholders})"
        cursor.execute(sql, model_ids)

    @connect_execute_commit_close_db
    def edit_model(self, model_id, re_name, userId, max_model_len, model_parameters, quota, model_config,
                   model_series, model_type, connection, cursor):
        if model_parameters != None:
            sql = """update t_llm_model set f_model_name='{}',f_update_time='{}',f_update_by='{}', f_max_model_len='{}', f_model_parameters='{}',
                     f_quota={},f_model_config='{}',f_model_type='{}',f_model_series='{}' where f_model_id='{}'""".format(
                re_name, datetime.datetime.today(), userId,
                max_model_len, model_parameters, quota, model_config, model_type, model_series, model_id)
        else:
            sql = """update t_llm_model set f_model_name='{}',f_update_time='{}',f_update_by='{}', f_max_model_len='{}', f_model_parameters={},
            f_quota={},f_model_config='{}',f_model_type='{}',f_model_series='{}' where f_model_id='{}'""".format(
                re_name, datetime.datetime.today(),
                userId, max_model_len, "null", quota, model_config, model_type, model_series, model_id)

        cursor.execute(sql)

    @connect_execute_close_db
    def get_data_from_model_list_by_name_fuzzy(self, name, page, size, order, rule, api_model, model_type,
                                               connection, cursor):
        where_sql, value_list = self._fuzzy_model_filters(name, api_model, model_type)
        sql = """select f_create_by,f_create_time,f_model,f_model_config,f_model_id,
                                     f_model_name,f_model_series,f_model_type,f_update_by,f_update_time,
                                      f_max_model_len, f_model_parameters,f_quota,f_default from
                                     t_llm_model where """ + where_sql
        if rule != "":
            sql += " order by f_" + rule
        if order == "desc":
            sql += " desc"
        sql += " limit {},{}".format(str((int(page) - 1) * int(size)), str(size))
        cursor.execute(sql, value_list)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def count_data_from_model_list_by_name_fuzzy(self, name, api_model, model_type, connection, cursor):
        """Count the same result set as the paginated administrator list."""
        where_sql, value_list = self._fuzzy_model_filters(name, api_model, model_type)
        cursor.execute("select count(*) as total from t_llm_model where " + where_sql, value_list)
        return cursor.fetchall()[0]["total"]

    @staticmethod
    def _fuzzy_model_filters(name, api_model, model_type):
        name = "%" + name + "%"
        where_sql = "(f_model_name like %s or f_model like %s or f_model_config like %s)"
        value_list = [name, name, name]
        if api_model != "":
            where_sql += " and f_model = %s"
            value_list.append(api_model)
        if model_type != "":
            where_sql += " and f_model_type = %s"
            value_list.append(model_type)
        return where_sql, value_list

    @connect_execute_close_db
    def get_data_from_model_list_by_name_fuzzy_and_series(self, name, series, page, size, order, rule,
                                                          api_model, model_type, connection, cursor):
        name = "%" + name + "%"
        sql = """select f_create_by,f_create_time,f_model,f_model_config,f_model_id,
                                      f_model_name,f_model_series,f_model_type,f_update_by,f_update_time,
                                      f_max_model_len, f_model_parameters from
                                      t_llm_model where f_model_series='{}'
                                      and (f_model_name like %s or f_model like %s or f_model_config like %s)""".format(
            series)
        value_list = [name, name, name]
        if api_model != "":
            sql += " and f_model = '%s' " % api_model
        if model_type != "":
            sql += " and f_model_type = '%s' " % model_type
        if rule != "":
            sql += " order by f_" + rule
        if order == "desc":
            sql += " desc"
        sql += " limit {},{}".format(str((int(page) - 1) * int(size)), str(size))
        cursor.execute(sql, value_list)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_all_data_from_model_param(self, connection, cursor):
        sql = """select f_box_component,f_box_lab_cn,f_box_lab_us,f_box_mark_cn,f_box_mark_us,f_max,
                            f_max_mes_cn,f_max_mes_us,f_param_field,f_param_id,f_param_type,f_pat_mes_cn,f_pat_mes_us,
                            f_pattern,f_req,f_req_mes_cn,f_req_mes_us from t_model_param"""

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_data_from_model_param_by_param_id(self, param_id, connection, cursor):
        sql = """select f_box_component,f_box_lab_cn,f_box_lab_us,f_box_mark_cn,f_box_mark_us,f_max,
                            f_max_mes_cn,f_max_mes_us,f_param_field,f_param_id,f_param_type,f_pat_mes_cn,f_pat_mes_us,
                            f_pattern,f_req,f_req_mes_cn,f_req_mes_us from t_model_param
                            where f_param_id='{}'""".format(param_id)

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_model_by_name(self, model_name, connection, cursor):
        sql = """select f_create_by,f_create_time,f_model,
                       f_model_config,f_model_id,f_model_name,f_model_series,f_model_type,
                       f_update_by,f_update_time, f_max_model_len, f_model_parameters 
                from t_llm_model where f_model_name=%s;"""

        cursor.execute(sql, model_name)
        res = cursor.fetchall()
        return res

    # Check whether a model is already bound.
    @connect_execute_close_db
    def check_model_is_exist(self, model_id, connection, cursor):
        sql = """select count(f_model_id) from t_llm_model where f_model_id = %s"""
        cursor.execute(sql, model_id)
        res = cursor.fetchall()
        if "COUNT(f_model_id)" in res[0].keys():
            res[0]["count(f_model_id)"] = res[0]["COUNT(f_model_id)"]
        if res[0]["count(f_model_id)"] > 0:
            return True
        return False

    # Check for an existing model with the same provider base, model, and API key.
    @connect_execute_close_db
    def check_model_unique(self, base, model, user_id, api_key, connection, cursor):
        sql = """select f_model_id, f_model_config from t_llm_model where f_model='{}'""".format(
            model)

        cursor.execute(sql)
        models = cursor.fetchall()
        for item in models:
            model_config = json.loads(item["f_model_config"])
            if "api_base" in model_config.keys() and model_config["api_base"] == base and \
                    (api_key is None or ("api_key" in model_config.keys() and model_config["api_key"] == api_key)):
                return True
            if "api_url" in model_config.keys() and model_config["api_url"] == base and \
                    (api_key is None or ("api_key" in model_config.keys() and model_config["api_key"] == api_key)):
                return True
        return False

    @connect_execute_close_db
    def find_model_by_unique_config(self, base, model, api_key, connection, cursor):
        """Return safe identity fields for a duplicate LLM configuration, if any."""
        cursor.execute(
            """select f_model_id, f_model_name, f_model_type, f_model_config, f_default
               from t_llm_model where f_model=%s""",
            model,
        )
        for item in cursor.fetchall():
            model_config = json.loads(item["f_model_config"])
            configured_base = model_config.get("api_base", model_config.get("api_url"))
            if configured_base == base and (
                    api_key is None or model_config.get("api_key") == api_key):
                return {
                    "id": str(item["f_model_id"]),
                    "name": item["f_model_name"],
                    "type": item["f_model_type"],
                    "default": item["f_default"] in (1, True, "1"),
                }
        return None

    @connect_execute_close_db
    def get_model_default_paras(self, connection, cursor):
        sql = """select f_model_id,f_model_name,f_model_series,f_model from t_llm_model"""

        cursor.execute(sql)
        res = cursor.fetchall()
        model_id_list = []
        for item in res:
            model_id_list.append(item["f_model_id"])
        res_dict = {}
        for item in res:
            res_dict[item["f_model_id"]] = {"model_name": item["f_model_name"], "model_series": item["f_model_series"],
                                            "model": item["f_model"]}
        return res_dict

    @connect_execute_commit_close_db
    def add_llm_model_call_log(self, config: dbaccess.ModelUsedAuditInfo, connection, cursor):
        call_id = worker.get_id()
        sql = """insert into t_llm_model_used (f_model_id, f_model_id, f_user_id, f_input_tokens, f_output_tokens,
                f_create_time) values(%s, %s, %s, %s, %s, %s)"""
        value_list = [call_id, config.model_id, config.user_id, config.input_tokens, config.output_tokens,
                      config.create_time]
        cursor.execute(sql, value_list)

    @connect_execute_close_db
    def get_all_tome_model_list(self, connection, cursor):
        sql = """select f_model_config,f_model_id,f_model_name from t_llm_model where f_model_series = 'tome'"""

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_close_db
    def get_ten_minutes_ago_monitor_data(self, model_ids, ten_minutes_ago_format, connection, cursor):
        sql = f"""select f_model_id, f_generation_tokens_total,f_prompt_tokens_total from t_model_monitor where f_create_time='{ten_minutes_ago_format}'
        and f_model_id in ({",".join(model_ids)})"""

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_commit_close_db
    def add_monitor_data(self, datas, connection, cursor):
        if not datas:
            return

        sql = """insert into t_model_monitor(f_id,f_create_time,f_model_name,f_model_id,f_generation_tokens_total,
        f_prompt_tokens_total,f_average_first_token_time,f_generation_token_speed,f_total_token_speed) values (%s, %s, %s, %s, %s, %s, %s, %s, %s)"""

        # Keep the query execution path consistent with other monitor reads.

        cursor.executemany(sql, datas)

    @connect_execute_commit_close_db
    def get_monitor_data(self, model_id, connection, cursor):
        sql = f"""select f_create_time,f_model_id, f_average_first_token_time,f_generation_token_speed,f_total_token_speed 
        from t_model_monitor where  f_model_id = {model_id} order by f_create_time desc limit 36"""

        # Keep the query execution path consistent with other monitor reads.

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_commit_close_db
    def delete_model_monitor_data(self, connection, cursor):
        now = datetime.datetime.now()
        thirty_days_ago = now - datetime.timedelta(days=30)

        # Use a parameterized query.
        sql = "DELETE FROM t_model_monitor WHERE f_create_time < %s"

        cursor.execute(sql, thirty_days_ago)

    @connect_execute_close_db
    def get_quota_by_user_and_model(self, user_id, model_id, connection, cursor):
        # Start of the current month.
        now_month = datetime.datetime.now().replace(day=1).strftime('%Y-%m-%d')
        sql = f"""SELECT
                    uqc.f_input_tokens, COALESCE(monthly_usage.used_input_tokens,0) AS used_input_tokens,mqc.f_billing_type,
                    uqc.f_output_tokens,COALESCE(monthly_usage.used_output_tokens,0) AS used_output_tokens,uqc.f_num_type
                FROM
                    t_model_quota_config mqc join t_user_quota_config uqc on uqc.f_model_conf=mqc.f_id
                LEFT JOIN (
                    SELECT
                        f_user_id,
                        f_model_id,
                        SUM(f_input_tokens) AS used_input_tokens,
                        SUM(f_output_tokens) AS used_output_tokens
                    FROM
                        t_model_op_detail
                    WHERE
                        f_user_id = '{user_id}'
                        AND f_model_id = '{model_id}'
                        AND f_create_time >= '{now_month}'
                    GROUP BY
                        f_user_id, f_model_id
                ) AS monthly_usage
                ON
                    uqc.f_user_id = monthly_usage.f_user_id
                    AND mqc.f_model_id = monthly_usage.f_model_id
                WHERE
                    uqc.f_user_id = '{user_id}'
                    AND mqc.f_model_id = '{model_id}';"""

        cursor.execute(sql)
        res = cursor.fetchall()

        # Return immediately when no quota is configured.
        if not res:
            return res

        # Unit multipliers aligned with model_used_audit_dao.py.
        num_type_list = [0, 1000, 10000, 100000000, 1000000, 10000000]

        # Read quota configuration.
        quota_info = res[0]
        f_billing_type = quota_info["f_billing_type"]
        f_num_type = json.loads(quota_info["f_num_type"])

        # Configured quota values before unit conversion.
        config_input_tokens = quota_info["f_input_tokens"]
        config_output_tokens = quota_info["f_output_tokens"]

        # Quota already consumed this month.
        used_input_tokens = quota_info["used_input_tokens"]
        used_output_tokens = quota_info["used_output_tokens"]

        # Apply the configured unit multipliers.
        total_input_tokens = int(config_input_tokens * num_type_list[f_num_type[0]])
        total_output_tokens = int(config_output_tokens * num_type_list[f_num_type[1]])

        # Calculate remaining quota.
        if f_billing_type == 1:
            # Track input and output quotas separately.
            remaining_input_tokens = total_input_tokens - used_input_tokens
            remaining_output_tokens = total_output_tokens - used_output_tokens
        else:
            # Deduct combined usage from the shared input quota.
            total_used_tokens = used_input_tokens + used_output_tokens
            remaining_input_tokens = total_input_tokens - total_used_tokens
            remaining_output_tokens = True

        # Add calculated remaining values to the result.
        res[0]["remaining_input_tokens"] = remaining_input_tokens
        res[0]["remaining_output_tokens"] = remaining_output_tokens

        # Preserve the converted configured totals.
        res[0]["total_input_tokens"] = total_input_tokens
        res[0]["total_output_tokens"] = total_output_tokens

        return res

    @connect_execute_close_db
    def get_default_model(self, connection, cursor):
        sql = """select f_model_id,f_default from t_llm_model where f_default=1"""

        cursor.execute(sql)
        res = cursor.fetchall()
        return res

    @connect_execute_commit_close_db
    def update_model_default_status(self, model_id, is_default, connection, cursor):
        """Update a model's default status."""
        sql = """update t_llm_model set f_default=%s where f_model_id=%s"""
        cursor.execute(sql, (1 if is_default else 0, model_id))

    def set_default_model(self, model_id):
        """Atomically make a model the sole large-model default."""
        with advisory_lock_transaction("mf-model-default:llm") as cursor:
            cursor.execute("update t_llm_model set f_default=0 where f_default=1")
            cursor.execute("update t_llm_model set f_default=1 where f_model_id=%s", model_id)

    @connect_execute_commit_close_db
    def get_overview_data(self, model_id, start_time, end_time, userId, connection, cursor):
        sql1 = f"""SELECT
                SUM(d.f_total_count) AS total_usage,
                CASE
                    WHEN SUM(d.f_total_count) = 0 THEN 0
                    ELSE (SUM(d.f_failed_count) / SUM(d.f_total_count))
                END AS error_rate,
                CASE
                    WHEN SUM(d.f_total_count) = 0 THEN 0
                    ELSE SUM(d.f_average_total_time * d.f_total_count) / SUM(d.f_total_count)
                END AS avg_response_time,
                SUM(d.f_input_tokens + d.f_output_tokens) AS total_tokens,
                SUM(d.f_input_tokens) AS input_tokens,
                SUM(d.f_output_tokens) AS output_tokens
            FROM t_model_op_detail d
            INNER JOIN t_llm_model m ON d.f_model_id = m.f_model_id
            WHERE DATE(d.f_create_time) BETWEEN '{start_time}' AND '{end_time}'
            """
        if model_id:
            sql1 += f" and d.f_model_id='{model_id}'"
        if userId != "266c6a42-6131-4d62-8f39-853e7093701c":
            sql1 += f" and d.f_user_id='{userId}'"
        cursor.execute(sql1)
        core_metrics = cursor.fetchall()
        sql2 = f"""
            SELECT
            DATE(d.f_create_time) AS date_group,
            SUM(d.f_input_tokens) AS input_tokens,
            SUM(d.f_output_tokens) AS output_tokens,
            CASE
                WHEN SUM(d.f_total_count) = 0 THEN 0
                ELSE SUM(d.f_average_total_time * d.f_total_count) / SUM(d.f_total_count)
            END AS avg_total_time,
            CASE
                WHEN SUM(d.f_total_count) = 0 THEN 0
                ELSE SUM(d.f_average_first_time * d.f_total_count) / SUM(d.f_total_count)
            END AS avg_first_time,
            SUM(d.f_input_tokens + d.f_output_tokens) / 86400.0 AS avg_rate,
            ROUND(SUM(d.f_total_count) / 86400.0, 6) AS avg_qps
        FROM t_model_op_detail d
        INNER JOIN t_llm_model m ON d.f_model_id = m.f_model_id
        WHERE DATE(d.f_create_time) BETWEEN '{start_time}' AND '{end_time}'"""
        if model_id:
            sql2 += f" and d.f_model_id='{model_id}'"
        if userId != "266c6a42-6131-4d62-8f39-853e7093701c":
            sql2 += f" and d.f_user_id='{userId}'"
        sql2 += " GROUP BY DATE(d.f_create_time) ORDER BY date_group"
        cursor.execute(sql2)
        trend_analysis = cursor.fetchall()
        sql3 = f"""SELECT DATE_FORMAT(d.f_create_time, '%Y-%m-%d %H:%i:00') AS date_group,ROUND(SUM(d.f_total_count) / 300, 6) AS avg_qps
                  FROM t_model_op_detail d
                  INNER JOIN t_llm_model m ON d.f_model_id = m.f_model_id
                  WHERE DATE(d.f_create_time) BETWEEN '{start_time}' AND '{end_time}'"""
        if model_id:
            sql3 += f" and d.f_model_id='{model_id}'"
        if userId != "266c6a42-6131-4d62-8f39-853e7093701c":
            sql3 += f" and d.f_user_id='{userId}'"
        sql3 += " GROUP BY DATE_FORMAT(d.f_create_time, '%Y-%m-%d %H:%i:00') ORDER BY date_group desc limit 96"
        cursor.execute(sql3)
        qps_analysis = cursor.fetchall()
        return core_metrics, trend_analysis, qps_analysis


llm_model_dao = ModelDao()
