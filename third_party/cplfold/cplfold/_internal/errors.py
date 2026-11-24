"""The input error of every public cplfold function; standard library only.

It lives here, free of NumPy, so that :mod:`cplfold` and :mod:`cplfold.sequence`
can raise it without loading the folding code.
"""


class InputError(ValueError):
    """Invalid argument, sequence, file contents, or input magnitude.

    Raised by :func:`cplfold.fold` and by the functions of :mod:`cplfold.sequence`
    and :mod:`cplfold.bonus`. File access errors stay ``OSError``.
    """


InputError.__module__ = "cplfold"  # its public name
