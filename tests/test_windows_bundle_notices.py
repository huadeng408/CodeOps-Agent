from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest


spec = importlib.util.spec_from_file_location(
    "windows_bundle", Path(__file__).parents[1] / "scripts/build-windows.py"
)
assert spec is not None and spec.loader is not None
windows_bundle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(windows_bundle)


@pytest.fixture
def components(tmp_path: Path) -> dict:
    source = tmp_path / "upstream-LICENSE.txt"
    source.write_bytes(b"abc")
    return {
        "python_packages": [{"name": "fixture-package", "version": "1.2.3"}],
        "python_notices": [{
            "name": "fixture-package",
            "version": "1.2.3",
            "notices": [{
                "file": "LICENSE.txt",
                "url": source.as_uri(),
                "sha256": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
            }],
        }],
    }


def test_bundle_includes_pinned_python_notice(tmp_path: Path, components: dict) -> None:
    windows_bundle.include_python_notices(components, tmp_path / "bundle", tmp_path / "cache")

    assert (tmp_path / "bundle/licenses/python/fixture-package/LICENSE.txt").read_bytes() == b"abc"


@pytest.mark.parametrize("packages", [[], [{"name": "fixture-package", "version": "2.0.0"}]])
def test_bundle_rejects_notice_for_missing_or_changed_package(tmp_path: Path, components: dict, packages: list) -> None:
    components["python_packages"] = packages

    with pytest.raises(ValueError, match="version"):
        windows_bundle.include_python_notices(components, tmp_path / "bundle", tmp_path / "cache")

    assert not (tmp_path / "bundle").exists()


@pytest.mark.parametrize(("field", "value"), [
    ("name", "../escape"),
    ("file", "../escape"),
    ("file", "..\\escape"),
    ("sha256", "../escape"),
    ("file", "NUL"),
    ("file", "con.txt"),
    ("name", "COM1"),
    ("file", "LICENSE."),
])
def test_bundle_rejects_unsafe_notice_paths(tmp_path: Path, components: dict, field: str, value: str) -> None:
    if field == "name":
        components["python_packages"][0]["name"] = value
        components["python_notices"][0]["name"] = value
    else:
        components["python_notices"][0]["notices"][0][field] = value

    with pytest.raises(ValueError, match="unsafe"):
        windows_bundle.include_python_notices(components, tmp_path / "bundle", tmp_path / "cache")

    assert not (tmp_path / "bundle").exists()


def test_bundle_reuses_verified_notice_without_upstream(tmp_path: Path, components: dict) -> None:
    windows_bundle.include_python_notices(components, tmp_path / "first", tmp_path / "cache")
    (tmp_path / "upstream-LICENSE.txt").unlink()

    windows_bundle.include_python_notices(components, tmp_path / "second", tmp_path / "cache")

    assert (tmp_path / "second/licenses/python/fixture-package/LICENSE.txt").read_bytes() == b"abc"


def test_bundle_rejects_corrupted_cached_notice(tmp_path: Path, components: dict) -> None:
    windows_bundle.include_python_notices(components, tmp_path / "first", tmp_path / "cache")
    (tmp_path / "upstream-LICENSE.txt").unlink()
    material, = [path for path in (tmp_path / "cache").rglob("*") if path.is_file()]
    material.write_bytes(b"corrupted")

    with pytest.raises(ValueError, match="checksum"):
        windows_bundle.include_python_notices(components, tmp_path / "second", tmp_path / "cache")

    assert not (tmp_path / "second").exists()
