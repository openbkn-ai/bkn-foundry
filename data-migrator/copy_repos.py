#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""从本地目录复制服务文件到 repos - 参考 executor.py 实现"""
import os
import shutil
import yaml
from typing import List, Optional


DEFAULT_DB_TYPE = "mariadb"


def is_version_dir(version_str: str) -> bool:
    """判断目录名是否为合法版本号"""
    try:
        [int(n) for n in version_str.split(".")]
        return True
    except (ValueError, AttributeError):
        return False


def load_config(config_path: str = "config.yaml") -> dict:
    """加载 YAML 配置文件"""
    with open(config_path, "r", encoding="utf-8") as f:
        return yaml.safe_load(f)


def copy_version_dirs(source_dir: str, dest_dir: str):
    """只复制版本号目录"""
    if not os.path.exists(source_dir):
        return

    for name in os.listdir(source_dir):
        if not is_version_dir(name):
            continue
        src = os.path.join(source_dir, name)
        dst = os.path.join(dest_dir, name)
        if os.path.exists(dst):
            shutil.rmtree(dst)
        shutil.copytree(src, dst, dirs_exist_ok=True)
        print(f"  复制版本目录: {name}")


def collect_repos(
    source_base_path: str,
    repo_output_path: str = "repos",
    config_path: str = "config.yaml",
    db_types: Optional[List[str]] = None,
):
    """
    复制数据库类型目录到 repos/

    Args:
        source_base_path: 源文件基础路径（如 adp 目录）
        repo_output_path: 输出目录路径
        config_path: 配置文件路径
        db_types: 数据库类型列表，默认从配置读取
    """
    cfg = load_config(config_path)
    services = cfg.get("services", {})

    if db_types is None:
        db_types = cfg.get("db_types", ["mariadb"])

    print(f"数据库类型: {db_types}")
    print(f"服务数量: {len(services)}")
    print("-" * 50)

    for service_name, service_cfg in services.items():
        db_path = service_cfg.get("path", "")

        print(f"\n处理服务: {service_name}, path={db_path}")

        # 构建源路径: source_base_path + service_path + db_type
        source_path = os.path.join(source_base_path, db_path)
        repo_path = os.path.join(repo_output_path, service_name)
        os.makedirs(repo_path, exist_ok=True)

        for db_type in db_types:
            source_db_path = os.path.join(source_path, db_type)

            if not os.path.isdir(source_db_path):
                print(f"  db_type({db_type})不存在，使用默认({DEFAULT_DB_TYPE})")
                source_db_path = os.path.join(source_path, DEFAULT_DB_TYPE)
                if not os.path.isdir(source_db_path):
                    print(f"  警告: 服务 {service_name} 缺少目录: {db_type}")
                    continue

            repo_db_path = os.path.join(repo_path, db_type)
            os.makedirs(repo_db_path, exist_ok=True)

            copy_version_dirs(source_db_path, repo_db_path)

    print(f"\n复制完成，输出目录: {os.path.abspath(repo_output_path)}")


if __name__ == "__main__":
    import argparse

    parser = argparse.ArgumentParser(description="从本地目录复制服务文件到 repos")
    parser.add_argument("source", help="源文件基础路径（如 adp 目录）")
    parser.add_argument("-o", "--output", default="repos", help="输出目录路径（默认: repos）")
    parser.add_argument("-c", "--config", default="config.yaml", help="配置文件路径（默认: config.yaml）")

    args = parser.parse_args()

    collect_repos(
        source_base_path=args.source,
        repo_output_path=args.output,
        config_path=args.config
    )
