"""Pure-Python versions of the Vienna-mode inside and outside kernels.

CPLfold_parser.parse_loop_vienna and CPLfold_parser._outside_kernel are written for
Numba.  Without Numba (the browser runs CPLfold in Pyodide) they execute as ordinary
Python on NumPy arrays, where every element read returns a NumPy scalar and every
arithmetic step on it goes through NumPy's scalar machinery, and every score helper
is a Python call.  These versions compute the same recursions in the same order on
Python lists and ints, with the score helpers inlined, and write the results back
into the caller's arrays, so the parser state afterwards is identical.

Bit-identity notes.  Energies are int32 table entries; Python int arithmetic gives
the same values (no intermediate exceeds int32).  Scores are float64 sums; Python
floats are IEEE doubles, so each addition rounds identically.  The bonus term is
``bonus_matrix[k] * alpha_scaled`` evaluated in NumPy's result type (float32 for
the float32 matrix and float32 alpha), so it is precomputed with the same NumPy
operation and only then converted to Python floats.  The rare logarithmic loop
extrapolation keeps ``np.log``.  Iteration order, strict/non-strict comparisons and
tie handling are those of the Numba kernels.

Only the Vienna (lv) mode is covered; CONTRAfold mode keeps the original code.
"""

import numpy as np

from Utils.shared import NOTON  # noqa: F401  (documented dependency: 5 nucleotide codes)

SINGLE_MAX_LEN = 30
VALUE_MIN = -1e18
PRUNE_MARGIN = float(np.float64(-1e18) + 1e10)

M_NONE = 0
M_H = 1
M_HAIRPIN = 2
M_SINGLE = 3
M_HELIX = 4
M_MULTI = 5
M_MULTI_eq_MULTI_plus_U = 6
M_P_eq_MULTI = 7
M_M2_eq_M_plus_P = 8
M_M_eq_M2 = 9
M_M_eq_M_plus_U = 10
M_M_eq_P = 11
M_C_eq_C_plus_U = 12
M_C_eq_C_plus_P = 13

S_H, S_P, S_M2, S_MULTI, S_M = 0, 1, 2, 3, 4

# Pair type indexed by (nuc + 1) * 6 + (nuc' + 1), nuc in -1..4 (-1 = outside the
# sequence, 4 = N): get_pair_type / NUM_TO_PAIR_JIT.
_PT = [0] * 36
for _a, _b, _t in ((0, 3, 5), (1, 2, 1), (2, 1, 2), (2, 3, 3), (3, 2, 4), (3, 0, 6)):
    _PT[(_a + 1) * 6 + (_b + 1)] = _t
# Vienna nucleotide code indexed by nuc + 1: get_vienna_nuc / _nuc_table.
_VN = [-1, 1, 2, 3, 4, 0]

# Single-loop energies depend only on (pt1, pt2, l1, l2, si1, sj1, sp1, sq1); the
# kernels memoise them per call under this mixed-radix integer key.
_K_PT1 = 7 * 32 * 32 * 1296
_K_PT2 = 32 * 32 * 1296
_K_L1 = 32 * 1296
_K_L2 = 1296
_PT_K = [t * _K_PT1 for t in _PT]


def _to_list(arr, fill):
    """Dense Python list equal to arr (cheap when arr is mostly `fill`)."""
    flat = arr.reshape(-1)
    out = [fill] * flat.size
    idx = np.flatnonzero(flat != fill)
    if idx.size:
        for k, v in zip(idx.tolist(), flat[idx].tolist()):
            out[k] = v
    return out


def _log_extra(size):
    return int(107.856 * np.log(size / 30.0))


