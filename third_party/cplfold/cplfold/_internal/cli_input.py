"""Sequence-file input shared by the command wrappers and Python entry points.

This module also defines :class:`CofoldInputError`, the input error of every
public function. It lives here, free of NumPy, so that :mod:`cplfold.sequence`
can raise it without loading the folding code.
"""

from pathlib import Path


class CofoldInputError(ValueError):
    """Expected input/range errors that CLI callers can report without traceback."""


def read_sequence_file(path: Path, cofold: bool = False) -> str:
    """Return the sequence of a plain or FASTA file (two records joined by '&' with ``cofold``).

    A missing or unreadable file raises ``OSError`` (``FileNotFoundError`` when
    it does not exist); invalid UTF-8 or file contents raise
    :class:`CofoldInputError`, a ``ValueError``.
    """
    path = Path(path)
    try:
        contents = path.expanduser().read_text(encoding="utf-8")
    except FileNotFoundError as error:
        raise FileNotFoundError(f"CPLfold sequence file was not found: {path}") from error
    except UnicodeError as error:
        raise CofoldInputError(f"CPLfold sequence file is not valid UTF-8: {path}") from error
    except OSError as error:
        raise OSError(f"Could not read CPLfold sequence file {path}: {error}") from error

    header_count, sequence_parts, records = 0, [], []
    for line in contents.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        if stripped.startswith(">"):
            if header_count == 0 and sequence_parts:
                raise CofoldInputError("FASTA sequence data must follow the first record header")
            if header_count:
                records.append("".join(sequence_parts))
                sequence_parts = []
            header_count += 1
            if header_count > (2 if cofold else 1):
                raise CofoldInputError("Cofold sequence files must contain exactly two FASTA records"
                                       if cofold else "CPLfold sequence files must contain exactly one FASTA record")
            continue
        sequence_parts.append("".join(stripped.split()))

    sequence = "".join(sequence_parts)
    if header_count == 2:
        records.append(sequence)
        if any(not record or "&" in record for record in records):
            raise CofoldInputError("Each cofold FASTA record must contain one nonempty RNA strand")
        sequence = "&".join(records)
    if not sequence:
        raise CofoldInputError(f"CPLfold sequence file contains no sequence: {path}")
    return sequence
