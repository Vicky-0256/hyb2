"""Queries over one or more analysis indexes (replicates are merged by summing).

Every function takes ``indexes``: a list of index paths.  Gene and type names
are resolved per index, so indexes built from different files merge by name.

Contact maps use the legacy ``plot_hybrids_3.awk USE_ENTIRE_HYBRIDS=1``
rule at the requested bin size: a hybrid adds one to every
``bin(arm 1) x bin(arm 2)`` cell its two arms cover, where
``bin(p) = bin_size * floor(p / bin_size)``.  At ``bin_size=10`` the cells
equal ``contact.txt`` exactly.
"""
from __future__ import annotations

import heapq
import json
import os
import sqlite3
import threading
from collections import OrderedDict
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Sequence, Tuple, Union

import numpy as np

from .index import FORMAT, RECT_COLUMNS, VERSION

PathLike = Union[str, "os.PathLike[str]"]

MAX_GRID_CELLS = 25_000_000
MAX_LIMIT = 10_000
_RECT_CACHE_BYTES = 512 * 1024 * 1024


class IndexError_(ValueError):
    """A missing, stale or foreign index."""


class _Index:
    def __init__(self, path: Path):
        self.path = path
        stat = path.stat()
        self.stamp = (stat.st_size, stat.st_mtime_ns)
        self.con = sqlite3.connect("file:%s?mode=ro" % path.as_posix(), uri=True, check_same_thread=False)
        try:
            self.meta = {k: json.loads(v) for k, v in self.con.execute("SELECT key, value FROM meta")}
        except sqlite3.DatabaseError as error:
            self.con.close()
            raise IndexError_("not an analysis index: %s (%s)" % (path, error))
        if self.meta.get("format") != FORMAT or self.meta.get("version") != VERSION:
            self.con.close()
            raise IndexError_("unsupported analysis index: %s" % path)
        self.genes = {name: (gid, max_end) for gid, name, max_end in self.con.execute("SELECT id, name, max_end FROM genes")}
        self.gene_names = {gid: name for name, (gid, _) in self.genes.items()}
        self.types = {name: tid for tid, name in self.con.execute("SELECT id, name FROM types")}
        self.type_names = {tid: name for name, tid in self.types.items()}
        self.spans = {(g1, g2): (s1, s2) for g1, g2, s1, s2 in self.con.execute("SELECT g1, g2, max_span1, max_span2 FROM pairs")}

    def gene(self, name: str) -> Optional[int]:
        entry = self.genes.get(name)
        return entry[0] if entry else None


_lock = threading.Lock()
_open: Dict[str, _Index] = {}
_rects: "OrderedDict[Tuple[str, Tuple[int, int], int, int], Dict[str, np.ndarray]]" = OrderedDict()
_rect_bytes = 0


def _get(path: PathLike) -> _Index:
    key = os.path.abspath(os.fspath(path))
    p = Path(key)
    if not p.is_file():
        raise IndexError_("analysis index not found: %s" % key)
    stat = p.stat()
    with _lock:
        index = _open.get(key)
        if index is not None and index.stamp == (stat.st_size, stat.st_mtime_ns):
            return index
        if index is not None:
            index.con.close()
        index = _open[key] = _Index(p)
        return index


def _indexes(indexes: Sequence[PathLike]) -> List[_Index]:
    if isinstance(indexes, (str, os.PathLike)):
        indexes = [indexes]
    if not indexes:
        raise ValueError("at least one analysis index is required")
    return [_get(path) for path in indexes]


def close_all() -> None:
    global _rect_bytes
    with _lock:
        for index in _open.values():
            index.con.close()
        _open.clear()
        _rects.clear()
        _rect_bytes = 0


# --------------------------------------------------------------------------
# summary


