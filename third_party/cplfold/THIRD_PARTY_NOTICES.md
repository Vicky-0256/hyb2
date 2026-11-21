# Third-party notices and distribution status

This directory is a vendored snapshot of CPLfold with two upstream code
lineages besides CPLfold's own. CPLfold's own code is licensed GPL-2.0-or-later
(`LICENSE`); the combination with the LinearFold-derived parser does **not** yet
have one resolved license (see "Compatibility review required"). Do not relabel
it, or the Hyb2 repository, as MIT or Apache licensed.

## CPLfold-authored code

`CPLfold.py` identifies Ke Wang as its author. Ke Wang, the author and rights
holder, licensed CPLfold's original orchestration code, tests, examples and
documentation under the GNU General Public License, version 2 or (at your
option) any later version, on 2026-10-17: see `LICENSE`, with the license text
in `LICENSES/GPL-2.0-or-later.txt`. The upstream repository
(https://github.com/Vicky-0256/CPLfold) should carry the same `LICENSE`.

## LinearFold-derived parser

`CPLfold_parser.py` and related scoring tables state that they are based on
LinearFold. LinearFold is credited to Liang Huang, He Zhang, Dezhong Deng, Kai
Zhao, Kaibo Liu, David Hendrix, and David Mathews. Its upstream terms permit
research, educational, and commercial use and modification, require author
credit, restrict redistribution fees to media costs, and request contact with
the corresponding author before inclusion in a commercial product. The exact
upstream text is preserved in `LICENSES/LinearFold-LICENSE.txt`.

Source: https://github.com/LinearFold/LinearFold

## LinearCoFold reference and cofold extension

`CPLcofold_parser.py` and `Utils/cofold_energy.py` implement strand-break
handling informed by LinearCoFold, reusing CPLfold's LinearFold-derived
Turner scoring kernels and tables. The implementation uses a new Numba inside
chart and lazy k-best enumeration. The reference is the LinearCoFold source
archive distributed at https://doi.org/10.5281/zenodo.8153422 (source directory
`VincentZhangHe-cofold-copartition-13c0beee5b11`). Its license is retained in
`LICENSES/LinearCoFold-LICENSE.txt`.

Credit: He Zhang, Sizhen Li, Ning Dai, Liang Zhang, David H. Mathews, and Liang
Huang. "LinearCoFold and LinearCoPartition: linear-time algorithms for
secondary structure prediction of interacting RNA molecules." Nucleic Acids
Research 51(18), e94 (2023), https://doi.org/10.1093/nar/gkad664.

The experimental cofold pseudoknot energy extension is a local CPLfold
heuristic; it is not an energy model supplied or validated by LinearCoFold.

## HotKnots-derived energy evaluator and parameters

`Utils/hotknots_energy.py` is a Python port/refactoring of the energy-calculation
portion of HotKnots 2.0. Files in `Utils/energy_params` are derived from the
HotKnots 2.0 parameter distribution. The HotKnots README credits Jihong Ren and
Baharak Rastegari, with modifications by Cristina Pop and Mirela Andronescu.
Relevant upstream source headers specify GNU GPL version 2 or later. A copy of
GPL version 2 is preserved in `LICENSES/GPL-2.0-or-later.txt`.

Source: https://www.cs.ubc.ca/labs/algorithms/Software/HotKnots/

The snapshot does not contain or execute the native HotKnots program. Runtime
independence does not remove the source and parameter provenance or its license
obligations.

## ViennaRNA-derived parameters and optional runtime use

`Utils/energy_parameter.py` holds Turner 2004 nearest-neighbour values taken
from the ViennaRNA Package; its header states "feature values from ViennaRNA".
`Utils/utility_v.py` implements the Vienna-model feature functions that use
them, and `Utils/turner_energy.py` evaluates nested structures with ViennaRNA's
double-dangle convention so that its result equals ViennaRNA's
`eval_structure` (Turner 2004, dangles=2). These files reach CPLfold through
the LinearFold-V tables of the LinearFold-derived parser.

`CPLfold_parser.py` also imports the ViennaRNA Python module (`RNA`) when it is
installed and uses `fold_compound(...).eval_structure` to re-evaluate a
structure; without the module it falls back to its own evaluation. The snapshot
does not contain ViennaRNA source code or binaries.

The ViennaRNA Package is credited to Ronny Lorenz, Ivo L. Hofacker and the
other ViennaRNA authors at the Institute for Theoretical Chemistry, University
of Vienna. Its `COPYING` notice permits research, educational and commercial
use and modification so long as the package and any derived works are not
redistributed for any fee other than media costs, and proper credit is given to
the authors and the Institute for Theoretical Chemistry of the University of
Vienna; it asks that the authors be contacted before inclusion in a commercial
product. The upstream text (ViennaRNA 2.7.2) is preserved in
`LICENSES/ViennaRNA-COPYING.txt`.

Credit: Lorenz R, Bernhart SH, Höner zu Siederdissen C, Tafer H, Flamm C,
Stadler PF, Hofacker IL. "ViennaRNA Package 2.0." Algorithms for Molecular
Biology 6, 26 (2011), https://doi.org/10.1186/1748-7188-6-26.

Source: https://github.com/ViennaRNA/ViennaRNA

## Compatibility review required

The GPL prohibits adding further downstream restrictions, while the LinearFold
terms include conditions beyond the GPL text. Because CPLfold imports the
LinearFold-derived parser and HotKnots-derived evaluator into one Python
program, publishing the combined snapshot may require an explicit compatible
license or exception from the LinearFold copyright holders. CPLfold's own code
and the HotKnots-derived files are both GPL-2.0-or-later, so this question
concerns the LinearFold terms and the ViennaRNA terms that the LinearFold-V
tables carry with them, which have the same no-fee and credit conditions.
Recording these texts is necessary attribution, but it is not itself a
compatibility determination or legal advice.
