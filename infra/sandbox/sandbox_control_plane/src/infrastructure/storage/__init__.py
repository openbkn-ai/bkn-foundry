"""
Storage module

The S3-compatible object storage implementation, for AWS S3 and MinIO.
"""

from .s3_storage import S3Storage

__all__ = ["S3Storage"]