def _single_energy(pt1, pt2, n1, n2, si1, sj1, sp1, sq1, T):
    """Energy (not score) of the single loop; v_score_single / _outside_v_single."""
    (stack, bulge, internal, int11, int21, int22, mmI, mm1nI, mm23I, tAU, ninio, max_ninio) = T
    if n1 > n2:
        nl = n1
        ns = n2
    else:
        nl = n2
        ns = n1
    if nl == 0:
        return stack[pt1][pt2]
    if ns == 0:
        energy = bulge[nl] if nl <= 30 else bulge[30] + _log_extra(nl)
        if nl == 1:
            energy += stack[pt1][pt2]
        else:
            if pt1 > 2:
                energy += tAU
            if pt2 > 2:
                energy += tAU
        return energy
    if ns == 1:
        if nl == 1:
            if si1 >= 0 and sj1 >= 0:
                return int11[pt1][pt2][si1][sj1]
            return 0
        if nl == 2:
            if n1 == 1:
                if si1 >= 0 and sq1 >= 0 and sj1 >= 0:
                    return int21[pt1][pt2][si1][sq1][sj1]
            else:
                if sq1 >= 0 and si1 >= 0 and sp1 >= 0:
                    return int21[pt2][pt1][sq1][si1][sp1]
            return 0
        u = nl + 1
        energy = internal[u] if u <= 30 else internal[30] + _log_extra(u)
        d = (nl - ns) * ninio
        energy += max_ninio if max_ninio < d else d
        if si1 >= 0 and sj1 >= 0:
            energy += mm1nI[pt1][si1][sj1]
        if sq1 >= 0 and sp1 >= 0:
            energy += mm1nI[pt2][sq1][sp1]
        return energy
    if ns == 2:
        if nl == 2:
            if si1 >= 0 and sp1 >= 0 and sq1 >= 0 and sj1 >= 0:
                return int22[pt1][pt2][si1][sp1][sq1][sj1]
            return 0
        if nl == 3:
            energy = internal[5] + ninio
            if si1 >= 0 and sj1 >= 0:
                energy += mm23I[pt1][si1][sj1]
            if sq1 >= 0 and sp1 >= 0:
                energy += mm23I[pt2][sq1][sp1]
            return energy
    u = nl + ns
    energy = internal[u] if u <= 30 else internal[30] + _log_extra(u)
    d = (nl - ns) * ninio
    energy += max_ninio if max_ninio < d else d
    if si1 >= 0 and sj1 >= 0:
        energy += mmI[pt1][si1][sj1]
    if sq1 >= 0 and sp1 >= 0:
        energy += mmI[pt2][sq1][sp1]
    return energy


def _prune(cands, beam):
    """beam_prune_deterministic + sort_by_position_desc on [(i, prune_score)]."""
    if beam > 0 and len(cands) > beam:
        threshold = sorted([c[1] for c in cands], reverse=True)[beam]
        keep = [c[0] for c in cands if c[1] >= threshold]
    else:
        keep = [c[0] for c in cands]
    keep.sort(reverse=True)
    return keep


