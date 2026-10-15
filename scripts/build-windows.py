"""Assemble the pinned portable Windows product; never install at launch time."""
from __future__ import annotations

import argparse
import email
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import urllib.request
import zipfile


ROOT = Path(__file__).resolve().parents[1]
PROFILE = ROOT / "deployments/windows"


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def extract(archive: Path, destination: Path) -> None:
    with zipfile.ZipFile(archive) as source:
        for item in source.infolist():
            name = PurePosixPath(item.filename)
            if name.is_absolute() or ".." in name.parts or "\\" in item.filename or ":" in item.filename or (item.external_attr >> 16) & 0o170000 == 0o120000:
                raise ValueError("archive has an unsafe member")
        source.extractall(destination)


def download(url: str, destination: Path, checksum: str) -> None:
    if not destination.exists():
        created = False
        try:
            with urllib.request.urlopen(url, timeout=30) as response, destination.open("xb") as target:
                created = True
                shutil.copyfileobj(response, target)
        except Exception:
            # A partial file is never accepted as a component or executed.
            if created and destination.exists() and sha(destination) != checksum:
                destination.unlink()
            raise
    if sha(destination) != checksum:
        raise ValueError("downloaded component checksum mismatch")


def source_inventory() -> dict[str, str]:
    names = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "--",
        "cmd", "internal", "pkg", "gen", "orchestrator", "codeagent", "frontend", "go.mod", "go.sum", "pyproject.toml",
        "LICENSE", "configs/local-core.yaml", "scripts/build-windows.py", "deployments/windows/README.md",
        "deployments/windows/components.json", "deployments/windows/requirements.lock"], cwd=ROOT, text=True).splitlines()
    # Go also compiles untracked/ignored .go files in package directories.
    names += [path.relative_to(ROOT).as_posix() for directory in ("cmd", "internal", "pkg", "gen") for path in (ROOT / directory).rglob("*.go")]
    result = {}
    for name in sorted(set(names)):
        path = ROOT / name
        if path.is_file():
            if path.is_symlink() or not path.resolve().is_relative_to(ROOT):
                raise ValueError("source inventory contains a redirected file")
            result[name] = sha(path)
    return result


