# Vendored snapshot

- Repository: `https://github.com/Vicky-0256/CPLfold.git`
- Upstream branch: `feature/pseudoknot-free-mode`
- Upstream commit: `24bab521e0bcf2cff71f4567363aac1d5f5c97d5`
- Import method: squashed Git subtree
- Destination: `third_party/cplfold`

The snapshot contains Python sources and data tracked by upstream. It excludes
the upstream Git database, Python bytecode, Numba caches, shared libraries, and
other ignored local artifacts.

## Hyb2-local changes after import

- `CPLfold.py` treats a completed Phase-1-only prediction as CLI success.
- `CPLfold.py` supports explicit pseudoknot-free prediction through
  `--no-pseudoknot` and `allow_pseudoknot=False`.
- `tests/test_cli_exit_status.py` locks that exit-status behavior.
- `CPLfold.py` accepts Hyb2's sparse bonus-matrix TSV export through
  `--bonus-matrix-file` and applies it with `--alpha`.
- `LICENSES/`, `THIRD_PARTY_NOTICES.md`, and this file record provenance and
  distribution constraints; they do not supply a missing upstream license.

Upstream commit `24bab52` does not identify an exact LinearFold source revision
or the exact HotKnots 2.0 archive/checksum used to create the Python-derived
files. That missing derivation metadata must be resolved before treating this
snapshot as independently reproducible provenance.
