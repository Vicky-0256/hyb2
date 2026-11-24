# Third-party licences

Hyb2's own code is GPL-3.0-or-later ([`LICENSE`](LICENSE)). This file lists the third-party code that Hyb2 contains or distributes, and where each one is distributed. Each component keeps its own licence.

## In this repository

| Component | Location | Licence |
|---|---|---|
| VARNA (source and the two jars) | `VARNA/` | GPL-3.0 ([`VARNA/LICENSE`](VARNA/LICENSE)) |
| IRIS, adapted from [JY-Zhou/IRIS](https://github.com/JY-Zhou/IRIS) | `third_party/iris/`, `web/iris-core.js` | GPL-3.0 ([`third_party/iris/LICENSE`](third_party/iris/LICENSE)) |
| Parsers derived from LinearFold and LinearCoFold | `third_party/cplfold/cplfold/_internal/parser.py`, `cofold_parser.py` | LinearFold and LinearCoFold terms: free use and modification, no redistribution fees beyond media costs, author credit required ([LinearFold](third_party/cplfold/LICENSES/LinearFold-LICENSE.txt), [LinearCoFold](third_party/cplfold/LICENSES/LinearCoFold-LICENSE.txt)) |
| Energy evaluator and parameters derived from HotKnots 2.0 | `third_party/cplfold/cplfold/_internal/hotknots_energy.py`, `energy_params/` | GPL-2.0-or-later ([text](third_party/cplfold/LICENSES/GPL-2.0-or-later.txt)) |
| Turner 2004 parameters from ViennaRNA | `third_party/cplfold/cplfold/_internal/energy_parameter.py`, `utility_v.py`, `turner_energy.py` | ViennaRNA terms ([`COPYING`](third_party/cplfold/LICENSES/ViennaRNA-COPYING.txt)) |
| D3 v3 | `web/vendor/d3/` | BSD-3-Clause ([`LICENSE.txt`](web/vendor/d3/LICENSE.txt)) |
| fornac | `web/vendor/fornac/` | Apache-2.0 ([`LICENSE.txt`](web/vendor/fornac/LICENSE.txt)) |

[`third_party/cplfold/THIRD_PARTY_NOTICES.md`](third_party/cplfold/THIRD_PARTY_NOTICES.md) records the full terms and original authors of the CPLfold parts.

## Built by CI and published with the website

These files are not in Git. `scripts/build-vienna-wasm.sh` and `scripts/build-cplfold-web.sh` build them into `web/vendor/`, and the Pages workflow publishes them with the website. Each build places the licence texts next to the files.

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

Versions are pinned in [`desktop/runtime/requirements-native.txt`](desktop/runtime/requirements-native.txt).

## Installed separately

The conda environments in `bin/` (`hyb2*.yml`, `renv.lock`) install Bowtie2, ViennaRNA, R, DESeq2 and the other command-line dependencies from their own channels under their own licences. Hyb2 does not redistribute them.

## Open question

CPLfold combines GPL code (its own code and the HotKnots-derived evaluator) with the LinearFold-derived parser, whose terms add conditions that the GPL does not allow. This combination is still being confirmed with the LinearFold authors and does not yet have one confirmed licence.