def build(destination: Path, cache: Path) -> None:
    if sys.platform != "win32":
        raise ValueError("the Windows distribution must be built on Windows")
    destination.mkdir(parents=True, exist_ok=False)
    cache.mkdir(parents=True, exist_ok=True)
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    source_files = source_inventory()
    components = json.loads((PROFILE / "components.json").read_text(encoding="utf-8"))
    for name in ("python", "git"):
        pin = components[name]
        archive = cache / pin["url"].rsplit("/", 1)[1]
        download(pin["url"], archive, pin["sha256"])
        extract(archive, destination / name)
    wheels = cache / "wheels"
    subprocess.run([sys.executable, "-m", "pip", "download", "--require-hashes", "--only-binary=:all:",
                    "--platform", "win_amd64", "--python-version", "313", "--implementation", "cp", "--abi", "cp313",
                    "--dest", str(wheels), "-r", str(PROFILE / "requirements.lock")], check=True, cwd=ROOT)
    # pip installs only during the build; the shipped isolated interpreter has no pip.
    subprocess.run([sys.executable, "-m", "pip", "install", "--require-hashes", "--no-deps", "--no-compile",
                    "--only-binary=:all:", "--platform", "win_amd64", "--python-version", "313", "--implementation", "cp", "--abi", "cp313",
                    "--no-index", "--find-links", str(wheels), "--target", str(destination / "python/packages"),
                    "-r", str(PROFILE / "requirements.lock")], check=True, cwd=ROOT)
    scripts = (destination / "python/packages/bin").resolve()
    if scripts.is_relative_to(destination / "python/packages") and scripts.exists():
        shutil.rmtree(scripts)  # Unused pip entry points contain the build interpreter's path.
    packages = []
    wheel_hashes = set(re.findall(r"--hash=sha256:([0-9a-f]{64})", (PROFILE / "requirements.lock").read_text(encoding="utf-8")))
    for wheel in sorted(wheels.glob("*.whl")):
        if sha(wheel) not in wheel_hashes:
            continue
        with zipfile.ZipFile(wheel) as archive:
            metadata = email.message_from_bytes(archive.read(next(n for n in archive.namelist() if n.endswith(".dist-info/METADATA"))))
            packages.append({"name": metadata["Name"], "version": metadata["Version"], "sha256": sha(wheel),
                             "license": metadata["License-Expression"] or metadata["License"] or "; ".join(
                                 x for x in metadata.get_all("Classifier", []) if x.startswith("License ::"))})
    components["python_packages"] = packages
    runtime_sources = subprocess.check_output(["git", "ls-files", "--cached", "--", "orchestrator", "codeagent"], cwd=ROOT, text=True).splitlines()
    for name in runtime_sources:
        if name not in source_files:
            raise ValueError("runtime source is missing from the frozen inventory")
        target = destination / "app" / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(ROOT / name, target)
    (destination / "python/python313._pth").write_text("python313.zip\n.\npackages\n../app\n", encoding="utf-8")
    subprocess.run(["npm.cmd", "run", "build"], cwd=ROOT / "frontend", check=True)
    shutil.copytree(ROOT / "frontend/dist", destination / "frontend/dist")
    (destination / "configs").mkdir()
    shutil.copyfile(ROOT / "configs/local-core.yaml", destination / "configs/local-core.yaml")
    (destination / "licenses").mkdir()
    shutil.copyfile(ROOT / "LICENSE", destination / "licenses/CodeOps-Agent-LICENSE")
    shutil.copyfile(PROFILE / "requirements.lock", destination / "licenses/requirements.lock")
    shutil.copyfile(PROFILE / "README.md", destination / "README.md")
    frontend_packages = []
    lock = json.loads((ROOT / "frontend/package-lock.json").read_text(encoding="utf-8"))
    for location, pin in lock["packages"].items():
        if not location or pin.get("dev"):
            continue
        source = ROOT / "frontend" / location
        installed = json.loads((source / "package.json").read_text(encoding="utf-8"))
        if installed["version"] != pin["version"]:
            raise ValueError("installed frontend dependency differs from its lock pin")
        notices = []
        for path in source.iterdir():
            if path.is_file() and path.name.upper().startswith(("LICENSE", "LICENCE", "NOTICE", "COPYING")):
                target = destination / "licenses/frontend" / installed["name"] / path.name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(path, target)
                notices.append(path.name)
        if not notices:
            raise ValueError("frontend runtime dependency has no license notice")
        frontend_packages.append({"name": installed["name"], "version": pin["version"], "integrity": pin["integrity"],
                                  "url": pin["resolved"], "license": pin["license"], "notices": notices})
    components["frontend_packages"] = frontend_packages
    shutil.copyfile(ROOT / "frontend/package-lock.json", destination / "licenses/frontend/package-lock.json")
    environment = dict(os.environ, GOOS="windows", GOARCH="amd64", CGO_ENABLED="0")
    (destination / "bin").mkdir()
    for package, output in (("server", "bin/code-agent-server.exe"), ("launcher", "CodeOps-Agent.exe")):
        subprocess.run(["go", "build", "-trimpath", "-o", str(destination / output), "./cmd/" + package],
                       cwd=ROOT, env=environment, check=True)
    modules_text = subprocess.check_output(["go", "list", "-m", "-json", "all"], cwd=ROOT, text=True)
    modules = {}
    decoder = json.JSONDecoder()
    while modules_text.strip():
        module, end = decoder.raw_decode(modules_text.lstrip())
        modules[module["Path"]] = module.get("Replace", module)
        modules_text = modules_text.lstrip()[end:]
    linked = {}
    for binary in ("CodeOps-Agent.exe", "bin/code-agent-server.exe"):
        info = json.loads(subprocess.check_output(["go", "version", "-m", "-json", str(destination / binary)], text=True))
        components["go_version"] = info["GoVersion"]
        for module in info.get("Deps", []):
            linked[module["Path"]] = module.get("Replace", module)
    go_packages = []
    for name, module in sorted(linked.items()):
        source = Path(modules[name]["Dir"])
        notices = []
        for path in source.rglob("*"):
            if path.is_file() and path.name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE", "PATENTS")):
                relative = path.relative_to(source)
                target = destination / "licenses/go" / name / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(path, target)
                notices.append(relative.as_posix())
        if not notices:
            raise ValueError("linked Go module has no license notice: " + name)
        go_packages.append({"name": name, "version": module["Version"], "sum": module.get("Sum", ""), "notices": notices})
    components["go_packages"] = go_packages
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
    shutil.copyfile(goroot / "LICENSE", destination / "licenses/Go-LICENSE")
    shutil.copyfile(goroot / "PATENTS", destination / "licenses/Go-PATENTS")
    if source_inventory() != source_files:
        raise ValueError("source changed during the build; the partial distribution is not deliverable")
    manifest = {"schema_version": 1, "source_commit": commit,
                "source_files": source_files,
                "components": components,
                "files": {path.relative_to(destination).as_posix(): sha(path) for path in sorted(destination.rglob("*")) if path.is_file()}}
    (destination / "bundle-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"files": len(manifest["files"]), "manifest_sha256": sha(destination / "bundle-manifest.json")}))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--cache", type=Path, default=PROFILE / ".local/downloads")
    args = parser.parse_args()
    build(args.output.resolve(), args.cache.resolve())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
