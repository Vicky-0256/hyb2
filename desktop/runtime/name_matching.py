"""HYB RNA names against reference sequence names: the web's "Name matching"
(web/analysis-pages.js renderFastaMapping, web/analysis-data.js
buildFastaMapping) for a project.

Each analysed sample's RNA names (from its analysis index) are matched to the
sequences of the sample's reference by exact name: the names
``samples.read_fasta_sequences`` reports, already rewritten as the hyb2
pipeline rewrites ``|``-separated FASTA headers, so the web's loose ``|`` ->
``_`` alias is built in.  A manual match is kept on the reference record as
``matches: {rna: sequence name}``; an empty name marks the RNA as not matched
(the web's "Mark as not matched").  Unlike the web, which keeps its mapping
for the browser tab only, the project keeps manual matches, and the runtime
applies them where it looks an RNA up in its reference:
``samples.read_reference_sequence`` (Structure, Region, exports) and the
reference lengths of analysis queries (``reference_lengths``).
"""
from __future__ import annotations

from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

from .project import _mutate_manifest
from . import samples as _samples

# sequence names sent as suggestions per reference (a transcriptome has
# hundreds of thousands; set_reference_match checks any exact name)
_MAX_SUGGESTIONS = 5000


def manual_matches(reference: Optional[Dict[str, Any]]) -> Dict[str, str]:
    """The reference's manual matches, ``{rna: sequence name or ""}``."""
    matches = (reference or {}).get("matches")
    if not isinstance(matches, dict):
        return {}
    return {rna: name for rna, name in matches.items() if isinstance(rna, str) and isinstance(name, str)}


def _sequence_names(reference: Dict[str, Any]) -> Dict[str, int]:
    names: Dict[str, int] = {}
    for sequence in reference.get("sequences") or []:
        if isinstance(sequence, dict) and isinstance(sequence.get("name"), str) \
                and type(sequence.get("length")) is int and sequence["length"] >= 0:
            names.setdefault(sequence["name"], sequence["length"])
    return names


def resolve(reference: Dict[str, Any], rna: str, *, _names: Optional[Dict[str, int]] = None,
            _manual: Optional[Dict[str, str]] = None) -> Optional[str]:
    """The sequence name ``rna`` uses in ``reference``; ``None`` when not matched."""
    matches = manual_matches(reference) if _manual is None else _manual
    if rna in matches:
        return matches[rna] or None
    return rna if rna in (_sequence_names(reference) if _names is None else _names) else None


def reference_lengths(reference: Dict[str, Any]) -> Dict[str, int]:
    """Gene name -> length: every sequence by its name, then the manual
    matches (an RNA marked as not matched has no length).  A match takes
    the length of its sequence in the FASTA, whatever the other matches say
    (``A -> B`` with ``B`` marked as not matched still gives ``A`` the
    length of sequence ``B``, as ``resolve`` reads it), so the order of the
    matches does not matter."""
    sequences = _sequence_names(reference)
    lengths = dict(sequences)
    for rna, name in manual_matches(reference).items():
        if name and name in sequences:
            lengths[rna] = sequences[name]
        elif not name:
            lengths.pop(rna, None)
    return lengths


def _unsupported(root: Path, reference: Dict[str, Any]) -> Optional[Dict[str, Any]]:
    """The reference's unsupported FASTA symbols: stored when it was added,
    read from the file for references added before that (None when missing)."""
    stored = reference.get("unsupported_symbols")
    if isinstance(stored, dict) and isinstance(stored.get("entries"), int):
        return {"entries": stored["entries"], "characters": list(stored.get("characters") or [])}
    status, _ = _samples.check_record(root, reference)
    if status == "missing":
        return None
    symbols: Dict[str, Any] = {}
    try:
        _samples.read_fasta_sequences(_samples.resolve_record_path(root, reference), symbols)
    except (OSError, ValueError):
        return None
    return symbols


