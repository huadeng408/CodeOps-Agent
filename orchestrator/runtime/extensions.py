from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, Iterable, Mapping


SUPPORTED_KINDS = frozenset({"attachment", "code_runtime", "lsp"})
DEFAULT_MAX_PAYLOAD_BYTES = 1 << 20


class ExtensionInvocationError(RuntimeError):
    """Stable, non-sensitive error raised at the Python orchestration seam."""


@dataclass(frozen=True, slots=True)
class ExtensionSpec:
    id: str
    kind: str
    version: str
    description: str
    operations: tuple[str, ...]
    max_payload_bytes: int = DEFAULT_MAX_PAYLOAD_BYTES

    def __post_init__(self) -> None:
        extension_id = self.id.strip()
        version = self.version.strip()
        kind = self.kind.strip()
        description = self.description.strip()
        if isinstance(self.operations, (str, bytes)):
            raise ValueError("operations must be a sequence of operation names")
        try:
            raw_operations = tuple(self.operations)
        except TypeError as exc:
            raise ValueError("operations must be a sequence of operation names") from exc
        operations = tuple(sorted({str(operation).strip() for operation in raw_operations if str(operation).strip()}))
        if not extension_id or any(char.isspace() for char in extension_id):
            raise ValueError("extension id must be a non-empty token")
        if kind not in SUPPORTED_KINDS:
            raise ValueError("unsupported extension kind")
        if not version or not description or "\n" in description or "\r" in description or not operations:
            raise ValueError("extension version, description, and operations are required")
        if any(any(char.isspace() for char in operation) for operation in operations):
            raise ValueError("extension operation must be a token")
        if len(operations) != len(raw_operations):
            raise ValueError("extension operations must be unique and non-empty")
        if self.max_payload_bytes <= 0:
            object.__setattr__(self, "max_payload_bytes", DEFAULT_MAX_PAYLOAD_BYTES)
        object.__setattr__(self, "id", extension_id)
        object.__setattr__(self, "kind", kind)
        object.__setattr__(self, "version", version)
        object.__setattr__(self, "description", description)
        object.__setattr__(self, "operations", operations)

    def metadata(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "kind": self.kind,
            "version": self.version,
            "description": self.description,
            "operations": list(self.operations),
            "max_payload_bytes": self.max_payload_bytes,
        }


HarnessCall = Callable[..., Mapping[str, Any]]


class ExtensionRegistry:
    """Python-side metadata and invocation seam; execution belongs to Go."""

    def __init__(self, specs: Iterable[ExtensionSpec] = ()) -> None:
        self._specs: dict[str, ExtensionSpec] = {}
        for spec in specs:
            self.register(spec)

    @classmethod
    def from_manifest(cls, path: str | Path) -> "ExtensionRegistry":
        """Load metadata exported by the Go Harness; bodies and adapters stay out."""
        try:
            payload = json.loads(Path(path).read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return cls()
        raw_specs = payload.get("extensions", []) if isinstance(payload, dict) else []
        specs: list[ExtensionSpec] = []
        for raw in raw_specs:
            if not isinstance(raw, Mapping):
                continue
            try:
                specs.append(
                    ExtensionSpec(
                        id=str(raw.get("id", "")),
                        kind=str(raw.get("kind", "")),
                        version=str(raw.get("version", "")),
                        description=str(raw.get("description", "")),
                        operations=tuple(str(item) for item in raw.get("operations", [])),
                        max_payload_bytes=int(raw.get("max_payload_bytes", DEFAULT_MAX_PAYLOAD_BYTES)),
                    )
                )
            except (TypeError, ValueError):
                continue
        return cls(specs)

    def register(self, spec: ExtensionSpec) -> None:
        if spec.id in self._specs:
            raise ValueError(f"extension {spec.id!r} is already registered")
        self._specs[spec.id] = spec

    def metadata(self) -> list[dict[str, Any]]:
        return [self._specs[key].metadata() for key in sorted(self._specs)]

    def resolve(self, extension_id: str) -> ExtensionSpec | None:
        return self._specs.get(extension_id.strip())

    def validate(
        self,
        extension_id: str,
        session_id: str,
        operation: str,
        payload: bytes,
        *,
        kind: str = "",
        version: str = "",
    ) -> ExtensionSpec:
        spec = self.resolve(extension_id)
        if spec is None:
            raise ExtensionInvocationError("extension is not registered")
        if not session_id.strip():
            raise ExtensionInvocationError("session id is required")
        if kind and kind != spec.kind:
            raise ExtensionInvocationError("extension kind mismatch")
        if version and version != spec.version:
            raise ExtensionInvocationError("extension version mismatch")
        if operation not in spec.operations:
            raise ExtensionInvocationError("extension operation is not supported")
        if len(payload) > spec.max_payload_bytes:
            raise ExtensionInvocationError("payload exceeds extension limit")
        return spec

    def invoke(
        self,
        extension_id: str,
        session_id: str,
        operation: str,
        payload: bytes,
        harness_call: HarnessCall | None = None,
    ) -> Mapping[str, Any]:
        spec = self.validate(extension_id, session_id, operation, payload)
        if harness_call is None:
            raise ExtensionInvocationError("Harness callback is required")
        try:
            result = harness_call(
                extension_id=spec.id,
                kind=spec.kind,
                version=spec.version,
                session_id=session_id.strip(),
                operation=operation,
                payload=bytes(payload),
            )
        except Exception as exc:  # pragma: no cover - exact adapter is external
            raise ExtensionInvocationError(f"Harness invocation failed ({type(exc).__name__})") from None
        if not isinstance(result, Mapping):
            raise ExtensionInvocationError("Harness returned an invalid result")
        if str(result.get("status", "ok")).lower() in {"error", "failed", "denied"}:
            raise ExtensionInvocationError("Harness rejected extension request")
        return dict(result)
