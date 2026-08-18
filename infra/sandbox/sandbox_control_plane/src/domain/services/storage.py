"""
Storage domain service interface

The storage abstraction: everything that touches stored files.
"""

from abc import ABC, abstractmethod
from typing import Dict, Optional


class IStorageService(ABC):
    """
    Storage service interface

    The port the domain layer defines; the infrastructure layer supplies the adapter.
    Talks to S3-compatible object storage.
    """

    @abstractmethod
    async def upload_file(
        self, s3_path: str, content: bytes, content_type: str = "application/octet-stream"
    ) -> None:
        """
        Upload a file

        Args:
            s3_path: S3 object path
            content: file content
            content_type: MIME type
        """
        pass

    @abstractmethod
    async def download_file(self, s3_path: str) -> bytes:
        """
        Download a file

        Args:
            s3_path: S3 object path

        Returns:
            The file content
        """
        pass

    @abstractmethod
    async def file_exists(self, s3_path: str) -> bool:
        """
        Check whether a file exists

        Args:
            s3_path: S3 object path

        Returns:
            Whether it exists
        """
        pass

    @abstractmethod
    async def get_file_info(self, s3_path: str) -> Dict:
        """
        Get the file metadata

        Args:
            s3_path: S3 object path

        Returns:
            A dict holding size, content_type, last_modified, and more
        """
        pass

    @abstractmethod
    async def generate_presigned_url(self, s3_path: str, expiration_seconds: int = 3600) -> str:
        """
        Generate a presigned URL

        Args:
            s3_path: S3 object path
            expiration_seconds: expiry in seconds

        Returns:
            The presigned URL
        """
        pass

    @abstractmethod
    async def delete_file(self, s3_path: str) -> None:
        """
        Delete a file

        Args:
            s3_path: S3 object path
        """
        pass

    @abstractmethod
    async def list_files(self, prefix: str, limit: int = 1000) -> list:
        """
        List files

        Args:
            prefix: S3 path prefix
            limit: how many to return at most

        Returns:
            The file list
        """
        pass

    @abstractmethod
    async def delete_prefix(self, prefix: str) -> int:
        """
        Delete every file under a prefix, used when cleaning up a session

        Args:
            prefix: S3 path prefix, such as "sessions/sess_abc123/"

        Returns:
            How many files were deleted
        """
        pass
