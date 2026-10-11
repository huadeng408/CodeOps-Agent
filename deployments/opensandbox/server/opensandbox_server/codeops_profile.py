"""The pinned local CodeOps profile; policy is baked into the immutable server image."""
import json
import os
import re
from pathlib import Path

from docker.errors import DockerException
from fastapi import HTTPException

PROFILE = json.loads(Path("/app/codeops-profile.json").read_text(encoding="utf-8"))

def reject():
    raise HTTPException(409, detail="request is outside the approved CodeOps runtime profile")

def validate_create(request):
    metadata = request.metadata or {}
    if (
        request.image is None or request.image.uri not in PROFILE["task_images"]
        or request.image.auth is not None or request.snapshot_id is not None
        or request.platform is not None or request.lifecycle is not None
        or request.credential_proxy is not None or request.secure_access
        or request.extensions or request.resource_requests is not None
        or request.entrypoint != ["sleep", "infinity"] or request.timeout != 120
        or set(metadata) != {"codeops.operation_id", "codeops.opensandbox.policy_sha256"}
        or not re.fullmatch(r"[0-9a-f]{64}", metadata.get("codeops.operation_id", ""))
        or metadata.get("codeops.opensandbox.policy_sha256") != PROFILE["policy_sha256"]
    ):
        reject()
    policy = request.network_policy
    if policy is None or policy.model_dump(by_alias=True, exclude_none=True) != {
        "defaultAction": "deny", "egress": [
            {"action": "deny", "target": "0.0.0.0/0"},
            {"action": "deny", "target": "::/0"},
        ],
    }:
        reject()
    volumes = request.volumes or []
    if len(volumes) != 1 or volumes[0].name != "workspace" or volumes[0].host is None or not volumes[0].read_only or volumes[0].mount_path != "/workspace" or volumes[0].sub_path:
        reject()
    env = request.env or {}
    expected = {
        "HOME": "/run/codeops", "TMPDIR": "/run/codeops", "GOCACHE": "/run/codeops/go-build",
        "GOPATH": "/run/codeops/go", "GOTOOLCHAIN": "local", "GOENV": "off", "GOPROXY": "off",
        "GOSUMDB": "off", "GOFLAGS": "-mod=readonly",
    }
    if env != expected:
        reject()
    limits = request.resource_limits.root if request.resource_limits else {}
    try:
        if set(limits) != {"cpu", "memory"} or not 0 < float(limits["cpu"]) <= 2 or not 0 < int(limits["memory"]) <= 1073741824:
            reject()
    except (ValueError, TypeError):
        reject()

def ensure_local_image(service, image_uri, auth_config, platform):
    approved = set(PROFILE["task_images"]) | {PROFILE["execd_image"], PROFILE["egress_image"]}
    if image_uri not in approved or auth_config is not None or platform is not None:
        reject()
    try:
        image = service.docker_client.images.get(image_uri)
    except DockerException:
        reject()
    if image.attrs.get("Id") != image_uri or image.attrs.get("Os") != "linux" or image.attrs.get("Architecture") != "amd64":
        reject()

def runtime_environment(environment):
    token = os.environ.get("CODEOPS_EXECD_ACCESS_TOKEN", "")
    if not re.fullmatch(r"[0-9a-f]{64}", token):
        reject()
    return environment + ["EXECD_ACCESS_TOKEN=" + token]

def verify_prebaked_runtime(service, container):
    image = service.docker_client.images.get(container.attrs["Config"]["Image"])
    labels = image.attrs["Config"].get("Labels") or {}
    if image.id not in PROFILE["task_images"] or labels.get("codeops.execd_sha256") != PROFILE["execd_sha256"] or labels.get("codeops.bootstrap_sha256") != PROFILE["bootstrap_sha256"]:
        reject()

def delete_owned(service, sandbox_id, container_id, sidecar_id, operation_id):
    if not all(re.fullmatch(r"[0-9a-f]{64}", value or "") for value in (container_id, sidecar_id, operation_id)):
        reject()
    try:
        task = service.docker_client.containers.get(container_id)
        bound = service.docker_client.containers.get("sandbox-" + sandbox_id)
        sidecar = service.docker_client.containers.get(sidecar_id)
    except DockerException:
        reject()
    labels = task.attrs["Config"].get("Labels") or {}
    sidecar_labels = sidecar.attrs["Config"].get("Labels") or {}
    if (
        task.id != container_id or bound.id != container_id or sidecar.id != sidecar_id
        or labels.get("opensandbox.io/id") != sandbox_id
        or labels.get("codeops.operation_id") != operation_id
        or labels.get("codeops.opensandbox.policy_sha256") != PROFILE["policy_sha256"]
        or labels.get("opensandbox.io/volume-managed-by")
        or task.attrs["HostConfig"].get("NetworkMode") != "container:" + sidecar_id
        or sidecar_labels.get("opensandbox.io/egress-sidecar-for") != sandbox_id
        or sidecar_labels.get("codeops.operation_id") != operation_id
        or sidecar_labels.get("codeops.opensandbox.policy_sha256") != PROFILE["policy_sha256"]
    ):
        reject()
    # Docker container objects retain physical IDs even if a logical name is rebound.
    task.remove(force=True)
    sidecar.remove(force=True)
    service._remove_expiration_tracking(sandbox_id)
    service._metadata_store.delete(sandbox_id)
