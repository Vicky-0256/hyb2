"""The bundled example data set: the web's "Try example" (web/example.js).

ZIKV virion RNA, African lineage (ZAFR) vs Asian lineage (ZASI = ZILM,
French Polynesian isolate), two replicates each: 1-in-10 subsets of the
replicate .hyb files, gzip-compressed, each lineage on its own genome, with
the NC_035889 GenBank gene track.  The files ship with the runtime under
``web/assets/examples/zikv-virion/`` (desktop/scripts/stage-native.py) and
are *copied* into the project, so it stays self-contained and portable.
"""
from __future__ import annotations

from pathlib import Path
from typing import Any, Dict

from .project import load_manifest
from . import samples as _samples

EXAMPLE_DIR = Path("web") / "assets" / "examples" / "zikv-virion"
TITLE = "ZIKV virion: ZAFR vs ZASI"
DESCRIPTION = ("ZIKV virion RNA of the African lineage (ZAFR, replicates 1 and 2) and the Asian lineage "
               "(ZASI = ZILM, French Polynesian isolate, replicates 2 and 3): every 10th record of each "
               "replicate .hyb, each on its own genome. Because the data are a subset, the results "
               "reproduce the method, not the published numbers.")

# (file, label) per reference; the conditions are web/example.js's labels A and B.
REFERENCES = (("ZAFR.fasta", "ZAFR"), ("ZILM.fasta", "ZASI"))
ANNOTATION = "NC_035889.gb"
# (file, sample name, condition, replicate, reference label)
SAMPLES = (
    ("ZAFR_REP1_1in10.hyb.gz", "ZAFR_REP1", "ZAFR", 1, "ZAFR"),
    ("ZAFR_REP2_1in10.hyb.gz", "ZAFR_REP2", "ZAFR", 2, "ZAFR"),
    ("ZILM_REP2_1in10.hyb.gz", "ZILM_REP2", "ZASI", 2, "ZASI"),
    ("ZILM_REP3_1in10.hyb.gz", "ZILM_REP3", "ZASI", 3, "ZASI"),
)


def example_directory(repo_root: Path) -> Path:
    """The example files under the runtime (or repository) root."""
    folder = Path(repo_root).resolve() / EXAMPLE_DIR
    names = [name for name, _ in REFERENCES] + [ANNOTATION] + [item[0] for item in SAMPLES]
    missing = [name for name in names if not (folder / name).is_file()]
    if missing:
        raise ValueError("the example data set is not bundled with this runtime (missing %s)" % ", ".join(missing))
    return folder


def add_example_data(root: Path, repo_root: Path) -> Dict[str, Any]:
    """Copy the example references, gene track and samples into an empty project."""
    from .annotation import set_reference_annotation

    folder = example_directory(repo_root)
    manifest = load_manifest(root)
    if manifest.get("references") or manifest.get("samples"):
        raise ValueError("the example data set goes into a project without references or samples; start a new project for it")
    references: Dict[str, Dict[str, Any]] = {}
    for name, label in REFERENCES:
        reference = _samples.add_reference(root, folder / name, label=label, mode="copied")["reference"]
        references[label] = set_reference_annotation(root, reference["id"], folder / ANNOTATION)
    added = []
    for name, sample_name, condition, replicate, reference in SAMPLES:
        added.append(_samples.add_sample(root, folder / name, name=sample_name, condition=condition, replicate=replicate,
                                         reference_id=references[reference]["id"], mode="copied"))
    return {"title": TITLE, "description": DESCRIPTION, "references": list(references.values()), "samples": added}
