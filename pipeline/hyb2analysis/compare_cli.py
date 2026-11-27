"""``python3 -m hyb2analysis compare|coverage|viewpoint`` (NumPy, no R).

``compare`` makes each sample's contact map as the desktop's comparisons do
(:func:`coverage.write_contact`, or an inter-RNA map counted as the web
counts it) and runs :func:`differential.compare`.  ``coverage`` and
``viewpoint`` read a .hyb(.gz) or an analysis index (:func:`index.build`) and
give the same cells and profiles from either (:func:`query.contact_bins`,
:func:`query.viewpoint`), except for malformed rows: a .hyb's default
contact.txt (one gene, 10-nt bins, records) is
:func:`coverage.write_contact`'s, which reads every line as hyb2_coverage
did, and its other maps skip a row with a position past ``_MAX_POSITION``
or an arm of ``_MAX_ARM`` nt or more, as the desktop's inter-RNA maps do.
Each command also draws a small SVG.  They take over from the removed
``hyb2_compare``, ``hyb2_coverage`` and ``plot_viewpoint`` scripts.
"""
from __future__ import annotations

import argparse
import gzip
import math
import os
import sys
import tempfile
import zlib
from pathlib import Path
from typing import Any, Dict, Iterator, List, Optional, Sequence, Tuple
from xml.sax.saxutils import escape

import numpy as np

from . import coverage, differential, hybfile, query

BIN_SIZE = coverage.BIN_SIZE
_SQLITE_MAGIC = b"SQLite format 3\x00"
_MAX_POSITION = 2 ** 31 - 1
_MAX_ARM = 10_000
_CHUNK = 16384
_BATCH_CELLS = 1 << 22
_SHIFT = 1 << 32
_MAX_SVG_CELLS = 8000
_SVG_GRID_TARGET = 160


def _is_sqlite(path: Path) -> bool:
    if path.suffix.lower() in (".sqlite", ".sqlite3", ".hyb2idx", ".db"):
        return True
    try:
        with open(path, "rb") as handle:
            return handle.read(len(_SQLITE_MAGIC)) == _SQLITE_MAGIC
    except OSError:
        return False


def _is_contact_path(path: Path) -> bool:
    name = path.name.lower()
    return name.endswith(".contact.txt") or name.endswith(".contact.tsv")


def _sample_stem(path: Path) -> str:
    name = path.name
    for suffix in (".contact.txt", ".contact.tsv", ".hyb.gz", ".hyb", ".txt", ".tsv", ".gz"):
        if name.lower().endswith(suffix):
            return name[: -len(suffix)] or path.stem
    return path.stem


def _unique_names(paths: Sequence[Path]) -> List[str]:
    """Sample names from the file names; each also gives its own contact file
    name (``_safe_filename``, compared without case)."""
    taken: set = set()
    out: List[str] = []
    for path in paths:
        base = name = _sample_stem(path)
        count = 1
        while _safe_filename(name).lower() in taken:
            count += 1
            name = f"{base}_{count}"
        taken.add(_safe_filename(name).lower())
        out.append(name)
    return out


def _safe_filename(text: str) -> str:
    return "".join(c if c.isalnum() or c in "._-" else "_" for c in text)


def _is_gzip(path: Path) -> bool:
    with open(path, "rb") as handle:
        return handle.read(2) == b"\x1f\x8b"


def _hyb_lines(hyb: Path) -> Iterator[bytes]:
    """The lines of a .hyb, gzip-compressed or not (told by its first bytes)."""
    packed = _is_gzip(hyb)
    try:
        with (gzip.open(hyb, "rb") if packed else open(hyb, "rb")) as handle:
            yield from hybfile.lines(handle)
    except (EOFError, zlib.error, gzip.BadGzipFile) as error:  # a truncated or corrupt .gz
        raise ValueError(f"{hyb}: {error}") from error


def _records(hyb: Path, skipped: Dict[str, int]) -> Iterator[hybfile.Record]:
    """The records of a .hyb.  Lines ``hybfile.parse`` (the web parser's
    rules) rejects are left out, as ``index.build`` and the web's differential
    test leave them out, and counted in ``skipped["invalid"]``."""
    for line in _hyb_lines(hyb):
        try:
            record = hybfile.parse(line)
        except ValueError:
            skipped["invalid"] = skipped.get("invalid", 0) + 1
            continue
        if record is not None:
            yield record


def _warn_skipped(hyb: Path, skipped: Dict[str, int]) -> None:
    if skipped.get("invalid"):
        print(f"warning: {hyb}: skipped {skipped['invalid']} lines that are not .hyb records", file=sys.stderr)
    if skipped.get("out_of_range"):
        print(f"warning: {hyb}: skipped {skipped['out_of_range']} rows with a position past {_MAX_POSITION} "
              f"or an arm of {_MAX_ARM} nt or more", file=sys.stderr)


def _is_homodimer(record: hybfile.Record) -> bool:
    """The web's homodimer proxy (``query.HOMODIMER``): both arms on one RNA, overlap score >= 5."""
    return record.gene1 == record.gene2 and record.overlap is not None and record.overlap >= 5


def _materialize_plain_hyb(hyb: Path, tmp_dir: Path) -> Path:
    """Return a plain uncompressed .hyb path for ``coverage.write_contact``."""
    if not _is_gzip(hyb):
        return hyb
    plain = tmp_dir / "plain.hyb"
    with open(plain, "wb") as dst:
        dst.writelines(_hyb_lines(hyb))
    return plain


def _parse_condition(value: str, lineno: int, table_path: Path) -> bool:
    norm = value.strip().lower()
    if norm in ("condition_one", "one", "1"):
        return True
    if norm in ("condition_two", "two", "2"):
        return False
    raise ValueError(
        f"{table_path}:{lineno}: invalid condition {value!r}; expected condition_one or condition_two"
    )


def _resolve_table_path(raw: str, table_dir: Path) -> Path:
    path = Path(raw).expanduser()
    if not path.is_absolute() and not path.exists():
        candidate = table_dir / path
        if candidate.exists():
            return candidate
    return path


Entry = Tuple[Optional[Path], Optional[Path], bool, Optional[str]]  # hyb, contact, condition one, gene


