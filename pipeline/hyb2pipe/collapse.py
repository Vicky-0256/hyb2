"""``collapse_blast_2.sh`` / ``collapse_hyb_2.sh``.

Both scripts only choose one representative line per key.  The read-count
arithmetic in the original is discarded by its own ``cut -f3-20``, so read IDs
are never changed; what matters is *which* line wins, and that depends on the
original's quirks (first line of each group dropped, one-true-awk hash order,
``sed -n 2p`` prepended, the last line re-emitted in END).
"""
from __future__ import annotations

from typing import Callable, List, Sequence, Tuple

from . import awk

KeyFn = Callable[[List[str]], Tuple[str, ...]]


def _tally_order(sorted_lines: Sequence[str], same: Callable[[List[str], Tuple[str, ...]], bool], key: KeyFn) -> List[str]:
    """Lines in the order the first awk program of the collapse scripts prints them."""
    emitted: List[str] = []
    group = awk.BwkArray()
    n = 0
    last: Tuple[str, ...] = ()
    for index, line in enumerate(sorted_lines):
        f = awk.fields(line)
        if index == 0:
            n = 0
            group["0"] = line
        if index > 0 and same(f, last):
            n += 1
            group[str(n)] = line
        else:
            for k in group.keys():
                emitted.append(group[k])
            n = 0
            group = awk.BwkArray()
        last = key(f)
    if sorted_lines:
        f = awk.fields(sorted_lines[-1])
        if awk.truthy(awk.field(f, 2)):
            emitted.append(sorted_lines[-1])
    return emitted


def _collapse(lines: Sequence[str], sort_field: int, key: KeyFn) -> List[str]:
    def same(f: List[str], last: Tuple[str, ...]) -> bool:
        current = key(f)
        return all(awk.compare(x, y) == 0 for x, y in zip(current, last))

    ordered = _tally_order(awk.sort_lines_by_field(lines, sort_field), same, key)
    # cut -f3-20 after prefixing two columns: the original line, at most 18 fields
    ordered = ["\t".join(line.split("\t")[:18]) for line in ordered]
    candidates = ordered[1:2] + ordered + list(lines)
    seen = set()
    result: List[str] = []
    for line in candidates:
        k = tuple(key(awk.fields(line)))
        if k not in seen:
            result.append(line)
        seen.add(k)
    return result


def collapse_blast(lines: Sequence[str]) -> List[str]:
    """Port of ``collapse_blast_2.sh`` (unique by gene and mapped sequence)."""
    return _collapse(lines, 13, lambda f: (awk.field(f, 2), awk.field(f, 13)))


def collapse_hyb(lines: Sequence[str]) -> List[str]:
    """Port of ``collapse_hyb_2.sh`` (unique by read sequence)."""
    return _collapse(lines, 2, lambda f: (awk.field(f, 2),))