def _single_summary(index: _Index) -> Dict[str, Any]:
    con = index.con
    genes = [{"name": n, "length": m, "records": r, "reads": rd}
             for n, m, r, rd in con.execute("SELECT name, max_end, records, reads FROM genes ORDER BY records DESC, name")]
    types = [{"name": n, "records": r, "reads": rd}
             for n, r, rd in con.execute("SELECT name, records, reads FROM types ORDER BY records DESC, name")]
    pairs = [{"gene1": index.gene_names[g1], "gene2": index.gene_names[g2], "records": r, "reads": rd}
             for g1, g2, r, rd in con.execute("SELECT g1, g2, records, reads FROM pairs ORDER BY records DESC")]
    meta = index.meta
    return {
        "index": str(index.path), "source": meta.get("source_path"), "built_at": meta.get("built_at"),
        "records": meta["records"], "reads": meta["reads"], "records_with_reads": meta["records_with_reads"],
        "with_dg": meta["with_dg"], "intra_records": meta["intra_records"], "inter_records": meta["inter_records"],
        "invalid": meta["invalid"], "skipped": meta["skipped"], "errors": meta.get("errors", []),
        "genes": genes, "types": types, "pairs": pairs,
    }


def _merge_named(lists: Iterable[List[Dict[str, Any]]], keys: Sequence[str], sums: Sequence[str],
                 maxes: Sequence[str] = ()) -> List[Dict[str, Any]]:
    merged: Dict[tuple, Dict[str, Any]] = {}
    for items in lists:
        for item in items:
            key = tuple(item[k] for k in keys)
            entry = merged.get(key)
            if entry is None:
                merged[key] = dict(item)
                continue
            for name in sums:
                entry[name] += item[name]
            for name in maxes:
                entry[name] = max(entry[name], item[name])
    return sorted(merged.values(), key=lambda item: (-item["records"],) + tuple(item[k] for k in keys))


def summary(indexes: Sequence[PathLike]) -> Dict[str, Any]:
    """Per-index summaries plus a merged total (``merged``)."""
    parts = [_single_summary(index) for index in _indexes(indexes)]
    merged = {name: sum(part[name] for part in parts)
              for name in ("records", "reads", "records_with_reads", "with_dg", "intra_records", "inter_records", "invalid", "skipped")}
    merged["genes"] = _merge_named((p["genes"] for p in parts), ("name",), ("records", "reads"), ("length",))
    merged["types"] = _merge_named((p["types"] for p in parts), ("name",), ("records", "reads"))
    merged["pairs"] = _merge_named((p["pairs"] for p in parts), ("gene1", "gene2"), ("records", "reads"))
    return {"indexes": parts, "merged": merged}


# --------------------------------------------------------------------------
# contact bins


def _load_rects(index: _Index, g1: int, g2: int) -> Optional[Dict[str, np.ndarray]]:
    global _rect_bytes
    key = (str(index.path), (g1, g2)) + index.stamp
    with _lock:
        cached = _rects.get(key)
        if cached is not None:
            _rects.move_to_end(key)
            return cached
    row = index.con.execute("SELECT n, dtypes, %s FROM rects WHERE g1 = ? AND g2 = ?" % ", ".join(RECT_COLUMNS), (g1, g2)).fetchone()
    if row is None:
        return None
    dtypes = json.loads(row[1])
    arrays = {name: np.frombuffer(blob, dtype=dtypes[name]) for name, blob in zip(RECT_COLUMNS, row[2:])}
    size = sum(array.nbytes for array in arrays.values())
    with _lock:
        _rects[key] = arrays
        _rect_bytes += size
        while _rect_bytes > _RECT_CACHE_BYTES and len(_rects) > 1:
            _, old = _rects.popitem(last=False)
            _rect_bytes -= sum(array.nbytes for array in old.values())
    return arrays


def _region(region: Any) -> Optional[Tuple[int, int, int, int]]:
    """``{"x": [start, end], "y": [start, end]}`` (nt, inclusive); either may be omitted."""
    if region is None:
        return None
    if not isinstance(region, dict):
        raise ValueError("region must be an object with x and/or y [start, end]")
    out = []
    for axis in ("x", "y"):
        span = region.get(axis)
        if span is None:
            out.extend((0, None))
            continue
        if (not isinstance(span, (list, tuple)) or len(span) != 2
                or any(isinstance(v, bool) or not isinstance(v, (int, float)) for v in span)):
            raise ValueError("region.%s must be [start, end]" % axis)
        start, end = int(span[0]), int(span[1])
        if start > end:
            raise ValueError("region.%s start must not exceed end" % axis)
        out.extend((max(0, start), end))
    return tuple(out)  # type: ignore[return-value]


