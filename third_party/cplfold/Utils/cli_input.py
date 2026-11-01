"""Sequence-file input shared by the command wrappers and Python entry points."""

from pathlib import Path


def read_sequence_file(path: Path, cofold: bool = False) -> str:
    try:
        contents = path.expanduser().read_text(encoding="utf-8")
    except FileNotFoundError as error:
        raise SystemExit(f"CPLfold sequence file was not found: {path}") from error
    except UnicodeError as error:
        raise SystemExit(f"CPLfold sequence file is not valid UTF-8: {path}") from error
    except OSError as error:
        raise SystemExit(f"Could not read CPLfold sequence file {path}: {error}") from error

    header_count, sequence_parts, records = 0, [], []
    for line in contents.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        if stripped.startswith(">"):
            if header_count == 0 and sequence_parts:
                raise SystemExit("FASTA sequence data must follow the first record header")
            if header_count:
                records.append("".join(sequence_parts))
                sequence_parts = []
            header_count += 1
            if header_count > (2 if cofold else 1):
                raise SystemExit("Cofold sequence files must contain exactly two FASTA records"
                                 if cofold else "CPLfold sequence files must contain exactly one FASTA record")
            continue
        sequence_parts.append("".join(stripped.split()))

    sequence = "".join(sequence_parts)
    if header_count == 2:
        records.append(sequence)
        if any(not record or "&" in record for record in records):
            raise SystemExit("Each cofold FASTA record must contain one nonempty RNA strand")
        sequence = "&".join(records)
    if not sequence:
        raise SystemExit(f"CPLfold sequence file contains no sequence: {path}")
    return sequence
