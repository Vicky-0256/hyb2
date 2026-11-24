# CPLfold: Chimeric and Pseudoknot-capable Linear-time RNA Secondary Structure Prediction

Two-phase pseudoknot prediction algorithm using LinearFold with experimental COMRADES/PARIS data support.

Energy scoring is provided by a Python port/refactoring built from the HotKnots
2.0 energy-calculation source code and parameter files. CPLfold does not use the
HotKnots heuristic search and does not require HotKnots binaries or libraries at
runtime; this runtime independence does not change the code and data provenance.

## Directory Structure

```
third_party/cplfold/                 # project root and import root
├── pyproject.toml                   # package "cplfold" (pip install third_party/cplfold)
├── cplfold/
│   ├── __init__.py                  # public API: fold(), Prediction, CofoldInputError
│   ├── bonus.py                     # reads-to-bonus matrices (strand-bounded Gaussians)
│   ├── sequence.py                  # sequence normalisation and strand layout
│   ├── models.py                    # energy models (MODELS, COFOLD_MODELS, DEFAULT_MODELS)
│   ├── cli.py                       # the cplfold command
│   └── _internal/                   # private implementation
│       ├── single.py                # two-phase algorithm (single strand; dispatches '&' to cofold)
│       ├── cofold.py                # two-strand CPLcofold
│       ├── parser.py                # LinearFold parser with bonus matrix support
│       ├── cofold_parser.py         # strand-aware Numba parser
│       ├── hotknots_energy.py       # Python port of HotKnots energy calculation
│       ├── energy_params/           # Parameter tables from HotKnots 2.0
│       └── ...
├── extract_paris_scores.py          # PARIS BAM file processing (IRIS method)
├── example.py, example_with_paris.py
├── data/                            # Example data files
│   ├── bpRNA_RFAM_5220.dbn          # Example RNA sequence/structure
│   ├── bpRNA_RFAM_5220_paris_scores.txt  # PARIS support scores
│   └── bpRNA_RFAM_5220_paris_scores.npy  # PARIS matrix (numpy)
└── tests/
```

Install with `pip install third_party/cplfold` (add `[vienna]` for ViennaRNA), or
put `third_party/cplfold` on `sys.path`. Only `cplfold`, `cplfold.bonus`,
`cplfold.sequence`, `cplfold.models` and `cplfold.cli` are public.

## Algorithm

### Two-Phase Approach

1. **Phase 1**: Generate suboptimal structures using LinearFold
2. **Phase 2**: For each Phase 1 structure, mask paired positions and fold again
3. **Merge**: Combine Phase 1 and Phase 2 pairs to form pseudoknots
4. **Score**: Calculate DP/CC/RE energy with the HotKnots-derived Python evaluator and rank structures

### Key Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `beam_size` | LinearFold beam size | 100 |
| `energy_delta` | Energy range for suboptimal structures | 5.0 |
| `max_phase1` | Maximum Phase 1 structures | 10 |
| `alpha` | Bonus matrix scaling factor | 0.0 |
| `beta` | Pseudoknot ranking bonus | 0.0 |

### Beta Parameter

For pseudoknot structures:
```
effective_energy = pseudoknot_energy + beta × phase1_energy
```

Since RNA energies are negative, higher beta favors pseudoknots in ranking.

## Usage

### Python API

```python
import cplfold

# Basic usage: a list of Prediction objects, best first
results = cplfold.fold(sequence, beam_size=100)
best = results[0]
print(best.structure, best.energy, best.kind)   # kind: 'phase1' or 'pseudoknot'

# With PARIS bonus matrix
import numpy as np
bonus = np.load("bonus.npy")
results = cplfold.fold(sequence, bonus=bonus, alpha=0.5, beta=0.3)

# Pseudoknot-free prediction
results = cplfold.fold(sequence, pseudoknots=False)
```

