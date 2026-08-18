#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright openbkn.ai
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.

"""
Concept index deletion script.
Connects to OpenSearch and deletes the adp-kn_concept concept index.
"""

import os
import sys
import logging
import argparse
from typing import Dict, Optional
from opensearchpy import OpenSearch, RequestsHttpConnection

# Concept index name, kept consistent with the definition in server/interfaces/common.go.
CONCEPT_INDEX_NAME = "adp-kn_concept"

# Configure logging.
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(levelname)s - %(message)s",
    handlers=[logging.StreamHandler(sys.stdout)],
)
logger = logging.getLogger(__name__)


class ConceptIndexDeleter:
    """Concept index deleter."""

    def __init__(self, os_config: dict, dry_run: bool = False):
        """
        Initialize the deleter.

        Args:
            os_config: OpenSearch configuration.
            dry_run: Whether to run in dry-run mode without deleting the index.
        """
        self.os_config = os_config
        self.dry_run = dry_run
        self.os_client: Optional[OpenSearch] = None

    def connect_opensearch(self) -> bool:
        """Connect to OpenSearch."""
        try:
            self.os_client = OpenSearch(
                hosts=[
                    {"host": self.os_config["host"], "port": self.os_config["port"]}
                ],
                http_auth=(
                    self.os_config.get("user", ""),
                    self.os_config.get("password", ""),
                ),
                use_ssl=self.os_config.get("protocol", "http") == "https",
                verify_certs=False,
                ssl_show_warn=False,
                connection_class=RequestsHttpConnection,
                timeout=30,
            )

            # Test the connection.
            if self.os_client.ping():
                logger.info("OpenSearch连接成功")
                return True
            else:
                logger.error("OpenSearch连接失败: ping超时")
                return False
        except Exception as e:
            logger.error(f"OpenSearch连接失败: {e}")
            return False

    def get_index_info(self, index_name: str) -> Optional[Dict[str, any]]:
        """
        Get index information such as document count and storage size.

        Args:
            index_name: Index name.

        Returns:
            Index information, or None if the index does not exist.
        """
        try:
            if not self.os_client.indices.exists(index=index_name):
                return None

            # Get index statistics.
            stats = self.os_client.indices.stats(index=index_name)
            index_stats = stats.get("indices", {}).get(index_name, {})

            # Get index settings and mapping information.
            settings = self.os_client.indices.get_settings(index=index_name)
            index_settings = settings.get(index_name, {}).get("settings", {})

            # Extract document count and storage size.
            total = index_stats.get("total", {})
            docs_count = total.get("docs", {}).get("count", 0)
            store_size = total.get("store", {}).get("size_in_bytes", 0)

            return {
                "docs": docs_count,
                "size": store_size,
                "settings": index_settings,
            }
        except Exception as e:
            logger.error(f"获取索引信息失败: {e}")
            return None

    def delete_index(self, index_name: str) -> bool:
        """
        Delete the specified index.

        Args:
            index_name: Name of the index to delete.

        Returns:
            True if deletion succeeds; otherwise False.
        """
        try:
            if not self.os_client.indices.exists(index=index_name):
                logger.warning(f"索引不存在: {index_name}")
                return False

            if self.dry_run:
                logger.info(f"[DRY RUN] 将删除索引: {index_name}")
                return True

            response = self.os_client.indices.delete(index=index_name)
            if response.get("acknowledged", False):
                logger.info(f"成功删除索引: {index_name}")
                return True
            else:
                logger.warning(f"删除索引失败（未确认）: {index_name}")
                return False
        except Exception as e:
            logger.error(f"删除索引 {index_name} 失败: {e}")
            return False

    def delete_concept_index(self) -> bool:
        """Run the concept index deletion operation."""
        logger.info("=" * 60)
        logger.info("开始删除概念索引")
        logger.info(f"索引名称: {CONCEPT_INDEX_NAME}")
        logger.info(f"试运行模式: {'是' if self.dry_run else '否'}")
        logger.info("=" * 60)

        # Connect to OpenSearch.
        if not self.connect_opensearch():
            return False

        # Check whether the index exists.
        if not self.os_client.indices.exists(index=CONCEPT_INDEX_NAME):
            logger.warning(f"概念索引 {CONCEPT_INDEX_NAME} 不存在，无需删除")
            return True

        # Get index information.
        index_info = self.get_index_info(CONCEPT_INDEX_NAME)
        if index_info:
            logger.info("\n索引信息:")
            logger.info("-" * 60)
            logger.info(f"  索引名称: {CONCEPT_INDEX_NAME}")
            logger.info(f"  文档数: {index_info['docs']:,}")
            logger.info(f"  存储大小: {self._format_bytes(index_info['size'])}")
            logger.info("-" * 60)
        else:
            logger.warning("无法获取索引信息，将继续尝试删除")

        # Delete the index.
        logger.info(f"\n开始删除索引 {CONCEPT_INDEX_NAME}...")
        success = self.delete_index(CONCEPT_INDEX_NAME)

        # Output the result.
        logger.info("\n" + "=" * 60)
        if success:
            logger.info("删除操作完成")
        else:
            logger.error("删除操作失败")
        logger.info("=" * 60)

        return success

    def _format_bytes(self, bytes_size: int) -> str:
        """
        Format a byte size.

        Args:
            bytes_size: Size in bytes.

        Returns:
            Formatted string.
        """
        for unit in ["B", "KB", "MB", "GB", "TB"]:
            if bytes_size < 1024.0:
                return f"{bytes_size:.2f} {unit}"
            bytes_size /= 1024.0
        return f"{bytes_size:.2f} PB"

    def close(self):
        """Close the connection."""
        if self.os_client:
            # The OpenSearch client does not require an explicit close.
            logger.info("OpenSearch连接已关闭")


