"""External evidence worker for HYB2 Desktop (RNAcentral MCP client).

Contract: desktop/spec/EVIDENCE.md.  The worker is a separate process that
speaks HYB2's newline JSON to Go and the official MCP Python SDK (v1) to one
fixed RNAcentral MCP server child over stdio.  It receives public query
fields only and never reads project files.
"""

ADAPTER_VERSION = "hyb2-evidence 1.0"
SCHEMA_VERSION = "1.0"
PROVIDER = "RNAcentral"

__all__ = ["ADAPTER_VERSION", "SCHEMA_VERSION", "PROVIDER"]
