"""RNA sequence text and two-strand coordinates accepted by :func:`cplfold.fold`.

``cutpoint`` is the length of strand A / zero-based index of the first B base.
All arrays and pair coordinates omit the display separator ``&``.
"""

from numbers import Integral
from types import MappingProxyType
from typing import Optional, Tuple

from ._internal.cli_input import read_sequence_file  # raises OSError or cplfold.InputError
from ._internal.errors import InputError

__all__ = ["ALPHABET", "ALIASES", "WHITESPACE", "clean", "normalise_sequence",
           "add_strand_break", "read_sequence_file"]

ALPHABET = "ACGU"
ALIASES = MappingProxyType({"T": "U"})  # read-only: clean() is built from it at import
# Unicode White_Space plus the byte-order mark; removed anywhere in a sequence.
WHITESPACE = (" \t\n\r\v\f\u0085\u00a0\u1680" + "".join(chr(n) for n in range(0x2000, 0x200b))
              + "\u2028\u2029\u202f\u205f\u3000\ufeff")

_TRANSLATION = {ord(c): ALIASES.get(c.upper(), c.upper()) for c in "abcdefghijklmnopqrstuvwxyz" + "".join(ALIASES)}
_TRANSLATION.update(dict.fromkeys(map(ord, WHITESPACE)))


def clean(text: str) -> str:
    """Upper-case ``text``, apply ``ALIASES`` (T to U) and drop ``WHITESPACE``.

    Only the type is checked: non-text raises :class:`cplfold.InputError`.
    """
    if not isinstance(text, str):
        raise InputError("sequence must be text")
    return text.translate(_TRANSLATION)


def normalise_sequence(sequence: str, cutpoint: Optional[int] = None) -> Tuple[str, Optional[int]]:
    """Return the compact sequence (:func:`clean`, no ``&``) and the cutpoint of its two strands.

    One ``&`` gives the cutpoint; ``cutpoint`` may give it for a compact sequence
    instead (both must agree). One strand returns ``None``. Invalid text raises
    :class:`cplfold.InputError`.
    """
    if not isinstance(sequence, str):
        raise InputError("RNA sequence must be a string")
    sequence = clean(sequence)
    if sequence.count("&") > 1:
        raise InputError("Cofold accepts exactly two strands separated by one '&'")
    if cutpoint is not None and (isinstance(cutpoint, bool) or not isinstance(cutpoint, Integral)):
        raise InputError("cutpoint must be an integer equal to the length of strand A")
    if "&" in sequence:
        boundary = sequence.index("&")
        if cutpoint is not None and cutpoint != boundary:
            raise InputError("cutpoint does not match the '&' strand separator")
        cutpoint = boundary
        sequence = sequence.replace("&", "")
    if not sequence:
        raise InputError("RNA sequence must not be empty")
    invalid = sorted(set(sequence) - set(ALPHABET))
    if invalid:
        raise InputError("RNA sequence accepts only A, C, G and U (T is converted to U)")
    if cutpoint is not None:
        cutpoint = int(cutpoint)
        if not 0 < cutpoint < len(sequence):
            raise InputError("cutpoint must separate two nonempty RNA strands")
    return sequence, cutpoint


def add_strand_break(text: str, cutpoint: Optional[int]) -> str:
    """Format an already compact sequence, structure, or constraint: ``&`` at ``cutpoint``.

    ``cutpoint=None`` returns ``text`` unchanged. A cutpoint that does not
    separate two nonempty parts raises :class:`cplfold.InputError`.
    """
    if not isinstance(text, str):
        raise InputError("add_strand_break needs text")
    if cutpoint is None:
        return text
    if isinstance(cutpoint, bool) or not isinstance(cutpoint, Integral) or not 0 < cutpoint < len(text):
        raise InputError("cutpoint must separate two nonempty strands")
    return text[:cutpoint] + "&" + text[cutpoint:]


def _strip_structure_break(structure: str, cutpoint: Optional[int], length: int) -> str:
    if not isinstance(structure, str):
        raise InputError("Structure must be a string")
    if "&" in structure:
        if structure.count("&") != 1 or cutpoint is None or structure.index("&") != cutpoint:
            raise InputError("Structure strand separator does not match cutpoint")
        structure = structure.replace("&", "")
    if len(structure) != length:
        raise InputError("Sequence and structure lengths differ (strand separators are not bases)")
    return structure
