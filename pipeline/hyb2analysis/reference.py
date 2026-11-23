"""Reference records and 1-based inclusive windows used for HYB-guided folding."""
from __future__ import annotations

import gzip
import re
from itertools import groupby
from operator import itemgetter
from pathlib import Path
from typing import Callable, Collection, Iterator, TextIO

# Input policy, not an engine's minimum foldable length. An explicit single
# reference window retains the task cutoff; a pasted single strand retains
# the browser's hairpin cutoff. Whole references, HYB records and each of two
# fragments need only be nonempty. Lengths are measured after normalization.
MIN_WINDOW_NT = 10
MIN_PASTED_NT = 5
MIN_SEQUENCE_NT = 1
MAX_HEADER = 1024 * 1024

# The pipeline formatter, reference readers and browser (via the generated
# catalog) all use these ordered substitutions. The trigger is file-wide.
FASTA_NAMES = {
    "trigger": "|",
    "replacements": (("_", "-"), ("protein-coding", "mRNA"), ("|", "_"), ("__", "_unknown_")),
}

# cplfold.sequence.WHITESPACE is Unicode White_Space plus BOM. Python's \s includes
# four additional information separators. Keep this header-only operation
# dependency-free: standalone hyb2pipe does not ship the folding runtime.
_HEADER_INDENT = re.compile(r"^(?:[^\S\x1c-\x1f]|\ufeff)+(?=>)")


def unindent_fasta_header(line: str) -> str:
    """Place a FASTA header marker in column one; leave all other text alone."""
    return _HEADER_INDENT.sub("", line, count=1)


def hyb2_reference_name(header: str) -> str:
    for old, new in FASTA_NAMES["replacements"]:
        header = header.replace(old, new)
    return header


def unique_fasta_id(header: str, formatted: bool, seen: set[str]) -> str:
    """The effective record ID (after file-wide HYB2 rewriting), never first/last wins."""
    words = (hyb2_reference_name(header) if formatted else header).split(None, 1)
    name = words[0] if words else ""
    if name in seen:
        raise ValueError(f"duplicate FASTA ID {name!r}; give each reference record a unique ID")
    seen.add(name)
    return name


def fasta_chunks(handle: TextIO, name: str) -> Iterator[tuple[bool, str]]:
    """Yield headers and coordinate-bearing sequence chunks without changing case or DNA T."""
    from cplfold.sequence import WHITESPACE as space

    remove_layout = dict.fromkeys(map(ord, space))
    at_start = True
    for piece in iter(lambda: handle.readline(MAX_HEADER), ""):
        ends = piece.endswith("\n")
        line = unindent_fasta_header(piece) if at_start else piece
        if at_start and line.startswith(">"):
            if not ends and len(piece) >= MAX_HEADER:
                raise ValueError(f"reference header line is longer than {MAX_HEADER} bytes: {name}")
            yield True, line[1:].strip(space)
        else:
            yield False, line.translate(remove_layout)
        at_start = ends


def _open_text(path: Path) -> TextIO:
    opener = gzip.open if path.name.endswith(".gz") else open
    return opener(path, "rt", encoding="utf-8", errors="replace")


def fasta_record_chunks(path: Path, *, open_text: Callable[[Path], TextIO] = _open_text) -> Iterator[tuple[str, str, Iterator[str]]]:
    """Yield (effective ID, original header, bounded raw sequence chunks).

    A pipe in even the last header changes every ID. Check the format first.
    Consume a record's chunks before advancing to the next record; skipped
    chunks are discarded. Exhaust the outer iterator to check all IDs.
    """
    with open_text(path) as handle:
        formatted = any(is_header and FASTA_NAMES["trigger"] in text for is_header, text in fasta_chunks(handle, path.name))
    seen: set[str] = set()

    def named_chunks(handle: TextIO) -> Iterator[tuple[str, str]]:
        name = None
        for is_header, text in fasta_chunks(handle, path.name):
            if is_header:
                name = unique_fasta_id(text, formatted, seen)
            if name is not None:
                yield name, text

    with open_text(path) as handle:
        for name, group in groupby(named_chunks(handle), key=itemgetter(0)):
            _, header = next(group)
            yield name, header, (text for _, text in group if text)
    if not seen:
        raise ValueError("reference FASTA has no sequences")


def fasta_records(path: Path, names: Collection[str] | None = None, *, open_text: Callable[[Path], TextIO] = _open_text,
                  max_length: int | None = None) -> Iterator[tuple[str, str, str]]:
    """Assemble only the requested records. Exhaust the iterator to check all IDs."""
    for name, header, chunks in fasta_record_chunks(path, open_text=open_text):
        if names is not None and name not in names:
            continue
        parts, size = [], 0
        for text in chunks:
            size += len(text)
            if max_length is not None and size > max_length:
                raise ValueError(f"reference sequence {name} is too large to read whole")
            parts.append(text)
        yield name, header, "".join(parts)


def read_fasta(path: Path) -> list[tuple[str, str]]:
    """Read all records, keeping their case and DNA T."""
    return [(name, sequence) for name, _, sequence in fasta_records(path)]


def reference_sequence(path: Path, gene_id: str | None, *, open_text: Callable[[Path], TextIO] = _open_text) -> tuple[str, str]:
    from hyb2analysis.fold_text import checked_fold_text

    record = None
    several = False
    for name, _, chunks in fasta_record_chunks(path, open_text=open_text):
        if gene_id is None or name == gene_id:
            if record is None:
                record = name, "".join(chunks)
            else:
                several = True
    if several:
        raise ValueError("the reference FASTA has several sequences; give gene_id")
    if record is None:
        raise ValueError(f"gene_id {gene_id} is not a sequence of the reference FASTA")
    name, sequence = record
    return name, checked_fold_text(sequence, "Reference sequence")


def parse_coordinate(value: object) -> int:
    """A positive browser-safe integer, without truncation or type coercion.

    Form text is ASCII decimal digits (leading zeros allowed), optionally
    surrounded by ASCII whitespace. Signs, decimal points, exponents and
    separators are not coordinate text. Keep this in sync with fold-text.js.
    """
    if isinstance(value, str):
        value = value.strip(" \t\r\n\f\v")
        if not re.fullmatch(r"[0-9]+", value):
            raise ValueError("coordinate must be decimal integer text")
        value = int(value)
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not 1 <= value <= 9007199254740991 \
            or (isinstance(value, float) and not value.is_integer()):
        raise ValueError("coordinate must be a positive safe integer")
    return int(value)


def validate_input_length(length: int, label: str, *, source: str = "reference", paired: bool = False,
                          window: bool = False) -> None:
    """Task/form limits on normalized input, not prepared engines or standalone CLIs.

    A uniquely located paste/record retains its source policy even when the
    task represents it as a reference window to collect matching evidence.
    """
    minimum = MIN_SEQUENCE_NT
    if not paired:
        if source == "paste":
            minimum = MIN_PASTED_NT
        elif source == "reference" and window:
            minimum = MIN_WINDOW_NT
    if length < minimum:
        raise ValueError(f"{label} is shorter than {minimum} nt")


def validate_window(start: int, end: int, length: int, number: int = 1, *, source: str = "reference") -> None:
    if not 1 <= start <= end <= length:
        raise ValueError(f"window {number} ({start}-{end}) is outside 1-{length}")
    validate_input_length(end - start + 1, f"window {number}", source=source, window=True)
