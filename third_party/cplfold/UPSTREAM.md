# Vendored snapshot

- Repository: `https://github.com/Vicky-0256/CPLfold.git`
- Upstream branch: `feature/standalone-pseudoknot-energy`
- Upstream commit: `af49f8ea3177adba089fa480d49259ec1ba14d50`
- Import method: squashed Git subtree
- Destination: `third_party/cplfold`

The snapshot contains Python sources and data tracked by upstream. It excludes
the upstream Git database, Python bytecode, Numba caches, shared libraries, and
other ignored local artifacts.

## Hyb2-local changes after import

- `CPLfold.py` treats a completed Phase-1-only prediction as CLI success.
- `tests/test_cli_exit_status.py` locks that exit-status behavior.
- `LICENSES/`, `THIRD_PARTY_NOTICES.md`, and this file record provenance and
  distribution constraints; they do not supply a missing upstream license.

Upstream commit `af49f8e` does not identify an exact LinearFold source revision
or the exact HotKnots 2.0 archive/checksum used to create the Python-derived
files. That missing derivation metadata must be resolved before treating this
snapshot as independently reproducible provenance.
