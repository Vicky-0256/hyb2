"""Strand-aware Vienna candidate search for two RNA strands.

The nick is the boundary ``cutpoint`` in an ordinary A+B nucleotide array.
The face containing that nick is exterior, even when a pair encloses it.
Other faces use the existing Turner/Vienna kernels.  Paired chart entries
are beam-pruned by right endpoint; ``beam_size=0`` retains the full chart.
Auxiliary multiloop entries are retained to preserve exact face connectivity.
The chart uses O(n**2) memory.  With a paired-state beam b and maximum ordinary
internal-loop size L=30, inside work is O(n**2 * (L**2 + b)); beam_size=0 can
require O(n**3) work.  This parser does not claim LinearCoFold's linear scaling.

Numba computes the inside chart and single-best traceback. An implicit,
unambiguous derivation forest over that same chart supplies lazy k-best
traceback when multiple candidates are requested. Reported search energies
include every pair's reads bonus once and the configurable duplex initiation
cost once per complex.
"""

from __future__ import annotations

import heapq
import math
import time

import numpy as np
from numba import njit

from CPLfold_parser import (
    get_pair_type, v_score_hairpin, v_score_single, v_score_external,
    _v_hairpin, _v_mismatchH, _v_terminalAU, _v_tetra, _v_hexa, _v_tri,
    _v_stack, _v_bulge, _v_internal, _v_int11, _v_int21, _v_int22,
    _v_mismatchI, _v_mismatch1nI, _v_mismatch23I, _v_ninio, _v_max_ninio,
    _v_mismatchM, _v_mismatchExt, _v_dangle5, _v_dangle3,
    _v_ml_intern, _v_ml_closing,
)
from Utils.energy_parameter import Tetraloops, Hexaloops, Triloops


_INF = 1.0e30
_MAX_LOOP = 30
_P, _M1, _M2, _C, _L, _R = range(6)
_SPECIAL_LOOPS = tuple({motif.strip(): index for index, motif in enumerate(motifs)}
                       for motifs in (Tetraloops, Hexaloops, Triloops))


def _nonnegative_finite(value, name):
    if isinstance(value, (bool, np.bool_)):
        raise ValueError(f"{name} must be finite and nonnegative")
    try:
        value = float(value)
    except (TypeError, ValueError, OverflowError) as error:
        raise ValueError(f"{name} must be finite and nonnegative") from error
    if not math.isfinite(value) or value < 0:
        raise ValueError(f"{name} must be finite and nonnegative")
    return value


@njit(cache=True)
def _external(a, b, left, right):
    return -v_score_external(left, a, b, right, _v_mismatchExt,
                             _v_dangle5, _v_dangle3, _v_terminalAU)


@njit(cache=True)
def _multistem(a, b, left, right):
    """LinearCoFold E_MLstem, including one-sided dangling ends."""
    pt = get_pair_type(a, b)
    energy = int(_v_ml_intern)
    if left >= 0 and right >= 0:
        energy += _v_mismatchM[pt, left + 1, right + 1]
    elif left >= 0:
        energy += _v_dangle5[pt, left + 1]
    elif right >= 0:
        energy += _v_dangle3[pt, right + 1]
    if pt > 2:
        energy += _v_terminalAU
    return energy


@njit(cache=True)
def _single(nucs, i, j, p, q):
    return -v_score_single(
        i, j, p, q, nucs[i], nucs[i + 1], nucs[j - 1], nucs[j],
        nucs[p - 1], nucs[p], nucs[q], nucs[q + 1],
        _v_stack, _v_bulge, _v_internal, _v_int11, _v_int21, _v_int22,
        _v_mismatchI, _v_mismatch1nI, _v_mismatch23I,
        _v_terminalAU, _v_ninio, _v_max_ninio,
    )