def _parse_sample_table(table_path: Path) -> List[Entry]:
    """The rows of a sample table: ``[(hyb, contact, is_condition_one, gene), ...]``.

    Columns are TAB-separated, or whitespace-separated in a line without a
    TAB: ``<hyb or contact.txt> <condition>``, hyb2_compare's
    ``<hyb> <contact.txt> <condition>`` (``-`` for no contact.txt), or that
    and the gene the row's map is of, as each desktop sample has a target
    gene (``-a`` overrides it).  ``#`` lines and a header row are skipped;
    a relative path is found from the working directory, else from the
    table's directory.  :func:`run_compare` says which file a row's map
    comes from.
    """
    if not table_path.is_file():
        raise ValueError(f"sample table not found: {table_path}")
    entries: List[Entry] = []
    table_dir = table_path.parent
    lines = table_path.read_text(encoding="utf-8").splitlines()
    for lineno, raw_line in enumerate(lines, start=1):
        stripped = raw_line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        cols = [c.strip() for c in (raw_line.split("\t") if "\t" in raw_line else stripped.split())]
        if len(cols) not in (2, 3, 4):
            raise ValueError(f"{table_path}:{lineno}: expected 2, 3 or 4 columns, got {len(cols)}")
        condition = cols[1] if len(cols) == 2 else cols[2]
        if not entries and condition.lower() in ("condition", "group", "cond"):
            continue
        is_one = _parse_condition(condition, lineno, table_path)
        gene = cols[3] if len(cols) == 4 and cols[3] not in ("", "-") else None
        if len(cols) >= 3:
            hyb_raw, contact_raw = cols[0], cols[1]
            hyb = None if hyb_raw in ("", "-") else _resolve_table_path(hyb_raw, table_dir)
            contact = None if contact_raw in ("", "-") else _resolve_table_path(contact_raw, table_dir)
            if hyb is None and contact is None:
                raise ValueError(f"{table_path}:{lineno}: both hyb and contact columns are empty")
        else:
            item = _resolve_table_path(cols[0], table_dir)
            if _is_contact_path(item):
                hyb, contact = None, item
            else:
                hyb, contact = item, None
        entries.append((hyb, contact, is_one, gene))
    if not entries:
        raise ValueError(f"sample table is empty: {table_path}")
    return entries


def _iter_hyb_pair_arms(
    hyb: Path,
    gene_x: str,
    gene_y: str,
    skipped: Dict[str, int],
    *,
    homodimer: Optional[str] = None,
    measure: str = "records",
):
    """Yield ``(x_start, x_end, y_start, y_end, weight)`` for ``gene_x`` x ``gene_y``.

    When ``gene_x != gene_y``, arms on ``(gene_y, gene_x)`` are swapped so ``x``
    always belongs to ``gene_x`` and ``y`` to ``gene_y``.  ``homodimer``
    ``"only"`` keeps the homodimer-proxy hybrids and ``"exclude"`` drops them,
    so ``"only"`` leaves an inter-RNA map empty (as ``query.contact_bins``).
    Rows with a position past ``_MAX_POSITION`` or an arm of ``_MAX_ARM`` nt
    or more are malformed: left out (as the desktop's inter-RNA maps) and
    counted in ``skipped["out_of_range"]``.
    """
    gx = gene_x.encode("utf-8")
    gy = gene_y.encode("utf-8")
    same = gx == gy
    for rec in _records(hyb, skipped):
        if rec.gene1 == gx and rec.gene2 == gy:
            s1, e1, s2, e2 = rec.start1, rec.end1, rec.start2, rec.end2
        elif not same and rec.gene1 == gy and rec.gene2 == gx:
            s1, e1, s2, e2 = rec.start2, rec.end2, rec.start1, rec.end1
        else:
            continue
        if homodimer is not None and _is_homodimer(rec) != (homodimer == "only"):
            continue
        if max(s1, e1, s2, e2) > _MAX_POSITION or (e1 - s1) >= _MAX_ARM or (e2 - s2) >= _MAX_ARM:
            skipped["out_of_range"] = skipped.get("out_of_range", 0) + 1
            continue
        if measure == "reads":
            w = int(rec.reads)
        elif measure == "support":
            w = int(rec.support)
        else:
            w = 1
        if w:
            yield s1, e1, s2, e2, w


