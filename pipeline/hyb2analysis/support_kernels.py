"""Selectable HYB support kernels. Keep stable IDs separate from display names.

Each option also names its B_ij transform: ``bonus`` is the kind written to
bonus-matrix exports and ``transform`` the rule recorded in results.
``resultLabel`` is the short name used in result summaries.
"""

DEFAULT_SUPPORT_KERNEL = "gaussian"
_SATURATING = ("{} cross-arm pairs, C = observed pair counts; symmetric; "
               "s C / (C + q), s = 3.6 kcal/mol, q = mean C of the n/3 best-supported pairs")
KERNEL_OPTIONS = (
    dict(value="gaussian", label="Gaussian around arm centres (default)", desktopLabel="Gaussian around arm centres · IRIS (default)",
         resultLabel="Gaussian arm support", bonus="log1p_hyb_bonus",
         transform="IRIS Gaussian arm blocks; symmetric float64 accumulation; threshold 2^-23; log1p"),
    dict(value="block", label="Full arm blocks",
         resultLabel="full arm blocks", bonus="log1p_hyb_bonus",
         transform="Full arm blocks, w per cell of arm one x arm two; symmetric; threshold 1e-6; log1p"),
    dict(value="block-normalized", label="Full arm blocks ÷ √(arm 1 length × arm 2 length)", desktopLabel="Full arm blocks ÷ √(arm lengths)",
         resultLabel="full arm blocks ÷ √(arm lengths)", bonus="log1p_hyb_bonus",
         transform="Full arm blocks, w / sqrt(len one x len two) per cell; symmetric; threshold 1e-6; log1p"),
    dict(value="duplex", label="RNAduplex inter-arm pairs · saturating bonus", desktopLabel="RNAduplex pairs, saturating", pairModel="RNAduplex",
         resultLabel="RNAduplex pairs (saturating)", bonus="saturating_hyb_bonus", transform=_SATURATING.format("RNAduplex")),
    dict(value="comrades", label="COMRADES (RNAcofold inter-arm pairs) · saturating bonus", desktopLabel="COMRADES (RNAcofold), saturating", pairModel="RNAcofold",
         resultLabel="COMRADES (RNAcofold, saturating)", bonus="saturating_hyb_bonus", transform=_SATURATING.format("RNAcofold")),
)
SUPPORT_KERNELS = tuple(option["value"] for option in KERNEL_OPTIONS)
PAIR_MODELS = {option["value"]: option["pairModel"] for option in KERNEL_OPTIONS if option.get("pairModel")}
SUPPORT_TRANSFORMS = {option["value"]: option["transform"] for option in KERNEL_OPTIONS}

# COMRADES score of a predicted structure: each endpoint of a pair adds the
# pair's raw count C, capped (Lau's bp_score.sh passes -m 1000000 to
# make_VARNA_scores_2.sh). The cap does not affect bonuses or drawing colours.
COMRADES_SCORE = dict(endpointCap=1_000_000, endpointsPerPair=2)


def comrades_pair_score(count):
    return COMRADES_SCORE["endpointsPerPair"] * min(count, COMRADES_SCORE["endpointCap"])
