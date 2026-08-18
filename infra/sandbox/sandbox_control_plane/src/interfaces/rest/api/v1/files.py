"""
File operation REST API routes

Defines the HTTP endpoints for uploading and downloading files.
"""

import fastapi
from fastapi import APIRouter, Depends, HTTPException, status, UploadFile, File, Query
from typing import Optional

from src.application.services.file_service import FileService
from src.infrastructure.config.settings import get_settings
from src.interfaces.rest.schemas.response import ErrorResponse
from src.infrastructure.dependencies import get_file_service_db
from src.shared.errors.domain import NotFoundError, ValidationError
from src.shared.i18n import message

router = APIRouter(prefix="/sessions/{session_id}/files", tags=["files"])


@router.get("")
async def list_files(
    session_id: str,
    path: Optional[str] = Query(
        None, description="A directory, relative to the workspace root. Without it every file is listed."
    ),
    limit: int = Query(1000, ge=1, le=10000, description="How many files to return at most"),
    service: FileService = Depends(get_file_service_db),
):
    """
    List the files of a session

    Returns the files in that session workspace, optionally under one directory.

    - **path**: optional directory, such as "src/" or "src/utils"; without it every file is listed
    - **limit**: how many files to return, 1-10000
    """
    try:
        files = await service.list_files(session_id=session_id, path=path, limit=limit)

        return {"session_id": session_id, "files": files, "count": len(files)}

    except Exception as e:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(e))


@router.post("/upload")
async def upload_file(
    session_id: str,
    path: str,
    extract: bool = Query(False, description="Whether to treat the upload as a ZIP and extract it into the target directory"),
    overwrite: bool = Query(False, description="Whether extraction overwrites existing files"),
    file: UploadFile = File(...),
    service: FileService = Depends(get_file_service_db),
):
    """
    Upload a file into the session workspace

    - **path**: where the file goes in the workspace
    - **file**: the file to upload, 100MB at most
    """
    try:
        # Validate the file size
        settings = get_settings()
        content = await file.read()
        max_upload_bytes = settings.max_upload_file_size_mb * 1024 * 1024
        if len(content) > max_upload_bytes:
            raise HTTPException(
                status_code=status.HTTP_413_REQUEST_ENTITY_TOO_LARGE,
                detail=message("Sandbox.File.SizeExceeded", limit_mb=settings.max_upload_file_size_mb),
            )

        if extract:
            content_type = file.content_type or ""
            filename = (file.filename or "").lower()
            if "zip" not in content_type.lower() and not filename.endswith(".zip"):
                raise HTTPException(
                    status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
                    detail=message("Sandbox.File.ZipOnly"),
                )

            result = await service.upload_and_extract_zip(
                session_id=session_id,
                path=path,
                content=content,
                overwrite=overwrite,
            )
            return {
                "session_id": session_id,
                **result,
            }

        file_path = await service.upload_file(
            session_id=session_id,
            path=path,
            content=content,
            content_type=file.content_type or "application/octet-stream",
        )

        return {
            "session_id": session_id,
            "mode": "file",
            "file_path": file_path,
            "size": len(content),
        }

    except HTTPException:
        raise
    except NotFoundError as e:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=str(e))
    except ValidationError as e:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=status.HTTP_500_INTERNAL_SERVER_ERROR, detail=str(e))


@router.get("/{file_path:path}")
async def download_file(
    session_id: str, file_path: str, service: FileService = Depends(get_file_service_db)
):
    """
    Download a file from the session workspace

    - **file_path**: where the file lives in the workspace
    """
    try:
        file_data = await service.download_file(session_id=session_id, path=file_path)

        if file_data.get("presigned_url"):
            return {
                "session_id": session_id,
                "file_path": file_path,
                "presigned_url": file_data["presigned_url"],
                "size": file_data["size"],
            }
        else:
            from fastapi.responses import Response

            return Response(
                content=file_data["content"],
                media_type=file_data["content_type"],
                headers={"Content-Disposition": f'attachment; filename="{file_path}"'},
            )

    except Exception as e:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=str(e))
