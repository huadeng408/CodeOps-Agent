"""Dependency-free model routing and native embedding dimension guards."""

from __future__ import annotations

from collections.abc import Iterable


BGE_M3_NATIVE_DIMENSIONS = 1024


def is_bge_m3(model_name: str) -> bool:
    return "bge-m3" in model_name.lower()


def native_dimensions(model_name: str) -> int | None:
    if is_bge_m3(model_name):
        return BGE_M3_NATIVE_DIMENSIONS
    return None


def backend_name(model_name: str) -> str:
    return "sentence-transformers" if is_bge_m3(model_name) else "fastembed"


def prepare_vectors(
    model_name: str,
    vectors: Iterable[Iterable[float]],
    requested_dimensions: int | None,
) -> list[list[float]]:
    """Validate native BGE-M3 output without padding or truncation."""
    values = [[float(value) for value in vector] for vector in vectors]
    native = native_dimensions(model_name)
    if native is None:
        return [_resize_vector(vector, requested_dimensions) for vector in values]
    if requested_dimensions not in (None, 0, native):
        raise ValueError(
            f"BGE-M3 native dimension is {native}; requested {requested_dimensions} would require resizing"
        )
    for index, vector in enumerate(values):
        if len(vector) != native:
            raise ValueError(
                f"BGE-M3 native dimension mismatch at vector {index}: expected {native}, got {len(vector)}"
            )
    return values


def _resize_vector(vector: list[float], target_dim: int | None) -> list[float]:
    if not target_dim or target_dim <= 0 or len(vector) == target_dim:
        return vector
    if len(vector) > target_dim:
        return vector[:target_dim]
    if not vector:
        return [0.0] * target_dim
    resized = list(vector)
    index = 0
    while len(resized) < target_dim:
        resized.append(vector[index % len(vector)])
        index += 1
    return resized
