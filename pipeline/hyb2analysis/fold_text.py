"""How Hyb2 reads sequence text from its sequence boxes, HYB records and windows.

The engine's own rules (alphabet, aliases, whitespace) come from
:mod:`cplfold.sequence`. Hyb2 adds paste boxes that also accept one FASTA
header and position numbers, never coordinate-bearing records or prepared
worker requests. ``FOLD_TEXT`` is exported to the browser and desktop UIs
(``scripts/export-support-kernels.py``) so they read text the same way.
"""

from cplfold.sequence import ALIASES, ALPHABET, WHITESPACE, clean

FOLD_TEXT = {
    "alphabet": ALPHABET,
    "aliases": dict(ALIASES),
    "numbers": "0123456789",
    "header": ">",
    "space": WHITESPACE,
}
_NUMBERS = dict.fromkeys(map(ord, FOLD_TEXT["numbers"]))


def fold_text(text: str, *, pasted=False) -> str:
    if not isinstance(text, str):
        raise ValueError("sequence must be text")
    if pasted:
        body = []
        headers = 0
        for line in text.replace("\r\n", "\n").replace("\r", "\n").split("\n"):
            if line.lstrip(FOLD_TEXT["space"]).startswith(FOLD_TEXT["header"]):
                headers += 1
                if headers > 1:
                    raise ValueError("paste only one FASTA record per sequence box")
            else:
                body.append(line)
        text = "".join(body).translate(_NUMBERS)
    return clean(text)


def unsupported_bases(sequence: str):
    """Characters outside the shared alphabet in already normalized fold input."""
    return set(sequence).difference(FOLD_TEXT["alphabet"])


def checked_fold_text(text: str, label: str = "RNA sequence") -> str:
    """Read one prepared strand. The engine owns length limits and pairing rules."""
    sequence = fold_text(text)
    bad = unsupported_bases(sequence)
    if bad:
        raise ValueError(f"{label} contains unsupported symbols: " + ", ".join(sorted(bad)))
    return sequence