def _hyb_contact_cells(
    hyb: Path,
    gene_x: str,
    gene_y: str,
    skipped: Dict[str, int],
    *,
    bin_size: int = BIN_SIZE,
    measure: str = "records",
    homodimer: Optional[str] = None,
) -> Tuple[np.ndarray, np.ndarray, np.ndarray, int]:
    """Compute ``(x_bins, y_bins, counts, record_count)`` for ``gene_x`` x ``gene_y``."""
    parts_keys: List[np.ndarray] = []
    parts_counts: List[np.ndarray] = []
    records = 0

    def flush(batch: List[Tuple[int, int, int, int, int]]) -> None:
        arr = np.asarray(batch, dtype=np.int64)
        bins = arr[:, :4] // bin_size
        w = arr[:, 4]
        a, b, e, f = bins[:, 0], bins[:, 1], bins[:, 2], bins[:, 3]
        nx, ny = b - a + 1, f - e + 1
        cells = nx * ny
        owner = np.repeat(np.arange(len(cells)), cells)
        offset = np.arange(int(cells.sum()), dtype=np.int64) - np.repeat(np.cumsum(cells) - cells, cells)
        keys = (a[owner] + offset // ny[owner]) * bin_size * _SHIFT + (e[owner] + offset % ny[owner]) * bin_size
        weights = w[owner]
        unique, inverse = np.unique(keys, return_inverse=True)
        counts = np.bincount(inverse, weights=weights, minlength=len(unique)).astype(np.int64)
        parts_keys.append(unique)
        parts_counts.append(counts)

    batch: List[Tuple[int, int, int, int, int]] = []
    cells = 0
    for s1, e1, s2, e2, w in _iter_hyb_pair_arms(hyb, gene_x, gene_y, skipped, homodimer=homodimer, measure=measure):
        batch.append((s1, e1, s2, e2, w))
        records += 1
        cells += (e1 // bin_size - s1 // bin_size + 1) * (e2 // bin_size - s2 // bin_size + 1)
        if len(batch) >= _CHUNK or cells >= _BATCH_CELLS:
            flush(batch)
            batch, cells = [], 0
    if batch:
        flush(batch)
    if not parts_keys:
        empty = np.zeros(0, dtype=np.int64)
        return empty, empty, empty, 0
    keys = np.concatenate(parts_keys)
    counts = np.concatenate(parts_counts)
    unique, inverse = np.unique(keys, return_inverse=True)
    merged = np.bincount(inverse, weights=counts, minlength=len(unique)).astype(np.int64)
    xs = (unique // _SHIFT).astype(np.int64)
    ys = (unique % _SHIFT).astype(np.int64)
    return xs, ys, merged, records


def _write_contact_tsv(path: Path, gene_x: str, gene_y: str, xs: Sequence[int], ys: Sequence[int], counts: Sequence[int]) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix="." + path.name + ".", suffix=".tmp", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(f"# hybrids between {gene_x} and {gene_y}\n")
            if len(xs):
                handle.write("x\ty\tcount\n")
                for x, y, c in zip(xs, ys, counts):
                    handle.write(f"{int(x)}\t{int(y)}\t{int(c)}\n")
        os.replace(tmp, path)
    except BaseException:
        Path(tmp).unlink(missing_ok=True)
        raise
    return path


def _build_contact_file(hyb: Path, gene: str, out_path: Path) -> Path:
    """``coverage.write_contact`` (hyb2_coverage's contact.txt, as the desktop makes it) of a .hyb(.gz)."""
    with tempfile.TemporaryDirectory() as tmp:
        return coverage.write_contact(_materialize_plain_hyb(hyb, Path(tmp)), gene, out_path)


def _read_fasta_gene_length(fasta: Path, gene: str) -> Optional[int]:
    from . import reference

    if not fasta.is_file():
        raise ValueError(f"reference FASTA not found: {fasta}")
    opener = gzip.open if fasta.name.lower().endswith(".gz") else open
    has_pipe = False
    with opener(fasta, "rt", encoding="utf-8", errors="replace") as handle:
        for line in handle:
            if line.lstrip().startswith(">") and reference.FASTA_NAMES["trigger"] in line:
                has_pipe = True
                break
    seen: set[str] = set()
    current_name: Optional[str] = None
    length = 0
    with opener(fasta, "rt", encoding="utf-8", errors="replace") as handle:
        for raw in handle:
            line = reference.unindent_fasta_header(raw)
            if line.startswith(">"):
                if current_name == gene:
                    return length
                header = line[1:].strip()
                current_name = reference.unique_fasta_id(header, has_pipe, seen)
                length = 0
            elif current_name == gene:
                length += len("".join(line.split()))
    if current_name == gene:
        return length
    return None


def _hyb_viewpoint(
    hyb: Path,
    gene: str,
    start: Optional[int],
    end: Optional[int],
    *,
    partner: Optional[str] = None,
    measure: str = "records",
    bin_size: int = 1,
    homodimer: Optional[str] = None,
    ref_length: Optional[int] = None,
    skipped: Optional[Dict[str, int]] = None,
) -> Dict[str, Any]:
    """``query.viewpoint`` of a .hyb: the same bins, records and default range."""
    target = gene.encode("utf-8")
    other = partner.encode("utf-8") if partner else None
    arms: List[Tuple[int, int, int, float]] = []  # (record number, start, end, weight)
    # the default end: the gene's last arm end in any record, as the index's genes.max_end
    obs_max: Optional[int] = None

    for number, rec in enumerate(_records(hyb, {} if skipped is None else skipped)):
        for name, end_ in ((rec.gene1, rec.end1), (rec.gene2, rec.end2)):
            if name == target and (obs_max is None or end_ > obs_max):
                obs_max = end_
        on1 = rec.gene1 == target and (other is None or rec.gene2 == other)
        on2 = rec.gene2 == target and (other is None or rec.gene1 == other)
        if not (on1 or on2):
            continue
        # homodimers are intramolecular: "only" leaves out every inter-RNA record
        if homodimer is not None and _is_homodimer(rec) != (homodimer == "only"):
            continue
        if measure == "reads":
            w = float(rec.reads)
        elif measure == "support":
            w = float(rec.support)
        else:
            w = 1.0
        if on1:
            arms.append((number, rec.start1, rec.end1, w))
        if on2:
            arms.append((number, rec.start2, rec.end2, w))

    lo = start if start is not None else 1
    hi = end if end is not None else (ref_length if ref_length is not None else obs_max)
    if hi is not None and lo > hi:
        raise ValueError("start must not exceed end")
    out = {
        "gene": gene,
        "partner": partner,
        "start": lo,
        "end": hi,
        "length": ref_length if ref_length is not None else obs_max,
        "bin_size": bin_size,
        "measure": measure,
        "records": 0,
        "bins": {"start": [], "end": [], "value": []},
        "max": 0.0,
    }
    if hi is None or hi < 1:
        return out
    covered = max(0, min(hi, max(ref_length or 0, obs_max or 0)) - lo + 1)
    width = max(bin_size, -(-(hi - lo + 1) // query.MAX_PROFILE_BINS))
    marks: List[int] = []
    weights: List[float] = []
    hit = set()  # the records with an arm in the range, which query.viewpoint counts
    for number, s, e, w in arms:
        if s <= hi and e >= lo:
            hit.add(number)
            if w:
                marks += [max(s, lo) - lo, min(e, hi) - lo + 1]
                weights += [w, -w]
    if marks:
        diff = np.bincount(np.asarray(marks, dtype=np.int64), weights=np.asarray(weights, dtype=np.float64),
                           minlength=covered + 1)
    else:
        diff = np.zeros(covered + 1, dtype=np.float64)
    starts, ends, values = query._bin_means(diff, lo, hi, width)
    out.update({
        "records": len(hit),
        "bin_size": width,
        "bins": {"start": starts.tolist(), "end": ends.tolist(), "value": values.tolist()},
        "max": float(values.max()) if len(values) else 0.0,
    })
    return out


# ---------------------------------------------------------------------------
# Pure-Python SVG plotting (zero external dependencies)
# ---------------------------------------------------------------------------

def _nice_ticks(vmin: float, vmax: float, target: int = 5) -> List[float]:
    if vmax <= vmin:
        return [vmin]
    span = vmax - vmin
    raw_step = span / max(1, target)
    mag = 10.0 ** math.floor(math.log10(raw_step))
    norm = raw_step / mag
    if norm <= 1.5:
        step = 1.0 * mag
    elif norm <= 3.0:
        step = 2.0 * mag
    elif norm <= 7.0:
        step = 5.0 * mag
    else:
        step = 10.0 * mag
    start = math.ceil(vmin / step - 1e-9) * step
    ticks: List[float] = []
    cur = start
    while cur <= vmax + step * 1e-9:
        ticks.append(0.0 if abs(cur) < 1e-12 else cur)
        cur += step
    return ticks


def _fmt_tick(val: float) -> str:
    if abs(val - round(val)) < 1e-9:
        return str(int(round(val)))
    return f"{val:.3g}"


def _lerp_color(c0: Tuple[int, int, int], c1: Tuple[int, int, int], t: float) -> str:
    t = max(0.0, min(1.0, t))
    r = int(round(c0[0] + (c1[0] - c0[0]) * t))
    g = int(round(c0[1] + (c1[1] - c0[1]) * t))
    b = int(round(c0[2] + (c1[2] - c0[2]) * t))
    return f"#{r:02x}{g:02x}{b:02x}"


def _downsample_heatmap(
    xs: np.ndarray,
    ys: np.ndarray,
    vals: np.ndarray,
    bin_size: int,
    max_x: float,
    max_y: float,
    *,
    diverging: bool,
) -> Tuple[np.ndarray, np.ndarray, np.ndarray, float, float]:
    if len(xs) <= _MAX_SVG_CELLS:
        return xs.astype(np.float64), ys.astype(np.float64), vals.astype(np.float64), float(bin_size), float(bin_size)
    cell_x = max(float(bin_size), max_x / _SVG_GRID_TARGET)
    cell_y = max(float(bin_size), max_y / _SVG_GRID_TARGET)
    gx = (xs // cell_x).astype(np.int64)
    gy = (ys // cell_y).astype(np.int64)
    keys = gx * _SHIFT + gy
    order = np.argsort(keys, kind="stable")
    sk = keys[order]
    sv = vals[order]
    breaks = np.flatnonzero(np.concatenate(([True], sk[1:] != sk[:-1])))
    uk = sk[breaks]
    if diverging:
        # keep the extreme signed score within each display bucket
        out_v = np.empty(len(uk), dtype=np.float64)
        ends = np.append(breaks[1:], len(sv))
        for idx, (lo, hi) in enumerate(zip(breaks, ends)):
            chunk = sv[lo:hi]
            out_v[idx] = float(chunk[int(np.argmax(np.abs(chunk)))])
    else:
        out_v = np.maximum.reduceat(sv.astype(np.float64), breaks)
    ux = (uk // _SHIFT).astype(np.float64) * cell_x
    uy = (uk % _SHIFT).astype(np.float64) * cell_y
    return ux, uy, out_v, cell_x, cell_y


def _render_heatmap_svg(
    out_path: Path,
    *,
    title: str,
    subtitle: str,
    xlabel: str,
    ylabel: str,
    legend_title: str,
    xs: Sequence[int],
    ys: Sequence[int],
    values: Sequence[float],
    bin_size: int = BIN_SIZE,
    max_x: Optional[float] = None,
    max_y: Optional[float] = None,
    diverging: bool = False,
    vmin: float = 0.0,
    vmax: float = 1.0,
    high_color: Tuple[int, int, int] = (17, 24, 39),
    low_color: Tuple[int, int, int] = (37, 99, 235),
    empty_message: str = "No contacts to display",
) -> Path:
    width, height = 740, 650
    left, top, size = 84, 72, 490
    bar_x, bar_y, bar_w, bar_h = left + size + 28, top + 60, 18, 280

    x_arr = np.asarray(xs, dtype=np.float64)
    y_arr = np.asarray(ys, dtype=np.float64)
    v_arr = np.asarray(values, dtype=np.float64)

    dom_x = float(max_x) if max_x and max_x > 0 else (float(x_arr.max()) + bin_size if len(x_arr) else 100.0)
    dom_y = float(max_y) if max_y and max_y > 0 else (float(y_arr.max()) + bin_size if len(y_arr) else 100.0)
    if max_x is None and max_y is None:
        dom_x = dom_y = max(dom_x, dom_y)

    parts: List[str] = [
        '<?xml version="1.0" encoding="UTF-8"?>\n',
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}" width="{width}" height="{height}">\n',
        "<style>",
        "text{font-family:system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;fill:#111827}",
        ".title{font-size:15px;font-weight:700}",
        ".sub{font-size:12px;fill:#4b5563}",
        ".axis{font-size:12px;font-weight:600}",
        ".tick{font-size:10px;fill:#374151}",
        "</style>\n",
        f'<rect width="{width}" height="{height}" fill="#ffffff"/>\n',
        f'<text x="{left}" y="30" class="title">{escape(title)}</text>\n',
        f'<text x="{left}" y="49" class="sub">{escape(subtitle)}</text>\n',
        f'<rect x="{left}" y="{top}" width="{size}" height="{size}" fill="#ffffff" stroke="#d1d5db" stroke-width="1"/>\n',
    ]

    # Gridlines & ticks
    for tick in _nice_ticks(0.0, dom_x, 5):
        px = left + (tick / dom_x) * size
        parts.append(f'<line x1="{px:.1f}" y1="{top}" x2="{px:.1f}" y2="{top + size}" stroke="#f3f4f6" stroke-width="1"/>\n')
        parts.append(f'<line x1="{px:.1f}" y1="{top + size}" x2="{px:.1f}" y2="{top + size + 5}" stroke="#374151" stroke-width="1"/>\n')
        parts.append(f'<text x="{px:.1f}" y="{top + size + 18}" text-anchor="middle" class="tick">{escape(_fmt_tick(tick))}</text>\n')

    for tick in _nice_ticks(0.0, dom_y, 5):
        py = top + size - (tick / dom_y) * size
        parts.append(f'<line x1="{left}" y1="{py:.1f}" x2="{left + size}" y2="{py:.1f}" stroke="#f3f4f6" stroke-width="1"/>\n')
        parts.append(f'<line x1="{left - 5}" y1="{py:.1f}" x2="{left}" y2="{py:.1f}" stroke="#374151" stroke-width="1"/>\n')
        parts.append(f'<text x="{left - 9}" y="{py + 3:.1f}" text-anchor="end" class="tick">{escape(_fmt_tick(tick))}</text>\n')

    if len(x_arr):
        dx, dy, dv, cw, ch = _downsample_heatmap(x_arr, y_arr, v_arr, bin_size, dom_x, dom_y, diverging=diverging)
        rw = max(1.2, (cw / dom_x) * size)
        rh = max(1.2, (ch / dom_y) * size)
        white = (255, 255, 255)
        parts.append('<g shape-rendering="crispEdges">\n')
        if diverging:
            bound = max(abs(vmin), abs(vmax), 1e-9)
            for xv, yv, vv in zip(dx.tolist(), dy.tolist(), dv.tolist()):
                if vv >= 0:
                    col = _lerp_color(white, high_color, min(1.0, vv / bound))
                else:
                    col = _lerp_color(white, low_color, min(1.0, -vv / bound))
                px = left + (xv / dom_x) * size
                py = top + size - ((yv + ch) / dom_y) * size
                parts.append(f'<rect x="{px:.1f}" y="{py:.1f}" width="{rw:.1f}" height="{rh:.1f}" fill="{col}"/>\n')
        else:
            top_v = vmax if vmax > 0 else (float(dv.max()) if len(dv) else 1.0)
            for xv, yv, vv in zip(dx.tolist(), dy.tolist(), dv.tolist()):
                t = min(1.0, max(0.0, vv / top_v)) if top_v > 0 else 0.0
                col = _lerp_color(white, high_color, t)
                px = left + (xv / dom_x) * size
                py = top + size - ((yv + ch) / dom_y) * size
                parts.append(f'<rect x="{px:.1f}" y="{py:.1f}" width="{rw:.1f}" height="{rh:.1f}" fill="{col}"/>\n')
        parts.append("</g>\n")
    else:
        parts.append(
            f'<text x="{left + size / 2:.1f}" y="{top + size / 2:.1f}" text-anchor="middle" '
            f'class="sub">{escape(empty_message)}</text>\n'
        )

    # Plot border on top of tiles
    parts.append(f'<rect x="{left}" y="{top}" width="{size}" height="{size}" fill="none" stroke="#374151" stroke-width="1"/>\n')

    # Axis titles
    parts.append(f'<text x="{left + size / 2:.1f}" y="{top + size + 42}" text-anchor="middle" class="axis">{escape(xlabel)}</text>\n')
    parts.append(
        f'<text x="24" y="{top + size / 2:.1f}" text-anchor="middle" class="axis" '
        f'transform="rotate(-90 24 {top + size / 2:.1f})">{escape(ylabel)}</text>\n'
    )

    # Colorbar legend
    grad_id = "grad_legend"
    high_hex = f"#{high_color[0]:02x}{high_color[1]:02x}{high_color[2]:02x}"
    low_hex = f"#{low_color[0]:02x}{low_color[1]:02x}{low_color[2]:02x}"
    parts.append("<defs>\n")
    parts.append(f'<linearGradient id="{grad_id}" x1="0" y1="0" x2="0" y2="1">\n')
    if diverging:
        parts.append(f'<stop offset="0%" stop-color="{high_hex}"/>\n')
        parts.append('<stop offset="50%" stop-color="#ffffff"/>\n')
        parts.append(f'<stop offset="100%" stop-color="{low_hex}"/>\n')
    else:
        parts.append(f'<stop offset="0%" stop-color="{high_hex}"/>\n')
        parts.append('<stop offset="100%" stop-color="#ffffff"/>\n')
    parts.append("</linearGradient>\n</defs>\n")
    parts.append(
        f'<rect x="{bar_x}" y="{bar_y}" width="{bar_w}" height="{bar_h}" '
        f'fill="url(#{grad_id})" stroke="#374151" stroke-width="1"/>\n'
    )
    parts.append(f'<text x="{bar_x}" y="{bar_y - 12}" class="tick" font-weight="600">{escape(legend_title)}</text>\n')
    if diverging:
        bound = max(abs(vmin), abs(vmax))
        labels = [
            (bar_y + 4, f"+{_fmt_tick(bound)}"),
            (bar_y + bar_h / 2 + 4, "0"),
            (bar_y + bar_h + 3, f"-{_fmt_tick(bound)}"),
        ]
    else:
        top_v = vmax if vmax > 0 else (float(v_arr.max()) if len(v_arr) else 0.0)
        labels = [
            (bar_y + 4, _fmt_tick(top_v)),
            (bar_y + bar_h / 2 + 4, _fmt_tick(top_v / 2.0)),
            (bar_y + bar_h + 3, "0"),
        ]
    for ly, text in labels:
        parts.append(f'<text x="{bar_x + bar_w + 6}" y="{ly:.1f}" class="tick">{escape(text)}</text>\n')

    parts.append("</svg>\n")
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text("".join(parts), encoding="utf-8")
    return out_path


def _render_viewpoint_svg(
    out_path: Path,
    *,
    title: str,
    subtitle: str,
    ylabel: str,
    starts: Sequence[int],
    ends: Sequence[int],
    values: Sequence[float],
    x_min: int,
    x_max: Optional[int],
) -> Path:
    width, height = 840, 420
    left, top, plot_w, plot_h = 78, 66, 720, 285

    s_arr = np.asarray(starts, dtype=np.float64)
    e_arr = np.asarray(ends, dtype=np.float64)
    v_arr = np.asarray(values, dtype=np.float64)
    lo = float(x_min)
    hi = float(x_max) if x_max is not None and x_max >= x_min else (float(e_arr.max()) if len(e_arr) else lo + 100.0)
    if hi <= lo:
        hi = lo + 1.0
    max_y = float(v_arr.max()) if len(v_arr) and float(v_arr.max()) > 0 else 1.0

    parts: List[str] = [
        '<?xml version="1.0" encoding="UTF-8"?>\n',
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}" width="{width}" height="{height}">\n',
        "<style>",
        "text{font-family:system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;fill:#111827}",
        ".title{font-size:15px;font-weight:700}",
        ".sub{font-size:12px;fill:#4b5563}",
        ".axis{font-size:12px;font-weight:600}",
        ".tick{font-size:10px;fill:#374151}",
        "</style>\n",
        f'<rect width="{width}" height="{height}" fill="#ffffff"/>\n',
        f'<text x="{left}" y="28" class="title">{escape(title)}</text>\n',
        f'<text x="{left}" y="47" class="sub">{escape(subtitle)}</text>\n',
        f'<rect x="{left}" y="{top}" width="{plot_w}" height="{plot_h}" fill="#ffffff" stroke="#d1d5db" stroke-width="1"/>\n',
    ]

    for tick in _nice_ticks(lo, hi, 6):
        px = left + ((tick - lo) / (hi - lo)) * plot_w
        parts.append(f'<line x1="{px:.1f}" y1="{top}" x2="{px:.1f}" y2="{top + plot_h}" stroke="#f3f4f6" stroke-width="1"/>\n')
        parts.append(f'<line x1="{px:.1f}" y1="{top + plot_h}" x2="{px:.1f}" y2="{top + plot_h + 5}" stroke="#374151" stroke-width="1"/>\n')
        parts.append(f'<text x="{px:.1f}" y="{top + plot_h + 18}" text-anchor="middle" class="tick">{escape(_fmt_tick(tick))}</text>\n')

    for tick in _nice_ticks(0.0, max_y, 5):
        py = top + plot_h - (tick / max_y) * plot_h
        parts.append(f'<line x1="{left}" y1="{py:.1f}" x2="{left + plot_w}" y2="{py:.1f}" stroke="#f3f4f6" stroke-width="1"/>\n')
        parts.append(f'<line x1="{left - 5}" y1="{py:.1f}" x2="{left}" y2="{py:.1f}" stroke="#374151" stroke-width="1"/>\n')
        parts.append(f'<text x="{left - 9}" y="{py + 3:.1f}" text-anchor="end" class="tick">{escape(_fmt_tick(tick))}</text>\n')

    if len(v_arr) and float(v_arr.max()) > 0:
        if len(v_arr) > 900:
            step = int(math.ceil(len(v_arr) / 900))
            trimmed = len(v_arr) - (len(v_arr) % step)
            s_ds = s_arr[:trimmed].reshape(-1, step)[:, 0]
            e_ds = e_arr[:trimmed].reshape(-1, step)[:, -1]
            v_ds = v_arr[:trimmed].reshape(-1, step).max(axis=1)
            if trimmed < len(v_arr):
                s_ds = np.append(s_ds, s_arr[trimmed])
                e_ds = np.append(e_ds, e_arr[-1])
                v_ds = np.append(v_ds, float(v_arr[trimmed:].max()))
            s_arr, e_arr, v_arr = s_ds, e_ds, v_ds

        mids = 0.5 * (s_arr + e_arr)
        pts = [(left + ((m - lo) / (hi - lo)) * plot_w, top + plot_h - (v / max_y) * plot_h)
               for m, v in zip(mids.tolist(), v_arr.tolist())]
        baseline_y = top + plot_h
        poly = [f"{pts[0][0]:.1f},{baseline_y:.1f}"] + [f"{x:.1f},{y:.1f}" for x, y in pts] + [f"{pts[-1][0]:.1f},{baseline_y:.1f}"]
        line = [f"{x:.1f},{y:.1f}" for x, y in pts]
        parts.append(f'<polygon points="{" ".join(poly)}" fill="#64748b" fill-opacity="0.35"/>\n')
        parts.append(f'<polyline points="{" ".join(line)}" fill="none" stroke="#0f172a" stroke-width="1.5"/>\n')
    else:
        parts.append(
            f'<text x="{left + plot_w / 2:.1f}" y="{top + plot_h / 2:.1f}" text-anchor="middle" '
            f'class="sub">No viewpoint coverage in range</text>\n'
        )

    parts.append(f'<rect x="{left}" y="{top}" width="{plot_w}" height="{plot_h}" fill="none" stroke="#374151" stroke-width="1"/>\n')
    parts.append(f'<text x="{left + plot_w / 2:.1f}" y="{top + plot_h + 40}" text-anchor="middle" class="axis">Nucleotide position (nt)</text>\n')
    parts.append(
        f'<text x="22" y="{top + plot_h / 2:.1f}" text-anchor="middle" class="axis" '
        f'transform="rotate(-90 22 {top + plot_h / 2:.1f})">{escape(ylabel)}</text>\n'
    )
    parts.append("</svg>\n")
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text("".join(parts), encoding="utf-8")
    return out_path


# ---------------------------------------------------------------------------
# Subcommand runners
# ---------------------------------------------------------------------------

def run_compare(args: argparse.Namespace) -> int:
    if args.input is not None and (args.one or args.two):
        raise ValueError("pass either -i/--input or --one/--two, not both")
    if args.input is not None:
        entries = _parse_sample_table(args.input)
    else:
        if not args.one or not args.two:
            raise ValueError("pass -i/--input sample table OR both --one and --two")
        entries = [(None, path, one, None) if _is_contact_path(path) else (path, None, one, None)
                   for one, paths in ((True, args.one), (False, args.two)) for path in paths]

    # differential.compare's checks, made before anything is written
    if args.min_count < 0:
        raise ValueError("-m/--min-count must not be negative")
    if args.range < 0:
        raise ValueError("-r/--range must not be negative")
    if args.similarity_min < 1:
        raise ValueError("--similarity-min must be at least 1")
    if not 0 < args.padj <= 1:
        raise ValueError("--padj must be in (0, 1]")
    if not 0 < args.alpha <= 1:
        raise ValueError("--alpha must be in (0, 1]")
    if not 0 < args.quantile <= 1:
        raise ValueError("-q/--quantile must be in (0, 1]")

    names = _unique_names([hyb if hyb is not None else contact for hyb, contact, _, _ in entries])
    condition_one = [one for _, _, one, _ in entries]
    if all(condition_one) or not any(condition_one) or len(condition_one) < 3:
        raise ValueError("a comparison needs both conditions and at least three samples")
    # As the desktop's comparisons (desktop/runtime/compare.py): a sample's map
    # is of its own gene (-a overrides the table's gene column) and, with -b,
    # of that gene x -b.  A row's contact.txt is used when it is that map (no
    # -b, and no -a naming another gene than the row's) or when the row has
    # no .hyb; otherwise, or when it does not exist, the map is made from the
    # .hyb.
    plan = []
    for name, (hyb, contact, _, row_gene) in zip(names, entries):
        gene = args.gene or row_gene
        partner = args.gene2 or gene
        own_map = partner == gene and row_gene in (None, gene)
        if contact is not None and (hyb is None or (own_map and contact.is_file())):
            if not contact.is_file():
                raise ValueError(f"contact file not found: {contact}")
            plan.append((name, None, contact, gene, partner))
        elif not gene:
            missing = f"contact file not found: {contact}; " if contact is not None else ""
            raise ValueError(f"{missing}{name}: give the gene of its contact map (-a/--gene, or the fourth column "
                             "of the -i table) to make the map from the .hyb")
        elif not hyb.is_file():
            raise ValueError(f"HYB file not found: {hyb}")
        else:
            plan.append((name, hyb, None, gene, partner))

    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    contacts: List[Path] = []
    for name, hyb, contact, gene, partner in plan:
        if contact is not None:
            contacts.append(contact)
            continue
        tag = _safe_filename(gene if partner == gene else f"{gene}__{partner}")
        out_contact = out_dir / "contacts" / f"{_safe_filename(name)}.{tag}.contact.txt"
        if partner == gene:
            contacts.append(_build_contact_file(hyb, gene, out_contact))
            continue
        skipped: Dict[str, int] = {}
        xs, ys, counts, records = _hyb_contact_cells(hyb, gene, partner, skipped)
        _warn_skipped(hyb, skipped)
        if not records:
            raise ValueError(f"{name} has no {gene} x {partner} records: every cell would have a zero count in it, "
                             "so size factors cannot be computed; leave it out or choose another RNA pair")
        contacts.append(_write_contact_tsv(out_contact, gene, partner, xs.tolist(), ys.tolist(), counts.tolist()))

    summary = differential.compare(
        contacts,
        names,
        condition_one,
        out_dir,
        min_count=args.min_count,
        radius=args.range,
        alpha=args.alpha,
        padj_cutoff=args.padj,
        similarity_min=args.similarity_min,
    )

    sig = differential.read_significant(out_dir / "significant.tsv")
    scores = [
        max(-3.0, min(3.0, -math.log10(max(p, 1e-300)) * (1.0 if (lfc or 0.0) >= 0 else -1.0)))
        for lfc, p in zip(sig["log2FoldChange"], sig["padj"])
        if p is not None
    ]
    sig_x = [x for x, p in zip(sig["x"], sig["padj"]) if p is not None]
    sig_y = [y for y, p in zip(sig["y"], sig["padj"]) if p is not None]

    genes = sorted({gene for _, _, _, gene, _ in plan if gene})
    partners = sorted({partner for _, _, _, _, partner in plan if partner})
    axis_x = " / ".join(genes) + " (nt)" if genes else "Arm 1 (nt)"
    axis_y = " / ".join(partners) + " (nt)" if partners else "Arm 2 (nt)"
    max_coord = float(summary.get("max_coordinate", 0)) + BIN_SIZE
    label_one = args.label_one or "Condition 1"
    label_two = args.label_two or "Condition 2"

    _render_heatmap_svg(
        out_dir / "differential_map.svg",
        title=f"Differential Contact Map ({label_one} vs {label_two})",
        subtitle=(
            f"padj < {args.padj:g} | significant: {summary['significant']} "
            f"(+{summary['significant_up']} / -{summary['significant_down']}) | "
            f"interactions: {summary['interactions']}"
        ),
        xlabel=axis_x,
        ylabel=axis_y,
        legend_title="signed -log10(padj)",
        xs=sig_x,
        ys=sig_y,
        values=scores,
        bin_size=BIN_SIZE,
        max_x=max_coord if max_coord > BIN_SIZE else None,
        max_y=max_coord if max_coord > BIN_SIZE else None,
        diverging=True,
        vmin=-3.0,
        vmax=3.0,
        high_color=(220, 38, 38),
        low_color=(37, 99, 235),
        empty_message=f"No significant cells (padj < {args.padj:g})",
    )

    sim = differential.read_similarity(out_dir / "similarity.tsv")
    sim_vals = sim["count"]
    sim_cap = float(np.quantile(sim_vals, args.quantile)) if sim_vals else 0.0
    _render_heatmap_svg(
        out_dir / "similarity_heatmap.svg",
        title=f"Similarity Heatmap ({label_one} & {label_two})",
        subtitle=(
            f"shared cells: {len(sim_vals)} | min count: {args.similarity_min} | "
            f"{int(round(args.quantile * 100))}% cap: {_fmt_tick(sim_cap)}"
        ),
        xlabel=axis_x,
        ylabel=axis_y,
        legend_title="Min count",
        xs=sim["x"],
        ys=sim["y"],
        values=sim_vals,
        bin_size=BIN_SIZE,
        max_x=max_coord if max_coord > BIN_SIZE else None,
        max_y=max_coord if max_coord > BIN_SIZE else None,
        diverging=False,
        vmin=0.0,
        vmax=sim_cap,
        high_color=(34, 139, 34),
        empty_message="No shared contact cells",
    )

    print(str(out_dir))
    return 0


def _resolve_output_targets(out: Optional[Path], default_dir: Path, default_tsv_name: str, svg_name: str, tsv_exts: Tuple[str, ...]) -> Tuple[Path, Path]:
    """The table and SVG paths for ``-o`` (a directory, or a table file); the
    writers make the directory, so a failed run leaves none behind."""
    if out is None:
        out_dir = default_dir
        return out_dir / default_tsv_name, out_dir / svg_name
    if out.is_dir() or not out.name.lower().endswith(tsv_exts):
        return out / default_tsv_name, out / svg_name
    return out, out.parent / svg_name


def run_coverage(args: argparse.Namespace) -> int:
    src = Path(args.input)
    if not src.is_file():
        raise ValueError(f"input file not found: {src}")
    if not 1 <= args.bin_size <= 100_000:  # the bin sizes query.contact_bins takes
        raise ValueError("--bin-size must be from 1 to 100000")
    if not 0 < args.quantile <= 1:
        raise ValueError("-q/--quantile must be in (0, 1]")

    homodimer = None if args.homodimer == "all" else args.homodimer
    partner = args.gene2 or args.gene
    stem = _sample_stem(src)
    gene_tag = _safe_filename(args.gene if partner == args.gene else f"{args.gene}-{partner}")
    tsv_path, svg_path = _resolve_output_targets(
        args.out, src.parent, f"{stem}.{gene_tag}.contact.txt", "contact_map.svg", (".txt", ".tsv")
    )

    if _is_sqlite(src):
        try:
            data = query.contact_bins(
                [src],
                args.gene,
                bin_size=args.bin_size,
                gene2=args.gene2,
                measure=args.measure,
                orientation="both" if partner != args.gene else "oriented",
                fixed_bin=True,
                homodimer=homodimer,
            )
        finally:
            query.close_all()
        xs, ys, counts = data["x"], data["y"], data["count"]
        _write_contact_tsv(tsv_path, args.gene, partner, xs, ys, counts)
    else:
        if partner == args.gene and args.bin_size == BIN_SIZE and args.measure == "records" and homodimer is None:
            _build_contact_file(src, args.gene, tsv_path)
            keys, table = differential.count_table([tsv_path])
            xs = (keys // _SHIFT).tolist()
            ys = (keys % _SHIFT).tolist()
            counts = table[:, 0].tolist() if len(keys) else []
        else:
            skipped: Dict[str, int] = {}
            x_arr, y_arr, c_arr, _ = _hyb_contact_cells(
                src, args.gene, partner, skipped, bin_size=args.bin_size, measure=args.measure, homodimer=homodimer
            )
            _warn_skipped(src, skipped)
            xs, ys, counts = x_arr.tolist(), y_arr.tolist(), c_arr.tolist()
            _write_contact_tsv(tsv_path, args.gene, partner, xs, ys, counts)

    cap = float(np.quantile(counts, args.quantile)) if counts else 0.0
    total = int(sum(counts))
    pair_label = args.gene if partner == args.gene else f"{args.gene} x {partner}"
    _render_heatmap_svg(
        svg_path,
        title=f"Contact Density Map: {pair_label}",
        subtitle=(
            f"cells: {len(counts)} | total ({args.measure}): {total} | bin: {args.bin_size} nt | "
            f"{int(round(args.quantile * 100))}% cap: {_fmt_tick(cap)}"
        ),
        xlabel=f"{args.gene} (nt)",
        ylabel=f"{partner} (nt)",
        legend_title="Count",
        xs=xs,
        ys=ys,
        values=counts,
        bin_size=args.bin_size,
        diverging=False,
        vmin=0.0,
        vmax=cap,
        high_color=(17, 24, 39),
        empty_message=f"No contacts found for {pair_label}",
    )

    print(str(tsv_path))
    return 0


def run_viewpoint(args: argparse.Namespace) -> int:
    src = Path(args.input)
    if not src.is_file():
        raise ValueError(f"input file not found: {src}")
    if not 1 <= args.bin_size <= 100_000:  # the bin sizes query.viewpoint takes
        raise ValueError("--bin-size must be from 1 to 100000")
    if args.start is not None and args.start < 1:
        raise ValueError("--start must be at least 1")
    if args.end is not None and args.end < 1:
        raise ValueError("--end must be at least 1")
    if args.start is not None and args.end is not None and args.start > args.end:
        raise ValueError("--start must not exceed --end")

    lengths: Optional[Dict[str, int]] = None
    ref_len: Optional[int] = None
    if args.reference is not None:
        try:
            ref_len = _read_fasta_gene_length(args.reference, args.gene)
        except (EOFError, zlib.error) as error:  # a truncated or corrupt .gz
            raise ValueError(f"{args.reference}: {error}") from error
        if ref_len is None:
            raise ValueError(f"gene {args.gene!r} not found in reference {args.reference}")
        lengths = {args.gene: ref_len}

    homodimer = None if args.homodimer == "all" else args.homodimer
    stem = _sample_stem(src)
    gene_tag = _safe_filename(args.gene if not args.gene2 else f"{args.gene}_{args.gene2}")
    gplot_path, svg_path = _resolve_output_targets(
        args.out, src.parent, f"{stem}_{gene_tag}.gplot", "viewpoint.svg", (".gplot", ".tsv", ".txt")
    )

    # both paths: from --start (default 1) to --end, else the gene's length
    # in -d, else the gene's last arm end
    if _is_sqlite(src):
        try:
            data = query.viewpoint(
                [src],
                args.gene,
                start=args.start or 1,
                end=args.end if args.end is not None else ref_len,
                partner=args.gene2,
                measure=args.measure,
                bin_size=args.bin_size,
                lengths=lengths,
                homodimer=homodimer,
            )
        finally:
            query.close_all()
    else:
        skipped: Dict[str, int] = {}
        data = _hyb_viewpoint(
            src,
            args.gene,
            args.start,
            args.end,
            partner=args.gene2,
            measure=args.measure,
            bin_size=args.bin_size,
            homodimer=homodimer,
            ref_length=ref_len,
            skipped=skipped,
        )
        _warn_skipped(src, skipped)

    starts = data["bins"]["start"]
    ends = data["bins"]["end"]
    values = data["bins"]["value"]
    span_end = data["end"] or 0

    gplot_path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix="." + gplot_path.name + ".", suffix=".tmp", dir=str(gplot_path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(f"#{stem}_{gene_tag}_{span_end}\n")
            for pos, val in zip(starts, values):
                fval = float(val)
                val_str = str(int(round(fval))) if abs(fval - round(fval)) < 1e-9 else f"{fval:.4f}"
                handle.write(f"{int(pos)}\t{val_str}\n")
        os.replace(tmp, gplot_path)
    except BaseException:
        Path(tmp).unlink(missing_ok=True)
        raise

    pair_label = args.gene if not args.gene2 else f"{args.gene} (partner: {args.gene2})"
    _render_viewpoint_svg(
        svg_path,
        title=f"Viewpoint Coverage: {pair_label}",
        subtitle=(
            f"records: {data['records']} | bin: {data['bin_size']} nt | "
            f"peak ({args.measure}): {_fmt_tick(float(data['max']))}"
        ),
        ylabel=f"Coverage ({args.measure})",
        starts=starts,
        ends=ends,
        values=values,
        x_min=int(data["start"]),
        x_max=data["end"],
    )

    print(str(gplot_path))
    return 0


class _Parser(argparse.ArgumentParser):
    """Usage errors as one line on stderr, exit status 2 (subcommand parsers share the class)."""

    def error(self, message: str):
        self.exit(2, f"{self.prog}: error: {message} (see --help)\n")


def build_parser() -> argparse.ArgumentParser:
    parser = _Parser(
        prog="hyb2analysis",
        description="Pure-Python HYB2 analysis CLI (differential comparison, contact maps, viewpoints)",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_cmp = sub.add_parser("compare", help="Differential interaction analysis and SVG heatmaps")
    p_cmp.add_argument("-i", "--input", type=Path, default=None,
                       help="Sample table: <hyb or contact.txt> <condition>, or <hyb> <contact.txt or -> <condition> "
                            "[<gene>]; condition is condition_one or condition_two. A contact.txt that is missing, "
                            "or not the map asked for (-a naming another gene, or -b), is made from the .hyb")
    p_cmp.add_argument("--one", action="extend", nargs="+", type=Path, default=[],
                       help="Condition 1 .hyb or .contact.txt files")
    p_cmp.add_argument("--two", action="extend", nargs="+", type=Path, default=[],
                       help="Condition 2 .hyb or .contact.txt files")
    p_cmp.add_argument("-a", "--gene", default=None,
                       help="Gene of the contact maps made from .hyb inputs (overrides the -i table's gene column)")
    p_cmp.add_argument("-b", "--gene2", default=None, help="Optional second gene ID for inter-RNA maps")
    defaults = differential.DEFAULTS  # also the desktop's (desktop/runtime/compare.py)
    p_cmp.add_argument("-m", "--min-count", type=int, default=defaults["min_count"],
                       help="Cells are tested when their count over all samples exceeds this (default %(default)s)")
    p_cmp.add_argument("-r", "--range", type=int, default=defaults["range"],
                       help="Interaction merge radius in nt (default %(default)s)")
    p_cmp.add_argument("--alpha", type=float, default=defaults["alpha"],
                       help="DESeq2 independent filtering alpha (default %(default)s)")
    p_cmp.add_argument("--padj", type=float, default=defaults["padj_cutoff"],
                       help="Adjusted p-value cutoff (default %(default)s)")
    p_cmp.add_argument("--similarity-min", type=int, default=1, help="Minimum count per condition in similarity.tsv (default 1)")
    p_cmp.add_argument("-q", "--quantile", type=float, default=0.95, help="Color scale upper quantile for similarity SVG (default 0.95)")
    p_cmp.add_argument("--label-one", default=None, help="Display label for condition 1")
    p_cmp.add_argument("--label-two", default=None, help="Display label for condition 2")
    p_cmp.add_argument("-o", "--out-dir", type=Path, required=True, help="Output directory")
    p_cmp.set_defaults(func=run_compare)

    p_cov = sub.add_parser("coverage", help="Contact map TSV (.contact.txt) and contact_map.svg")
    p_cov.add_argument("-i", "--input", type=Path, required=True, help="Input .hyb(.gz) or .sqlite/.hyb2idx index")
    p_cov.add_argument("-a", "--gene", required=True, help="Target gene ID")
    p_cov.add_argument("-b", "--gene2", default=None, help="Optional second gene ID for inter-RNA maps")
    p_cov.add_argument("-o", "--out", type=Path, default=None, help="Output .contact.txt file or output directory")
    p_cov.add_argument("--bin-size", type=int, default=BIN_SIZE, help="Bin size in nt, 1 to 100000 (default 10)")
    p_cov.add_argument("-q", "--quantile", type=float, default=0.95, help="Color scale upper quantile (default 0.95)")
    p_cov.add_argument("--measure", choices=("records", "support", "reads"), default="records", help="Count weighting (default records)")
    p_cov.add_argument("--homodimer", choices=("all", "only", "exclude"), default="all", help="Homodimer handling (default all)")
    p_cov.set_defaults(func=run_coverage)

    p_vp = sub.add_parser("viewpoint", help="1D viewpoint coverage TSV (.gplot) and viewpoint.svg")
    p_vp.add_argument("-i", "--input", type=Path, required=True, help="Input .hyb(.gz) or .sqlite/.hyb2idx index")
    p_vp.add_argument("-a", "--gene", required=True, help="Viewpoint gene ID")
    p_vp.add_argument("-b", "--gene2", default=None, help="Optional partner gene ID")
    p_vp.add_argument("-d", "--reference", type=Path, default=None, help="Optional reference FASTA for gene length")
    p_vp.add_argument("--start", type=int, default=None, help="1-based start coordinate")
    p_vp.add_argument("--end", type=int, default=None, help="1-based end coordinate")
    p_vp.add_argument("--bin-size", type=int, default=1, help="Bin size in nt, 1 to 100000 (default 1)")
    p_vp.add_argument("--measure", choices=("records", "support", "reads"), default="records", help="Count weighting (default records)")
    p_vp.add_argument("--homodimer", choices=("all", "only", "exclude"), default="all", help="Homodimer handling (default all)")
    p_vp.add_argument("-o", "--out", type=Path, default=None, help="Output .gplot file or output directory")
    p_vp.set_defaults(func=run_viewpoint)

    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except (ValueError, OSError) as error:
        print("error: " + " ".join(str(error).splitlines()), file=sys.stderr)
        return 2
