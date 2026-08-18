"""
S3 storage implementation

S3-compatible object storage through boto3, for both AWS S3 and MinIO.
"""

import asyncio
import logging
import os
from typing import Optional
from urllib.parse import urlparse

import boto3
from botocore.exceptions import ClientError

from src.domain.services.storage import IStorageService
from src.infrastructure.config.settings import get_settings

logger = logging.getLogger(__name__)


class S3Storage(IStorageService):
    """
    S3-compatible storage implementation

    Supports:
    - AWS S3
    - MinIO, for local development
    - any S3-compatible storage, such as Wasabi or DigitalOcean Spaces
    """

    def __init__(self):
        """
        Initialize the S3 client

        Reads the S3 configuration from settings:
        - s3_endpoint_url: S3 endpoint URL, used by MinIO
        - s3_access_key_id: access key id
        - s3_secret_access_key: secret key
        - s3_region: region
        - s3_bucket: bucket name
        """
        settings = get_settings()

        # Initialize the S3 client
        self._client = boto3.client(
            "s3",
            endpoint_url=settings.s3_endpoint_url or None,  # AWS S3 needs no endpoint_url
            aws_access_key_id=settings.s3_access_key_id,
            aws_secret_access_key=settings.s3_secret_access_key,
            region_name=settings.s3_region,
        )
        self._bucket = settings.s3_bucket

    async def initialize(self) -> None:
        """
        Initialize asynchronously, making sure the bucket exists

        Called while the control plane starts, to ensure the S3 bucket is created.
        """
        try:
            await self._ensure_bucket_exists()
            logger.info(f"S3 storage initialized successfully (bucket: {self._bucket})")
        except Exception as e:
            logger.error(f"Failed to initialize S3 storage: {e}")
            # Do not raise: the system keeps running when MinIO is unavailable.
            # File operations will fail, but the control plane still starts.

    def _parse_s3_path(self, s3_path: str) -> tuple[str, str]:
        """
        Parse an S3 path into bucket and key

        Two formats are accepted:
        1. s3://bucket/key
        2. bucket/key, a relative path

        Args:
            s3_path: S3 object path

        Returns:
            A (bucket, key) tuple
        """
        if s3_path.startswith("s3://"):
            parsed = urlparse(s3_path)
            bucket = parsed.netloc
            key = parsed.path.lstrip("/")
        else:
            # A relative path: use the default bucket
            bucket = self._bucket
            key = s3_path.lstrip("/")

        return bucket, key

    def _build_s3_path(self, bucket: str, key: str) -> str:
        """
        Build an S3 path

        Args:
            bucket: bucket name
            key: object key

        Returns:
            The S3 path, in s3://bucket/key form
        """
        return f"s3://{bucket}/{key}"

    async def _ensure_bucket_exists(self) -> None:
        """Make sure the bucket exists, creating it when it does not"""
        try:
            await asyncio.to_thread(self._client.head_bucket, Bucket=self._bucket)
        except ClientError as e:
            error_code = e.response.get("Error", {}).get("Code")
            if error_code == "404":
                # The bucket does not exist, so create it
                try:
                    if self._client.meta.region_name == "us-east-1":
                        # us-east-1 needs no LocationConstraint
                        await asyncio.to_thread(self._client.create_bucket, Bucket=self._bucket)
                    else:
                        await asyncio.to_thread(
                            self._client.create_bucket,
                            Bucket=self._bucket,
                            CreateBucketConfiguration={
                                "LocationConstraint": self._client.meta.region_name
                            },
                        )
                    logger.info(f"Created S3 bucket: {self._bucket}")
                except ClientError as create_error:
                    logger.error(f"Failed to create bucket {self._bucket}: {create_error}")
                    raise
            else:
                logger.error(f"Error checking bucket {self._bucket}: {e}")
                raise

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
        await self._ensure_bucket_exists()

        bucket, key = self._parse_s3_path(s3_path)

        # Pick the upload method by size
        content_size = len(content)

        if content_size > 5 * 1024 * 1024:  # over 5MB, use a multipart upload
            from boto3.s3.transfer import TransferConfig

            # Write to a temporary file first
            import tempfile

            with tempfile.NamedTemporaryFile(delete=False) as tmp_file:
                tmp_file.write(content)
                tmp_file_path = tmp_file.name

            try:
                config = TransferConfig(multipart_threshold=5 * 1024 * 1024, max_concurrency=4)
                await asyncio.to_thread(
                    self._client.upload_file,
                    Filename=tmp_file_path,
                    Bucket=bucket,
                    Key=key,
                    ExtraArgs={"ContentType": content_type},
                    Config=config,
                )
            finally:
                os.unlink(tmp_file_path)
        else:
            # Upload a small file directly
            await asyncio.to_thread(
                self._client.put_object,
                Bucket=bucket,
                Key=key,
                Body=content,
                ContentType=content_type,
            )

        # Remove the directory marker if one appeared, an s3fs compatibility fix.
        # Uploading test/test_data.csv can make S3 create a test/ directory marker,
        # which makes s3fs show test as a file rather than a directory.
        if "/" in key:
            dir_marker = key.rsplit("/", 1)[0] + "/"
            try:
                await asyncio.to_thread(self._client.head_object, Bucket=bucket, Key=dir_marker)
                # The marker exists, so delete it
                await asyncio.to_thread(self._client.delete_object, Bucket=bucket, Key=dir_marker)
                logger.debug(f"Removed S3 directory marker for s3fs compatibility: {dir_marker}")
            except ClientError as e:
                error_code = e.response.get("Error", {}).get("Code")
                if error_code == "404":
                    # No marker, nothing to do
                    pass

        logger.debug(f"Uploaded file to {s3_path}, size={content_size}")

    async def download_file(self, s3_path: str) -> bytes:
        """
        Download a file

        Args:
            s3_path: S3 object path

        Returns:
            The file content
        """
        bucket, key = self._parse_s3_path(s3_path)

        response = await asyncio.to_thread(self._client.get_object, Bucket=bucket, Key=key)

        content = response["Body"].read()
        logger.debug(f"Downloaded file from {s3_path}, size={len(content)}")

        return content

    async def file_exists(self, s3_path: str) -> bool:
        """
        Check whether a file exists

        Args:
            s3_path: S3 object path

        Returns:
            Whether it exists
        """
        bucket, key = self._parse_s3_path(s3_path)

        try:
            await asyncio.to_thread(self._client.head_object, Bucket=bucket, Key=key)
            return True
        except ClientError as e:
            error_code = e.response.get("Error", {}).get("Code")
            if error_code == "404":
                return False
            logger.error(f"Error checking file existence {s3_path}: {e}")
            raise

    async def get_file_info(self, s3_path: str) -> dict:
        """
        Get the file metadata

        Args:
            s3_path: S3 object path

        Returns:
            A dict holding size, content_type, last_modified, and more
        """
        bucket, key = self._parse_s3_path(s3_path)

        response = await asyncio.to_thread(self._client.head_object, Bucket=bucket, Key=key)

        return {
            "size": response["ContentLength"],
            "content_type": response.get("ContentType", "application/octet-stream"),
            "last_modified": response["LastModified"],
            "etag": response["ETag"].strip('"'),
        }

    async def generate_presigned_url(self, s3_path: str, expiration_seconds: int = 3600) -> str:
        """
        Generate a presigned URL

        Args:
            s3_path: S3 object path
            expiration_seconds: expiry in seconds

        Returns:
            The presigned URL
        """
        bucket, key = self._parse_s3_path(s3_path)

        url = await asyncio.to_thread(
            self._client.generate_presigned_url,
            "get_object",
            Params={"Bucket": bucket, "Key": key},
            ExpiresIn=expiration_seconds,
        )

        logger.debug(f"Generated presigned URL for {s3_path}, expires in {expiration_seconds}s")

        return url

    async def delete_file(self, s3_path: str) -> None:
        """
        Delete a file

        Args:
            s3_path: S3 object path
        """
        bucket, key = self._parse_s3_path(s3_path)

        await asyncio.to_thread(self._client.delete_object, Bucket=bucket, Key=key)

        logger.debug(f"Deleted file {s3_path}")

    async def delete_prefix(self, prefix: str) -> int:
        """
        Delete every file under a prefix, used when cleaning up a session

        Args:
            prefix: S3 path prefix, such as "sessions/sess_abc123/" or "s3://bucket/sessions/sess_abc123/"

        Returns:
            How many files were deleted
        """
        deleted_count = 0
        bucket = self._bucket

        # Pull the bucket out when the prefix carries one
        if prefix.startswith("s3://"):
            parsed = urlparse(prefix)
            bucket = parsed.netloc
            prefix = parsed.path.lstrip("/")

        # Run the synchronous list and delete through asyncio.to_thread
        def _delete_all_files():
            """Synchronous helper that performs the bulk delete"""
            count = 0
            paginator = self._client.get_paginator("list_objects_v2")
            delete_chunks = []

            try:
                # Iterate the paginator directly, which is synchronous
                for page in paginator.paginate(Bucket=bucket, Prefix=prefix):
                    if "Contents" in page:
                        for obj in page["Contents"]:
                            delete_chunks.append({"Key": obj["Key"]})

                # Delete once 1000 objects have accumulated
                        if len(delete_chunks) >= 1000:
                            self._client.delete_objects(
                                Bucket=bucket, Delete={"Objects": delete_chunks}
                            )
                            count += len(delete_chunks)
                            delete_chunks = []

                # Delete whatever is left
                if delete_chunks:
                    self._client.delete_objects(Bucket=bucket, Delete={"Objects": delete_chunks})
                    count += len(delete_chunks)

            except ClientError as e:
                logger.error(f"Error deleting files with prefix {prefix}: {e}")
            return count

        # Run the synchronous work in a thread pool
        deleted_count = await asyncio.to_thread(_delete_all_files)

        logger.info(f"Deleted {deleted_count} files with prefix {prefix} (bucket: {bucket})")

        return deleted_count

    async def list_files(self, prefix: str, limit: int = 1000) -> list:
        """
        List files

        Args:
            prefix: S3 path prefix
            limit: how many to return at most

        Returns:
            The file list, each entry holding key, size, and last_modified
        """
        bucket = self._bucket

        # Pull the bucket out when the prefix carries one
        if prefix.startswith("s3://"):
            parsed = urlparse(prefix)
            bucket = parsed.netloc
            prefix = parsed.path.lstrip("/")

        def _list_all_files():
            """Synchronous helper that performs the listing"""
            files = []
            paginator = self._client.get_paginator("list_objects_v2")

            try:
                # Iterate the paginator directly, which is synchronous
                for page in paginator.paginate(Bucket=bucket, Prefix=prefix):
                    if "Contents" in page:
                        for obj in page["Contents"]:
                            files.append(
                                {
                                    "key": obj["Key"],
                                    "size": obj["Size"],
                                    "last_modified": obj["LastModified"],
                                    "etag": obj["ETag"].strip('"'),
                                }
                            )
                            if limit and len(files) >= limit:
                                break
                    if limit and len(files) >= limit:
                        break
            except ClientError as e:
                logger.error(f"Error listing objects with prefix {prefix}: {e}")
            return files

        # Run the synchronous work in a thread pool
        files = await asyncio.to_thread(_list_all_files)

        return files
