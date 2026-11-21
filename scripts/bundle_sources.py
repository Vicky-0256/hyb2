"""Select paths from the Git index and copy their current working-tree contents."""
import argparse
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess


EXCLUDED_DIRS = {".git", "__pycache__", "tests", "data"}
EXCLUDED_SUFFIXES = (".pyc", ".pyo", ".nbc", ".nbi")


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
                subdirs[:] = [subdir for subdir in subdirs if subdir not in EXCLUDED_DIRS]
                for filename in filenames:
                    candidates.add((Path(directory) / filename).relative_to(root).as_posix())

    for name in required_files:
        if name not in candidates:
            raise ValueError(f"required package source is missing from the Git index: {name}")
    result = []
    for name in sorted(candidates):
        parts = PurePosixPath(name).parts
        if (EXCLUDED_DIRS.intersection(parts) or parts[-1] == ".DS_Store"
                or parts[-1].endswith(EXCLUDED_SUFFIXES)):
            continue
        if include is not None and name not in required_files and not include(name):
            continue
        source = root / name
        if not source.is_file() or not source.resolve().is_relative_to(root):
            raise ValueError(f"package source is missing or outside the source tree: {name}")
        result.append(name)
    return result


CPLFOLD = "third_party/cplfold"
WEB_BRIDGE = "web/cplfold"
CPLFOLD_SOURCE_DIRS = (CPLFOLD, WEB_BRIDGE)
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
    if source_root == CPLFOLD:
        if relative.as_posix() in LOCAL_PROGRAMS:
            return None
        if relative.parts[:2] == ("Utils", "energy_params"):
            return relative
    if (relative.suffix == ".py" and relative.parts[0] not in {"docs", "examples", "LICENSES"}
            and not any(part.startswith(".") for part in relative.parts)):
        return relative
    return None


def stage_web_sources(root, package, assets):
    if not (root / CPLFOLD / "Utils").is_dir():
        raise ValueError(f"package source directory is missing: {CPLFOLD}/Utils")
    notices = {f"{CPLFOLD}/{name}": destination for name, destination in WEB_NOTICES.items()}
    required = [f"{WEB_BRIDGE}/cplfold_web.py", *(f"{CPLFOLD}/{name}" for name in REQUIRED_CPLFOLD_MODULES), *notices]
    modules = {CPLFOLD: {}, WEB_BRIDGE: {}}
    destinations = {}
    for name in selected_files(root, CPLFOLD_SOURCE_DIRS, required, include=web_package_path):
        if name in notices:
            destinations[name] = assets / notices[name]
        else:
            relative = web_package_path(name)
            destinations[name] = package / relative
            if relative.suffix == ".py":
                parts = relative.with_suffix("").parts
                module = parts[:-1] if len(parts) > 1 and parts[-1] == "__init__" else parts
                source_root = WEB_BRIDGE if name.startswith(WEB_BRIDGE + "/") else CPLFOLD
                modules[source_root][module] = name

    # Both native import roots share one directory in the browser archive. Preserve
    # split namespace packages, but reject a module or regular package from one
    # root masking all or part of the same import path from the other.
    for own, other in ((CPLFOLD, WEB_BRIDGE), (WEB_BRIDGE, CPLFOLD)):
        for module, name in modules[own].items():
            for length in range(1, len(module) + 1):
                competing = modules[other].get(module[:length])
                if competing is not None:
                    raise ValueError(f"conflicting browser modules: {name} and {competing}")
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