`fold(sequence, *, partner=None, pseudoknots=True, energy_model=None, bonus=None,
alpha=0.0, beta=0.0, beam_size=100, energy_delta=5.0, max_phase1=10, max_phase2=5)`
never prints and never writes files. Each `Prediction` has `structure`, `energy`,
`effective_energy`, `kind`, `score`, `phase1_structure`, `phase1_energy` and
`phase2_structure`; `details` is a read-only mapping of every field (nested
dicts are read-only mappings and lists are tuples), and `as_dict()` returns a
plain copy of the original candidate dict. `max_phase2` limits the Phase 2
candidates per Phase 1 structure only for two strands; one strand runs
exactly one Phase 2 fold per Phase 1 structure. `cplfold.models.DEFAULT_MODELS`
and `cplfold.sequence.ALIASES` are read-only mappings.
`cplfold.bonus.load_bonus_matrix_file` and `cplfold.sequence.read_sequence_file`
raise `OSError` (`FileNotFoundError` for a missing file) or
`cplfold.CofoldInputError` for malformed contents; only the commands exit.

Two-strand **CPLcofold** (Python with Numba acceleration) preserves alpha-guided
reads and beta pseudoknot ranking:

```python
results = cplfold.fold("GGGG", partner="CCCC", alpha=0.5, beta=0.3)
results = cplfold.fold("GGGG&CCCC")          # the same two strands
```