def load_config_from_env() -> dict:
    """
    Load configuration from environment variables.

    Returns:
        OpenSearch configuration dictionary.
    """
    os_config = {
        "host": os.getenv("OPENSEARCH_HOST", "localhost"),
        "port": int(os.getenv("OPENSEARCH_PORT", "9200")),
        "protocol": os.getenv("OPENSEARCH_PROTOCOL", "http"),
        "user": os.getenv("OPENSEARCH_USER", ""),
        "password": os.getenv("OPENSEARCH_PASSWORD", ""),
    }

    return os_config


def main():
    """Main function."""
    parser = argparse.ArgumentParser(
        description="概念索引删除工具 - 删除 OpenSearch 中的概念索引 adp-kn_concept"
    )
    parser.add_argument(
        "--dry-run", action="store_true", help="试运行模式，不实际删除索引"
    )
    parser.add_argument(
        "--os-host", help="OpenSearch主机地址（默认从环境变量OPENSEARCH_HOST读取）"
    )
    parser.add_argument(
        "--os-port",
        type=int,
        help="OpenSearch端口（默认从环境变量OPENSEARCH_PORT读取）",
    )
    parser.add_argument(
        "--os-user", help="OpenSearch用户名（默认从环境变量OPENSEARCH_USER读取）"
    )
    parser.add_argument(
        "--os-password", help="OpenSearch密码（默认从环境变量OPENSEARCH_PASSWORD读取）"
    )
    parser.add_argument(
        "--os-protocol",
        choices=["http", "https"],
        help="OpenSearch协议（默认从环境变量OPENSEARCH_PROTOCOL读取）",
    )

    args = parser.parse_args()

    # Load configuration.
    os_config = load_config_from_env()

    # Command-line arguments override environment variables.
    if args.os_host:
        os_config["host"] = args.os_host
    if args.os_port:
        os_config["port"] = args.os_port
    if args.os_user:
        os_config["user"] = args.os_user
    if args.os_password:
        os_config["password"] = args.os_password
    if args.os_protocol:
        os_config["protocol"] = args.os_protocol

    # Validate required configuration.
    if not os_config["password"]:
        logger.error(
            "OpenSearch密码未设置，请设置OPENSEARCH_PASSWORD环境变量或使用--os-password参数"
        )
        sys.exit(1)

    # Create the deleter and execute deletion.
    deleter = ConceptIndexDeleter(os_config=os_config, dry_run=args.dry_run)

    try:
        success = deleter.delete_concept_index()
        sys.exit(0 if success else 1)
    except KeyboardInterrupt:
        logger.info("\n用户中断操作")
        sys.exit(130)
    except Exception as e:
        logger.error(f"删除过程中发生错误: {e}", exc_info=True)
        sys.exit(1)
    finally:
        deleter.close()


if __name__ == "__main__":
    main()
