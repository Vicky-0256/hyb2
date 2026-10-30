"""Strand-aware energies for two-strand CPLfold candidates.

Nested structures use LinearCoFold's Turner tables and double-dangle
convention, in kcal/mol at 37 C. The nick opens a loop face; it is never
represented by dummy nucleotides. Association costs 4.10 kcal/mol once.

Pseudoknots use an experimental RNA ribbon-graph ranking heuristic, not a
fitted cofold free-energy model. A face traversal crosses each base pair and
then follows the covalent 3' backbone; a strand end terminates an open face.
Ordinary closed faces use Turner loop terms. Other faces receive oriented
helix-end terms, using only unpaired neighbors for crossing-pair caps.

Crossing-pair graph components define PK components. Within each component,
intact two-pair faces connect a band: true stacks are scaled by DP stP, and
other interiors by intP. The empirical penalties are Ps per PK component,
Pb per band, and Pup per unpaired nucleotide on an otherwise unscored
backbone span bounded by consecutive endpoints of the same PK component.
Exterior tails have no such two-sided boundary and receive no Pup penalty.
Linear-tree Psm/Psp/Pps and opaque-branch conventions are not used. This is
an explicitly changed, unvalidated use of DP03/DP09 coefficients, rather
than a port of either original single-chain thermodynamic model.

Connected and disconnected candidates share these Turner local terms.
Single-strand HotKnots code and its existing call sites remain unchanged.
"""

from __future__ import annotations

import numpy as np
from numba import njit

import CPLfold_parser as _turner
from Utils.hotknots_energy import (
    HotKnotsEnergy, NUC_TO_INT, _crosses, parse_dot_bracket,
)
from Utils.strand_layout import normalise_sequence, strip_structure_break

DUPLEX_INIT_KCAL = 4.10
_SPECIAL_LOOPS = {
    size: {motif.strip(): index for index, motif in enumerate(table)}
    for size, table in ((3, _turner.Triloops), (4, _turner.Tetraloops), (6, _turner.Hexaloops))
}


@njit(cache=True)
def _neighbor(nucs, center, position, cutpoint):
    if position < 0 or position >= len(nucs):
        return -1
    if (center < cutpoint) != (position < cutpoint):
        return -1
    return nucs[position]


@njit(cache=True)
def _stem_energy(nucs, left, right, before, after, cutpoint, multi=False):
    """Face stem term in centikcal; reverse the closing pair on its inside."""
    first = _neighbor(nucs, left, before, cutpoint)
    second = _neighbor(nucs, right, after, cutpoint)
    if not multi:
        return -_turner.v_score_external(
            first, nucs[left], nucs[right], second,
            _turner._v_mismatchExt, _turner._v_dangle5,
            _turner._v_dangle3, _turner._v_terminalAU,
        )
    # LinearCoFold E_MLstem includes the single-neighbor fallback. The old
    # single-strand CPLfold parser omits it; its behavior is not changed here.
    pair_type = _turner.get_pair_type(nucs[left], nucs[right])
    first = _turner.get_vienna_nuc(first)
    second = _turner.get_vienna_nuc(second)
    energy = int(_turner._v_ml_intern)
    if first >= 0 and second >= 0:
        energy += _turner._v_mismatchM[pair_type, first, second]
    elif first >= 0:
        energy += _turner._v_dangle5[pair_type, first]
    elif second >= 0:
        energy += _turner._v_dangle3[pair_type, second]
    if pair_type > 2:
        energy += _turner._v_terminalAU
    return energy


@njit(cache=True)
def _single_energy(nucs, i, j, p, q):
    return -_turner.v_score_single(
        i, j, p, q, nucs[i], nucs[i + 1], nucs[j - 1], nucs[j],
        nucs[p - 1], nucs[p], nucs[q], nucs[q + 1],
        _turner._v_stack, _turner._v_bulge, _turner._v_internal,
        _turner._v_int11, _turner._v_int21, _turner._v_int22,
        _turner._v_mismatchI, _turner._v_mismatch1nI,
        _turner._v_mismatch23I, _turner._v_terminalAU,
        _turner._v_ninio, _turner._v_max_ninio,
    )