Supply a symmetric `bonus` matrix to apply reads guidance; alpha alone adds
no evidence. Nested cofold uses Turner energies; cofold pseudoknots use an
explicitly **experimental Turner/DP ranking heuristic**, reported in each
candidate's `details["energy_metadata"]`. Invalid two-strand input raises
`cplfold.CofoldInputError` (a `ValueError`).
See [the cofold API, reads coordinates, and model limitations](https://github.com/Vicky-0256/hyb2/blob/main/docs/cplfold-python.md#two-strand-cplcofold-python-and-numba).

### Command Line

```bash
# Basic usage (from third_party/cplfold, or anywhere after pip install)
python -m cplfold.cli -s GGCGCGGCACCGUCCGCGGAACAAACGG

# With parameters
python -m cplfold.cli -s SEQUENCE -b 200 -d 10.0 --beta 0.3

# With output file
python -m cplfold.cli -s SEQUENCE -o results.txt

# Two strands
python -m cplfold.cli -s 'GGGG&CCCC' --mode standard
```

A pip install provides the same command as `cplfold`, and `cplcofold`, which
requires two strands and accepts two-record FASTA. In Hyb2, `bin/cplfold`
and `bin/cplcofold` add an input-length limit and a private Numba cache.

### Energy-only API

CPLfold's energy calculator is based on the **HotKnots 2.0 source code and
parameter distribution**. `cplfold/_internal/hotknots_energy.py` ports and refactors the
energy-relevant `Stack`/`Loop`/`LoopList`/`Bands` structure decomposition,
SimFold secondary-structure terms, and DP/CC/RE pseudoknot scoring behavior into
Python. The eight files in `cplfold/_internal/energy_params` come from the parameter tables
distributed with HotKnots 2.0; only formatting such as line endings or trailing
whitespace was normalized where needed, not the numerical values.

Here, **standalone** means runtime-independent, not independently invented or
clean-room implemented. The current scoring path does not import the HotKnots
wrapper, start `computeEnergy`, load a compiled HotKnots library, or require the
former `Utils/HotKnots_v2.0` source tree. It does, however, remain a Python port
derived from HotKnots' energy-calculation code and data.

| CPLfold component | Relationship to HotKnots |
|---|---|
| Candidate generation | CPLfold's two-phase LinearFold workflow; it does not use the HotKnots hotspot/heuristic search |
| Structure energy | Python port/refactoring based on HotKnots 2.0 energy-calculation code |
| Energy parameters | Required tables taken from the HotKnots 2.0 distribution |
| Runtime | No HotKnots package, source tree, shared library, or executable is required |

The Python API evaluates the complete closed-region/Loop/Bands tree directly:

```python
from cplfold._internal.hotknots_energy import HotKnotsEnergy  # private module

result = HotKnotsEnergy().compute_energy(
    "GGCGCGGCACCGUCCGCGGAACAAACGG",
    "..(((((..[[[[)))))......]]]]",
    model="CC09",
)
print(result["energy"], result["breakdown"])
```

The evaluator supports pseudoknot-free and H-type structures as well as nested
secondary structures inside pseudoloops, nested pseudoknots, multi-band chains,
kissing pseudoknots, and multiloops spanning a band. DP09, CC09, and RE follow
the original Loop/Bands scoring behavior; CC uses its defined DP fallback for
topologies outside its two-stem entropy table.

Numerical parity applies to valid canonical RNA structures, including a minimum
of three unpaired nucleotides in every hairpin. For a shorter, non-physical
hairpin the original C program exposes its `16000 kcal/mol` internal `INF`
sentinel; the Python API raises `ValueError` instead of treating that sentinel
as a meaningful energy.

See [the port, provenance, and formula analysis](docs/hotknots_energy_analysis.md)
for the source-to-Python relationship, parameter layout, DP09/CC09/RE equations,
supported topology, and reference validation results.

### PARIS Data Processing

Extract PARIS support matrix from BAM files (based on IRIS method):

```bash
python extract_paris_scores.py paris_reads.bam 207 output_scores.txt
```

The script:
1. Reads chimeric reads from BAM file (reads with exactly one gap)
2. Uses Normal distribution to spread support around interval centers
3. Creates outer product for pairwise support
4. Applies log transformation

Reference: IRIS (https://github.com/qczhang/IRIS)

### Example with PARIS Data

```bash
python example_with_paris.py
```

This runs CPLfold on bpRNA_RFAM_5220 (snoRNA, 207nt) with PARIS data from 9592 chimeric reads.

## Dependencies and provenance

### LinearFold

Linear-time RNA secondary structure prediction algorithm.

- Source: https://github.com/LinearFold/LinearFold
- Reference: Huang, L., Zhang, H., Deng, D., Zhao, K., Liu, K., Hendrix, D. A., & Mathews, D. H. (2019). LinearFold: linear-time approximate RNA folding by 5'-to-3' dynamic programming and beam search. Bioinformatics, 35(14), i295-i304.

### Relationship to HotKnots 2.0 and attribution

The energy calculator in this branch is derived from the energy-calculation
portion of HotKnots 2.0 and uses a packaged subset of its parameter files. It is
not a new independently fitted DP/CC/RE model. The former complete
`Utils/HotKnots_v2.0` tree was removed only because the HotKnots search code,
wrapper, native libraries, and executable are not needed at runtime; that
removal does not change the implementation or parameter provenance described
above.

The HotKnots 2.0 README credits the original implementation to Jihong Ren and
Baharak Rastegari, with modifications by Cristina Pop and Mirela Andronescu.
Relevant upstream source headers contain GNU GPL version 2-or-later notices;
redistribution of this derived implementation should preserve the upstream
attribution and comply with the applicable upstream license terms.

- Source: https://www.cs.ubc.ca/labs/algorithms/Software/HotKnots/
- Reference: Ren, J., Rastegari, B., Condon, A., & Hoos, H. H. (2005). HotKnots: Heuristic prediction of RNA secondary structures including pseudoknots. RNA, 11(10), 1494-1504.

## Pseudoknot Energy Models

Available energy models:

- `DP09` - Dirks & Pierce 2009 (recommended)
- `DP03` - Dirks & Pierce 2003
- `CC06` - Cao & Chen 2006
- `CC09` - Cao & Chen 2009
- `RE` - Rivas & Eddy

`DP09`, `CC09`, and `RE` are covered by regression vectors from the original
`computeEnergy`; `DP03` and `CC06` remain available for compatibility.

## Requirements

- Python 3.8+
- NumPy
- Numba
- SciPy
- pysam (for PARIS BAM file processing)
- ViennaRNA (optional, for comparison)

## Author

Ke Wang