def parse_loop_vienna_py(
    n, beam, nucs, allowed_pairs, next_pair, no_sharp_turn,
    scores, manners, splits, l1s, l2s,
    c_scores, c_manners, c_splits,
    beam_arr, beam_scores,
    m_beam_idx, m_beam_count,
    if_tetra, if_hexa, if_tri,
    v_hairpin, v_mismatchH, v_terminalAU, v_tetra, v_hexa, v_tri,
    v_stack, v_bulge, v_internal,
    v_int11, v_int21, v_int22,
    v_mismatchI, v_mismatch1nI, v_mismatch23I,
    v_ninio, v_max_ninio,
    v_mismatchM, v_mismatchExt, v_dangle5, v_dangle3,
    v_ml_intern, v_ml_closing,
    use_constraints, cons_arr, allow_unpaired_position, allow_unpaired_range,
    bonus_matrix, alpha_scaled
):
    n = int(n)
    beam = int(beam)
    n2 = n * n
    vmin = VALUE_MIN
    margin = PRUNE_MARGIN
    PT = _PT
    VN = _VN

    nu = nucs.tolist()
    NP = next_pair.tolist()
    AP = allowed_pairs.tolist()
    use_cons = bool(use_constraints)
    cons = cons_arr.tolist()
    aup = allow_unpaired_position.tolist()
    aur = allow_unpaired_range.tolist()
    tet = if_tetra.tolist()
    hexa_i = if_hexa.tolist()
    tri_i = if_tri.tolist()

    hairpin = v_hairpin.tolist()
    mmH = v_mismatchH.tolist()
    tAU = int(v_terminalAU)
    tetra_e = v_tetra.tolist()
    hexa_e = v_hexa.tolist()
    tri_e = v_tri.tolist()
    stack = v_stack.tolist()
    bulge = v_bulge.tolist()
    internal = v_internal.tolist()
    int11 = v_int11.tolist()
    int21 = v_int21.tolist()
    int22 = v_int22.tolist()
    mmI = v_mismatchI.tolist()
    mm1nI = v_mismatch1nI.tolist()
    mm23I = v_mismatch23I.tolist()
    ninio = int(v_ninio)
    max_ninio = int(v_max_ninio)
    mmM = v_mismatchM.tolist()
    mmExt = v_mismatchExt.tolist()
    d5 = v_dangle5.tolist()
    d3 = v_dangle3.tolist()
    ml_intern = int(v_ml_intern)
    ml_closing = int(v_ml_closing)
    T = (stack, bulge, internal, int11, int21, int22, mmI, mm1nI, mm23I, tAU, ninio, max_ninio)
    single_energy = _single_energy
    ecache = {}
    PT_K = _PT_K

    use_bonus = bool(alpha_scaled != 0.0)
    if use_bonus:
        # Same NumPy operation and result type as bonus_matrix[k] * alpha_scaled.
        bonus = (bonus_matrix * alpha_scaled).tolist()
    else:
        bonus = None

    sc = _to_list(scores, vmin)
    mn = _to_list(manners, 0)
    sp = _to_list(splits, 0)
    L1 = _to_list(l1s, 0)
    L2 = _to_list(l2s, 0)
    cs = c_scores.tolist()
    cm = c_manners.tolist()
    csp = c_splits.tolist()
    mbeam = [None] * n

    def hairpin_score(i, j, nuci, nuci1, nucj_1, nucj, tetra_idx):
        size = j - i - 1
        pt = PT[(nuci + 1) * 6 + nucj + 1]
        energy = hairpin[size] if size <= 30 else hairpin[30] + _log_extra(size)
        if size < 3:
            return -energy
        if size == 4 and tetra_idx >= 0:
            return -tetra_e[tetra_idx]
        if size == 6 and tetra_idx >= 0:
            return -hexa_e[tetra_idx]
        if size == 3:
            if tetra_idx >= 0:
                return -tri_e[tetra_idx]
            if pt > 2:
                energy += tAU
            return -energy
        si1 = VN[nuci1 + 1]
        sj1 = VN[nucj_1 + 1]
        if si1 >= 0 and sj1 >= 0:
            energy += mmH[pt][si1][sj1]
        return -energy

    def m1_score(nuci_1, nuci, nucj, nucj1):
        pt = PT[(nuci + 1) * 6 + nucj + 1]
        si1 = VN[nuci_1 + 1]
        sj1 = VN[nucj1 + 1]
        energy = 0
        if si1 >= 0 and sj1 >= 0:
            energy += mmM[pt][si1][sj1]
        if pt > 2:
            energy += tAU
        energy += ml_intern
        return -energy

    def external_score(nuci_1, nuci, nucj, nucj1):
        pt = PT[(nuci + 1) * 6 + nucj + 1]
        si1 = VN[nuci_1 + 1]
        sj1 = VN[nucj1 + 1]
        energy = 0
        if si1 >= 0 and sj1 >= 0:
            energy += mmExt[pt][si1][sj1]
        elif si1 >= 0:
            energy += d5[pt][si1]
        elif sj1 >= 0:
            energy += d3[pt][sj1]
        if pt > 2:
            energy += tAU
        return -energy

    def collect(base, j):
        # beam_prune_score: rank (i, j) by C[i - 1] + score; an unreachable prefix ranks last.
        out = []
        for i, s in enumerate(sc[base:base + j + 1]):
            if s > vmin:
                if i > 0:
                    pre = cs[i - 1]
                    out.append((i, vmin if pre <= margin else pre + s))
                else:
                    out.append((i, 0.0 + s))
        return out

    if n > 0:
        if not use_cons or aup[0] != 0:
            cs[0] = 0.0
            cm[0] = M_C_eq_C_plus_U
    if n > 1:
        if not use_cons or (aup[0] != 0 and aup[1] != 0):
            cs[1] = 0.0
            cm[1] = M_C_eq_C_plus_U

    bH = S_H * n2
    bP = S_P * n2
    bM2 = S_M2 * n2
    bMU = S_MULTI * n2
    bM = S_M * n2

    for j in range(n):
        nucj = nu[j]
        nucj1 = nu[j + 1] if j + 1 < n else -1
        nucj_1 = nu[j - 1] if j > 0 else -1
        jn = j * n

        # ---- H beam
        H = _prune(collect(bH + jn, j), beam)

        jnext = NP[nucj][j]
        if no_sharp_turn:
            while jnext != -1 and jnext - j < 4:
                jnext = NP[nucj][jnext]
        if use_cons:
            if aup[j] == 0:
                jnext = cons[j] if cons[j] > j else -1
            if jnext != -1:
                nucjnext = nu[jnext]
                if jnext > aur[j]:
                    jnext = -1
                elif not ((cons[j] == -1 or cons[j] == jnext) and
                          (cons[jnext] == -1 or cons[jnext] == j) and
                          AP[nucj][nucjnext]):
                    jnext = -1
        if jnext != -1 and jnext < n:
            nucjnext = nu[jnext]
            nucjnext_1 = nu[jnext - 1] if jnext > 0 else -1
            tetra_idx = -1
            length = jnext - j - 1
            if length == 4:
                tetra_idx = tet[j]
            elif length == 6:
                tetra_idx = hexa_i[j]
            elif length == 3:
                tetra_idx = tri_i[j]
            newscore = float(hairpin_score(j, jnext, nucj, nucj1, nucjnext_1, nucjnext, tetra_idx))
            if use_bonus:
                newscore += bonus[j * n + jnext]
            idx = bH + jnext * n + j
            if sc[idx] < newscore:
                sc[idx] = newscore
                mn[idx] = M_H

        for i in H:
            h_score = sc[bH + jn + i]
            p_idx = bP + jn + i
            if sc[p_idx] < h_score:
                sc[p_idx] = h_score
                mn[p_idx] = M_HAIRPIN
            nuci = nu[i]
            jnext_i = NP[nuci][j]
            if jnext_i != -1 and use_cons:
                nucjnext_check = nu[jnext_i]
                if jnext_i > aur[i]:
                    jnext_i = -1
                elif not ((cons[i] == -1 or cons[i] == jnext_i) and
                          (cons[jnext_i] == -1 or cons[jnext_i] == i) and
                          AP[nuci][nucjnext_check]):
                    jnext_i = -1
            if jnext_i != -1 and jnext_i < n:
                nuci1 = nu[i + 1] if i + 1 < n else -1
                nucjnext = nu[jnext_i]
                nucjnext_1 = nu[jnext_i - 1] if jnext_i > 0 else -1
                length = jnext_i - i - 1
                tetra_idx = -1
                if length == 4:
                    tetra_idx = tet[i]
                elif length == 6:
                    tetra_idx = hexa_i[i]
                elif length == 3:
                    tetra_idx = tri_i[i]
                newscore = float(hairpin_score(i, jnext_i, nuci, nuci1, nucjnext_1, nucjnext, tetra_idx))
                if use_bonus:
                    newscore += bonus[i * n + jnext_i]
                idx = bH + jnext_i * n + i
                if sc[idx] < newscore:
                    sc[idx] = newscore
                    mn[idx] = M_H

        if j == 0:
            continue

        # ---- Multi beam
        MU = _prune(collect(bMU + jn, j), beam)
        for i in MU:
            m_idx = bMU + jn + i
            multi_score = sc[m_idx]
            nuci = nu[i]
            nuci1 = nu[i + 1] if i + 1 < n else -1
            # v_score_multi: reversed pair, flanks (nuci1, nucj_1) -> mismatchM[pt, sj1, si1]
            pt = PT[(nucj + 1) * 6 + nuci + 1]
            si1 = VN[nuci1 + 1]
            sj1 = VN[nucj_1 + 1]
            energy = 0
            if si1 >= 0 and sj1 >= 0:
                energy += mmM[pt][sj1][si1]
            if pt > 2:
                energy += tAU
            energy += ml_intern + ml_closing
            newscore = multi_score + (-energy)
            if use_bonus:
                newscore += bonus[i * n + j]
            p_idx = bP + jn + i
            if sc[p_idx] < newscore:
                sc[p_idx] = newscore
                mn[p_idx] = M_P_eq_MULTI
            jnext_i = NP[nuci][j]
            if jnext_i != -1 and use_cons:
                nucjnext_check = nu[jnext_i]
                if jnext_i > aur[j]:
                    jnext_i = -1
                elif not ((cons[i] == -1 or cons[i] == jnext_i) and
                          (cons[jnext_i] == -1 or cons[jnext_i] == i) and
                          AP[nuci][nucjnext_check]):
                    jnext_i = -1
            if jnext_i != -1 and jnext_i < n:
                idx = bMU + jnext_i * n + i
                if sc[idx] < multi_score:
                    sc[idx] = multi_score
                    mn[idx] = M_MULTI_eq_MULTI_plus_U
                    L1[idx] = L1[m_idx]
                    L2[idx] = L2[m_idx] + jnext_i - j

        # ---- P beam
        PB = _prune(collect(bP + jn, j), beam)
        for i in PB:
            p_score = sc[bP + jn + i]
            nuci = nu[i]
            nuci_1 = nu[i - 1] if i > 0 else -1

            if i > 0 and j < n - 6:
                newscore = m1_score(nuci_1, nuci, nucj, nucj1) + p_score
                m_idx = bM + jn + i
                if sc[m_idx] < newscore:
                    sc[m_idx] = newscore
                    mn[m_idx] = M_M_eq_P

            if i > 1:
                M1 = m1_score(nuci_1, nuci, nucj, nucj1) + p_score
                k = i - 1
                kb = mbeam[k]
                if kb:
                    mrow = bM + k * n
                    m2row = bM2 + jn
                    for newi in kb:
                        m_score = sc[mrow + newi]
                        if m_score > vmin:
                            newscore = M1 + m_score
                            m2_idx = m2row + newi
                            if sc[m2_idx] < newscore:
                                sc[m2_idx] = newscore
                                mn[m2_idx] = M_M2_eq_M_plus_P
                                sp[m2_idx] = k

            ext = external_score(nuci_1, nuci, nucj, nucj1)
            k = i - 1
            if k >= 0:
                newscore = ext + cs[k] + p_score
                if cs[j] < newscore:
                    cs[j] = newscore
                    cm[j] = M_C_eq_C_plus_P
                    csp[j] = k
            else:
                newscore = external_score(-1, nuci, nucj, nucj1) + p_score
                if cs[j] < newscore:
                    cs[j] = newscore
                    cm[j] = M_C_eq_C_plus_P
                    csp[j] = -1

            # SINGLE / HELIX, inner pair (i, j) closed by (p, q)
            pt2 = PT[(nucj + 1) * 6 + nuci + 1]
            sp1 = VN[nuci_1 + 1]
            sq1 = VN[nucj1 + 1]
            kij = pt2 * _K_PT2 + (sp1 + 1) * 6 + sq1 + 1
            p_min = i - SINGLE_MAX_LEN
            if p_min < 0:
                p_min = 0
            for p in range(i - 1, p_min - 1, -1):
                if use_cons:
                    if p < i - 1 and aup[p + 1] == 0:
                        break
                    if aup[p] == 0:
                        q = cons[p]
                        if q < p:
                            break
                        if q <= j:
                            continue
                    else:
                        q = -1
                nucp = nu[p]
                nucp1 = nu[p + 1]
                if not use_cons or aup[p] != 0:
                    q = NP[nucp][j]
                l1 = i - p - 1
                max_l2 = SINGLE_MAX_LEN - l1
                ptrow = (nucp + 1) * 6 + 1
                si1 = VN[nucp1 + 1]
                kp = kij + l1 * _K_L1 + (si1 + 1) * 216
                NPp = NP[nucp]
                while q != -1 and q < n and q - j - 1 <= max_l2:
                    if use_cons:
                        if q > j + 1 and q > aur[j]:
                            break
                        nucq = nu[q]
                        if not ((cons[p] == -1 or cons[p] == q) and
                                (cons[q] == -1 or cons[q] == p) and
                                AP[nucp][nucq]):
                            break
                    nucq = nu[q]
                    pt1 = PT[ptrow + nucq]
                    l2 = q - j - 1
                    if l1 == 0 and l2 == 0:
                        newscore = -stack[pt1][pt2] + p_score
                    else:
                        sj1 = VN[nu[q - 1] + 1] if q > 0 else -1
                        key = kp + PT_K[ptrow + nucq] + l2 * 1296 + (sj1 + 1) * 36
                        e = ecache.get(key)
                        if e is None:
                            e = ecache[key] = single_energy(pt1, pt2, l1, l2, si1, sj1, sp1, sq1, T)
                        newscore = -e + p_score
                    if use_bonus:
                        newscore += bonus[p * n + q]
                    target = bP + q * n + p
                    if sc[target] < newscore:
                        sc[target] = newscore
                        if p == i - 1 and q == j + 1:
                            mn[target] = M_HELIX
                            L1[target] = 0
                            L2[target] = 0
                        else:
                            mn[target] = M_SINGLE
                            L1[target] = i - p
                            L2[target] = q - j
                    q = NPp[q]

        # ---- M2 beam
        M2B = _prune(collect(bM2 + jn, j), beam)
        for i in M2B:
            m2_score = sc[bM2 + jn + i]
            m_idx = bM + jn + i
            if sc[m_idx] < m2_score:
                sc[m_idx] = m2_score
                mn[m_idx] = M_M_eq_M2
            p_min = i - SINGLE_MAX_LEN
            if p_min < 0:
                p_min = 0
            for p in range(i - 1, p_min - 1, -1):
                nucp = nu[p]
                q = NP[nucp][j]
                if use_cons:
                    if p < i - 1 and aup[p + 1] == 0:
                        break
                    if aup[p] == 0:
                        q = cons[p]
                        if q < p:
                            break
                    if q > j + 1 and q > aur[j]:
                        continue
                    if q != -1 and q < n:
                        nucq = nu[q]
                        if not ((cons[p] == -1 or cons[p] == q) and
                                (cons[q] == -1 or cons[q] == p) and
                                AP[nucp][nucq]):
                            continue
                if q != -1 and q < n and (i - p - 1) <= SINGLE_MAX_LEN:
                    target = bMU + q * n + p
                    if sc[target] < m2_score:
                        sc[target] = m2_score
                        mn[target] = M_MULTI
                        L1[target] = i - p
                        L2[target] = q - j

        # ---- M beam
        MB = _prune(collect(bM + jn, j), beam)
        mbeam[j] = MB
        if j < n - 1:
            if not use_cons or aup[j + 1] != 0:
                nrow = bM + jn + n
                for i in MB:
                    m_score = sc[bM + jn + i]
                    target = nrow + i
                    if sc[target] < m_score:
                        sc[target] = m_score
                        mn[target] = M_M_eq_M_plus_U
        if j < n - 1:
            if not use_cons or aup[j + 1] != 0:
                if cs[j + 1] < cs[j]:
                    cs[j + 1] = cs[j]
                    cm[j + 1] = M_C_eq_C_plus_U

    # ---- write the state back into the caller's arrays
    scores[:] = sc
    manners[:] = mn
    splits[:] = sp
    l1s[:] = L1
    l2s[:] = L2
    c_scores[:] = cs
    c_manners[:] = cm
    c_splits[:] = csp
    for j in range(n):
        kb = mbeam[j]
        cnt = len(kb) if kb else 0
        m_beam_count[j] = cnt
        if cnt:
            m_beam_idx[j, :cnt] = kb
    return c_scores[n - 1]