@njit(cache=True)
def _face_energies(nucs, cut, tetra, hexa, tri):
    n = len(nucs)
    hairpin = np.full((n, n), _INF, dtype=np.float64)
    outer = np.zeros((n, n), dtype=np.float64)
    inner = np.zeros((n, n), dtype=np.float64)
    mlstem = np.zeros((n, n), dtype=np.float64)
    mlclose = np.zeros((n, n), dtype=np.float64)
    for i in range(n):
        for j in range(i + 1, n):
            if not get_pair_type(nucs[i], nucs[j]):
                continue
            before = nucs[i - 1] if i > 0 and i != cut else -1
            after = nucs[j + 1] if j + 1 < n and j + 1 != cut else -1
            inside_left = nucs[i + 1] if i + 1 != cut else -1
            inside_right = nucs[j - 1] if j != cut else -1
            outer[i, j] = _external(nucs[i], nucs[j], before, after)
            inner[i, j] = _external(nucs[j], nucs[i], inside_right, inside_left)
            mlstem[i, j] = _multistem(nucs[i], nucs[j], before, after)
            mlclose[i, j] = (_multistem(nucs[j], nucs[i], inside_right, inside_left)
                            + _v_ml_closing)
            if (i < cut) == (j < cut) and j - i >= 4:
                size = j - i - 1
                idx = tetra[i] if size == 4 else hexa[i] if size == 6 else tri[i] if size == 3 else -1
                hairpin[i, j] = -v_score_hairpin(
                    i, j, nucs[i], nucs[i + 1], nucs[j - 1], nucs[j], idx,
                    _v_hairpin, _v_mismatchH, _v_terminalAU, _v_tetra, _v_hexa, _v_tri,
                )
    return hairpin, outer, inner, mlstem, mlclose


@njit(cache=True)
def _inside(nucs, cut, beam, cons, forced_prefix, bonus, duplex,
            hairpin, outer, inner, mlstem, mlclose):
    """Inside energies in centikcal; no derivation references are overwritten."""
    n = len(nucs)
    pair = np.full((n, n), _INF, dtype=np.float64)
    m1 = np.full((n, n), _INF, dtype=np.float64)
    m2 = np.full((n, n), _INF, dtype=np.float64)
    exterior = np.full(n + 1, _INF, dtype=np.float64)
    left = np.full(n + 1, _INF, dtype=np.float64)
    right = np.full(n + 1, _INF, dtype=np.float64)
    exterior[0] = 0.0
    left[cut] = 0.0
    right[cut] = 0.0
    for j in range(n):
        # All children of P(i,j) have strictly smaller right endpoints.
        for i in range(j):
            cross = i < cut <= j
            if (not cross and j - i < 4) or not get_pair_type(nucs[i], nucs[j]):
                continue
            if cons[i] == -2 or cons[j] == -2:
                continue
            if (cons[i] >= 0 and cons[i] != j) or (cons[j] >= 0 and cons[j] != i):
                continue
            best = _INF
            if cross:
                # Arbitrary independent intra-strand branches on the nicked face.
                best = inner[i, j] + left[i + 1] + right[j]
            elif forced_prefix[j] == forced_prefix[i + 1]:
                best = hairpin[i, j]
            for p in range(i + 1, min(j, i + _MAX_LOOP + 2)):
                if forced_prefix[p] != forced_prefix[i + 1]:
                    break
                remaining = _MAX_LOOP - (p - i - 1)
                for q in range(max(p + 1, j - remaining - 1), j):
                    if cross and not (p < cut <= q):
                        continue
                    if forced_prefix[j] != forced_prefix[q + 1] or pair[p, q] >= _INF / 2:
                        continue
                    value = pair[p, q] + _single(nucs, i, j, p, q)
                    if value < best:
                        best = value
            if j > i + 2 and (not cross or i + 1 < cut <= j - 1):
                best = min(best, m2[i + 1, j - 1] + mlclose[i, j])
            if best < _INF / 2:
                pair[i, j] = best - bonus[i, j]

        # Keep paired entries with the best available exterior-prefix heuristic.
        # Forced pairs survive pruning even when a small beam is requested.
        if beam > 0:
            priorities = np.full(j, _INF, dtype=np.float64)
            count = 0
            for i in range(j):
                if pair[i, j] < _INF / 2 and cons[i] != j:
                    prefix = exterior[i] if exterior[i] < _INF / 2 else 0.0
                    priorities[i] = pair[i, j] + prefix + outer[i, j]
                    if i < cut <= j:
                        priorities[i] += duplex
                    count += 1
            if count > beam:
                # Stable sorting preserves increasing left index on ties in
                # both Numba and the identity-njit Python fallback.
                order = np.argsort(priorities, kind="mergesort")
                for rank in range(beam, j):
                    i = order[rank]
                    if cons[i] != j:
                        pair[i, j] = _INF

        # Multiloop segments start/end at exact interval boundaries.  An
        # unpaired extension or concatenation may not jump across the nick.
        for i in range(j + 1):
            if j > i and cons[j] < 0 and j != cut:
                m1[i, j] = m1[i, j - 1]
                m2[i, j] = m2[i, j - 1]
        for k in range(j):
            if pair[k, j] >= _INF / 2:
                continue
            branch = pair[k, j] + mlstem[k, j]
            for i in range(k + 1):
                if (forced_prefix[k] == forced_prefix[i]
                        and not (i < cut <= k)):
                    m1[i, j] = min(m1[i, j], branch)
                if k > i and k != cut:
                    value = m1[i, k - 1] + branch
                    m1[i, j] = min(m1[i, j], value)
                    m2[i, j] = min(m2[i, j], value)

        if cons[j] < 0:
            exterior[j + 1] = exterior[j]
            if j >= cut:
                right[j + 1] = right[j]
        for i in range(j):
            if pair[i, j] >= _INF / 2:
                continue
            branch = pair[i, j] + outer[i, j]
            if i < cut <= j:
                branch += duplex
            exterior[j + 1] = min(exterior[j + 1], exterior[i] + branch)
            if i >= cut:
                right[j + 1] = min(right[j + 1], right[i] + branch)

        # All first-strand pairs are now final, so compute its exterior suffixes.
        if j == cut - 1:
            for i in range(cut - 1, -1, -1):
                if cons[i] < 0:
                    left[i] = left[i + 1]
                for k in range(i + 1, cut):
                    left[i] = min(left[i], pair[i, k] + outer[i, k] + left[k + 1])
    return pair, m1, m2, exterior, left, right


