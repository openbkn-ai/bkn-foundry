"""
Kubernetes container scheduler

Creates and manages Pods through the official Python kubernetes client.

MinIO + s3fs architecture:
- The control plane writes files into MinIO under /sessions/{session_id}/ via the S3 API
- The executor Pod mounts s3fs in its start script, mapping that session subdirectory to /workspace
- The s3fs process and the executor process run inside the same container

Python dependency installation follows section 5 of sandbox-design-v2.1.md.
"""

import asyncio
import json
from urllib.parse import urlparse

from kubernetes import client, config
from kubernetes.client import (
    V1Capabilities,
    V1Container,
    V1ContainerPort,
    V1EmptyDirVolumeSource,
    V1EnvVar,
    V1LocalObjectReference,
    V1ObjectMeta,
    V1OwnerReference,
    V1Pod,
    V1PodSpec,
    V1ResourceRequirements,
    V1SecretVolumeSource,
    V1SecurityContext,
    V1Volume,
    V1VolumeMount,
)
from kubernetes.client.rest import ApiException

from src.infrastructure.config.settings import get_settings
from src.infrastructure.container_scheduler.base import (
    ContainerConfig,
    ContainerInfo,
    ContainerOwnershipInfo,
    ContainerResult,
    IContainerScheduler,
)
from src.infrastructure.logging import get_logger
from src.shared.utils.dependencies import (
    format_dependencies_for_script,
    format_dependency_install_script_for_shell,
)

logger = get_logger(__name__)


def s3_prefix_from_path(prefix: str) -> str:
    """
    Extract the session id from an S3 path prefix

    Args:
        prefix: S3 path prefix, for example "sessions/test-001/workspace"

    Returns:
        The session id, for example "test-001"
    """
    parts = prefix.strip("/").split("/")
    if len(parts) >= 2 and parts[0] == "sessions":
        return parts[1]
    return prefix