def outside_kernel_vienna_py(n, nucs, next_pair, lv,
                             scores, manners, splits, l1s, l2s,
                             c_scores, c_manners, c_splits,
                             beta_scores, beta_manners, beta_splits, beta_l1s, beta_l2s,
                             beta_c_scores, beta_c_manners, beta_c_splits):
    """_outside_kernel for lv=True on Python lists."""
    n = int(n)
    n2 = n * n
    vmin = VALUE_MIN
    margin = PRUNE_MARGIN
    PT = _PT
    VN = _VN
    nu = nucs.tolist()
    NP = next_pair.tolist()

    import CPLfold_parser as cp
    stack = cp._v_stack.tolist()
    T = (stack, cp._v_bulge.tolist(), cp._v_internal.tolist(), cp._v_int11.tolist(),
         cp._v_int21.tolist(), cp._v_int22.tolist(), cp._v_mismatchI.tolist(),
         cp._v_mismatch1nI.tolist(), cp._v_mismatch23I.tolist(), int(cp._v_terminalAU),
         int(cp._v_ninio), int(cp._v_max_ninio))
    mmM = cp._v_mismatchM.tolist()
    mmExt = cp._v_mismatchExt.tolist()
    d5 = cp._v_dangle5.tolist()
    d3 = cp._v_dangle3.tolist()
    tAU = int(cp._v_terminalAU)
    ml_intern = int(cp._v_ml_intern)
    ml_closing = int(cp._v_ml_closing)
    single_energy = _single_energy
    ecache = {}
    PT_K = _PT_K

    sc = _to_list(scores, vmin)
    mn = _to_list(manners, 0)
    cs = c_scores.tolist()
    bs = _to_list(beta_scores, vmin)
    bm = _to_list(beta_manners, 0)
    bsp = _to_list(beta_splits, 0)
    bl1 = _to_list(beta_l1s, 0)
    bl2 = _to_list(beta_l2s, 0)
    bcs = beta_c_scores.tolist()
    bcm = beta_c_manners.tolist()
    bcsp = beta_c_splits.tolist()

    def v_m1(i, j):
        nuci_1 = nu[i - 1] if i > 0 else -1
        nucj1 = nu[j + 1] if j + 1 < n else -1
        t = PT[(nu[i] + 1) * 6 + nu[j] + 1]
        si1 = VN[nuci_1 + 1]
        sj1 = VN[nucj1 + 1]
        score = 0
        if si1 >= 0 and sj1 >= 0:
            score += mmM[t][si1][sj1]
        elif si1 >= 0:
            score += d5[t][si1]
        elif sj1 >= 0:
            score += d3[t][sj1]
        if t > 2:
            score += tAU
        score += ml_intern
        return -score

    def v_external(i, j):
        t = PT[(nu[i] + 1) * 6 + nu[j] + 1]
        score = 0
        if t > 2:
            score += tAU
        nuci_1 = nu[i - 1] if i > 0 else -1
        nucj1 = nu[j + 1] if j + 1 < n else -1
        si1 = VN[nuci_1 + 1]
        sj1 = VN[nucj1 + 1]
        if si1 >= 0 and sj1 >= 0:
            score += mmExt[t][si1][sj1]
        elif si1 >= 0:
            score += d5[t][si1]
        elif sj1 >= 0:
            score += d3[t][sj1]
        return -score

    def v_multi(i, j):
        nuci1 = nu[i + 1] if i + 1 < n else -1
        nucj_1 = nu[j - 1] if j > 0 else -1
        t = PT[(nu[j] + 1) * 6 + nu[i] + 1]
        si1 = VN[nucj_1 + 1]
        sj1 = VN[nuci1 + 1]
        score = 0
        if si1 >= 0 and sj1 >= 0:
            score += mmM[t][si1][sj1]
        elif si1 >= 0:
            score += d5[t][si1]
        elif sj1 >= 0:
            score += d3[t][sj1]
        if t > 2:
            score += tAU
        score += ml_intern
        score += ml_closing
        return -score

    bP = S_P * n2
    bM2 = S_M2 * n2
    bMU = S_MULTI * n2
    bM = S_M * n2

    # Inside-reachable i of each (state, j), ascending: the kernel's
    # `for i in range(j): if manners[...] == MANNER_NONE: continue`.
    def rows(base):
        out = []
        for j in range(n):
            r = mn[base + j * n: base + j * n + j]
            out.append([i for i, m in enumerate(r) if m != 0])
        return out
    pres_M, pres_M2, pres_P, pres_MU = rows(bM), rows(bM2), rows(bP), rows(bMU)

    SI1 = [VN[nu[p + 1] + 1] if p + 1 < n else -1 for p in range(n)]
    VN216 = [(x + 1) * 216 for x in SI1]
    SJ1 = [VN[nu[q - 1] + 1] if q > 0 else -1 for q in range(n)]

    def parents_of(p, j):
        nucp = nu[p]
        NPp = NP[nucp]
        ptrow = (nucp + 1) * 6 + 1
        lim = SINGLE_MAX_LEN + 1 + j
        out = []
        q = NPp[j]
        while q != -1 and q <= lim:
            idx_pq = bP + q * n + p
            if bm[idx_pq] != M_NONE:
                t = ptrow + nu[q]
                sj1 = SJ1[q]
                out.append((q, bs[idx_pq], PT[t], PT_K[t] + (q - j - 1) * 1296 + (sj1 + 1) * 36, sj1))
            q = NPp[q]
        return out

    bcs[n - 1] = 0.0
    bcm[n - 1] = M_NONE

    for j in range(n - 1, -1, -1):
        jn = j * n
        if j < n - 1:
            if bcm[j + 1] != M_NONE or bcs[j + 1] > margin:
                newscore = bcs[j + 1]
                if newscore > bcs[j]:
                    bcs[j] = newscore
                    bcm[j] = M_C_eq_C_plus_U
        if j == 0:
            break

        # M beam: M[i,j] <- M[i,j+1]
        if j < n - 1:
            for i in pres_M[j]:
                idx_m = bM + jn + i
                idx_next = idx_m + n
                if bm[idx_next] != M_NONE:
                    newscore = bs[idx_next]
                    if newscore > bs[idx_m]:
                        bs[idx_m] = newscore
                        bm[idx_m] = M_M_eq_M_plus_U

        # M2 beam
        for i in pres_M2[j]:
            idx_m2 = bM2 + jn + i
            p0 = i - SINGLE_MAX_LEN
            if p0 < 0:
                p0 = 0
            for p in range(p0, i):
                q = NP[nu[p]][j]
                if q != -1:
                    idx_multi = bMU + q * n + p
                    if bm[idx_multi] != M_NONE:
                        newscore = bs[idx_multi]
                        if newscore > bs[idx_m2]:
                            bs[idx_m2] = newscore
                            bm[idx_m2] = M_MULTI
                            bl1[idx_m2] = i - p
                            bl2[idx_m2] = q - j
            idx_m = bM + jn + i
            if bm[idx_m] != M_NONE:
                if bs[idx_m] > bs[idx_m2]:
                    bs[idx_m2] = bs[idx_m]
                    bm[idx_m2] = M_M_eq_M2

        # P beam
        nucj = nu[j]
        nucj1 = nu[j + 1] if j + 1 < n else -1
        sq1 = VN[nucj1 + 1]
        parents = {}
        for i in pres_P[j]:
            idx_p = bP + jn + i
            nuci = nu[i]
            nuci_1 = nu[i - 1] if i > 0 else -1

            if i > 0 and j < n - 1:
                pt2 = PT[(nucj + 1) * 6 + nuci + 1]
                sp1 = VN[nuci_1 + 1]
                kij = pt2 * _K_PT2 + (sp1 + 1) * 6 + sq1 + 1
                best = bs[idx_p]
                bman = bm[idx_p]
                b1 = bl1[idx_p]
                b2 = bl2[idx_p]
                p0 = i - SINGLE_MAX_LEN
                if p0 < 0:
                    p0 = 0
                for p in range(p0, i):
                    # Outer pairs (p, q), q > j, with an outside score; the list
                    # depends on (p, j) only, so it is built once per j.
                    outer = parents.get(p)
                    if outer is None:
                        outer = parents_of(p, j)
                        parents[p] = outer
                    if not outer:
                        continue
                    lim = SINGLE_MAX_LEN + 2 - (i - p) + j
                    l1 = i - p - 1
                    kp = kij + l1 * _K_L1 + VN216[p]
                    for q, beta_pq, pt1, kq, sj1 in outer:
                        if q > lim:
                            break
                        if l1 == 0 and q == j + 1:
                            newscore = beta_pq + (-stack[pt1][pt2])
                            if newscore > best:
                                best = newscore
                                bman = M_HELIX
                                b1 = 1
                                b2 = 1
                        else:
                            key = kp + kq
                            e = ecache.get(key)
                            if e is None:
                                e = ecache[key] = single_energy(pt1, pt2, l1, q - j - 1, SI1[p], sj1, sp1, sq1, T)
                            newscore = beta_pq + (-e)
                            if newscore > best:
                                best = newscore
                                bman = M_SINGLE
                                b1 = i - p
                                b2 = q - j
                if bman != M_NONE:
                    bs[idx_p] = best
                    bm[idx_p] = bman
                    bl1[idx_p] = b1
                    bl2[idx_p] = b2

            # P <- M (M_eq_P)
            idx_m = bM + jn + i
            if i > 0 and j < n - 1 and bm[idx_m] != M_NONE:
                newscore = bs[idx_m] + v_m1(i, j)
                if newscore > bs[idx_p]:
                    bs[idx_p] = newscore
                    bm[idx_p] = M_M_eq_P

            # P contributes to M2 = M + P
            k = i - 1
            if k > 0:
                m1 = v_m1(i, j)
                p_alpha = sc[idx_p]
                mrow = bM + k * n
                m2row = bM2 + jn
                for newi in pres_M[k]:
                    idx_m2_ji = m2row + newi
                    if bm[idx_m2_ji] != M_NONE:
                        idx_m_ki = mrow + newi
                        m_alpha = sc[idx_m_ki]
                        newscore = bs[idx_m2_ji] + p_alpha + m1
                        if newscore > bs[idx_m_ki]:
                            bs[idx_m_ki] = newscore
                            bm[idx_m_ki] = M_M2_eq_M_plus_P
                            bsp[idx_m_ki] = j
                        newscore2 = bs[idx_m2_ji] + m_alpha + m1
                        if newscore2 > bs[idx_p]:
                            bs[idx_p] = newscore2
                            bm[idx_p] = M_M2_eq_M_plus_P
                            bsp[idx_p] = newi

            # P <- C (C_eq_C_plus_P)
            k = i - 1
            if k >= 0:
                newscore = bcs[j] + v_external(k + 1, j)
                c_newscore = sc[idx_p] + newscore
                if c_newscore > bcs[k]:
                    bcs[k] = c_newscore
                    bcm[k] = M_C_eq_C_plus_P
                    bcsp[k] = j
                p_newscore = cs[k] + newscore
                if p_newscore > bs[idx_p]:
                    bs[idx_p] = p_newscore
                    bm[idx_p] = M_C_eq_C_plus_P
                    bsp[idx_p] = -1
            elif i == 0:
                newscore = bcs[j] + v_external(0, j)
                if newscore > bs[idx_p]:
                    bs[idx_p] = newscore
                    bm[idx_p] = M_C_eq_C_plus_P
                    bsp[idx_p] = -1

        # Multi beam
        for i in pres_MU[j]:
            idx_multi = bMU + jn + i
            jnext = NP[nu[i]][j]
            if jnext != -1:
                idx_next = bMU + jnext * n + i
                if bm[idx_next] != M_NONE:
                    newscore = bs[idx_next]
                    if newscore > bs[idx_multi]:
                        bs[idx_multi] = newscore
                        bm[idx_multi] = M_MULTI_eq_MULTI_plus_U
                        bsp[idx_multi] = jnext - j
            idx_p = bP + jn + i
            if bm[idx_p] != M_NONE:
                newscore = bs[idx_p] + v_multi(i, j)
                if newscore > bs[idx_multi]:
                    bs[idx_multi] = newscore
                    bm[idx_multi] = M_P_eq_MULTI

    beta_scores[:] = bs
    beta_manners[:] = bm
    beta_splits[:] = bsp
    beta_l1s[:] = bl1
    beta_l2s[:] = bl2
    beta_c_scores[:] = bcs
    beta_c_manners[:] = bcm
    beta_c_splits[:] = bcsp


def wrap_parse_loop_vienna(original):
    def parse_loop_vienna(*args):
        return parse_loop_vienna_py(*args)
    parse_loop_vienna.original = original
    return parse_loop_vienna


def wrap_outside_kernel(original):
    def _outside_kernel(n, nucs, next_pair, lv, *arrays):
        if not lv:
            return original(n, nucs, next_pair, lv, *arrays)
        return outside_kernel_vienna_py(n, nucs, next_pair, lv, *arrays)
    _outside_kernel.original = original
    return _outside_kernel