@njit(cache=True, inline="always")
def _prefer_edge(best, local, a_kind, a_i, a_j, a_score,
                 b_kind=-1, b_i=0, b_j=0, b_score=0.0):
    """Rank one edge exactly as _KBest._record does, including float order."""
    if local >= _INF / 2:
        return best
    children = 0.0
    if a_kind >= 0:
        if a_score >= _INF / 2:
            return best
        children += a_score
    if b_kind >= 0:
        if b_score >= _INF / 2:
            return best
        children += b_score
    value = local + children
    # Equal scores retain the first emitted edge, matching the heap edge id.
    if value < best[0]:
        return value, a_kind, a_i, a_j, b_kind, b_i, b_j
    return best


@njit(cache=True)
def _best_edge(kind, i, j, nucs, cut, cons, forced, bonus, duplex,
               hairpin, outer, inner, mlstem, mlclose,
               pair, m1, m2, exterior, left, right):
    """Select rank zero without constructing Python edge lists or heaps.

    Emission order, impossible-edge filtering and arithmetic order mirror
    _Forest.edges and _KBest._record. In particular, float read bonuses mean
    the winning inside recurrence need not be the winning forest edge.
    """
    best = (np.inf, -1, 0, 0, -1, 0, 0)
    if kind == _P:
        local_bonus = -bonus[i, j]
        cross = i < cut <= j
        if cross:
            best = _prefer_edge(best, inner[i, j] + local_bonus,
                                _L, i + 1, 0, left[i + 1], _R, 0, j, right[j])
        elif forced[j] == forced[i + 1]:
            best = _prefer_edge(best, hairpin[i, j] + local_bonus, -1, 0, 0, 0.0)
        for p in range(i + 1, min(j, i + _MAX_LOOP + 2)):
            if forced[p] != forced[i + 1]:
                break
            remaining = _MAX_LOOP - (p - i - 1)
            for q in range(max(p + 1, j - remaining - 1), j):
                if cross and not (p < cut <= q):
                    continue
                if forced[j] != forced[q + 1] or pair[p, q] >= _INF / 2:
                    continue
                best = _prefer_edge(best, float(_single(nucs, i, j, p, q)) + local_bonus,
                                    _P, p, q, pair[p, q])
        if j > i + 2 and (not cross or i + 1 < cut <= j - 1):
            best = _prefer_edge(best, mlclose[i, j] + local_bonus,
                                _M2, i + 1, j - 1, m2[i + 1, j - 1])
    elif kind == _M1 or kind == _M2:
        if j > i and cons[j] < 0 and j != cut:
            score = m1[i, j - 1] if kind == _M1 else m2[i, j - 1]
            best = _prefer_edge(best, 0.0, kind, i, j - 1, score)
        for k in range(i, j):
            if pair[k, j] >= _INF / 2:
                continue
            local = mlstem[k, j]
            if kind == _M1 and forced[k] == forced[i] and not (i < cut <= k):
                best = _prefer_edge(best, local, _P, k, j, pair[k, j])
            if k > i and k != cut:
                best = _prefer_edge(best, local, _M1, i, k - 1, m1[i, k - 1],
                                    _P, k, j, pair[k, j])
    elif kind == _L:
        if i == cut:
            best = _prefer_edge(best, 0.0, -1, 0, 0, 0.0)
        else:
            if cons[i] < 0:
                best = _prefer_edge(best, 0.0, _L, i + 1, 0, left[i + 1])
            for k in range(i + 1, cut):
                if pair[i, k] < _INF / 2:
                    best = _prefer_edge(best, outer[i, k], _P, i, k, pair[i, k],
                                        _L, k + 1, 0, left[k + 1])
    else:
        start = cut if kind == _R else 0
        if j == start:
            best = _prefer_edge(best, 0.0, -1, 0, 0, 0.0)
        else:
            end = j - 1
            if cons[end] < 0:
                score = right[end] if kind == _R else exterior[end]
                best = _prefer_edge(best, 0.0, kind, 0, end, score)
            for k in range(start, end):
                if pair[k, end] >= _INF / 2:
                    continue
                local = outer[k, end]
                if k < cut <= end:
                    local += duplex
                score = right[k] if kind == _R else exterior[k]
                best = _prefer_edge(best, local, kind, 0, k, score,
                                    _P, k, end, pair[k, end])
    return best


