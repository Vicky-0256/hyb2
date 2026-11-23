"""Selectable HYB support kernels. Keep stable IDs separate from display names."""

DEFAULT_SUPPORT_KERNEL = "gaussian"
KERNEL_OPTIONS = (
    dict(value="gaussian", label="Gaussian around arm centres (default)", desktopLabel="Gaussian around arm centres · IRIS (default)"),
    dict(value="block", label="Full arm blocks"),
    dict(value="block-normalized", label="Full arm blocks ÷ √(arm 1 length × arm 2 length)", desktopLabel="Full arm blocks ÷ √(arm lengths)"),
    dict(value="duplex", label="RNAduplex inter-arm pairs · saturating bonus", desktopLabel="RNAduplex pairs, saturating", pairModel="RNAduplex"),
    dict(value="comrades", label="COMRADES (RNAcofold inter-arm pairs) · saturating bonus", desktopLabel="COMRADES (RNAcofold), saturating", pairModel="RNAcofold"),
)
SUPPORT_KERNELS = tuple(option["value"] for option in KERNEL_OPTIONS)
PAIR_MODELS = {option["value"]: option["pairModel"] for option in KERNEL_OPTIONS if option.get("pairModel")}
