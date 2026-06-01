#!/usr/bin/env python3
# -*- coding: utf-8 -*-
import os

import rdsdriver


def get_conn(user, password, host, port, database):
    """获取数据库连接，支持多租户模式"""
    try:

        conn = rdsdriver.connect(host=host,
                                 port=int(port),
                                 user=user,
                                 password=password,
                                 database=database,
                                 autocommit=False)
    except Exception as e:
        print(f"connect database error: {str(e)}")
        raise e
    return conn


def migrate_data_source(conn):
    """执行数据源迁移"""
    conn.begin()
    cursor = conn.cursor()
    # 判断t_table是否存在
    try:
        cursor.execute("SHOW TABLES FROM adp LIKE 't_table'")
        t_table_exist = cursor.fetchall()
        if not t_table_exist:
            print("adp.t_table不存在!退出！")
            return
    except Exception as e:
        raise e
    # 判断t_table_field是否存在
    try:
        cursor.execute("SHOW TABLES FROM adp LIKE 't_table_field'")
        t_table_exist = cursor.fetchall()
        if not t_table_exist:
            print("adp.t_table_field不存在!退出！")
            return
    except Exception as e:
        raise e
    try:
        is_have_data_sql = """
            select * from adp.t_table where f_data_source_type_name in ('mysql','maria','oracle') limit 1
        """
        cursor.execute(is_have_data_sql)
        result2 = cursor.fetchall()
        if not result2:
            print("==========没有需要迁移的数据，结束==========")
            return
        result1 = cursor.fetchall()
        if result1:
            print("====成功执行探测sql:已经执行过迁移，不需要重复迁移，结束===")
        if not result1:
            print("====================下面开始执行数据迁移==========================")
            insert_table_sql = """
        insert into adp.t_table_scan(
    f_id,
	f_name,
	f_advanced_params,
	f_description,
	f_table_rows,
	f_data_source_id,
	f_data_source_name,
	f_schema_name,
	f_task_id,
	f_version,
	f_create_time,
	f_create_user,
	f_operation_time,
	f_operation_user,
	f_operation_type,
	f_status,
	f_status_change,
	f_scan_source
)
select
	f_id,
	f_name,
	f_advanced_params,
	f_description,
	f_table_rows,
	a.f_data_source_id as ds_id,
	f_data_source_name,
	f_schema_name,
	b.task_id,
	1,
	f_create_time,
	f_create_user,
	f_update_time,
	f_update_user,
	IF(f_delete_time is null, 0, 1) AS f_operation_type,
	2,
	1,
	f_scan_source
from
	(select * from adp.t_table where f_data_source_type_name in ('mysql','maria','oracle')) a
left join
(
select a.ds_id,a.id as task_id
from
	(select * from adp.t_task_scan where scan_status = 2)a
inner join
    (select ds_id, max(start_time) as max_start_time
		from adp.t_task_scan
		where scan_status = 2
		group by ds_id
	)t
 on a.ds_id = t.ds_id and a.start_time = t.max_start_time
)b
on a.f_data_source_id = b.ds_id;
        """
            cursor.execute(insert_table_sql)
            insert_field_sql = """
        insert into  adp.t_table_field_scan
(f_id,
	f_field_name,
	f_table_id,
	f_table_name,
	f_field_type,
	f_field_length,
	f_field_precision,
	f_field_comment,
	f_field_order_no,
	f_advanced_params,
	f_version,
	f_create_time,
	f_create_user,
	f_operation_time,
	f_operation_user,
	f_operation_type,
	f_status_change
)
select
    uuid(),
	f_field_name,
	f_table_id,
	f_name as f_table_name,
	f_field_type,
	f_field_length,
	f_field_precision,
	f_field_comment,
	null as f_field_order_no,
	f_advanced_params,
	1 as f_version,
	f_update_time as f_create_time,
	'' as f_create_user,
	f_update_time as f_create_time,
	'' as f_create_user,
    IF(f_delete_time is null, 0, 1) AS f_operation_type,
    1 as f_status_change
from
(select * from adp.t_table_field)t1
join
(select f_id ,f_name  from adp.t_table where f_data_source_type_name in ('mysql','maria','oracle'))t2
on t1.f_table_id=t2.f_id;
        """
            cursor.execute(insert_field_sql)
            delete_field_sql = """
                delete from adp.t_table_field
            where f_table_id in ( select f_id from adp.t_table where f_data_source_type_name in ('mysql','maria','oracle'))
            """
            delete_table_sql = """
                delete from adp.t_table where f_data_source_type_name in ('mysql','maria','oracle')
            """
            cursor.execute(delete_field_sql)
            cursor.execute(delete_table_sql)
            conn.commit()
            print("====================数据迁移成功结束==========================")
    except Exception as e:
        conn.rollback()
        raise Exception(f"数据迁移失败: {str(e)}")
    finally:
        cursor.close()


if __name__ == "__main__":
    try:
        # 从环境变量获取数据库连接信息
        conn = get_conn(os.environ["DB_USER"],
                        os.environ["DB_PASSWD"],
                        os.environ["DB_HOST"],
                        os.environ["DB_PORT"],
                        "adp")
        migrate_data_source(conn)
    except Exception as e:
        print(f"执行过程中发生错误: {str(e)}")
        raise e
    finally:
        if 'conn' in locals() and conn:
            conn.close()