def reference_matching(root: Path, manifest: Dict[str, Any], sample_ids: List[str],
                       rna_names: Callable[[str], List[str]]) -> Dict[str, Any]:
    """The name matching of the given (analysed) samples.

    ``rna_names(sample_id)`` lists one sample's RNA names (in display order).
    ``references[]`` has, per reference the samples use, ``reference_id,
    label, sequences`` (count), ``sequence_names`` (the first 5,000), ``sample_ids``,
    ``unsupported_entries``/``unsupported_characters`` (``None`` when the file
    cannot be read), ``rnas[]`` (``{name, match, manual}``; ``match`` is
    ``None`` when not matched) and ``matched``.  The totals: ``rna_names``
    (distinct names over the samples), ``matched`` (names matched in their
    sample's reference), ``sequences``, ``unreferenced_sample_ids`` and
    ``rna_name_list`` (the distinct names in the web's order: its
    ``summary.rnaNames``).
    """
    references = {r.get("id"): r for r in manifest.get("references") or [] if isinstance(r, dict)}
    samples = {s.get("id"): s for s in manifest.get("samples") or [] if isinstance(s, dict)}
    by_reference: Dict[str, List[str]] = {}
    names_by_reference: Dict[str, Dict[str, None]] = {}
    every: Dict[str, None] = {}
    matched: set = set()
    unreferenced: List[str] = []
    for sample_id in sample_ids:
        names = rna_names(sample_id)
        every.update(dict.fromkeys(names))
        reference_id = (samples.get(sample_id) or {}).get("reference_id")
        if reference_id not in references:
            unreferenced.append(sample_id)
            continue
        by_reference.setdefault(reference_id, []).append(sample_id)
        names_by_reference.setdefault(reference_id, {}).update(dict.fromkeys(names))
    out = []
    for reference_id, ids in by_reference.items():
        reference = references[reference_id]
        manual = manual_matches(reference)
        sequence_names = _sequence_names(reference)
        rows = []
        for name in names_by_reference[reference_id]:
            match = resolve(reference, name, _names=sequence_names, _manual=manual)
            rows.append({"name": name, "match": match, "manual": name in manual})
            if match:
                matched.add(name)
        unsupported = _unsupported(root, reference)
        out.append({
            "reference_id": reference_id, "label": reference.get("label") or reference_id,
            "sequences": len(reference.get("sequences") or []),
            "sequence_names": list(sequence_names)[:_MAX_SUGGESTIONS],
            "sample_ids": ids, "rnas": rows, "matched": sum(1 for row in rows if row["match"]),
            "unsupported_entries": None if unsupported is None else unsupported["entries"],
            "unsupported_characters": None if unsupported is None else unsupported["characters"],
        })
    return {"references": out, "rna_names": len(every), "matched": len(matched),
            "sequences": sum(item["sequences"] for item in out), "unreferenced_sample_ids": unreferenced,
            "rna_name_list": _web_order(every, len(sample_ids))}


def _web_order(names: Dict[str, None], samples: int) -> List[str]:
    """The distinct RNA names in the web's order (parser.worker.js rnaNames,
    localeCompare): one sample's ``rna_names`` already come in it; several
    are sorted again by the analysis backend's name key (plain string order
    if it is not loaded)."""
    if samples <= 1:
        return list(names)
    try:
        from hyb2analysis.query import _name_key
    except ImportError:
        return sorted(names)
    return sorted(names, key=_name_key)


def set_reference_match(root: Path, reference_id: str, rna: Any, sequence: Any) -> Dict[str, Any]:
    """Match ``rna`` to the sequence named ``sequence`` (``""``: mark it as
    not matched; ``None``: back to the automatic exact-name match)."""
    if not isinstance(rna, str) or not rna.strip():
        raise ValueError("rna must be a non-empty RNA name")
    if sequence is not None and not isinstance(sequence, str):
        raise ValueError("sequence must be a reference sequence name, \"\" or null")
    rna = rna.strip()
    sequence = None if sequence is None else sequence.strip()

    def mutate(manifest: Dict[str, Any]) -> Dict[str, Any]:
        reference = next((r for r in manifest.get("references") or [] if r.get("id") == reference_id), None)
        if reference is None:
            raise ValueError("unknown reference: %s" % reference_id)
        names = _sequence_names(reference)
        if sequence and sequence not in names:
            raise ValueError("Enter an exact reference ID from the loaded FASTA file.")
        matches = manual_matches(reference)
        automatic = rna if rna in names else None
        if sequence is None or (sequence or None) == automatic:
            matches.pop(rna, None)
        else:
            matches[rna] = sequence
        if matches:
            reference["matches"] = matches
        else:
            reference.pop("matches", None)
        return reference
    return _mutate_manifest(_samples._project_root(root), mutate)
