# VARNA upstream

- Source: https://github.com/yannponty/VARNA, `master` at commit `85a3ddccfb` (2023-11-30)
- Version: 3.9 (`VARNAConfig.MAJOR_VERSION` and `MINOR_VERSION`)
- Licence: GPL-3.0 (see `LICENSE`)
- Imported into Hyb2 on 2024-10-11 (commit `9a38875`), and moved from `VARNA/` to `third_party/varna/` in 2026-11.

Hyb2 does not modify VARNA. The source here matches upstream except that six files in `src/fr/orsay/lri/varna/models/export/` (`Backbone*Command.java`, `Base*Command.java`, `BasePair*Command.java`) are re-encoded from ISO-8859-1 to UTF-8; their text is the same.

`build/jar/VARNA.jar` and `build/jar/VARNAcmd.jar` are built from this source with `ant jar` (Apache Ant 1.9.4, OpenJDK 1.8.0_412). The original `bin/` scripts drew structures with `VARNAcmd.jar`. Since they were removed in November 2026, nothing in Hyb2 runs the jars; the desktop runtime only reports whether both are present.