class K8sScheduler(IContainerScheduler):
    """
    Kubernetes container scheduler

    Manages the Pod lifecycle through the Kubernetes API.
    """

    def __init__(
        self,
        namespace: str = "sandbox-system",
        kube_config_path: str | None = None,
        service_account_token: str | None = None,
        executor_service_account: str = "sandbox-control-plane",
    ):
        """
        Initialize the K8s scheduler

        Args:
            namespace: Kubernetes namespace
            kube_config_path: path to a kubeconfig file, optional, for local development
            service_account_token: ServiceAccount token, used when running inside a Pod
            executor_service_account: name of the ServiceAccount the executor Pod uses
        """
        self._namespace = namespace
        self._executor_service_account = executor_service_account

        # Load the Kubernetes configuration
        if service_account_token:
            # Running inside a Pod: use the ServiceAccount
            self._load_incluster_config()
        elif kube_config_path:
            # Use the kubeconfig file that was given
            config.load_kube_config(config_file=kube_config_path)
        else:
            # Try the default kubeconfig
            try:
                config.load_kube_config()
            except Exception:
                # No kubeconfig: try the in-cluster configuration
                try:
                    self._load_incluster_config()
                except Exception:
                    # Last resort, the default configuration, for local development
                    from kubernetes.client import Configuration

                    Configuration.set_default(Configuration())

        # Create the API clients
        self._core_v1 = client.CoreV1Api()
        self._initialized = False

    def _load_incluster_config(self):
        """Load the in-cluster configuration"""
        config.load_incluster_config()
        logger.info("Loaded in-cluster Kubernetes config")

    async def _ensure_connected(self) -> bool:
        """Make sure the K8s connection is established"""
        if not self._initialized:
            try:
                # Probe the connection by listing Pods in the current namespace
                # rather than namespaces, so only namespace-level permissions are needed
                self._core_v1.list_namespaced_pod(self._namespace, limit=1)
                self._initialized = True
                logger.info(f"Connected to Kubernetes cluster, namespace: {self._namespace}")
            except Exception as e:
                logger.error(f"Failed to connect to Kubernetes: {e}")
                raise
        return self._initialized

    async def close(self) -> None:
        """Close the connection. The Kubernetes client is stateless, so there is nothing to close."""
        self._initialized = False

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

    def _build_pod_name(self, session_id: str) -> str:
        """Generate the Pod name"""
        # A K8s Pod name has to follow the DNS subdomain rules:
        # lower-case letters, digits, and '-', starting and ending alphanumeric.
        pod_name = f"sandbox-{session_id.lower()}"
        # Replace anything that does not fit
        pod_name = "".join(c if c.isalnum() or c == "-" else "-" for c in pod_name)
        # Make sure it neither starts nor ends with '-'
        pod_name = pod_name.strip("-")
        # Cap the length; a K8s Pod name allows at most 253 characters
        return pod_name[:253]

    def _build_owner_references(self, config: ContainerConfig) -> list[V1OwnerReference] | None:
        """Build ownerReferences from the control plane owner context."""
        if config.owner_context is None:
            return None

        return [
            V1OwnerReference(
                api_version="v1",
                kind="Pod",
                name=config.owner_context.pod_name,
                uid=config.owner_context.pod_uid,
                controller=True,
                block_owner_deletion=True,
            )
        ]

    async def _read_pod(self, pod_name: str) -> V1Pod | None:
        """Read one Pod, returning None when it does not exist."""
        try:
            return await asyncio.to_thread(
                self._core_v1.read_namespaced_pod,
                name=pod_name,
                namespace=self._namespace,
            )
        except ApiException as e:
            if e.status == 404:
                return None
            raise

    async def _wait_until_pod_deleted(
        self,
        pod_name: str,
        timeout_seconds: float = 30.0,
        poll_interval_seconds: float = 1.0,
    ) -> bool:
        """Wait until the Pod is gone from the API, so recreating the same name cannot 409."""
        deadline = asyncio.get_running_loop().time() + timeout_seconds
        while asyncio.get_running_loop().time() < deadline:
            existing_pod = await self._read_pod(pod_name)
            if existing_pod is None:
                return True
            await asyncio.sleep(poll_interval_seconds)
        return False

    def _build_image_pull_secrets(self) -> list[V1LocalObjectReference] | None:
        """Build the imagePullSecrets the executor Pod uses."""
        settings = get_settings()
        secret_names = [
            secret.strip()
            for secret in settings.executor_image_pull_secrets.split(",")
            if secret.strip()
        ]
        if not secret_names:
            return None
        return [V1LocalObjectReference(name=secret_name) for secret_name in secret_names]

    def _build_executor_container(
        self,
        config: ContainerConfig,
        use_s3_mount: bool,
        has_dependencies: bool,
        session_id: str = None,
        s3_workspace: dict = None,
    ) -> V1Container:
        """
        Build the main executor container

        With an S3 mount, the start script mounts s3fs first and then starts the executor

        Args:
            config: container configuration
            use_s3_mount: whether to mount S3, through s3fs inside the container
            has_dependencies: whether there are dependency packages
            session_id: session id, used for the S3 subdirectory mount
            s3_workspace: S3 workspace configuration, holding bucket and prefix

        Returns:
            A V1Container object
        """
        env_vars = [V1EnvVar(name=k, value=v) for k, v in config.env_vars.items()]

        # Add the S3 environment variables
        s3_workspace = self._parse_s3_workspace(config.workspace_path)
        if s3_workspace:
            env_vars.extend(
                [
                    V1EnvVar(name="WORKSPACE_PATH", value="/workspace"),
                    V1EnvVar(name="S3_BUCKET", value=s3_workspace["bucket"]),
                    V1EnvVar(name="S3_PREFIX", value=s3_workspace["prefix"]),
                ]
            )

        # The MCP address for sandbox_sdk.bkn. A deployment-level constant, injected
        # once; a caller that passes mcp in the event wins (see _mcp_url in sandbox_sdk/bkn.py).
        bkn_mcp_url = get_settings().bkn_sandbox_mcp_url.strip()
        if bkn_mcp_url:
            env_vars.append(V1EnvVar(name="BKN_SANDBOX_MCP_URL", value=bkn_mcp_url))

        # Add PYTHONPATH so dependency imports resolve
        if has_dependencies:
            # Dependencies are installed locally under /opt/sandbox-venv
            env_vars.append(V1EnvVar(name="PYTHONPATH", value="/opt/sandbox-venv:/app:/workspace"))
            env_vars.append(V1EnvVar(name="SANDBOX_VENV_PATH", value="/opt/sandbox-venv"))

        # Resource limits
        resources = V1ResourceRequirements(
            limits={
                "cpu": config.cpu_limit,
                "memory": config.memory_limit,
                "ephemeral-storage": config.disk_limit,
            },
            requests={
                # Keep requests at zero in K8s session pods so scheduling is not blocked
                # by per-session resource reservations. Runtime protection still comes from limits.
                "cpu": "0",
                "memory": "0",
            },
        )

        # Container ports
        ports = [
            V1ContainerPort(
                container_port=8080,
                name="executor",
                protocol="TCP",
            )
        ]

        # Volume mounts
        volume_mounts = [
            V1VolumeMount(
                name="workspace",
                mount_path="/workspace",
            )
        ]
        if use_s3_mount:
            volume_mounts.append(
                V1VolumeMount(
                    name="s3fs-passwd",
                    mount_path="/etc/s3fs-passwd",
                    read_only=True,
                )
            )

        # Security context: s3fs needs privileged and the root user.
        # With privileged=True the container has to run as root to perform the FUSE mount,
        # so runAsUser=0 is set explicitly to override the USER directive in the Dockerfile.
        # Dependencies also need root to install, after which gosu drops to the sandbox user.
        needs_root = use_s3_mount or has_dependencies
        security_context = V1SecurityContext(
            # An s3fs mount or a dependency install needs root; gosu drops to the sandbox user at the end
            run_as_non_root=not needs_root,
            run_as_user=0 if needs_root else 1000,  # 0 = root, set explicitly to override the Dockerfile USER
            run_as_group=0 if needs_root else 1000,
            allow_privilege_escalation=use_s3_mount,  # s3fs needs privileges
            capabilities=V1Capabilities(drop=["ALL"]) if not needs_root else None,
            read_only_root_filesystem=False,
            privileged=use_s3_mount,  # s3fs needs privileged mode
        )

        # Build the start command
        command = None
        if use_s3_mount:
            # Read the settings
            settings = get_settings()
            minio_url = (
                settings.s3_endpoint_url or "http://minio.sandbox-system.svc.cluster.local:9000"
            )
            bucket = s3_workspace["bucket"]

            # S3 mount script: create the session prefix first, then mount only that prefix on /workspace, never the whole bucket
            s3_prefix = s3_workspace["prefix"].rstrip("/")
            mount_script = f"""#!/bin/sh
set -e

echo "📂 Preparing S3 workspace {bucket}:/{s3_prefix} (session: {session_id})..."

# 1) Briefly mount the whole bucket as root (root-visible only, no allow_other) to create
#    the session prefix, then unmount immediately. s3fs cannot mount a prefix that does not
#    exist yet. The executor starts as the sandbox user afterwards, so user code never sees
#    this temporary mount point.
mkdir -p /mnt/s3-init
s3fs {bucket} /mnt/s3-init \\
    -o url={minio_url} \\
    -o use_path_request_style \\
    -o passwd_file=/etc/s3fs-passwd/s3fs-passwd
mkdir -p "/mnt/s3-init/{s3_prefix}"
umount /mnt/s3-init || fusermount -u /mnt/s3-init
rmdir /mnt/s3-init

# 2) Mount only this session's prefix on /workspace. Mounting the whole bucket with
#    allow_other would let any session read, write, or delete another session's data, which
#    is a cross-session leak and corruption surface; per-prefix code reaches only its own /workspace.
s3fs {bucket}:/{s3_prefix} /workspace \\
    -o url={minio_url} \\
    -o use_path_request_style \\
    -o allow_other \\
    -o uid=1000 \\
    -o gid=1000 \\
    -o passwd_file=/etc/s3fs-passwd/s3fs-passwd

# 3) Verify s3fs really mounted, so a silent fall back to the local emptyDir cannot leave data out of MinIO.
mount | grep -q "on /workspace type fuse" || {{ echo "❌ s3fs failed to mount /workspace" >&2; exit 1; }}

echo "✅ Session workspace mounted (scoped to {s3_prefix})"
ls -la /workspace/

"""

            # Install the dependencies when there are any
            if has_dependencies:
                dependencies_json = config.labels.get("dependencies", "")
                dependencies = json.loads(dependencies_json) if dependencies_json else []
                _, deps_list = format_dependencies_for_script(dependencies)
                mount_script += f"""
echo "📦 Installing dependencies..."
VENV_DIR="/opt/sandbox-venv"
mkdir -p $VENV_DIR

pip3 install \\
    --target $VENV_DIR \\
    --no-cache-dir \\
    --no-warn-script-location \\
    --disable-pip-version-check \\
    --index-url https://mirrors.aliyun.com/pypi/web/simple/ \\
    {deps_list}

echo "✅ Dependencies installed"

export PYTHONPATH="$VENV_DIR:/app:/workspace"
export SANDBOX_VENV_PATH="$VENV_DIR"
"""

            # Start the executor in the foreground, dropping to the sandbox user with gosu
            mount_script += """
echo "🚀 Starting executor as sandbox user..."
# Drop to the sandbox user with gosu and start the executor.
# gosu forwards signals correctly, which keeps the process from becoming a zombie.
exec gosu sandbox python -m executor.interfaces.http.rest
"""
            command = ["sh", "-c", mount_script]

        elif has_dependencies:
            # The executor container installs the dependencies at start-up
            dependencies_json = config.labels.get("dependencies", "")
            dependencies = json.loads(dependencies_json) if dependencies_json else []
            install_script = format_dependency_install_script_for_shell(dependencies)

            install_script = f"""#!/bin/sh
set -e
echo "📦 Installing dependencies..."

{install_script}

# Fix the venv permissions: it was installed as root and the sandbox user has to read it.
chown -R sandbox:sandbox /opt/sandbox-venv

# Start the executor, dropping to the sandbox user with gosu.
echo "🚀 Starting executor as sandbox user..."
exec gosu sandbox python -m executor.interfaces.http.rest
"""
            command = ["sh", "-c", install_script]

        settings = get_settings()

        return V1Container(
            name="executor",
            image=config.image,
            image_pull_policy=settings.executor_image_pull_policy,
            command=command,
            env=env_vars,
            resources=resources,
            ports=ports,
            volume_mounts=volume_mounts,
            security_context=security_context,
        )

    async def create_container(self, config: ContainerConfig) -> str:
        """
        Create a Kubernetes Pod, mounting s3fs inside the executor container

        How it fits together:
        - The control plane writes files into MinIO under /sessions/{session_id}/ via the S3 API
        - The executor Pod mounts s3fs in its start script, mapping that session subdirectory to /workspace
        - The s3fs process and the executor process run inside the same container
        - When there are dependencies, the executor container installs them at start-up
        """
        await self._ensure_connected()

        pod_name = self._build_pod_name(config.name)
        s3_workspace = self._parse_s3_workspace(config.workspace_path)
        use_s3_mount = s3_workspace is not None

        # Check whether there are dependencies
        dependencies_json = config.labels.get("dependencies", "")
        has_dependencies = bool(dependencies_json)

        # session_id is extracted for logging only; the mount uses the full s3_prefix
        session_id = s3_prefix_from_path(s3_workspace["prefix"]) if s3_workspace else config.name

        # Build the container list: the executor container only
        containers = []

        # Build the executor container, which mounts s3fs in its start script.
        # The mount script uses the full s3_prefix rather than session_id, to avoid path-depth problems.
        executor_container = self._build_executor_container(
            config=config,
            use_s3_mount=use_s3_mount,
            has_dependencies=has_dependencies,
            session_id=session_id,
            s3_workspace=s3_workspace,
        )
        containers.append(executor_container)

        # Build the volumes
        volumes = []
        if use_s3_mount:
            # An emptyDir backs the s3fs mount
            volumes.append(
                V1Volume(
                    name="workspace",
                    empty_dir=V1EmptyDirVolumeSource(),
                )
            )
            # Add the s3fs-passwd secret
            volumes.append(
                V1Volume(
                    name="s3fs-passwd",
                    secret=V1SecretVolumeSource(
                        secret_name="s3fs-passwd",
                        default_mode=0o400,
                    ),
                )
            )
        else:
            # Local workspace: use an emptyDir
            volumes.append(
                V1Volume(
                    name="workspace",
                    empty_dir=V1EmptyDirVolumeSource(),
                )
            )

        # Build the labels, excluding dependencies, because K8s labels have a strict format
        # and dependencies contains brackets, quotes, and other illegal characters.
        dependencies_value = config.labels.pop("dependencies", None)
        labels = {
            "app": "sandbox-executor",  # matches the sandbox-executor service selector
            "sandbox-session": config.name,
            "sandbox-type": "execution",
        }
        if use_s3_mount:
            labels["mount-method"] = "s3fs"
        labels.update(config.labels)

        # Build the annotations; dependencies goes here, where the format is unconstrained
        annotations = {
            "sandbox-session-id": config.name,
        }
        if config.owner_context is not None:
            annotations["control-plane-pod-name"] = config.owner_context.pod_name
            annotations["control-plane-pod-uid"] = config.owner_context.pod_uid
        if dependencies_value:
            annotations["dependencies"] = dependencies_value
        # Restore dependencies onto config.labels so later calls are unaffected
        if dependencies_value is not None:
            config.labels["dependencies"] = dependencies_value

        # Build the Pod spec
        pod = V1Pod(
            metadata=V1ObjectMeta(
                name=pod_name,
                labels=labels,
                annotations=annotations,
                owner_references=self._build_owner_references(config),
            ),
            spec=V1PodSpec(
                containers=containers,
                volumes=volumes,
                restart_policy="Always",  # restart the container after it exits, keeping the runtime available
                host_network=False,
                termination_grace_period_seconds=30,
                service_account_name=self._executor_service_account,
                image_pull_secrets=self._build_image_pull_secrets(),
                # Keep the default DNS policy (ClusterFirst) so the Pod uses cluster DNS,
                # which matters for executor-to-control-plane communication.
            ),
        )

        try:
            # Create the Pod
            created_pod = await asyncio.to_thread(
                self._core_v1.create_namespaced_pod,
                namespace=self._namespace,
                body=pod,
            )
            mount_method = "s3fs" if use_s3_mount else "emptyDir"
            logger.info(
                f"Created pod {created_pod.metadata.name} for session {config.name} "
                f"in namespace {self._namespace} (mount method: {mount_method})"
            )
            return created_pod.metadata.name

        except ApiException as e:
            if e.status == 409:
                existing_pod = await self._read_pod(pod_name)
                deletion_timestamp = getattr(
                    getattr(existing_pod, "metadata", None),
                    "deletion_timestamp",
                    None,
                )
                if existing_pod is None or deletion_timestamp is not None:
                    logger.warning(
                        "Pod name conflict during create, waiting for stale pod deletion before retry",
                        pod_name=pod_name,
                        namespace=self._namespace,
                        deleting=deletion_timestamp is not None,
                    )
                    deleted = await self._wait_until_pod_deleted(pod_name)
                    if deleted:
                        created_pod = await asyncio.to_thread(
                            self._core_v1.create_namespaced_pod,
                            namespace=self._namespace,
                            body=pod,
                        )
                        mount_method = "s3fs" if use_s3_mount else "emptyDir"
                        logger.info(
                            f"Created pod {created_pod.metadata.name} for session {config.name} "
                            f"in namespace {self._namespace} after waiting for stale pod deletion "
                            f"(mount method: {mount_method})"
                        )
                        return created_pod.metadata.name
            logger.error(f"Failed to create pod: {e}")
            raise

    async def start_container(self, container_id: str) -> None:
        """
        Start the Pod

        A Kubernetes Pod starts on its own once created; this method exists for interface compatibility.
        """
        await self._ensure_connected()
        # A K8s Pod starts automatically after creation, so there is nothing to call
        logger.debug(f"Pod {container_id} starts automatically after creation")

    async def stop_container(self, container_id: str, timeout: int = 30) -> None:
        """
        Stop, meaning delete, the Pod

        With s3fs there is no PVC to clean up; the mount goes away with the Pod.

        Args:
            container_id: Pod name
            timeout: graceful termination timeout in seconds
        """
        await self._ensure_connected()

        try:
            await asyncio.to_thread(
                self._core_v1.delete_namespaced_pod,
                name=container_id,
                namespace=self._namespace,
                grace_period_seconds=timeout,
            )
            logger.info(f"Stopped pod {container_id}")
        except ApiException as e:
            if e.status == 404:
                logger.warning(f"Pod {container_id} not found")
            else:
                logger.error(f"Failed to stop pod {container_id}: {e}")
                raise

    async def remove_container(self, container_id: str, force: bool = False) -> None:
        """
        Delete the Pod

        With s3fs there is no PVC to clean up; the mount goes away with the Pod.

        Args:
            container_id: Pod name
            force: whether to force the delete with grace_period_seconds=0
        """
        await self._ensure_connected()

        try:
            await asyncio.to_thread(
                self._core_v1.delete_namespaced_pod,
                name=container_id,
                namespace=self._namespace,
                grace_period_seconds=0 if force else 30,
            )
            logger.info(f"Removed pod {container_id}")
        except ApiException as e:
            if e.status == 404:
                logger.warning(f"Pod {container_id} not found")
            else:
                logger.warning(f"Failed to remove pod {container_id}: {e}")

    async def get_container_status(self, container_id: str) -> ContainerInfo:
        """
        Get the Pod status

        Args:
            container_id: Pod name

        Returns:
            A ContainerInfo object
        """
        await self._ensure_connected()
        try:
            pod = await asyncio.to_thread(
                self._core_v1.read_namespaced_pod,
                name=container_id,
                namespace=self._namespace,
            )

            # Translate the K8s Pod status into ContainerInfo
            phase = pod.status.phase
            if phase == "Running" and pod.status.container_statuses:
                for container_status in pod.status.container_statuses:
                    if container_status.name == "executor":
                        if container_status.state.terminated:
                            phase = "exited"
                        elif container_status.state.waiting:
                            phase = "waiting"
                        break

            ip_address = pod.status.pod_ip
            created_at = (
                pod.metadata.creation_timestamp.isoformat()
                if pod.metadata.creation_timestamp
                else ""
            )
            started_at = pod.status.start_time.isoformat() if pod.status.start_time else None

            # Read the exit code, when it has terminated
            exit_code = None
            if pod.status.container_statuses:
                for container_status in pod.status.container_statuses:
                    if container_status.name == "executor" and container_status.state.terminated:
                        exit_code = container_status.state.terminated.exit_code
                        break

            # Read the image name
            image = ""
            if pod.spec.containers:
                for container in pod.spec.containers:
                    if container.name == "executor":
                        image = container.image
                        break

            return ContainerInfo(
                id=container_id,
                name=container_id,
                image=image,
                status=phase.lower(),
                ip_address=ip_address,
                created_at=created_at,
                started_at=started_at,
                exited_at=None,
                exit_code=exit_code,
            )

        except ApiException as e:
            if e.status == 404:
                logger.error(f"Pod {container_id} not found")
                raise ValueError(f"Pod {container_id} not found") from e
            else:
                logger.error(f"Failed to get pod status {container_id}: {e}")
                raise

    async def is_container_running(self, container_id: str) -> bool:
        """
        Check whether the Pod is running

        Args:
            container_id: Pod name

        Returns:
            bool: whether the Pod is running
        """
        try:
            container_info = await self.get_container_status(container_id)
            return container_info.status == "running"
        except Exception as e:
            logger.warning(f"Failed to check pod {container_id} status: {e}")
            return False

    async def get_container_ownership(
        self,
        container_id: str,
    ) -> ContainerOwnershipInfo | None:
        """Read the Pod ownerReferences and the takeover annotations."""
        await self._ensure_connected()

        try:
            pod = await asyncio.to_thread(
                self._core_v1.read_namespaced_pod,
                name=container_id,
                namespace=self._namespace,
            )
        except ApiException as e:
            if e.status == 404:
                return None
            logger.error(f"Failed to get ownership for pod {container_id}: {e}")
            raise

        owner_refs = list(pod.metadata.owner_references or [])
        owner_pod_name = None
        owner_pod_uid = None
        has_pod_owner_reference = False

        for owner_ref in owner_refs:
            if owner_ref.kind == "Pod":
                owner_pod_name = owner_ref.name
                owner_pod_uid = owner_ref.uid
                has_pod_owner_reference = True
                break

        annotations = dict(pod.metadata.annotations or {})
        if owner_pod_name is None:
            owner_pod_name = annotations.get("control-plane-pod-name")
        if owner_pod_uid is None:
            owner_pod_uid = annotations.get("control-plane-pod-uid")

        return ContainerOwnershipInfo(
            owner_pod_name=owner_pod_name,
            owner_pod_uid=owner_pod_uid,
            annotations=annotations,
            has_owner_reference=has_pod_owner_reference,
        )

    async def get_container_logs(
        self, container_id: str, tail: int = 100, since: str | None = None
    ) -> str:
        """
        Get the Pod logs

        Args:
            container_id: Pod name
            tail: how many trailing lines to return
            since: timestamp, optional

        Returns:
            The logs as a string
        """
        await self._ensure_connected()
        try:
            logs = await asyncio.to_thread(
                self._core_v1.read_namespaced_pod_log,
                name=container_id,
                namespace=self._namespace,
                container="executor",
                tail_lines=tail,
                since_seconds=None,  # since_time needs converting
            )
            return logs
        except ApiException as e:
            logger.error(f"Failed to get logs for pod {container_id}: {e}")
            raise

    async def wait_container(
        self, container_id: str, timeout: int | None = None
    ) -> ContainerResult:
        """
        Wait for the Pod to finish

        Args:
            container_id: Pod name
            timeout: timeout in seconds

        Returns:
            A ContainerResult object
        """
        await self._ensure_connected()

        async def _wait() -> ContainerResult:
            while True:
                try:
                    pod = await asyncio.to_thread(
                        self._core_v1.read_namespaced_pod,
                        name=container_id,
                        namespace=self._namespace,
                    )

                    # Check the Pod status
                    if pod.status.phase == "Succeeded":
                        # Read the logs
                        logs = await self.get_container_logs(container_id, tail=-1)
                        return ContainerResult(
                            status="completed",
                            stdout=logs,
                            stderr="",
                            exit_code=0,
                        )
                    elif pod.status.phase == "Failed":
                        # Read the logs
                        logs = await self.get_container_logs(container_id, tail=-1)
                        return ContainerResult(
                            status="failed",
                            stdout=logs,
                            stderr="Pod failed",
                            exit_code=1,
                        )

                    # Check the container status
                    if pod.status.container_statuses:
                        for container_status in pod.status.container_statuses:
                            if (
                                container_status.name == "executor"
                                and container_status.state.terminated
                            ):
                                logs = await self.get_container_logs(container_id, tail=-1)
                                terminated = container_status.state.terminated
                                return ContainerResult(
                                    status="completed" if terminated.exit_code == 0 else "failed",
                                    stdout=logs,
                                    stderr="",
                                    exit_code=terminated.exit_code,
                                )

                    await asyncio.sleep(1)

                except ApiException as e:
                    if e.status == 404:
                        return ContainerResult(
                            status="failed",
                            stdout="",
                            stderr=f"Pod {container_id} not found",
                            exit_code=1,
                        )
                    raise

        try:
            if timeout:
                return await asyncio.wait_for(_wait(), timeout=timeout)
            else:
                return await _wait()

        except TimeoutError:
            logger.warning(f"Pod {container_id} timed out")
            return ContainerResult(
                status="timeout",
                stdout="",
                stderr=f"Pod execution timed out after {timeout}s",
                exit_code=124,
            )

    async def ping(self) -> bool:
        """
        Check the Kubernetes connection

        Returns:
            bool: whether the connection is healthy
        """
        try:
            await self._ensure_connected()
            await asyncio.to_thread(
                self._core_v1.list_namespace,
                limit=1,
            )
            return True
        except Exception as e:
            logger.error(f"Kubernetes ping failed: {e}")
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
