"""RNA sequence text and two-strand coordinates accepted by :func:`cplfold.fold`.

``cutpoint`` is the length of strand A / zero-based index of the first B base.
All arrays and pair coordinates omit the display separator ``&``.
"""

from numbers import Integral
from types import MappingProxyType
from typing import Optional, Tuple

from ._internal.cli_input import read_sequence_file  # raises OSError or cplfold.CofoldInputError

__all__ = ["ALPHABET", "ALIASES", "WHITESPACE", "clean", "normalise_sequence",
           "add_strand_break", "strip_structure_break", "read_sequence_file"]

ALPHABET = "ACGU"
ALIASES = MappingProxyType({"T": "U"})  # read-only: clean() is built from it at import
# Unicode White_Space plus the byte-order mark; removed anywhere in a sequence.
WHITESPACE = (" \t\n\r\v\f\u0085\u00a0\u1680" + "".join(chr(n) for n in range(0x2000, 0x200b))
              + "\u2028\u2029\u202f\u205f\u3000\ufeff")

_TRANSLATION = {ord(c): ALIASES.get(c.upper(), c.upper()) for c in "abcdefghijklmnopqrstuvwxyz" + "".join(ALIASES)}
_TRANSLATION.update(dict.fromkeys(map(ord, WHITESPACE)))


def clean(text: str) -> str:
    """Upper-case ``text``, apply ``ALIASES`` (T to U) and drop ``WHITESPACE``; no validation."""
    if not isinstance(text, str):
        raise ValueError("sequence must be text")
    return text.translate(_TRANSLATION)


def normalise_sequence(sequence: str, cutpoint: Optional[int] = None) -> Tuple[str, Optional[int]]:
    if not isinstance(sequence, str):
        raise ValueError("RNA sequence must be a string")
    sequence = clean(sequence)
    if sequence.count("&") > 1:
        raise ValueError("Cofold accepts exactly two strands separated by one '&'")
    if cutpoint is not None and (isinstance(cutpoint, bool) or not isinstance(cutpoint, Integral)):
        raise ValueError("cutpoint must be an integer equal to the length of strand A")
    if "&" in sequence:
        boundary = sequence.index("&")
        if cutpoint is not None and cutpoint != boundary:
            raise ValueError("cutpoint does not match the '&' strand separator")
        cutpoint = boundary
        sequence = sequence.replace("&", "")
    if not sequence:
        raise ValueError("RNA sequence must not be empty")
    invalid = sorted(set(sequence) - set(ALPHABET))
    if invalid:
        raise ValueError("RNA sequence accepts only A, C, G and U (T is converted to U)")
    if cutpoint is not None:
        cutpoint = int(cutpoint)
        if not 0 < cutpoint < len(sequence):
            raise ValueError("cutpoint must separate two nonempty RNA strands")
    return sequence, cutpoint


def add_strand_break(text: str, cutpoint: Optional[int]) -> str:
    """Format an already compact sequence, structure, or constraint."""
    return text if cutpoint is None else text[:cutpoint] + "&" + text[cutpoint:]


def strip_structure_break(structure: str, cutpoint: Optional[int], length: int) -> str:
    if not isinstance(structure, str):
        raise ValueError("Structure must be a string")
    if "&" in structure:
        if structure.count("&") != 1 or cutpoint is None or structure.index("&") != cutpoint:
            raise ValueError("Structure strand separator does not match cutpoint")
        structure = structure.replace("&", "")
    if len(structure) != length:
        raise ValueError("Sequence and structure lengths differ (strand separators are not bases)")
    return structure
