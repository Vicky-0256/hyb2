# Third-party licences

Hyb2's own code is GPL-3.0-or-later ([`LICENSE`](LICENSE)). This file lists the third-party code that Hyb2 contains or distributes, and where each one is distributed. Each component keeps its own licence.

## In this repository

| Component | Location | Licence |
|---|---|---|
| VARNA 3.9, unmodified (source and the two jars; [upstream](third_party/varna/UPSTREAM.md)) | `third_party/varna/` | GPL-3.0 ([`LICENSE`](third_party/varna/LICENSE)) |
| IRIS, adapted from [JY-Zhou/IRIS](https://github.com/JY-Zhou/IRIS) | `third_party/iris/`, `web/iris-core.js` | GPL-3.0 ([`third_party/iris/LICENSE`](third_party/iris/LICENSE)) |
| Parsers derived from LinearFold and LinearCoFold | `third_party/cplfold/cplfold/_internal/parser.py`, `cofold_parser.py` | LinearFold and LinearCoFold terms: free use and modification, no redistribution fees beyond media costs, author credit required ([LinearFold](third_party/cplfold/LICENSES/LinearFold-LICENSE.txt), [LinearCoFold](third_party/cplfold/LICENSES/LinearCoFold-LICENSE.txt)) |
| Energy evaluator and parameters derived from HotKnots 2.0 | `third_party/cplfold/cplfold/_internal/hotknots_energy.py`, `energy_params/` | GPL-2.0-or-later ([text](third_party/cplfold/LICENSES/GPL-2.0-or-later.txt)) |
| Turner 2004 parameters from ViennaRNA | `third_party/cplfold/cplfold/_internal/energy_parameter.py`, `utility_v.py`, `turner_energy.py` | ViennaRNA terms ([`COPYING`](third_party/cplfold/LICENSES/ViennaRNA-COPYING.txt)) |
| D3 v3 | `web/vendor/d3/` | BSD-3-Clause ([`LICENSE.txt`](web/vendor/d3/LICENSE.txt)) |
| fornac | `web/vendor/fornac/` | Apache-2.0 ([`LICENSE.txt`](web/vendor/fornac/LICENSE.txt)) |

[`third_party/cplfold/THIRD_PARTY_NOTICES.md`](third_party/cplfold/THIRD_PARTY_NOTICES.md) records the full terms and original authors of the CPLfold parts.

## Built by CI and published with the website

These files are not in Git. `scripts/build/build-vienna-wasm.sh` and `scripts/build/build-cplfold-web.sh` build them into `web/vendor/`, and the Pages workflow publishes them with the website. Each build places the licence texts next to the files.

| Component | Version | Location on the website | Licence |
|---|---|---|---|
| ViennaRNA, compiled to WebAssembly | 2.7.2 | `web/vendor/viennarna/` | ViennaRNA terms (`COPYING-ViennaRNA.txt`) |
| Pyodide (CPython for WebAssembly) | 0.29.4 (Python 3.13) | `web/vendor/pyodide-cplfold/` | MPL-2.0 (`Pyodide-LICENSE.txt`); CPython under the PSF License |
| NumPy wheel for Pyodide | 2.2.5 | `web/vendor/pyodide-cplfold/` | BSD-3-Clause |
| ViennaRNA wheel for Pyodide | 2.7.2 | `web/vendor/pyodide-cplfold/` | ViennaRNA terms (`CPLfold-ViennaRNA-COPYING.txt`) |
| CPLfold archive | this repository | `web/vendor/pyodide-cplfold/` | as above (`CPLfold-*` licence files) |

## Bundled with the desktop app

The desktop release stages these from verified archives (`desktop/scripts/`); they are not in Git.

| Component | Version | Licence |
|---|---|---|
| CPython | 3.11 | PSF License |
| NumPy | 1.24.4 | BSD-3-Clause |
| Numba | 0.58.1 | BSD-2-Clause |
| llvmlite | 0.41.1 | BSD-2-Clause (bundled LLVM: Apache-2.0 with LLVM exception) |
| ViennaRNA | 2.7.2 | ViennaRNA terms |
| Bowtie2 (`bowtie2-align-s`, `bowtie2-build-s`) | 2.5.5 | GPL-3.0 |
| cutadapt, dnaio, xopen | 5.2, 1.2.5, 2.1.0 | MIT |
| backports.zstd | 1.8.1 | PSF-2.0 (bundled Zstandard: BSD-3-Clause) |
| isal, zlib-ng (Windows and Linux x86_64 only) | 1.9.1, 1.1.0 | PSF-2.0 (bundled ISA-L: BSD-3-Clause; zlib-ng: zlib licence) |

Versions are pinned in [`desktop/runtime/requirements-native.txt`](desktop/runtime/requirements-native.txt).

## Installed separately

The command-line tools install their dependencies from PyPI under their own licences: `pip install ./pipeline` installs ViennaRNA, NumPy and cutadapt for `hyb2pipe` and `hyb2analysis` ([`pipeline/pyproject.toml`](pipeline/pyproject.toml)); `bin/cplfold` and `bin/cplcofold` need NumPy, Numba and llvmlite ([`requirements/cplfold-core.txt`](requirements/cplfold-core.txt)), and `bin/iris` NumPy and ViennaRNA. `hyb2pipe` maps reads with a Bowtie2 that you install, and the desktop app's COMRADES(mfold) runs an mfold 3.6 that you install (bioconda `mfold`). Hyb2 does not redistribute them.

## Open question

CPLfold combines GPL code (its own code and the HotKnots-derived evaluator) with the LinearFold-derived parser, whose terms add conditions that the GPL does not allow. This combination is still being confirmed with the LinearFold authors and does not yet have one confirmed licence.
