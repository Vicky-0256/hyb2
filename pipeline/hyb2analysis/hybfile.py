"""Parsing of .hyb records, mirroring ``web/parser.worker.js``.

A hyb2 ``sam2hyb`` line has 18 TAB-separated columns::

    1 id (<read>_<count>)   2 sequence   3 dG or "."
    4 gene 1   5-6 read start/end   7-8 gene start/end   9 e-value
    10 gene 2  11-12 read start/end  13-14 gene start/end  15 e-value
    16 "" (txt2hyb.awk leaves it empty)  17 overlap score  18 chimera type

17-column files carry the overlap score and type in columns 16 and 17; older
15/16-column files have neither, and the type is inferred from coordinates.

``support`` is the web parser's cluster support: 1 per row, except older
clustered 15/16-column files, where a ``count_total=N`` annotation or a
positive integer in column 16 gives the count.
"""
from __future__ import annotations

import functools
import re
from typing import BinaryIO, Iterator, NamedTuple, Optional

_DIGITS = re.compile(rb"^\d+$")


# A real .hyb line is well under 100 kB; a file with one endless line (a
# corrupt or hostile copy) must fail instead of being loaded whole.
MAX_LINE = 1 << 20


def lines(handle: BinaryIO, limit: int = MAX_LINE) -> Iterator[bytes]:
    """The lines of a binary file; ``ValueError`` at one longer than ``limit``."""
    number = 0
    for line in iter(functools.partial(handle.readline, limit + 1), b""):
        number += 1
        if len(line) > limit:
            raise ValueError("line %d is longer than %d bytes: not a .hyb file" % (number, limit))
        yield line


class Record(NamedTuple):
    gene1: bytes
    start1: int
    end1: int
    gene2: bytes
    start2: int
    end2: int
    reads: int          # raw read count from the id suffix, 0 when absent
    dg: Optional[float]
    overlap: Optional[float]
    chimera_type: bytes
    support: int = 1    # cluster support (see the module docstring)


_COUNT_TOTAL = re.compile(rb"(?:^|[;,\s])count_total\s*=\s*([^;,\s]+)(?=$|[;,\s])", re.I)
_NUMBER = re.compile(rb"^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$", re.I)
_SAFE_INTEGER = 2 ** 53 - 1


def legacy_annotations(cols: list) -> "tuple[int, Optional[float]]":
    """``(support, overlap)`` of a 15/16-column line, as ``parseAnnotations``
    in ``web/parser.worker.js``; ``ValueError`` where the web marks it invalid."""
    tail = [value.strip() for value in cols[15:]]
    match = _COUNT_TOTAL.search(b";".join(tail))
    if match:
        value = match.group(1)
        if not _DIGITS.match(value) or not 1 <= int(value) <= _SAFE_INTEGER:
            raise ValueError("count_total metadata must be a positive safe integer")
        return int(value), None
    column16 = tail[0] if tail else b""
    integer = int(column16) if _DIGITS.match(column16) else None
    if integer is not None and integer > _SAFE_INTEGER:
        if len(cols) == 16:
            raise ValueError("legacy column 16 count is outside the safe positive-integer range")
        integer = None
    if integer is not None and integer > 0:
        return integer, None
    if _NUMBER.match(column16):
        number = float(column16)
        if number == number and abs(number) != float("inf"):
            return 1, number
    return 1, None


def layout(line: bytes) -> "tuple[bool, bool]":
    """``(canonical, legacy_column16)`` of a line ``parse`` accepted, as the
    web parser counts them: a standard HYB2 17/18-column record, or a 15/16-
    column one whose column 16 is a positive integer read as cluster support
    (``ambiguousColumn16``: it may be a truncated 17-column record)."""
    cols = line.rstrip(b"\r\n").split(b"\t")
    if len(cols) >= 17:
        return True, False
    tail = [value.strip() for value in cols[15:]]
    if _COUNT_TOTAL.search(b";".join(tail)):
        return False, False
    column16 = tail[0] if tail else b""
    return False, bool(_DIGITS.match(column16)) and 1 <= int(column16) <= _SAFE_INTEGER


def _coordinate(value: bytes) -> int:
    value = value.strip()
    if not _DIGITS.match(value):
        raise ValueError("coordinate")
    number = int(value)
    if not 1 <= number <= _SAFE_INTEGER:  # as the web: a positive safe integer
        raise ValueError("coordinate")
    return number


def _optional_number(value: bytes) -> Optional[float]:
    value = value.strip()
    if not value or value == b".":
        return None
    try:
        number = float(value)
    except ValueError:
        return None
    return number if number == number and abs(number) != float("inf") else None


def raw_read_count(identifier: bytes) -> int:
    parts = identifier.strip().split(b"_")
    if len(parts) < 2 or not _DIGITS.match(parts[1]):
        return 0
    count = int(parts[1])
    return count if count <= _SAFE_INTEGER else 0  # the web reads no count


def infer_type(g1: bytes, s1: int, e1: int, g2: bytes, s2: int, e2: int) -> bytes:
    """``inferChimeraType`` from the web parser."""
    if g1 != g2:
        return b"Intermolecular"
    if s1 < s2 and e1 < s2: return b"Type_1"
    if s1 > e2 and e1 > e2: return b"Type_2"
    if s1 < s2 and s2 < e1 < e2: return b"Type_3"
    if s1 < s2 and e1 == e2: return b"Type_4"
    if s1 < s2 and e1 > e2: return b"Type_5"
    if s1 == s2 and e1 > e2: return b"Type_6"
    if s1 < e2 and e1 > e2: return b"Type_7"
    if s1 == s2 and e1 == e2: return b"Type_8"
    if s1 > s2 and e1 == e2: return b"Type_9"
    if s1 == s2 and e1 < e2: return b"Type_10"
    if s1 > s2 and e1 < e2: return b"Type_11"
    if s1 < s2 and e1 == s2: return b"Type_12"
    if s1 == e2 and e1 > e2: return b"Type_13"
    return b"Unclassified intramolecular"


def _type_name(value: bytes) -> bytes:
    value = value.strip()
    return b"" if not value or value == b"." or value.lower() == b"na" else value


def parse(line: bytes) -> Optional[Record]:
    """Parse one line; ``None`` for blank/comment lines, ``ValueError`` if invalid."""
    line = line.rstrip(b"\r\n")
    if not line.strip() or line.lstrip().startswith(b"#"):
        return None
    cols = line.split(b"\t")
    if len(cols) < 15:
        raise ValueError("expected at least 15 tab-separated columns")
    g1, g2 = cols[3].strip(), cols[9].strip()
    if not g1 or not g2:
        raise ValueError("missing RNA name")
    s1, e1, s2, e2 = _coordinate(cols[6]), _coordinate(cols[7]), _coordinate(cols[12]), _coordinate(cols[13])
    if s1 > e1 or s2 > e2:
        raise ValueError("invalid RNA coordinates")
    overlap = None
    ctype = b""
    support = 1
    if len(cols) >= 18 and cols[15].strip() == b"":
        overlap, ctype = _optional_number(cols[16]), _type_name(cols[17])
    elif len(cols) >= 17:
        overlap, ctype = _optional_number(cols[15]), _type_name(cols[16])
    else:
        support, overlap = legacy_annotations(cols)
    return Record(g1, s1, e1, g2, s2, e2, raw_read_count(cols[0]), _optional_number(cols[2]),
                  None if overlap is None else (int(overlap) if overlap.is_integer() and abs(overlap) <= _SAFE_INTEGER else overlap),
                  ctype or infer_type(g1, s1, e1, g2, s2, e2), support)
