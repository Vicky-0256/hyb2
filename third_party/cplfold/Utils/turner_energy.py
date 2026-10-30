"""Turner 2004 free energy of a nested one-strand structure, in kcal/mol at 37 C.

The loop terms are LinearFold-V's Turner tables with ViennaRNA's double-dangle
convention, the tables CPLfold's beam search uses; they are evaluated by the
same nick-aware decomposition as :mod:`Utils.cofold_energy`, with no nick.
The result equals ViennaRNA's ``eval_structure`` (Turner 2004, dangles=2).

CPLfold ranks pseudoknot-free candidates with this function, so that one run
scores all of its candidates with one validated model.
"""

import numpy as np

from Utils.cofold_energy import _nested_terms, _special_hairpins
from Utils.hotknots_energy import HotKnotsEnergy, NUC_TO_INT, _crosses, parse_dot_bracket


def turner_energy(sequence: str, structure: str) -> float:
    sequence = sequence.upper().replace("T", "U")
    parsed, pair_table = parse_dot_bracket(structure)
    HotKnotsEnergy._validate(sequence, structure, parsed)
    if any(_crosses(a.pair, b.pair) for index, a in enumerate(parsed) for b in parsed[index + 1:]):
        raise ValueError("Turner 2004 scoring needs a pseudoknot-free structure")
    nucs = np.array([NUC_TO_INT[base] for base in sequence], dtype=np.int32)
    pairs = np.array(pair_table, dtype=np.int32)
    terms = _nested_terms(nucs, pairs, len(sequence), _special_hairpins(sequence, pair_table))
    return float(terms.sum()) / 100.0