@njit(cache=True)
def _traceback_best(nucs, cut, cons, forced, bonus, duplex,
                    hairpin, outer, inner, mlstem, mlclose,
                    pair, m1, m2, exterior, left, right):
    """Walk only chosen rank-zero edges with a bounded explicit stack.

    No fastmath: edge comparisons and the root score must preserve the
    existing lazy forest's floating-point order for fractional evidence.
    """
    n = len(nucs)
    partners = np.full(n, -1, dtype=np.int32)
    stack = np.empty((2 * n + 4, 3), dtype=np.int64)
    stack[0, 0], stack[0, 1], stack[0, 2] = _C, 0, n
    size = 1
    root_value = 0.0
    first = True
    while size:
        size -= 1
        kind, i, j = stack[size, 0], stack[size, 1], stack[size, 2]
        edge = _best_edge(kind, i, j, nucs, cut, cons, forced, bonus, duplex,
                          hairpin, outer, inner, mlstem, mlclose,
                          pair, m1, m2, exterior, left, right)
        if not np.isfinite(edge[0]):
            raise ValueError("No finite edge in single-best traceback")
        if first:
            # _KBest rank zero returns this root heap key, not a newly summed
            # energy from child traceback choices (which may round differently).
            root_value = edge[0]
            first = False
        if kind == _P:
            partners[i], partners[j] = j, i
        # Reverse push order reproduces the forest's left-to-right traversal.
        if edge[4] >= 0:
            stack[size, 0], stack[size, 1], stack[size, 2] = edge[4], edge[5], edge[6]
            size += 1
        if edge[1] >= 0:
            stack[size, 0], stack[size, 1], stack[size, 2] = edge[1], edge[2], edge[3]
            size += 1
    return root_value, partners


