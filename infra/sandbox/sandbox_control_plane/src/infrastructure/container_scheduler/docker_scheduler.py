"""
Docker container scheduler

Creates and manages Docker containers through aiodocker.

Supports an S3 workspace mount: when workspace_path starts with s3://, the
container mounts the S3 bucket on /workspace through s3fs.

Python dependency installation follows section 5 of sandbox-design-v2.1.md.
"""

import asyncio
import json
from urllib.parse import urlparse

from aiodocker import Docker
from aiodocker.exceptions import DockerError

from src.infrastructure.config.settings import get_settings, resolve_bkn_base_url
from src.infrastructure.container_scheduler.base import (
    ContainerConfig,
    ContainerInfo,
    ContainerResult,
    IContainerScheduler,
)
from src.infrastructure.logging import get_logger
from src.shared.utils.dependencies import (
    format_dependency_install_script_for_shell,
)

logger = get_logger(__name__)


class DockerScheduler(IContainerScheduler):
    """
    Docker container scheduler

    Connects to the Docker daemon over a socket or TCP and manages the container lifecycle.
    """

    def __init__(self, docker_url: str = "unix:///var/run/docker.sock"):
        """
        Initialize the Docker scheduler

        Args:
            docker_url: Docker daemon connection URL
                - unix:///var/run/docker.sock (Unix socket)
                - tcp://localhost:2375 (TCP)
        """
        self._docker_url = docker_url
        self._docker: Docker | None = None
        self._initialized = False

    async def _ensure_docker(self) -> Docker:
        """Make sure the Docker client is initialized"""
        if not self._initialized:
            logger.debug(
                "Initializing Docker client",
                docker_url=self._docker_url,
            )
            self._docker = Docker(url=self._docker_url)
            self._initialized = True

            # Verify the Docker connection
            try:
                version = await self._docker.version()
                logger.debug(
                    "Docker client initialized and verified",
                    docker_url=self._docker_url,
                    docker_version=version.get("Version"),
                    api_version=version.get("ApiVersion"),
                )
            except Exception as e:
                logger.error(
                    "Failed to verify Docker connection",
                    docker_url=self._docker_url,
                    error=str(e),
                )
        return self._docker

    async def close(self) -> None:
        """Close the Docker connection"""
        if self._docker:
            await self._docker.close()
            self._initialized = False

    async def _ensure_image_available(self, docker: Docker, image: str) -> None:
        """Make sure the image is available locally, pulling from the remote registry when it is not."""
        try:
            await docker.images.inspect(image)
            logger.debug("Docker image already available locally", image=image)
            return
        except DockerError as e:
            if e.status != 404:
                logger.exception(
                    "Failed to inspect Docker image",
                    image=image,
                    error=str(e),
                    status=e.status,
                )
                raise

        logger.info("Docker image not found locally, pulling from registry", image=image)
        try:
            await docker.images.pull(image)
            logger.info("Docker image pulled successfully", image=image)
        except DockerError as e:
            logger.exception(
                "Failed to pull Docker image",
                image=image,
                error=str(e),
                status=e.status,
            )
            raise

    def _parse_s3_workspace(self, workspace_path: str) -> dict | None:
        """
        Parse an S3 workspace path

        Args:
            workspace_path: S3 path, formatted as s3://bucket/sessions/{session_id}/

        Returns:
            A dict holding bucket and prefix, or None when this is not an S3 path
        """
        if not workspace_path or not workspace_path.startswith("s3://"):
            return None

        parsed = urlparse(workspace_path)
        return {
            "bucket": parsed.netloc,
            "prefix": parsed.path.lstrip("/"),
        }

    def _build_s3_mount_entrypoint(
        self,
        s3_bucket: str,
        s3_prefix: str,
        s3_endpoint_url: str,
        s3_access_key: str,
        s3_secret_key: str,
        dependencies: list[str] | None = None,
    ) -> str:
        """
        Build the container start script that mounts the S3 bucket and installs dependencies

        Args:
            s3_bucket: S3 bucket name
            s3_prefix: S3 path prefix
            s3_endpoint_url: S3 endpoint URL
            s3_access_key: S3 access key id
            s3_secret_key: S3 secret key
            dependencies: pip requirement specifiers, such as ["requests==2.31.0", "pandas>=2.0"]

        Returns:
            The shell script as a string

        How it works:
        1. Mount the S3 bucket on /mnt/s3-root
        2. Bind mount the session directory onto /workspace
        3. Install dependencies into /workspace/.venv/ when any were given
        4. Drop to the sandbox user with gosu and run the executor
        """
        path_style_option = "-o use_path_request_style" if s3_endpoint_url else ""
        dependency_install_script = format_dependency_install_script_for_shell(dependencies)

        return f"""#!/bin/bash
set -e

# Create the s3fs credential file
echo "{s3_access_key}:{s3_secret_key}" > /tmp/.passwd-s3fs
chmod 600 /tmp/.passwd-s3fs

# 1) Briefly mount the whole bucket as root (no allow_other) to create the session prefix,
#    then unmount immediately. s3fs cannot mount a prefix that does not exist yet, and user
#    code never sees this temporary mount point.
mkdir -p /mnt/s3-init
s3fs {s3_bucket} /mnt/s3-init \\
    -o passwd_file=/tmp/.passwd-s3fs \\
    -o url={s3_endpoint_url or "https://s3.amazonaws.com"} \\
    {path_style_option}
mkdir -p "/mnt/s3-init/{s3_prefix}"
umount /mnt/s3-init || fusermount -u /mnt/s3-init
rmdir /mnt/s3-init

# 2) Mount only this session's prefix on /workspace, never the whole bucket. A whole-bucket
#    mount would let any session read, write, or delete another session's data, which is a
#    cross-session leak and corruption surface; per-prefix code reaches only its own /workspace.
echo "Mounting S3 workspace {s3_bucket}:/{s3_prefix} to /workspace..."
mkdir -p /workspace
s3fs {s3_bucket}:/{s3_prefix} /workspace \\
    -o passwd_file=/tmp/.passwd-s3fs \\
    -o url={s3_endpoint_url or "https://s3.amazonaws.com"} \\
    {path_style_option} \\
    -o allow_other \\
    -o umask=000

# 3) Verify s3fs really mounted, so it cannot silently fall back to a local directory.
mount | grep -q "on /workspace type fuse" || {{ echo "s3fs failed to mount /workspace" >&2; exit 1; }}
echo "Workspace mounted (scoped to {s3_prefix}): $(ls -la /workspace)"

# ========== Install dependencies ==========
{dependency_install_script}

# 6. Drop to the sandbox user with gosu and run the executor.
# The environment variables are set after gosu, through bash -c.
echo "Starting sandbox executor as sandbox user..."
# When dependencies were installed, PYTHONPATH includes the local venv directory.
if [ -d "/opt/sandbox-venv" ]; then
    export PYTHONPATH="/opt/sandbox-venv:/app:/workspace"
    export SANDBOX_VENV_PATH="/opt/sandbox-venv"
    echo "Using installed dependencies from /opt/sandbox-venv"
else
    export PYTHONPATH="/app:/workspace"
    echo "No dependencies installed, using default PYTHONPATH"
fi
exec gosu sandbox bash -c 'export PYTHONPATH=$PYTHONPATH; export SANDBOX_VENV_PATH=$SANDBOX_VENV_PATH; exec python -m executor.interfaces.http.rest'
"""

    def _build_dependency_install_entrypoint(
        self,
        dependencies: list[str] | None = None,
    ) -> str:
        """
        Build the dependency install script for the non-S3 mode

        Args:
            dependencies: pip requirement specifiers, such as ["requests==2.31.0", "pandas>=2.0"]

        Returns:
            The shell script as a string

        How it works:
        1. Run as the sandbox user
        2. Install dependencies into /opt/sandbox-venv/ on the local filesystem
        3. Start the executor
        """
        dependency_install_script = format_dependency_install_script_for_shell(dependencies)

        return f"""#!/bin/bash
set -e

echo "🚀 Starting sandbox executor (non-S3 mode)..."

# ========== Install dependencies ==========
{dependency_install_script}

# Start the executor
echo "🎯 Starting executor daemon..."
exec python -m executor.interfaces.http.rest
"""

    async def create_container(self, config: ContainerConfig) -> str:
        """
        Create the Docker container

        Container configuration:
        - NetworkMode: sandbox_network, the container network used for executor traffic
        - CAP_DROP: ALL, drop every capability
        - CAP_ADD: SYS_ADMIN, needed only for an S3 workspace, for the FUSE mount
        - SecurityOpt: no-new-privileges
        - User: 1000:1000, unprivileged
        - ReadonlyRootfs: false, the workspace has to be writable

        S3 workspace mount:
        When workspace_path starts with s3://, the container mounts the bucket on /workspace via s3fs:
        - add the /dev/fuse device, which FUSE needs
        - add the SYS_ADMIN capability, which the FUSE mount needs
        - create an entrypoint script that mounts S3 before starting the executor
        - cd into the workspace subdirectory once the container is up
        """
        logger.info(
            "Starting container creation",
            container_name=config.name,
            image=config.image,
            network_name=config.network_name,
        )

        docker = await self._ensure_docker()

        logger.debug("Docker client obtained")
        await self._ensure_image_available(docker, config.image)

        # Parse the resource limits
        cpu_quota = int(float(config.cpu_limit) * 100000)
        memory_bytes = self._parse_memory_to_bytes(config.memory_limit)

        logger.debug(
            "Resource limits parsed",
            cpu_limit=config.cpu_limit,
            cpu_quota=cpu_quota,
            memory_limit=config.memory_limit,
            memory_bytes=memory_bytes,
        )

        # Check whether an S3 workspace mount is needed
        s3_workspace = self._parse_s3_workspace(config.workspace_path)
        use_s3_mount = s3_workspace is not None

        # Check whether dependencies have to be installed
        dependencies_json = config.labels.get("dependencies", "")
        has_dependencies = bool(dependencies_json)

        logger.debug(
            "Container configuration checks",
            use_s3_mount=use_s3_mount,
            s3_workspace=s3_workspace,
            has_dependencies=has_dependencies,
            dependencies_json=dependencies_json,
        )

        # Base environment variables
        env_vars = dict(config.env_vars)

        # Both BKN faces get their address here, configured in the same place as in
        # k8s_scheduler. Deployment-level constants, injected once.
        bkn_mcp_url = get_settings().bkn_sandbox_mcp_url.strip()
        if bkn_mcp_url:
            env_vars.setdefault("BKN_SANDBOX_MCP_URL", bkn_mcp_url)
        bkn_base_url = resolve_bkn_base_url()
        if bkn_base_url:
            env_vars.setdefault("BKN_BASE_URL", bkn_base_url)

        # Base container configuration
        container_config = {
            "Image": config.image,
            "Hostname": config.name,
            "Env": [f"{k}={v}" for k, v in env_vars.items()],
            "HostConfig": {
                "NetworkMode": config.network_name,
                # Default; the S3 mount mode overrides it
                "CpuQuota": cpu_quota,
                "CpuPeriod": 100000,
                "Memory": memory_bytes,
                "MemorySwap": memory_bytes,
            },
            "Labels": config.labels,
            "ExposedPorts": {"8080/tcp": {}},
        }

        logger.debug(
            "Base container config prepared",
            image=config.image,
            hostname=config.name,
            env_count=len(container_config["Env"]),
            network_mode=config.network_name,
        )

        # StorageOpt.size only works on Linux with overlay2 + xfs (pquota).
        # Docker Desktop on Mac does not support it, so StorageOpt is left unset.
        # In production, cap disk usage through K8s ephemeral-storage or a Linux disk quota.

        # Without an S3 workspace, keep the original security configuration.
        # Bubblewrap needs user-namespace support; on a permission error either:
        # 1. enable it on the host: sudo sysctl -w kernel.unprivileged_userns_clone=1
        # 2. or set DISABLE_BWRAP=true to turn bubblewrap off
        if not use_s3_mount:
            logger.debug("Configuring non-S3 container mode")

            # Read the dependency list out of config.labels
            dependencies_json = config.labels.get("dependencies", "")
            dependencies = json.loads(dependencies_json) if dependencies_json else None

            # Add PYTHONPATH so dependency imports resolve
            if dependencies:
                container_config["Env"].append("PYTHONPATH=/opt/sandbox-venv:/workspace")
                container_config["Env"].append("SANDBOX_VENV_PATH=/opt/sandbox-venv")

                # Give the dependency install some tmpfs space
                container_config["HostConfig"]["Tmpfs"] = {
                    "/tmp": "size=512M,mode=1777",
                    "/root/.cache": "size=256M,mode=1777",
                }

                # With dependencies, use the dynamic entrypoint script
                entrypoint_script = self._build_dependency_install_entrypoint(
                    dependencies=dependencies,
                )
                container_config["Entrypoint"] = ["/bin/sh", "-c"]
                container_config["Cmd"] = [entrypoint_script]

                logger.info(
                    f"Configuring dependency installation for {config.name}: "
                    f"dependencies={len(dependencies)}"
                )

            container_config["HostConfig"]["CapDrop"] = ["ALL"]
            container_config["HostConfig"]["SecurityOpt"] = ["no-new-privileges"]
            # Add the seccomp configuration that allows user namespaces
            container_config["HostConfig"]["SecurityOpt"].append("seccomp=default")
            container_config["HostConfig"]["User"] = "1000:1000"

            logger.debug(
                "Non-S3 security config applied",
                cap_drop=["ALL"],
                security_opt=["no-new-privileges", "seccomp=default"],
                user="1000:1000",
            )

        # With an S3 workspace mount, add what that needs
        if use_s3_mount:
            logger.debug("Configuring S3 mount mode")

            settings = get_settings()

            # Read the dependency list out of config.labels
            dependencies_json = config.labels.get("dependencies", "")
            dependencies = json.loads(dependencies_json) if dependencies_json else None

            # Start as root, overriding USER sandbox from the Dockerfile,
            # so the entrypoint script can run the s3fs mount as root.
            container_config["User"] = "root"

            # Add the SYS_ADMIN capability, which FUSE needs
            container_config["HostConfig"]["CapAdd"] = ["SYS_ADMIN"]

            # Add the /dev/fuse device
            container_config["HostConfig"]["Devices"] = [
                {
                    "PathOnHost": "/dev/fuse",
                    "PathInContainer": "/dev/fuse",
                    "CgroupPermissions": "rwm",
                }
            ]

            logger.debug(
                "S3 mount capabilities configured",
                user="root",
                cap_add=["SYS_ADMIN"],
                devices_added=1,
            )

            # Add tmpfs for the s3fs cache and the dependency install
            if dependencies:
                # Dependencies need more tmpfs space
                container_config["HostConfig"]["Tmpfs"] = {
                    "/tmp": "size=512M,mode=1777",  # pip cache and temporary files
                    "/root/.cache": "size=256M,mode=1777",  # pip cache
                }
                logger.info(
                    "Added tmpfs for dependency installation: /tmp=512M, /root/.cache=256M"
                )
            else:
                # Without dependencies a smaller tmpfs is enough
                container_config["HostConfig"]["Tmpfs"] = {"/tmp": "size=100M,mode=1777"}

            # Add the S3 environment variables
            s3_env_vars = {
                "S3_BUCKET": s3_workspace["bucket"],
                "S3_PREFIX": s3_workspace["prefix"],
                "S3_ENDPOINT_URL": settings.s3_endpoint_url or "https://s3.amazonaws.com",
                "S3_REGION": settings.s3_region,
                "WORKSPACE_MOUNT_POINT": "/workspace",
                "WORKSPACE_PATH": "/workspace",  # tell the executor to use the local mount point
            }
            for k, v in s3_env_vars.items():
                container_config["Env"].append(f"{k}={v}")

            logger.debug(
                "S3 environment variables added",
                s3_bucket=s3_workspace["bucket"],
                s3_prefix=s3_workspace["prefix"],
                s3_endpoint_url=settings.s3_endpoint_url,
            )

            # Add PYTHONPATH so dependency imports resolve.
            # /app has to come first so the executor module is found.
            if dependencies:
                container_config["Env"].append("PYTHONPATH=/opt/sandbox-venv:/app:/workspace")
                container_config["Env"].append("SANDBOX_VENV_PATH=/opt/sandbox-venv")

            # Pass the dependency list through to the entrypoint script
            entrypoint_script = self._build_s3_mount_entrypoint(
                s3_bucket=s3_workspace["bucket"],
                s3_prefix=s3_workspace["prefix"],
                s3_endpoint_url=settings.s3_endpoint_url or "",
                s3_access_key=settings.s3_access_key_id,
                s3_secret_key=settings.s3_secret_access_key,
                dependencies=dependencies,
            )
            container_config["Entrypoint"] = ["/bin/sh", "-c"]
            container_config["Cmd"] = [entrypoint_script]

            logger.info(
                f"Configuring S3 workspace mount for {config.name}: "
                f"bucket={s3_workspace['bucket']}, prefix={s3_workspace['prefix']}, "
                f"dependencies={len(dependencies) if dependencies else 0}"
            )

        logger.debug(
            "Container config finalized",
            container_name=config.name,
            has_entrypoint="Entrypoint" in container_config,
            has_cmd="Cmd" in container_config,
            env_count=len(container_config["Env"]),
        )

        try:
            logger.info(
                "Calling Docker API to create container",
                container_name=config.name,
                image=config.image,
            )

            container = await docker.containers.create(container_config, name=config.name)

            logger.info(
                "Container created successfully",
                container_id=container.id,
                container_name=config.name,
                network_name=config.network_name,
                use_s3_mount=use_s3_mount,
            )
            return container.id
        except DockerError as e:
            logger.exception(
                "Docker API error during container creation",
                container_name=config.name,
                error=str(e),
                error_type=type(e).__name__,
            )
            raise
        except Exception as e:
            logger.exception(
                "Unexpected error during container creation",
                container_name=config.name,
                error=str(e),
                error_type=type(e).__name__,
            )
            raise

    async def start_container(self, container_id: str) -> None:
        """Start the container"""
        logger.info("Starting container", container_id=container_id)

        docker = await self._ensure_docker()
        try:
            logger.debug("Getting container reference", container_id=container_id)
            container = docker.containers.container(container_id)

            logger.debug("Calling container.start()", container_id=container_id)
            await container.start()

            logger.info(
                "Container started successfully",
                container_id=container_id,
            )

            # Wait briefly, then check the container status
            await asyncio.sleep(0.5)

            try:
                info = await container.show()
                container_status = info["State"]["Status"]
                logger.info(
                    "Container status after start",
                    container_id=container_id,
                    status=container_status,
                    running=info["State"].get("Running", False),
                    exit_code=info["State"].get("ExitCode"),
                    error=info["State"].get("Error"),
                )

                # Log it when the container has already exited
                if container_status == "exited":
                    exit_code = info["State"].get("ExitCode", -1)
                    logger.error(
                        "Container exited immediately after start",
                        container_id=container_id,
                        exit_code=exit_code,
                        error=info["State"].get("Error", "unknown"),
                        oom_killed=info["State"].get("OOMKilled", False),
                    )

            except Exception as status_error:
                logger.warning(
                    "Failed to get container status after start",
                    container_id=container_id,
                    error=str(status_error),
                )

        except DockerError as e:
            logger.exception(
                "Docker error during container start",
                container_id=container_id,
                error=str(e),
                error_type=type(e).__name__,
            )
            raise
        except Exception as e:
            logger.exception(
                "Unexpected error during container start",
                container_id=container_id,
                error=str(e),
                error_type=type(e).__name__,
            )
            raise

    async def stop_container(self, container_id: str, timeout: int = 10) -> None:
        """Stop the container"""
        docker = await self._ensure_docker()
        try:
            container = docker.containers.container(container_id)
            await container.stop(timeout=timeout)
            logger.info(f"Stopped container {container_id}")
        except DockerError as e:
            logger.error(f"Failed to stop container {container_id}: {e}")
            raise

    async def remove_container(self, container_id: str, force: bool = True) -> None:
        """Delete the container"""
        docker = await self._ensure_docker()
        try:
            container = docker.containers.container(container_id)
            await container.delete(force=force)
            logger.info(f"Removed container {container_id}")
        except DockerError as e:
            logger.warning(f"Failed to remove container {container_id}: {e}")

    async def get_container_status(self, container_id: str) -> ContainerInfo:
        """Get the container status"""
        docker = await self._ensure_docker()
        try:
            container = docker.containers.container(container_id)
            info = await container.show()

            status = info["State"]["Status"]
            if status == "running":
                # Docker may report running while the container is actually paused
                if info["State"].get("Paused", False):
                    status = "paused"
            elif status == "exited":
                # exit_code tells completed from failed
                pass

            return ContainerInfo(
                id=container_id,
                name=info["Name"].lstrip("/"),
                image=info["Config"]["Image"],
                status=status,
                ip_address=info["NetworkSettings"].get("IPAddress"),
                created_at=info["Created"],
                started_at=info["State"].get("StartedAt"),
                exited_at=info["State"].get("FinishedAt"),
                exit_code=info["State"].get("ExitCode"),
            )
        except DockerError as e:
            logger.error(f"Failed to get container status {container_id}: {e}")
            raise

    async def is_container_running(self, container_id: str) -> bool:
        """
        Check whether the container is running

        Queries the Docker API directly, without going through the database.
        StateSyncService uses this.

        Args:
            container_id: container id

        Returns:
            bool: whether the container is running
        """
        try:
            container_info = await self.get_container_status(container_id)
            return container_info.status == "running"
        except Exception as e:
            logger.warning(f"Failed to check container {container_id} status: {e}")
            return False

    async def get_container_logs(
        self, container_id: str, tail: int = 100, since: str | None = None
    ) -> str:
        """Get the container logs"""
        docker = await self._ensure_docker()
        try:
            container = docker.containers.container(container_id)
            # Build the log parameters
            params = {"stdout": True, "stderr": True, "tail": tail}
            if since:
                params["since"] = since
            logs = await container.log(**params)
            return "".join(logs)
        except DockerError as e:
            logger.error(f"Failed to get logs for container {container_id}: {e}")
            raise

    async def wait_container(
        self, container_id: str, timeout: int | None = None
    ) -> ContainerResult:
        """Wait for the container to finish"""
        docker = await self._ensure_docker()
        try:
            container = docker.containers.container(container_id)

            if timeout:
                # asyncio.wait_for provides the timeout
                result = await asyncio.wait_for(container.wait(), timeout=timeout)
            else:
                result = await container.wait()

            exit_code = result["StatusCode"]
            status = "completed" if exit_code == 0 else "failed"

            # Read the logs
            logs = await self.get_container_logs(container_id, tail=-1)

            return ContainerResult(
                status=status,
                stdout=logs,
                stderr="",
                exit_code=exit_code,
            )
        except TimeoutError:
            logger.warning(f"Container {container_id} timed out")
            return ContainerResult(
                status="timeout",
                stdout="",
                stderr=f"Container execution timed out after {timeout}s",
                exit_code=124,
            )
        except DockerError as e:
            logger.error(f"Failed to wait for container {container_id}: {e}")
            raise

    async def ping(self) -> bool:
        """Check the Docker connection"""
        try:
            docker = await self._ensure_docker()
            # Read the Docker version to verify the connection
            version = await docker.version()
            return version is not None
        except Exception as e:
            logger.error(f"Docker ping failed: {e}")
            return False

    def _parse_memory_to_bytes(self, value: str) -> int:
        """
        Parse a memory limit into bytes

        Args:
            value: such as "512Mi" or "1Gi"

        Returns:
            The number of bytes
        """
        value = value.strip()
        if value.endswith("Gi") or value.endswith("GB") or value.endswith("G"):
            return int(float(value[:-2]) * 1024 * 1024 * 1024)
        elif value.endswith("Mi") or value.endswith("MB") or value.endswith("M"):
            return int(float(value[:-2]) * 1024 * 1024)
        elif value.endswith("Ki") or value.endswith("KB") or value.endswith("K"):
            return int(float(value[:-2]) * 1024)
        else:
            # Default to MB
            return int(float(value) * 1024 * 1024)

    def _parse_disk_to_bytes(self, value: str) -> int:
        """Parse a disk limit into bytes"""
        return self._parse_memory_to_bytes(value)
