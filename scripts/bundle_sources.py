"""Select paths from the Git index and copy their current working-tree contents."""
import argparse
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess


CPLFOLD = "third_party/cplfold"
WEB_BRIDGE = "web/cplfold"
CPLFOLD_SOURCE_DIRS = (CPLFOLD, WEB_BRIDGE)
EXCLUDED_DIRS = {".git", "__pycache__", "tests"}
EXCLUDED_SUFFIXES = (".pyc", ".pyo", ".nbc", ".nbi")


def excluded_directory(path):
    if EXCLUDED_DIRS.intersection(path.parts):
        return True
    if "data" not in path.parts:
        return False
    # A package can load its own data/ with importlib.resources. The top-level
    # data/ in the vendored project and datasets in other trees are examples.
    for source in CPLFOLD_SOURCE_DIRS:
        if path.is_relative_to(source):
            return path.relative_to(source).parts[0] == "data"
    return True


def selected_files(root, directories, required_files, *, include=None):
    """List package inputs. An extracted source archive supplies its own file tree."""
    root = Path(root).resolve()
    for name in directories:
        if not (root / name).is_dir():
            raise ValueError(f"package source directory is missing: {name}")

    if (root / ".git").exists():
        try:
            output = subprocess.check_output(
                ["git", "ls-files", "--recurse-submodules", "-z", "--", *directories, *required_files],
                cwd=root,
            )
        except FileNotFoundError as error:
            raise ValueError("Git is needed to select tracked files in a checkout. "
                             "An extracted Git source archive can be built without Git.") from error
        candidates = {os.fsdecode(name) for name in output.split(b"\0") if name}
    else:
        candidates = set(required_files)
        for name in directories:
            for directory, subdirs, filenames in os.walk(root / name):
                parent = Path(directory).relative_to(root)
                subdirs[:] = [subdir for subdir in subdirs if not excluded_directory(parent / subdir)]
                for filename in filenames:
                    candidates.add((Path(directory) / filename).relative_to(root).as_posix())

    for name in required_files:
        if name not in candidates:
            raise ValueError(f"required package source is missing from the Git index: {name}")
    result = []
    for name in sorted(candidates):
        path = PurePosixPath(name)
        if (excluded_directory(path.parent) or path.name == ".DS_Store"
                or path.name.endswith(EXCLUDED_SUFFIXES)):
            continue
        if include is not None and name not in required_files and not include(name):
            continue
        source = root / name
        if not source.is_file() or not source.resolve().is_relative_to(root):
            raise ValueError(f"package source is missing or outside the source tree: {name}")
        result.append(name)
    return result


REQUIRED_CPLFOLD_MODULES = ("CPLfold.py", "CPLfold_parser.py", "CPLcofold.py", "CPLcofold_parser.py")
# These upstream command-line programs use example data or the optional BAM toolchain.
LOCAL_PROGRAMS = {"example.py", "example_with_paris.py", "extract_paris_scores.py"}
WEB_NOTICES = {
    "LICENSE": "CPLfold-LICENSE.txt",
    "THIRD_PARTY_NOTICES.md": "CPLfold-THIRD_PARTY_NOTICES.md",
    "LICENSES/GPL-2.0-or-later.txt": "CPLfold-GPL-2.0-or-later.txt",
    "LICENSES/LinearFold-LICENSE.txt": "CPLfold-LinearFold-LICENSE.txt",
    "LICENSES/LinearCoFold-LICENSE.txt": "CPLfold-LinearCoFold-LICENSE.txt",
    "LICENSES/ViennaRNA-COPYING.txt": "CPLfold-ViennaRNA-COPYING.txt",
}


def web_package_path(name):
    source_root = WEB_BRIDGE if name.startswith(WEB_BRIDGE + "/") else CPLFOLD
    relative = PurePosixPath(name).relative_to(source_root)
    if (source_root == CPLFOLD and relative.as_posix() in LOCAL_PROGRAMS
            or relative.parts[0] in {"data", "docs", "examples", "LICENSES"}
            or any(part.startswith(".") for part in relative.parts)
            or relative.suffix.lower() in {".so", ".pyd", ".dylib", ".dll", ".o", ".a"}):
        return None
    if relative.suffix == ".py" or len(relative.parts) > 1:
        return relative
    return None


def stage_web_sources(root, package, assets):
    if not (root / CPLFOLD / "Utils").is_dir():
        raise ValueError(f"package source directory is missing: {CPLFOLD}/Utils")
    notices = {f"{CPLFOLD}/{name}": destination for name, destination in WEB_NOTICES.items()}
    required = [f"{WEB_BRIDGE}/cplfold_web.py", *(f"{CPLFOLD}/{name}" for name in REQUIRED_CPLFOLD_MODULES), *notices]
    files = selected_files(root, CPLFOLD_SOURCE_DIRS, required, include=web_package_path)
    paths = {name: web_package_path(name) for name in files if name not in notices}
    packages = {path.parts[0] for path in paths.values() if path.suffix == ".py" and len(path.parts) > 1}
    paths = {name: path for name, path in paths.items() if path.suffix == ".py" or path.parts[0] in packages}
    modules = {CPLFOLD: {}, WEB_BRIDGE: {}}
    for name, path in paths.items():
        if path.suffix == ".py":
            source = WEB_BRIDGE if name.startswith(WEB_BRIDGE + "/") else CPLFOLD
            parts = path.with_suffix("").parts
            module = parts[:-1] if len(parts) > 1 and parts[-1] == "__init__" else parts
            modules[source][module] = name

    # Both native import roots share one directory in the browser archive. Preserve
    # split namespace packages, but reject a module or regular package from one
    # root masking all or part of the same import path from the other.
    owners = {}
    for name, path in paths.items():
        other = CPLFOLD if name.startswith(WEB_BRIDGE + "/") else WEB_BRIDGE
        parts = path.with_suffix("").parts if path.suffix == ".py" else path.parent.parts
        for length in range(1, len(parts) + 1):
            competing = modules[other].get(parts[:length])
            if competing is not None:
                raise ValueError(f"conflicting browser modules: {name} and {competing}")
        if path in owners:
            raise ValueError(f"conflicting browser package files: {name} and {owners[path]}")
        owners[path] = name
    for path, name in owners.items():
        for parent in path.parents:
            if parent in owners:
                raise ValueError(f"conflicting browser package files: {name} and {owners[parent]}")
    destinations = {name: package / path for name, path in paths.items()}
    destinations.update({name: assets / target for name, target in notices.items()})
    for name, destination in destinations.items():
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(root / name, destination)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Stage the CPLfold sources for the browser archive.")
    parser.add_argument("package", type=Path)
    parser.add_argument("assets", type=Path)
    args = parser.parse_args()
    try:
        stage_web_sources(Path(__file__).resolve().parents[1], args.package, args.assets)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f"browser source staging failed: {error}\n")
