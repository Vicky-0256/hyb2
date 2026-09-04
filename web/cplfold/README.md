# Browser CPLfold bridge

`cplfold_web.py` adapts the vendored pure-Python CPLfold source to a small JSON
API used by `cplfold.worker.mjs`. It also reproduces CPLfold's IRIS-style
HYB/PARIS interval-to-bonus transform directly from the prepared HYB arms.

The browser runtime is intentionally conservative:

- Pyodide 0.29.4, Python 3.13.2 and NumPy 2.2.5 are pinned by
  `scripts/build-cplfold-web.sh`.
- Pyodide does not provide Numba. The bridge replaces `numba.njit` with an
  identity decorator, so the same Python algorithm runs without JIT.
- The UI and bridge therefore enforce a 75-nt maximum. Longer production
  workloads should use the local `bin/cplfold` command.
- Build output is generated into `web/vendor/pyodide-cplfold/` and is not
  committed. The Python source bundle has a content-addressed filename and is
  checked against its SHA-256 manifest in the worker before import. Increment
  `BRIDGE_VERSION` in the bridge, worker and build manifest whenever that
  packaged interface changes. GitHub Actions publishes the same-origin assets.

The CPLfold/LinearFold/HotKnots licensing boundary remains documented in
`third_party/cplfold/THIRD_PARTY_NOTICES.md`. Resolve the upstream CPLfold
licence before a public redistribution or GitHub Pages release.