def _type_mask(index: _Index, rects: Dict[str, np.ndarray], types: Optional[Sequence[str]]) -> Optional[np.ndarray]:
    if not types:
        return None
    wanted = [index.types[name] for name in types if name in index.types]
    return np.isin(rects["type"], np.asarray(wanted, dtype=rects["type"].dtype))


def _rasterise(parts: List[Tuple[np.ndarray, ...]], bin_size: int, bounds: Tuple[int, int, int, int]):
    """Sum rectangles into a dense grid (2-D difference array + cumulative sums)."""
    bx0, bx1, by0, by1 = bounds
    nx, ny = bx1 - bx0 + 1, by1 - by0 + 1
    if nx <= 0 or ny <= 0:
        return np.zeros((0, 0), dtype=np.int64)
    if nx * ny > MAX_GRID_CELLS:
        raise ValueError("contact map would need %d cells; increase bin_size or narrow the region" % (nx * ny))
    width = ny + 1
    corners, weights = [], []
    for s1, e1, s2, e2, weight in parts:
        a = np.clip(s1 // bin_size, bx0, None) - bx0
        b = np.clip(e1 // bin_size, None, bx1) - bx0 + 1
        e = np.clip(s2 // bin_size, by0, None) - by0
        f = np.clip(e2 // bin_size, None, by1) - by0 + 1
        w = weight.astype(np.float64)
        corners += [a * width + e, a * width + f, b * width + e, b * width + f]
        weights += [w, -w, -w, w]
    diff = np.bincount(np.concatenate(corners), weights=np.concatenate(weights), minlength=(nx + 1) * width)
    del corners, weights
    grid = np.rint(diff).astype(np.int64).reshape(nx + 1, width)
    del diff
    np.cumsum(grid, axis=0, out=grid)
    np.cumsum(grid, axis=1, out=grid)
    return grid[:nx, :ny]


def _contact_grid(indexes: List[_Index], gene: str, gene2: Optional[str], bin_size: int, region,
                  types: Optional[Sequence[str]], measure: str):
    """Per-index rectangle selections and the merged grid."""
    if isinstance(bin_size, bool) or not isinstance(bin_size, int) or not 1 <= bin_size <= 100_000:
        raise ValueError("bin_size must be an integer between 1 and 100000")
    if measure not in ("records", "reads"):
        raise ValueError("measure must be 'records' or 'reads'")
    gene2 = gene2 or gene
    bounds_nt = _region(region)
    selections = []
    length_x = length_y = 0
    for index in indexes:
        g1, g2 = index.gene(gene), index.gene(gene2)
        if g1 is not None:
            length_x = max(length_x, index.genes[gene][1])
        if g2 is not None:
            length_y = max(length_y, index.genes[gene2][1])
        rects = _load_rects(index, g1, g2) if g1 is not None and g2 is not None else None
        if rects is None:
            selections.append(None)
            continue
        s1, e1, s2, e2 = (rects[k].astype(np.int64) for k in ("s1", "e1", "s2", "e2"))
        weight = rects["count" if measure == "records" else "reads"].astype(np.int64)
        mask = _type_mask(index, rects, types)
        if bounds_nt is not None:
            x0, x1, y0, y1 = bounds_nt
            # keep rectangles that reach the region's bins
            keep = np.ones(len(s1), dtype=bool)
            keep &= e1 // bin_size >= x0 // bin_size
            if x1 is not None:
                keep &= s1 // bin_size <= x1 // bin_size
            keep &= e2 // bin_size >= y0 // bin_size
            if y1 is not None:
                keep &= s2 // bin_size <= y1 // bin_size
            mask = keep if mask is None else (mask & keep)
        if mask is not None:
            s1, e1, s2, e2, weight = s1[mask], e1[mask], s2[mask], e2[mask], weight[mask]
        selections.append((s1, e1, s2, e2, weight))
    present = [sel for sel in selections if sel is not None and len(sel[0])]
    if bounds_nt is not None:
        x0, x1, y0, y1 = bounds_nt
        bx0, by0 = x0 // bin_size, y0 // bin_size
        bx1 = (x1 if x1 is not None else max([int(sel[1].max()) for sel in present] or [x0])) // bin_size
        by1 = (y1 if y1 is not None else max([int(sel[3].max()) for sel in present] or [y0])) // bin_size
    else:
        bx0 = by0 = 0
        bx1 = max([int(sel[1].max()) for sel in present] or [0]) // bin_size
        by1 = max([int(sel[3].max()) for sel in present] or [0]) // bin_size
    return selections, (bx0, bx1, by0, by1), (length_x, length_y)


def _cells(grid: np.ndarray, bounds, bin_size: int):
    ix, iy = np.nonzero(grid)
    return (ix + bounds[0]) * bin_size, (iy + bounds[2]) * bin_size, grid[ix, iy]


def contact_bins(indexes: Sequence[PathLike], gene: str, bin_size: int = 10, region: Any = None, *,
                 gene2: Optional[str] = None, types: Optional[Sequence[str]] = None, measure: str = "records",
                 max_cells: Optional[int] = None) -> Dict[str, Any]:
    """Non-empty contact-map cells for ``gene`` (x = arm 1, y = arm 2).

    Returns ``{gene, gene2, bin_size, requested_bin_size, adjusted, measure,
    length, length2, region, cells, total, max, x: [], y: [], count: []}``
    where ``x``/``y`` are bin starts in nt (row-major order) and ``total`` is
    the summed weight of the hybrids drawn.  With ``max_cells`` the bin size
    is doubled until the map has at most that many non-empty cells.
    """
    opened = _indexes(indexes)
    requested = bin_size
    while True:
        selections, bounds, lengths = _contact_grid(opened, gene, gene2, bin_size, region, types, measure)
        parts = [sel for sel in selections if sel is not None and len(sel[0])]
        grid = _rasterise(parts, bin_size, bounds) if parts else np.zeros((0, 0), dtype=np.int64)
        cells = int(np.count_nonzero(grid))
        if max_cells is None or cells <= max_cells or bin_size >= 100_000:
            break
        bin_size *= 2
    x, y, count = _cells(grid, bounds, bin_size)
    return {
        "gene": gene, "gene2": gene2 or gene, "bin_size": bin_size, "requested_bin_size": requested,
        "adjusted": bin_size != requested, "measure": measure, "length": lengths[0], "length2": lengths[1],
        "region": region, "cells": cells, "total": int(sum(int(sel[4].sum()) for sel in parts)),
        "max": int(count.max()) if len(count) else 0,
        "x": x.tolist(), "y": y.tolist(), "count": count.tolist(),
    }


def top_interactions(indexes: Sequence[PathLike], gene: str, bin_size: int = 10, n: int = 50, *,
                     gene2: Optional[str] = None, region: Any = None, types: Optional[Sequence[str]] = None,
                     measure: str = "records") -> Dict[str, Any]:
    """The ``n`` strongest contact cells, with the per-index counts behind each.

    Ties are broken by position (x, then y).  Each row is
    ``{x, y, x_end, y_end, count, per_index: [..]}``.
    """
    if isinstance(n, bool) or not isinstance(n, int) or not 1 <= n <= MAX_LIMIT:
        raise ValueError("n must be an integer between 1 and %d" % MAX_LIMIT)
    opened = _indexes(indexes)
    selections, bounds, lengths = _contact_grid(opened, gene, gene2, bin_size, region, types, measure)
    grids = [(_rasterise([sel], bin_size, bounds) if sel is not None and len(sel[0]) else None) for sel in selections]
    shape = (bounds[1] - bounds[0] + 1, bounds[3] - bounds[2] + 1)
    total = np.zeros(shape, dtype=np.int64)
    for grid in grids:
        if grid is not None:
            total += grid
    flat = total.reshape(-1)
    nonzero = np.flatnonzero(flat)
    if len(nonzero) > n:
        # partial selection, then exact ordering of the candidates (ties included)
        threshold = np.partition(flat[nonzero], len(nonzero) - n)[len(nonzero) - n]
        nonzero = nonzero[flat[nonzero] >= threshold]
    order = nonzero[np.lexsort((nonzero, -flat[nonzero]))][:n]
    rows = []
    for cell in order.tolist():
        ix, iy = divmod(cell, shape[1])
        x, y = (ix + bounds[0]) * bin_size, (iy + bounds[2]) * bin_size
        rows.append({"x": x, "y": y, "x_end": x + bin_size - 1, "y_end": y + bin_size - 1, "count": int(flat[cell]),
                     "per_index": [int(g[ix, iy]) if g is not None else 0 for g in grids]})
    return {"gene": gene, "gene2": gene2 or gene, "bin_size": bin_size, "measure": measure,
            "length": lengths[0], "length2": lengths[1], "indexes": [str(i.path) for i in opened], "rows": rows}


# --------------------------------------------------------------------------
# hybrids


_ORDERS = {
    "file": ("id", False),
    "reads": ("reads", True),
    "position": ("s1", False),
    "dg": ("dg", False),
}


def _filter_sql(index: _Index, filters: Dict[str, Any]) -> Optional[Tuple[str, list]]:
    """WHERE clause for one index; ``None`` when the filter cannot match."""
    clauses: List[str] = []
    params: list = []
    g1 = g2 = None
    if filters.get("gene1") is not None:
        g1 = index.gene(str(filters["gene1"]))
        if g1 is None:
            return None
        clauses.append("g1 = ?")
        params.append(g1)
    if filters.get("gene2") is not None:
        g2 = index.gene(str(filters["gene2"]))
        if g2 is None:
            return None
        clauses.append("g2 = ?")
        params.append(g2)
    if filters.get("gene") is not None:
        g = index.gene(str(filters["gene"]))
        if g is None:
            return None
        clauses.append("(g1 = ? OR g2 = ?)")
        params.extend((g, g))
    if filters.get("intra") is not None:
        clauses.append("g1 = g2" if filters["intra"] else "g1 != g2")
    region = _region(filters.get("region"))
    if region is not None:
        x0, x1, y0, y1 = region
        if filters.get("region", {}).get("x") is not None:
            clauses.append("e1 >= ? AND s1 <= ?")
            params.extend((x0, x1))
            # bound the start so SQLite can range-scan the (g1, g2, s1) index
            if g1 is not None and g2 is not None:
                span = index.spans.get((g1, g2), (0, 0))[0]
                clauses.append("s1 >= ?")
                params.append(x0 - span)
        if filters.get("region", {}).get("y") is not None:
            clauses.append("e2 >= ? AND s2 <= ?")
            params.extend((y0, y1))
    types = filters.get("types")
    if types:
        if not isinstance(types, (list, tuple)):
            raise ValueError("types must be a list of chimera type names")
        ids = [index.types[t] for t in types if t in index.types]
        if not ids:
            return None
        clauses.append("type IN (%s)" % ",".join("?" * len(ids)))
        params.extend(ids)
    for key, column, op in (("min_reads", "reads", ">="), ("max_reads", "reads", "<="),
                            ("min_dg", "dg", ">="), ("max_dg", "dg", "<=")):
        value = filters.get(key)
        if value is not None:
            if isinstance(value, bool) or not isinstance(value, (int, float)):
                raise ValueError("%s must be a number" % key)
            clauses.append("%s %s ?" % (column, op))
            params.append(value)
    return (" WHERE " + " AND ".join(clauses) if clauses else ""), params


def _read_lines(source: Optional[str], meta: Dict[str, Any], offsets: List[int]) -> Dict[int, Tuple[str, str]]:
    """(name, sequence) for rows, read back from the .hyb when it is unchanged."""
    if not source or not offsets:
        return {}
    try:
        stat = os.stat(source)
        if stat.st_size != meta.get("source_size") or stat.st_mtime_ns != meta.get("source_mtime_ns"):
            return {}
        out = {}
        with open(source, "rb") as handle:
            for offset in sorted(set(offsets)):
                handle.seek(offset)
                cols = handle.readline().rstrip(b"\r\n").split(b"\t")
                out[offset] = (cols[0].decode("utf-8", "replace"), cols[1].decode("utf-8", "replace") if len(cols) > 1 else "")
        return out
    except OSError:
        return {}


_ROW_COLUMN = {"id": 0, "s1": 2, "reads": 7, "dg": 8}


def _merge_key(row: tuple, column: str, descending: bool, number: int) -> tuple:
    """Sort key matching each index's SQL ORDER BY, extended across indexes."""
    if column == "id":
        return (number, row[0])
    value = row[_ROW_COLUMN[column]]
    if value is None:
        return (True, 0, number, row[0])
    return (False, -value if descending else value, number, row[0])


def hybrids(indexes: Sequence[PathLike], filters: Optional[Dict[str, Any]] = None, limit: int = 100, offset: int = 0, *,
            order: str = "file", with_sequence: bool = True) -> Dict[str, Any]:
    """Filtered hybrid rows across indexes.

    ``filters``: ``gene1``, ``gene2``, ``gene`` (either arm), ``intra`` (bool),
    ``region`` (``{"x": [s, e], "y": [s, e]}``, overlap with arm 1 / arm 2),
    ``types`` (list), ``min_reads``/``max_reads``, ``min_dg``/``max_dg``.
    ``order``: ``file`` (index order, then file order), ``reads`` (desc),
    ``position`` (arm 1 start), ``dg`` (asc, missing last).
    Returns ``{total, limit, offset, order, rows: [{index, id, name, sequence,
    gene1, start1, end1, gene2, start2, end2, reads, dg, overlap, type}]}``.
    """
    filters = dict(filters or {})
    if isinstance(limit, bool) or not isinstance(limit, int) or not 0 <= limit <= MAX_LIMIT:
        raise ValueError("limit must be an integer between 0 and %d" % MAX_LIMIT)
    if isinstance(offset, bool) or not isinstance(offset, int) or offset < 0:
        raise ValueError("offset must be a non-negative integer")
    if order not in _ORDERS:
        raise ValueError("order must be one of %s" % ", ".join(sorted(_ORDERS)))
    column, descending = _ORDERS[order]
    opened = _indexes(indexes)
    total = 0
    streams = []
    for number, index in enumerate(opened):
        where = _filter_sql(index, filters)
        if where is None:
            continue
        clause, params = where
        total += index.con.execute("SELECT count(*) FROM hybrids" + clause, params).fetchone()[0]
        if limit == 0:
            continue
        sort = "%s IS NULL, %s %s, id" % (column, column, "DESC" if descending else "ASC")
        rows = index.con.execute(
            "SELECT id, g1, s1, e1, g2, s2, e2, reads, dg, overlap, type, offset FROM hybrids%s ORDER BY %s LIMIT ?"
            % (clause, sort), params + [offset + limit]).fetchall()
        streams.append([(_merge_key(row, column, descending, number), number, row) for row in rows])
    merged = list(heapq.merge(*streams, key=lambda item: item[0]))[offset:offset + limit] if streams else []
    lookups: Dict[int, Dict[int, Tuple[str, str]]] = {}
    if with_sequence:
        for number, index in enumerate(opened):
            offs = [row[11] for _, n, row in merged if n == number]
            lookups[number] = _read_lines(index.meta.get("source_path"), index.meta, offs)
    out = []
    for _, number, row in merged:
        index = opened[number]
        name, sequence = lookups.get(number, {}).get(row[11], (None, None))
        out.append({"index": number, "id": row[0], "name": name, "sequence": sequence,
                    "gene1": index.gene_names[row[1]], "start1": row[2], "end1": row[3],
                    "gene2": index.gene_names[row[4]], "start2": row[5], "end2": row[6],
                    "reads": row[7], "dg": row[8], "overlap": row[9], "type": index.type_names.get(row[10])})
    return {"total": total, "limit": limit, "offset": offset, "order": order, "rows": out}