class _Forest:
    """Implicit retained hypergraph.  Each edge's local energy is explicit."""

    def __init__(self, parser, seq, cons):
        self.n = len(seq)
        self.cut = parser.cutpoint
        self.nucs = np.array(["ACGU".index(base) for base in seq], dtype=np.int32)
        self.cons = cons
        self.forced = np.concatenate(([0], np.cumsum(cons >= 0))).astype(np.int32)
        self.bonus = (np.zeros((self.n, self.n), dtype=np.float64)
                      if parser._bonus_matrix is None else parser._bonus_matrix.copy())
        if self.bonus.shape != (self.n, self.n):
            raise ValueError("Bonus matrix dimensions must match the nucleotide sequence")
        # Scale only active evidence. In particular, alpha * 100 can overflow
        # even when alpha is finite and every bonus is zero or very small.
        if parser._alpha == 0.0:
            self.bonus.fill(0.0)
        elif np.any(self.bonus):
            try:
                with np.errstate(over="raise", invalid="raise"):
                    self.bonus *= parser._alpha
                    self.bonus *= 100.0
            except FloatingPointError as error:
                raise ValueError("Scaled bonus exceeds the finite numeric range") from error
            # The chart uses a finite impossible-state sentinel. Keep the
            # largest possible sum of pair bonuses safely below that sentinel,
            # including signed matrices that penalize selected pairs.
            if np.max(np.abs(self.bonus)) >= _INF / (4.0 * max(1, self.n // 2)):
                raise ValueError("Scaled bonus total exceeds the parser numeric range")
        self.duplex = parser.duplex_init * 100.0
        if not math.isfinite(self.duplex) or self.duplex >= _INF / 4.0:
            raise ValueError("Duplex initiation exceeds the parser numeric range")
        special = [np.full(self.n, -1, dtype=np.int32) for _ in range(3)]
        # The vendored parameter strings include C-style separator spaces.
        # Strip those table delimiters when matching actual RNA motifs.
        for array, width, motifs in zip(special, (6, 8, 5), _SPECIAL_LOOPS):
            for i in range(self.n - width + 1):
                array[i] = motifs.get(seq[i:i + width], -1)
        self.hairpin, self.outer, self.inner, self.mlstem, self.mlclose = _face_energies(
            self.nucs, self.cut, *special)
        (self.pair, self.m1, self.m2, self.exterior, self.left, self.right) = _inside(
            self.nucs, self.cut, parser.beam, cons, self.forced, self.bonus, self.duplex,
            self.hairpin, self.outer, self.inner, self.mlstem, self.mlclose,
        )
        self._pair_starts = None

    @property
    def pair_starts(self):
        # Only the multi-candidate Python forest needs these Python arrays.
        if self._pair_starts is None:
            self._pair_starts = [np.flatnonzero(self.pair[:, j] < _INF / 2) for j in range(self.n)]
        return self._pair_starts

    def best(self):
        value, partners = _traceback_best(
            self.nucs, self.cut, self.cons, self.forced, self.bonus, self.duplex,
            self.hairpin, self.outer, self.inner, self.mlstem, self.mlclose,
            self.pair, self.m1, self.m2, self.exterior, self.left, self.right,
        )
        return value, [(i, j) for i, j in enumerate(partners.tolist()) if j > i]

    def score(self, node):
        kind, i, j = node
        if kind == _P:
            return self.pair[i, j]
        if kind == _M1:
            return self.m1[i, j]
        if kind == _M2:
            return self.m2[i, j]
        if kind == _C:
            return self.exterior[j]
        if kind == _L:
            return self.left[i]
        return self.right[j]

    def edges(self, node):
        """Yield (local centikcal energy, child nodes) for retained alternatives."""
        kind, i, j = node
        if kind == _P:
            bonus = -self.bonus[i, j]
            cross = i < self.cut <= j
            if cross:
                yield self.inner[i, j] + bonus, ((_L, i + 1, 0), (_R, 0, j))
            elif self.forced[j] == self.forced[i + 1]:
                yield self.hairpin[i, j] + bonus, ()
            for p in range(i + 1, min(j, i + _MAX_LOOP + 2)):
                if self.forced[p] != self.forced[i + 1]:
                    break
                remaining = _MAX_LOOP - (p - i - 1)
                for q in range(max(p + 1, j - remaining - 1), j):
                    if cross and not (p < self.cut <= q):
                        continue
                    if self.forced[j] != self.forced[q + 1] or self.pair[p, q] >= _INF / 2:
                        continue
                    yield float(_single(self.nucs, i, j, p, q)) + bonus, ((_P, p, q),)
            if j > i + 2 and (not cross or i + 1 < self.cut <= j - 1):
                yield self.mlclose[i, j] + bonus, ((_M2, i + 1, j - 1),)
        elif kind in (_M1, _M2):
            if j > i and self.cons[j] < 0 and j != self.cut:
                yield 0.0, ((kind, i, j - 1),)
            for k in self.pair_starts[j]:
                k = int(k)
                if k < i:
                    continue
                branch = (_P, k, j)
                local = self.mlstem[k, j]
                if (kind == _M1 and self.forced[k] == self.forced[i]
                        and not (i < self.cut <= k)):
                    yield local, (branch,)
                if k > i and k != self.cut:
                    yield local, ((_M1, i, k - 1), branch)
        elif kind == _L:
            if i == self.cut:
                yield 0.0, ()
            else:
                if self.cons[i] < 0:
                    yield 0.0, ((_L, i + 1, 0),)
                for k in range(i + 1, self.cut):
                    if self.pair[i, k] < _INF / 2:
                        yield self.outer[i, k], ((_P, i, k), (_L, k + 1, 0))
        else:
            start = self.cut if kind == _R else 0
            if j == start:
                yield 0.0, ()
            else:
                end = j - 1
                if self.cons[end] < 0:
                    yield 0.0, ((kind, 0, end),)
                for k in self.pair_starts[end]:
                    k = int(k)
                    if k < start:
                        continue
                    local = self.outer[k, end]
                    if k < self.cut <= end:
                        local += self.duplex
                    yield local, ((kind, 0, k), (_P, k, end))


class _KBest:
    """Lazy Cartesian-product enumeration with an explicit dependency stack."""

    def __init__(self, forest):
        self.forest = forest
        self.records = {}

    def _record(self, node):
        if node not in self.records:
            edges, heap = [], []
            for local, children in self.forest.edges(node):
                scores = [self.forest.score(child) for child in children]
                if local >= _INF / 2 or any(score >= _INF / 2 for score in scores):
                    continue
                edge_id = len(edges)
                edges.append((float(local), children))
                heap.append((float(local + sum(scores)), edge_id, (0,) * len(children)))
            heapq.heapify(heap)
            self.records[node] = {
                "edges": edges, "heap": heap, "results": [], "seen": set(),
                "pending": None, "done": not bool(heap),
            }
        return self.records[node]

    def _advance(self, node):
        record = self._record(node)
        pending = record["pending"]
        if pending is not None:
            edge_id, ranks = pending
            local, children = record["edges"][edge_id]
            for index, child in enumerate(children):
                next_ranks = list(ranks)
                next_ranks[index] += 1
                next_ranks = tuple(next_ranks)
                key = (edge_id, next_ranks)
                if key in record["seen"]:
                    continue
                record["seen"].add(key)
                yield child, next_ranks[index]
                child_record = self._record(child)
                if len(child_record["results"]) <= next_ranks[index]:
                    continue
                value = local + sum(self._record(c)["results"][r][0]
                                    for c, r in zip(children, next_ranks))
                heapq.heappush(record["heap"], (value, edge_id, next_ranks))
            record["pending"] = None
        if not record["heap"]:
            record["done"] = True
            return
        value, edge_id, ranks = heapq.heappop(record["heap"])
        _, children = record["edges"][edge_id]
        pairs = ((node[1], node[2]),) if node[0] == _P else ()
        for child, rank in zip(children, ranks):
            yield child, rank
            pairs += self._record(child)["results"][rank][1]
        record["results"].append((value, pairs))
        record["pending"] = (edge_id, ranks)

    def get(self, node, rank):
        # A long unpaired sequence must not consume Python recursion depth.
        stack = [[node, rank, None]]
        while stack:
            current, target, generator = stack[-1]
            record = self._record(current)
            if len(record["results"]) > target or record["done"]:
                stack.pop()
                continue
            if generator is None:
                generator = self._advance(current)
                stack[-1][2] = generator
            try:
                child, child_rank = next(generator)
                child_record = self._record(child)
                if len(child_record["results"]) <= child_rank and not child_record["done"]:
                    stack.append([child, child_rank, None])
            except StopIteration:
                stack[-1][2] = None
        results = self._record(node)["results"]
        return results[rank] if rank < len(results) else None


class BeamCKYCofoldParser:
    """Two-strand, pseudoknot-free candidate parser with reads bonuses.

    ``cutpoint`` is the zero-based start of strand B in A+B.  Energies returned
    by both methods are kcal/mol, including ``-alpha * sum(pair_bonus)``.
    ``duplex_init=0`` supports a second phase whose first phase is already bound.
    Constraints are honored whenever supplied, independent of the legacy
    ``use_constraints`` flag.  No ViennaRNA Python package is required.
    """

    def __init__(self, beam_size=100, lv=True, use_constraints=False,
                 is_verbose=False, cutpoint=None, duplex_init=4.1):
        if not lv:
            raise ValueError("Cofolding currently supports Vienna scoring only (lv=True)")
        if isinstance(beam_size, bool) or int(beam_size) != beam_size or beam_size < 0:
            raise ValueError("beam_size must be a nonnegative integer")
        if cutpoint is None or isinstance(cutpoint, bool) or int(cutpoint) != cutpoint:
            raise ValueError("Cofolding requires an integer cutpoint")
        self.beam = int(beam_size)
        self.lv = True
        self.use_constraints = use_constraints
        self.is_verbose = is_verbose
        self.cutpoint = int(cutpoint)
        self.duplex_init = _nonnegative_finite(duplex_init, "duplex_init")
        self._alpha = 0.0
        self._bonus_matrix = None

    def set_alpha(self, alpha):
        self._alpha = _nonnegative_finite(alpha, "alpha")

    def set_bonus_matrix(self, matrix, seq_length):
        if matrix is None:
            self._bonus_matrix = None
            return
        if np.iscomplexobj(matrix):
            raise ValueError("Bonus matrix must contain real finite values")
        try:
            matrix = np.asarray(matrix, dtype=np.float64)
        except (TypeError, ValueError, OverflowError) as error:
            raise ValueError("Bonus matrix must contain real finite values") from error
        if matrix.size != seq_length * seq_length or matrix.ndim not in (1, 2):
            raise ValueError("Bonus matrix dimensions must match the nucleotide sequence")
        if matrix.ndim == 2 and matrix.shape != (seq_length, seq_length):
            raise ValueError("Bonus matrix must be square")
        if not np.all(np.isfinite(matrix)):
            raise ValueError("Bonus matrix values must be finite")
        self._bonus_matrix = matrix.reshape(seq_length, seq_length).copy()

    def _prepare(self, seq, cons):
        seq = seq.upper().replace("T", "U")
        if not seq or any(base not in "ACGU" for base in seq):
            raise ValueError("Parser sequence must contain only A, C, G, U (without '&')")
        n = len(seq)
        if not 0 < self.cutpoint < n:
            raise ValueError("cutpoint must separate two nonempty RNA strands")
        constraints = np.full(n, -1, dtype=np.int32)
        if cons is not None:
            if len(cons) != n:
                raise ValueError("Constraint length must match the nucleotide sequence")
            if isinstance(cons, str):
                stack = []
                for index, symbol in enumerate(cons):
                    if symbol == "(":
                        stack.append(index)
                    elif symbol == ")":
                        if not stack:
                            raise ValueError("Unbalanced forced-pair constraints")
                        partner = stack.pop()
                        constraints[index] = partner
                        constraints[partner] = index
                    elif symbol == ".":
                        constraints[index] = -2
                    elif symbol != "?":
                        raise ValueError("Constraint symbols must be '?', '.', '(' or ')'")
                if stack:
                    raise ValueError("Unbalanced forced-pair constraints")
            else:
                values = np.asarray(cons)
                if values.ndim != 1 or not np.all(np.isin(values, np.arange(-2, n))):
                    raise ValueError("Constraint partners must be integer nucleotide indices")
                constraints = values.astype(np.int32)
            nesting = []
            for i, partner in enumerate(constraints):
                if partner < 0:
                    continue
                if partner == i or constraints[partner] != i:
                    raise ValueError("Forced pairs must have symmetric, distinct partner indices")
                if i < partner:
                    if seq[i] + seq[partner] not in ("AU", "UA", "GC", "CG", "GU", "UG"):
                        raise ValueError("Forced pair is not a canonical AU, CG or GU pair")
                    if (i < self.cutpoint) == (partner < self.cutpoint) and partner - i < 4:
                        raise ValueError("Forced intra-strand pairs require at least three unpaired loop bases")
                    nesting.append(partner)
                elif not nesting or nesting.pop() != i:
                    raise ValueError("Forced pairs must be pseudoknot-free")
        return _Forest(self, seq, constraints)

    def parse(self, seq, cons=None):
        start = time.perf_counter()
        forest = self._prepare(seq, cons)
        root = (_C, 0, forest.n)
        if forest.score(root) >= _INF / 2:
            return None, float("inf"), 0, time.perf_counter() - start
        value, pairs = forest.best()
        structure = self._structure(forest.n, pairs)
        return structure, value / 100.0, 0, time.perf_counter() - start

    @staticmethod
    def _structure(n, pairs):
        result = ["."] * n
        for i, j in pairs:
            result[i], result[j] = "(", ")"
        return "".join(result)

    def parse_subopt(self, seq, energy_delta=5.0, max_structures=100,
                     window_size=0, cons=None):
        """Return distinct candidates in ascending penalized-energy order.

        The window is relative to the retained chart's MFE.  Zero/negative
        ``window_size`` retains every distinct structure; a positive value
        additionally requires more than that many differing base pairs from
        each accepted candidate.  No arbitrary candidate-attempt cap is used.
        """
        energy_delta = _nonnegative_finite(energy_delta, "energy_delta")
        if isinstance(max_structures, bool) or int(max_structures) != max_structures or max_structures < 0:
            raise ValueError("max_structures must be a nonnegative integer")
        if max_structures == 0:
            return []
        forest = self._prepare(seq, cons)
        root = (_C, 0, forest.n)
        best = forest.score(root)
        if best >= _INF / 2:
            return []
        limit = best + float(energy_delta) * 100.0 + 1.0e-7
        if max_structures == 1:
            value, pairs = forest.best()
            if value > limit:
                return []
            return [(self._structure(forest.n, pairs), value / 100.0)]
        kbest = _KBest(forest)
        results, accepted_pairs, seen = [], [], set()
        rank = 0
        while len(results) < max_structures:
            candidate = kbest.get(root, rank)
            rank += 1
            if candidate is None or candidate[0] > limit:
                break
            value, pairs = candidate
            structure = self._structure(forest.n, pairs)
            if structure in seen:
                continue
            seen.add(structure)
            pair_set = frozenset(pairs)
            if window_size > 0 and any(len(pair_set ^ previous) <= window_size
                                       for previous in accepted_pairs):
                continue
            accepted_pairs.append(pair_set)
            results.append((structure, value / 100.0))
        return results