@njit(cache=True)
def _face_energy(nucs, i, j, lefts, rights, opaque, cutpoint, special):
    """Return (centikcal, kind): hairpin=0, interior=1, multi=2, nicked=3.

    Opaque children occupy their interval and score caps in their PK subtree.
    """
    nicked = i < cutpoint <= j
    for k in range(len(lefts)):
        if lefts[k] < cutpoint <= rights[k]:
            nicked = False
            break
    if nicked:
        energy = _stem_energy(nucs, j, i, j - 1, i + 1, cutpoint)
        for k in range(len(lefts)):
            if not opaque[k]:
                p, q = lefts[k], rights[k]
                energy += _stem_energy(nucs, p, q, p - 1, q + 1, cutpoint)
        return energy, 3
    if len(lefts) == 0:
        if j - i - 1 < 3:
            raise ValueError("An intramolecular hairpin must contain at least three unpaired bases")
        energy = -_turner.v_score_hairpin(
            i, j, nucs[i], nucs[i + 1], nucs[j - 1], nucs[j], special[i],
            _turner._v_hairpin, _turner._v_mismatchH,
            _turner._v_terminalAU, _turner._v_tetra,
            _turner._v_hexa, _turner._v_tri,
        )
        return energy, 0
    if len(lefts) == 1 and not opaque[0]:
        return _single_energy(nucs, i, j, lefts[0], rights[0]), 1
    energy = _stem_energy(nucs, j, i, j - 1, i + 1, cutpoint, True)
    energy += _turner._v_ml_closing
    for k in range(len(lefts)):
        if opaque[k]:
            energy += _turner._v_ml_intern
        else:
            p, q = lefts[k], rights[k]
            energy += _stem_energy(nucs, p, q, p - 1, q + 1, cutpoint, True)
    return energy, 2


@njit(cache=True)
def _nested_terms(nucs, pairs, cutpoint, special):
    """Linear-time nested loop decomposition and scoring, without init."""
    n = len(pairs)
    parent = np.full(n, -1, dtype=np.int32)
    first_child = np.full(n, -1, dtype=np.int32)
    next_child = np.full(n, -1, dtype=np.int32)
    last_child = np.full(n, -1, dtype=np.int32)
    stack = np.empty(n, dtype=np.int32)
    depth = 0
    terms = np.zeros(5, dtype=np.int64)
    for i in range(n):
        j = pairs[i]
        if j > i:
            if depth:
                p = stack[depth - 1]
                parent[i] = p
                if first_child[p] < 0:
                    first_child[p] = i
                else:
                    next_child[last_child[p]] = i
                last_child[p] = i
            stack[depth] = i
            depth += 1
        elif j >= 0:
            depth -= 1
    for i in range(n):
        j = pairs[i]
        if j <= i:
            continue
        if parent[i] < 0:
            terms[4] += _stem_energy(nucs, i, j, i - 1, j + 1, cutpoint)
        count = 0
        child = first_child[i]
        while child >= 0:
            count += 1
            child = next_child[child]
        lefts = np.empty(count, dtype=np.int32)
        rights = np.empty(count, dtype=np.int32)
        opaque = np.zeros(count, dtype=np.int32)
        child = first_child[i]
        for k in range(count):
            lefts[k], rights[k] = child, pairs[child]
            child = next_child[child]
        value, kind = _face_energy(nucs, i, j, lefts, rights, opaque, cutpoint, special)
        terms[kind] += value
    return terms


def _special_hairpins(sequence, pair_table):
    result = np.full(len(sequence), -1, dtype=np.int32)
    if not _turner.SPECIAL_HP:
        return result
    for i, j in enumerate(pair_table):
        size = j - i - 1
        if size in _SPECIAL_LOOPS:
            result[i] = _SPECIAL_LOOPS[size].get(sequence[i:j + 1], -1)
    return result


def _backbone_faces(pair_table, cutpoint):
    """Partition nucleotide sides into actual backbone paths and cycles.

    The pair involution followed by the covalent successor is injective
    wherever a successor exists. Its open paths start at the two 5' ends;
    remaining components are cycles. A pair's face orientation is
    (endpoint, partner). No successor crosses a nick or joins strand ends.
    """
    n = len(pair_table)
    successor = [-1] * n
    for position, partner in enumerate(pair_table):
        base = partner if partner >= 0 else position
        if base + 1 not in (cutpoint, n):
            successor[position] = base + 1
    visited = [False] * n
    faces = []
    for start in [0, cutpoint] + list(range(n)):
        if visited[start]:
            continue
        positions = []
        current = start
        while current >= 0 and not visited[current]:
            visited[current] = True
            positions.append(current)
            current = successor[current]
        faces.append((current == start, positions))
    return faces


