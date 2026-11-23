"""Select paths from the Git index and copy their current working-tree contents."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import zipfile


CPLFOLD = "third_party/cplfold"
CPLFOLD_PACKAGE = f"{CPLFOLD}/cplfold"
WEB_BRIDGE = "web/cplfold"
CPLFOLD_SOURCE_DIRS = (CPLFOLD, WEB_BRIDGE)
# The bridge's Hyb2 settings. pipeline/ is the import root of hyb2analysis; the
# browser ships only these modules from it, under hyb2analysis/.
PIPELINE = "pipeline"
HYB2_SETTINGS = tuple(f"{PIPELINE}/hyb2analysis/{name}.py"
                      for name in ("__init__", "engine_parameters", "fold_text", "iris_parameters", "support_kernels"))
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


def excluded_file(path):
    return (excluded_directory(path.parent) or path.name == ".DS_Store"
            or path.name.endswith(EXCLUDED_SUFFIXES))


def git_source_blobs(root, directories, required_files, revision=None, include=None):
    """Read the same regular-file inventory from the index or a commit, without dereferencing links."""
    paths = [*directories, *required_files]
    prefixes = tuple(name + '/' for name in directories)
    parents = {p.as_posix() for name in paths for p in PurePosixPath(name).parents} | set(directories)
    # Include ancestors: a submodule at third_party would otherwise disappear
    # from a query for third_party/cplfold, looking like an empty source tree.
    roots = sorted({PurePosixPath(name).parts[0] for name in paths})
    cmd = ['ls-tree', '-rz', revision] if revision else ['ls-files', '--stage', '-z']
    entries = subprocess.check_output(['git', '--no-replace-objects', *cmd, '--', *roots], cwd=root)
    blobs = {}
    for entry in entries.split(b'\0'):
        if not entry:
            continue
        metadata, raw_name = entry.split(b'\t', 1)
        name = os.fsdecode(raw_name)
        mode, middle, last = metadata.decode('ascii').split()
        if name in parents:
            raise ValueError(f'package source needs a regular directory, not Git mode {mode}: {name}')
        if name not in required_files and not name.startswith(prefixes):
            continue
        if excluded_file(PurePosixPath(name)):
            continue
        if include is not None and name not in required_files and not include(name):
            continue
        if mode not in ('100644', '100755'):
            raise ValueError(f'unsupported Git mode {mode} in package sources: {name} (no symlinks or submodules)')
        if not revision and last != '0':
            raise ValueError(f'unmerged package source: {name}')
        blobs[name] = last if revision else middle
    return blobs


def selected_files(root, directories, required_files, *, include=None):
    """List package inputs. An extracted source archive supplies its own file tree."""
    root = Path(root).resolve()
    for name in directories:
        if not (root / name).is_dir():
            raise ValueError(f"package source directory is missing: {name}")

    if (root / ".git").exists():
        try:
            candidates = git_source_blobs(root, directories, required_files, include=include)
        except FileNotFoundError as error:
            raise ValueError("Git is needed to select tracked files in a checkout. "
                             "An extracted Git source archive can be built without Git.") from error
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
        if excluded_file(PurePosixPath(name)):
            continue
        if include is not None and name not in required_files and not include(name):
            continue
        source = root / name
        if not source.is_file() or not source.resolve().is_relative_to(root):
            raise ValueError(f"package source is missing or outside the source tree: {name}")
        result.append(name)
    return result


# The fold entry points and their shared settings must be present even in a local build.
# Other tracked Python modules and package data are still discovered from the source roots.
REQUIRED_FOLD_SOURCES = (
    f"{WEB_BRIDGE}/cplfold_web.py",
    *(f"{CPLFOLD_PACKAGE}/{name}.py" for name in ("__init__", "bonus", "sequence", "models")),
    *(f"{CPLFOLD_PACKAGE}/_internal/{name}.py" for name in ("__init__", "single", "cofold", "parser", "cofold_parser")),
    *HYB2_SETTINGS,
)
# These upstream command-line programs use example data or the optional BAM toolchain.
LOCAL_PROGRAMS = {"example.py", "example_with_paris.py", "extract_paris_scores.py"}
WEB_NOTICES = {
    "LICENSE": "CPLfold-LICENSE.txt",
    "THIRD_PARTY_NOTICES.md": "CPLfold-THIRD_PARTY_NOTICES.md",
    "LICENSES/GPL-3.0-or-later.txt": "CPLfold-GPL-3.0-or-later.txt",
    "LICENSES/GPL-2.0-or-later.txt": "CPLfold-GPL-2.0-or-later.txt",
    "LICENSES/LinearFold-LICENSE.txt": "CPLfold-LinearFold-LICENSE.txt",
    "LICENSES/LinearCoFold-LICENSE.txt": "CPLfold-LinearCoFold-LICENSE.txt",
    "LICENSES/ViennaRNA-COPYING.txt": "CPLfold-ViennaRNA-COPYING.txt",
}
NOTICE_PATHS = {f"{CPLFOLD}/{name}": target for name, target in WEB_NOTICES.items()}


def web_source_root(name):
    """The import root of a selected source: the bridge, the cplfold project or pipeline/."""
    if name.startswith(WEB_BRIDGE + "/"):
        return WEB_BRIDGE
    return PIPELINE if name.startswith(PIPELINE + "/") else CPLFOLD


def web_package_path(name):
    source_root = web_source_root(name)
    if source_root == PIPELINE and name not in HYB2_SETTINGS:
        return None
    relative = PurePosixPath(name).relative_to(source_root)
    if (source_root == CPLFOLD and relative.as_posix() in LOCAL_PROGRAMS
            or relative.parts[0] in {"data", "docs", "examples", "LICENSES"}
            or any(part.startswith(".") for part in relative.parts)
            or relative.suffix.lower() in {".so", ".pyd", ".dylib", ".dll", ".o", ".a"}):
        return None
    if relative.suffix == ".py" or len(relative.parts) > 1:
        return relative
    return None


def web_package_files(names):
    paths = {}
    for name in names:
        if name in NOTICE_PATHS or excluded_file(PurePosixPath(name)):
            continue
        path = web_package_path(name)
        if path is not None:
            paths[name] = path
    packages = {path.parts[0] for path in paths.values() if path.suffix == ".py" and len(path.parts) > 1}
    return {name: path for name, path in paths.items() if path.suffix == ".py" or path.parts[0] in packages}


def web_source_layout(root):
    """Map the selected working-tree sources to their names inside the Python package."""
    if not (root / CPLFOLD_PACKAGE).is_dir():
        raise ValueError(f"package source directory is missing: {CPLFOLD_PACKAGE}")
    required = [*REQUIRED_FOLD_SOURCES, *NOTICE_PATHS]
    files = selected_files(root, CPLFOLD_SOURCE_DIRS, required, include=web_package_path)
    paths = web_package_files(files)
    modules = {CPLFOLD: {}, WEB_BRIDGE: {}, PIPELINE: {}}
    for name, path in paths.items():
        if path.suffix == ".py":
            parts = path.with_suffix("").parts
            module = parts[:-1] if len(parts) > 1 and parts[-1] == "__init__" else parts
            modules[web_source_root(name)][module] = name

    # The native import roots share one directory in the browser archive. Preserve
    # split namespace packages, but reject a module or regular package from one
    # root masking all or part of the same import path from another.
    owners = {}
    for name, path in paths.items():
        source = web_source_root(name)
        parts = path.with_suffix("").parts if path.suffix == ".py" else path.parent.parts
        for other in sorted(modules.keys() - {source}):
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
    return paths


def stage_web_sources(root, package, assets):
    destinations = {name: package / path for name, path in web_source_layout(root).items()}
    destinations.update({name: assets / target for name, target in NOTICE_PATHS.items()})
    for name, destination in destinations.items():
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(root / name, destination)
    return destinations


def package_build_commit(root, sources, requested_sha, *, directories, required_files, select, package, include=None):
    """Only stamp HEAD if these packaged names and bytes are present in that commit."""
    head = None
    if (root / ".git").exists():
        result = subprocess.run(["git", "rev-parse", "--verify", "--quiet", "HEAD"], cwd=root,
                                stdout=subprocess.PIPE, text=True)
        if result.returncode != 1:  # an index without a first commit can still supply local sources
            result.check_returncode()
            head = result.stdout.strip()
    if not head:
        if requested_sha is not None:
            raise ValueError("cannot verify GITHUB_SHA without a Git HEAD; unset it for a local build")
        return "local"
    if requested_sha is not None and requested_sha != head:
        raise ValueError(f"GITHUB_SHA {requested_sha!r} does not match checkout HEAD {head!r}")

    # Select from the committed tree too: a staged deletion is no longer in the
    # index. Compare the raw bytes we ship, not Git's filtered working-tree diff.
    blobs = git_source_blobs(root, directories, required_files, head, include=include)
    committed = select(blobs)
    missing = committed - sources.keys()
    extra = sources.keys() - committed
    if missing:
        mismatch = 'missing: ' + ', '.join(sorted(missing))
    elif extra:
        mismatch = 'unexpected: ' + ', '.join(sorted(extra))
    else:
        for name, path in sources.items():
            # Raw Git blob ID; avoid passing every runtime path in a single
            # command line (which can exceed CreateProcess's limit on Windows).
            data = path.read_bytes()
            digest = hashlib.new('sha1' if len(head) == 40 else 'sha256', f'blob {len(data)}\0'.encode('ascii'))
            digest.update(data)
            if digest.hexdigest() != blobs[name]:
                mismatch = f'changed: {name}'
                break
        else:
            return head
    if requested_sha is not None:
        raise ValueError(f"packaged {package} sources differ from HEAD ({mismatch}); cannot stamp release {head}")
    return "local"


def browser_build_commit(root, sources, requested_sha):
    return package_build_commit(root, sources, requested_sha, directories=CPLFOLD_SOURCE_DIRS, required_files=HYB2_SETTINGS,
                                select=lambda blobs: web_package_files(blobs).keys() | (NOTICE_PATHS.keys() & blobs.keys()),
                                package='browser', include=lambda name: (
                                    name in NOTICE_PATHS or web_package_path(name) is not None))


def verify_web_sources(root, assets, requested_sha):
    paths = web_source_layout(root)
    sources = {name: root / name for name in paths.keys() | NOTICE_PATHS.keys()}
    expected_commit = browser_build_commit(root, sources, requested_sha)
    manifest = json.loads((assets / "build-manifest.json").read_text(encoding="utf-8"))
    if manifest.get("buildCommit") != expected_commit:
        raise ValueError(f"buildCommit mismatch: expected {expected_commit!r}, got {manifest.get('buildCommit')!r}")
    digest = manifest.get("cplfoldArchiveSha256")
    archive_name = f"cplfold-python-{digest}.zip"
    if (not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest)
            or manifest.get("cplfoldArchiveFile") != archive_name):
        raise ValueError("invalid content-addressed CPLfold archive name in build manifest")

    expected = {f"cplfold/{path.as_posix()}": name for name, path in paths.items()}
    with zipfile.ZipFile(assets / archive_name) as archive:
        members = [entry.filename for entry in archive.infolist() if not entry.is_dir()]
        actual = set(members)
        if len(actual) != len(members):
            raise ValueError("duplicate file in browser source archive")
        missing = expected.keys() - actual
        extra = actual - expected.keys()
        if missing:
            raise ValueError("missing bundled sources: " + ", ".join(sorted(missing)))
        if extra:
            raise ValueError("unexpected bundled sources: " + ", ".join(sorted(extra)))
        for member, name in expected.items():
            if archive.read(member) != (root / name).read_bytes():
                raise ValueError(f"stale bundled source: {member} (source: {name})")
    for name, target in NOTICE_PATHS.items():
        if (assets / target).read_bytes() != (root / name).read_bytes():
            raise ValueError(f"stale bundled notice: {target} (source: {name})")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Stage or verify the browser's CPLfold sources.")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("package", nargs="?", type=Path, help="directory for staging the Python package")
    mode.add_argument("--verify", action="store_true", help="verify the built release against current sources")
    parser.add_argument("assets", type=Path, help="browser runtime asset directory")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    requested_sha = os.environ.get("GITHUB_SHA")
    try:
        if args.verify:
            verify_web_sources(root, args.assets, requested_sha)
        else:
            staged = stage_web_sources(root, args.package, args.assets)
            print(browser_build_commit(root, staged, requested_sha))
    except (OSError, ValueError, subprocess.SubprocessError, zipfile.BadZipFile) as error:
        parser.exit(1, f"browser sources: {error}\n")
