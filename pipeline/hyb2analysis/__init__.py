"""Analysis backend for .hyb files (contact maps, index, queries).

* :func:`coverage.write_contact` / :func:`coverage.write_for_table`: the
  ``hyb2_coverage`` contact.txt and the ``hyb2_compare`` forTable, byte-for-byte.
* :func:`index.build`: a per-sample SQLite index of a .hyb file.
* :mod:`query`: ``summary``, ``contact_bins``, ``hybrids``, ``top_interactions``
  over one or more indexes (replicates merged by summing), and
  ``compare_bins`` for two conditions on one shared bin grid.

Requires numpy; the import of ``hyb2pipe.awk`` expects ``pipeline/`` on sys.path.
"""
from importlib import import_module

_FUNCTIONS = {"write_contact": "coverage", "write_for_table": "coverage", "build": "index",
              **dict.fromkeys(("summary", "contact_bins", "compare_bins", "hybrids", "top_interactions"), "query")}
__all__ = ["coverage", "index", "query", *_FUNCTIONS]
__version__ = "0.1.0"


def __getattr__(name):
    # Preserve the analysis API without making the IRIS-only CLI ship the
    # unrelated contact-map and SQLite backends just to import its HYB reader.
    if name not in __all__:
        raise AttributeError(name)
    module = import_module("." + _FUNCTIONS.get(name, name), __name__)
    return getattr(module, name) if name in _FUNCTIONS else module