class CofoldEnergy:
    """Score the complete pair graph independently of search alpha/beta.

    ``cutpoint`` is the zero-based first base of strand B. Extended bracket
    alphabets have the same meaning as in :class:`HotKnotsEnergy`.
    """

    def __init__(self, parameter_dir=None):
        self._parameter_dir = parameter_dir
        self._hotknots = None

    @property
    def hotknots(self):
        if self._hotknots is None:
            self._hotknots = HotKnotsEnergy(self._parameter_dir)
        return self._hotknots

    @staticmethod
    def _add(breakdown, name, value):
        breakdown[name] = breakdown.get(name, 0.0) + float(value)

    def compute_energy(self, sequence, structure, cutpoint, model="DP09"):
        sequence, cutpoint = normalise_sequence(sequence, cutpoint)
        if cutpoint is None:
            raise ValueError("CofoldEnergy requires a cutpoint separating two nonempty strands")
        structure = strip_structure_break(structure, cutpoint, len(sequence))
        if model not in HotKnotsEnergy.MODELS:
            raise ValueError(f"Unknown energy model {model!r}")
        parsed, pair_table = parse_dot_bracket(structure)
        HotKnotsEnergy._validate(sequence, structure, parsed)
        # Validate before dispatch for connected and disconnected graphs.
        # A cross-strand pair with no intervening bases is legal.
        for pair in parsed:
            if (pair.right - pair.left < 4
                    and not pair.left < cutpoint <= pair.right
                    and all(pair_table[k] < 0 for k in range(pair.left + 1, pair.right))):
                raise ValueError("An intramolecular hairpin must contain at least three unpaired bases")
        crossing = any(_crosses(a.pair, b.pair) for index, a in enumerate(parsed)
                       for b in parsed[index + 1:])
        interstrand = sum(p.left < cutpoint <= p.right for p in parsed)
        metadata = {
            "energy_kind": "thermodynamic", "energy_model": "Turner-LinearCoFold",
            "requested_model": model, "cutpoint": cutpoint,
            "interstrand_pairs": interstrand,
            "duplex_initiations": int(interstrand > 0),
            "pseudoknot_components": 0, "temperature_celsius": 37.0, "dangles": 2,
        }
        nucs = np.array([NUC_TO_INT[base] for base in sequence], dtype=np.int32)
        pairs = np.array(pair_table, dtype=np.int32)
        special = _special_hairpins(sequence, pair_table)
        if not crossing:
            terms = _nested_terms(nucs, pairs, cutpoint, special)
            names = ("hairpin", "stack_and_internal", "multiloop", "nicked_exterior", "exterior")
            breakdown = {name: float(value) / 100.0 for name, value in zip(names, terms)}
        else:
            if model not in ("DP03", "DP09"):
                raise ValueError("Cofold pseudoknots support DP03 or DP09 experimental "
                                 f"hybrid scoring, not {model}; no model substitution is performed")
            breakdown, pk_metadata = self._pseudoknot(nucs, pairs, cutpoint, special, model)
            metadata.update(pk_metadata)
        breakdown["duplex_init"] = DUPLEX_INIT_KCAL if interstrand else 0.0
        energy = float(sum(breakdown.values()))
        return {"energy": energy, "model": model, "breakdown": breakdown, "metadata": metadata}

    def _pseudoknot(self, nucs, pair_table, cutpoint, special, model):
        """Score actual backbone faces; never reorder or artificially join strands."""
        _, dp = self.hotknots._parameters_for(model)
        pairs = [(i, int(j)) for i, j in enumerate(pair_table) if i < j]
        pair_ids = [-1] * len(pair_table)
        for identifier, (i, j) in enumerate(pairs):
            pair_ids[i] = pair_ids[j] = identifier
        component_parent = list(range(len(pairs)))
        band_parent = list(range(len(pairs)))
        crossing_pairs = set()

        def find(parent, item):
            while parent[item] != item:
                parent[item] = parent[parent[item]]
                item = parent[item]
            return item

        def join(parent, first, second):
            first, second = find(parent, first), find(parent, second)
            if first != second:
                parent[second] = first

        for first, pair in enumerate(pairs):
            for second in range(first + 1, len(pairs)):
                if _crosses(pair, pairs[second]):
                    crossing_pairs.update((first, second))
                    join(component_parent, first, second)
        components = {}
        for identifier in crossing_pairs:
            root = find(component_parent, identifier)
            components.setdefault(root, []).append(identifier)
        core_component = {identifier: find(component_parent, identifier)
                          for identifier in crossing_pairs}
        breakdown = {}
        eligible_unpaired = np.zeros(len(pair_table), dtype=np.int32)
        ordinary_faces = 0
        irregular_faces = 0
        empty = np.empty(0, dtype=np.int32)

        def end_cap(endpoint):
            partner = int(pair_table[endpoint])
            before, after = endpoint - 1, partner + 1
            # Preserve reference dangles=2 for ordinary stems. Only crossing
            # pair caps use the experimental unpaired-neighbor convention.
            if pair_ids[endpoint] in crossing_pairs:
                if before < 0 or pair_table[before] >= 0:
                    before = -1
                if after >= len(pair_table) or pair_table[after] >= 0:
                    after = -1
            return _stem_energy(nucs, endpoint, partner, before, after, cutpoint) / 100.0

        for closed, positions in _backbone_faces(pair_table, cutpoint):
            endpoints = [position for position in positions if pair_table[position] >= 0]
            identifiers = [pair_ids[position] for position in endpoints]
            unique = len(identifiers) == len(set(identifiers))
            noncrossing = unique and all(
                not _crosses(pairs[first], pairs[second])
                for index, first in enumerate(identifiers)
                for second in identifiers[index + 1:]
            )
            regular = closed and noncrossing and bool(endpoints)
            if regular and len(endpoints) == 1:
                i, j = pairs[identifiers[0]]
                value, _ = _face_energy(nucs, i, j, empty, empty, empty, cutpoint, special)
                self._add(breakdown, "hairpin", value / 100.0)
            elif regular and len(endpoints) == 2:
                outer, inner = sorted(pairs[identifier] for identifier in identifiers)
                i, j = outer
                p, q = inner
                if not i < p < q < j:
                    raise ValueError("Invalid two-pair backbone face")
                value = _single_energy(nucs, i, j, p, q) / 100.0
                first, second = identifiers
                if (first in core_component and second in core_component
                        and core_component[first] == core_component[second]):
                    join(band_parent, first, second)
                    # Both intact arms are required for a true stack. A
                    # one-sided bulge stays an interior after a strand swap.
                    scale = dp["stP"] if p == i + 1 and q == j - 1 else dp["intP"]
                    self._add(breakdown, "scaled_band_stack_and_internal", scale * value)
                else:
                    self._add(breakdown, "stack_and_internal", value)
            elif regular:
                value = float(_turner._v_ml_closing)
                for endpoint in endpoints:
                    partner = int(pair_table[endpoint])
                    value += _stem_energy(nucs, endpoint, partner, endpoint - 1,
                                          partner + 1, cutpoint, True)
                self._add(breakdown, "multiloop", value / 100.0)
            else:
                if closed:
                    irregular_faces += 1
                for position in positions:
                    if pair_table[position] < 0:
                        eligible_unpaired[position] = 1
                term = "pseudoknot_face_caps" if closed else "exterior"
                for endpoint in endpoints:
                    self._add(breakdown, term, end_cap(endpoint))
            ordinary_faces += int(regular)

        # Charge empirical entropy only on a covalent span bounded by this
        # PK. Exterior tails and ordinary Turner loop bases are excluded.
        prefix = np.concatenate(([0], np.cumsum(eligible_unpaired)))
        unpaired = 0
        for start, end in ((0, cutpoint), (cutpoint, len(pair_table))):
            previous = -1
            previous_component = None
            for position in range(start, end):
                component = core_component.get(pair_ids[position])
                if component is None:
                    continue
                if previous >= 0 and component == previous_component:
                    unpaired += int(prefix[position] - prefix[previous + 1])
                previous, previous_component = position, component
        band_counts = [len({find(band_parent, identifier) for identifier in members})
                       for members in components.values()]
        self._add(breakdown, "pseudoknot_initiation", dp["Ps"] * len(components))
        self._add(breakdown, "pseudoknot_bands", dp["Pb"] * sum(band_counts))
        self._add(breakdown, "pseudoknot_unpaired", dp["Pup"] * unpaired)
        interstrand_components = sum(
            any(pairs[identifier][0] < cutpoint <= pairs[identifier][1] for identifier in members)
            for members in components.values()
        )
        metadata = {
            "energy_kind": "heuristic",
            "energy_model": f"experimental-Turner-{model}-ribbon-cofold-PK",
            "energy_model_version": "strand-graph-v2",
            "pseudoknot_components": len(components),
            "bands_per_pseudoknot": sorted(band_counts),
            "nick_spanning_pseudoknot_components": interstrand_components,
            "ordinary_closed_faces": ordinary_faces,
            "irregular_closed_faces": irregular_faces,
            "pseudoloop_unpaired_bases": unpaired,
            "local_energy_baseline": "Turner-LinearCoFold",
            "limitations": ["Experimental ribbon-graph use of DP coefficients, not original DP model parity",
                            "Turner local terms mixed with DP Ps/Pb/Pup and band scaling without a cofold fit",
                            "No fitted intermolecular pseudoknot coaxial-stacking or loop entropy model"],
        }
        return breakdown, metadata


def compute_energy(sequence, structure, cutpoint, model="DP09"):
    """Return kcal/mol; use the class API for the energy-model metadata."""
    return float(CofoldEnergy().compute_energy(sequence, structure, cutpoint, model)["energy"])
