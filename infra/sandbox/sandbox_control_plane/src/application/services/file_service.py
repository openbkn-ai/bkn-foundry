"""
File application service

Orchestrates the file upload and download use cases.
"""

import io
import mimetypes
import re
import stat
import zipfile
from pathlib import PurePosixPath
from typing import Dict, List, Any
from urllib.parse import urlparse

from src.domain.repositories.session_repository import ISessionRepository
from src.domain.services.storage import IStorageService
from src.shared.errors.domain import NotFoundError, ValidationError
from src.shared.i18n import message


class FileService:
    """
    File application service

    Orchestrates uploading and downloading files.
    """

    def __init__(
        self,
        session_repo: ISessionRepository,
        storage_service: IStorageService,
        max_extracted_file_count: int = 10000,
        max_extracted_total_size_mb: int = 512,
    ):
        self._session_repo = session_repo
        self._storage_service = storage_service
        self._max_extracted_file_count = max_extracted_file_count
        self._max_extracted_total_size_bytes = max_extracted_total_size_mb * 1024 * 1024

    async def upload_file(
        self,
        session_id: str,
        path: str,
        content: bytes,
        content_type: str = "application/octet-stream",
    ) -> str:
        """
        Upload-file use case

        Steps:
        1. Verify the session exists and is running
        2. Validate the path
        3. Upload to storage
        4. Return the file path
        """
        session = await self._session_repo.find_by_id(session_id)
        if not session:
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=session_id))

        if not session.is_active():
            raise ValidationError(message("Sandbox.Session.NotActive", session_id=session_id))

        normalized_path = self._validate_relative_path(path)

        s3_path = f"{session.workspace_path}/{normalized_path}"
        await self._storage_service.upload_file(
            s3_path=s3_path, content=content, content_type=content_type
        )

        return normalized_path

    async def upload_and_extract_zip(
        self,
        session_id: str,
        path: str,
        content: bytes,
        overwrite: bool = False,
    ) -> Dict[str, Any]:
        """
        Upload a ZIP and extract it into the session workspace.

        Returns the extraction statistics.
        """
        session = await self._session_repo.find_by_id(session_id)
        if not session:
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=session_id))

        if not session.is_active():
            raise ValidationError(message("Sandbox.Session.NotActive", session_id=session_id))

        extract_path = self._validate_directory_path(path)

        try:
            archive = zipfile.ZipFile(io.BytesIO(content))
        except zipfile.BadZipFile as exc:
            raise ValidationError(message("Sandbox.File.InvalidZipArchive")) from exc

        with archive:
            entries: list[tuple[zipfile.ZipInfo, str, str]] = []
            total_uncompressed_size = 0

            for zip_info in archive.infolist():
                if zip_info.is_dir():
                    continue

                entry_path = self._validate_zip_entry_path(zip_info)
                total_uncompressed_size += zip_info.file_size

                if len(entries) + 1 > self._max_extracted_file_count:
                    raise ValidationError(message("Sandbox.File.ZipTooManyFiles"))
                if total_uncompressed_size > self._max_extracted_total_size_bytes:
                    raise ValidationError(message("Sandbox.File.ZipUncompressedTooLarge"))

                entries.append(
                    (
                        zip_info,
                        entry_path,
                        self._join_paths(extract_path, entry_path),
                    )
                )

            extracted_file_count = 0
            skipped_file_count = 0

            for zip_info, entry_path, destination_path in entries:
                s3_path = f"{session.workspace_path}/{destination_path}"

                if not overwrite and await self._storage_service.file_exists(s3_path):
                    skipped_file_count += 1
                    continue

                with archive.open(zip_info, "r") as member:
                    file_content = member.read()

                content_type = mimetypes.guess_type(entry_path)[0] or "application/octet-stream"
                await self._storage_service.upload_file(
                    s3_path=s3_path,
                    content=file_content,
                    content_type=content_type,
                )
                extracted_file_count += 1

        return {
            "mode": "archive_extract",
            "extract_path": extract_path,
            "extracted_file_count": extracted_file_count,
            "skipped_file_count": skipped_file_count,
            "size": len(content),
        }

    async def download_file(self, session_id: str, path: str) -> Dict:
        """
        Download-file use case

        Steps:
        1. Verify the session exists
        2. Verify the file exists
        3. Return the content, or a presigned URL
        """
        session = await self._session_repo.find_by_id(session_id)
        if not session:
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=session_id))

        s3_path = f"{session.workspace_path}/{path}"
        file_exists = await self._storage_service.file_exists(s3_path)
        if not file_exists:
            raise NotFoundError(message("Sandbox.File.NotFound", path=path))

        file_info = await self._storage_service.get_file_info(s3_path)
        file_size = file_info["size"]

        # Return the content directly under 10MB; hand back a presigned URL above it
        SMALL_FILE_THRESHOLD = 10 * 1024 * 1024  # 10MB

        if file_size < SMALL_FILE_THRESHOLD:
            content = await self._storage_service.download_file(s3_path)
            return {
                "content": content,
                "content_type": file_info.get("content_type", "application/octet-stream"),
                "size": file_size,
            }

        presigned_url = await self._storage_service.generate_presigned_url(s3_path)
        return {
            "presigned_url": presigned_url,
            "size": file_size,
        }

    async def list_files(
        self, session_id: str, path: str = None, limit: int = 1000
    ) -> List[Dict[str, Any]]:
        """
        List the files of a session

        Args:
            session_id: Session ID
            path: optional directory, relative to the workspace root
            limit: how many files to return at most

        Returns:
            The file list, each entry holding name, size, modified_time, container_path, and more
        """
        session = await self._session_repo.find_by_id(session_id)
        if not session:
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=session_id))

        # Parse workspace_path to get the S3 key prefix.
        # workspace_path is s3://bucket/sessions/{session_id}/
        # and an S3 key is sessions/{session_id}/...
        parsed = urlparse(session.workspace_path)
        s3_key_prefix = parsed.path.lstrip("/")  # drop the leading /, leaving "sessions/{session_id}/"

        # Build the S3 query prefix
        if path:
            normalized_path = path.strip().strip("/")
            if normalized_path:
                # Make sure s3_key_prefix ends with /
                base = s3_key_prefix.rstrip("/")
                prefix = f"{base}/{normalized_path}"
            else:
                prefix = s3_key_prefix.rstrip("/")
        else:
            prefix = s3_key_prefix.rstrip("/")

        files = await self._storage_service.list_files(prefix, limit)

        result = []

        for file in files:
            key = file["key"]

            # Work out the path relative to the session workspace.
            # A key looks like sessions/{session_id}/conversation-1231/uploads/temparea/test.csv
            # and s3_key_prefix looks like sessions/{session_id}/
            if key.startswith(s3_key_prefix):
                relative_path = key[len(s3_key_prefix) :].lstrip("/")
            else:
                relative_path = key.lstrip("/")

            # Drop the empty path, which is the directory itself, and the trailing-slash directory markers
            if not relative_path or relative_path.endswith("/"):
                continue

            # The in-container mount path is /workspace/{relative_path}
            container_path = f"/workspace/{relative_path}"

            result.append(
                {
                    "name": relative_path,
                    "container_path": container_path,
                    "size": file["size"],
                    "modified_time": file.get("last_modified"),
                    "etag": file.get("etag"),
                }
            )

        return result

    def _validate_relative_path(self, path: str) -> str:
        if not path:
            raise ValidationError(message("Sandbox.File.InvalidPath"))

        stripped = path.strip()
        if not stripped or stripped.startswith("/") or "\\" in stripped:
            raise ValidationError(message("Sandbox.File.InvalidPath"))
        if re.match(r"^[A-Za-z]:", stripped):
            raise ValidationError(message("Sandbox.File.InvalidPath"))

        normalized = PurePosixPath(stripped).as_posix()
        parts = PurePosixPath(normalized).parts
        if any(part == ".." for part in parts):
            raise ValidationError(message("Sandbox.File.InvalidPath"))

        if normalized.startswith("./"):
            normalized = normalized[2:]
        if not normalized or normalized == ".":
            raise ValidationError(message("Sandbox.File.InvalidPath"))
        return normalized

    def _validate_directory_path(self, path: str) -> str:
        normalized = self._validate_relative_path(path)
        return normalized.rstrip("/")

    def _validate_zip_entry_path(self, zip_info: zipfile.ZipInfo) -> str:
        path = zip_info.filename
        if not path:
            raise ValidationError(message("Sandbox.File.InvalidZipEntryPath"))
        if path.startswith("/") or "\\" in path or re.match(r"^[A-Za-z]:", path):
            raise ValidationError(message("Sandbox.File.InvalidZipEntryPath"))
        mode = zip_info.external_attr >> 16
        if stat.S_ISLNK(mode):
            raise ValidationError(message("Sandbox.File.InvalidZipEntryPath"))

        normalized = PurePosixPath(path).as_posix()
        parts = PurePosixPath(normalized).parts
        if any(part in ("", ".", "..") for part in parts):
            raise ValidationError(message("Sandbox.File.InvalidZipEntryPath"))
        return normalized

    def _join_paths(self, base: str, child: str) -> str:
        if not base:
            return child
        return f"{base}/{child}"
