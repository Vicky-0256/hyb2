// Included with the generated catalog so the page and worker resolve the same task settings.
globalThis.Hyb2CplfoldSettings = (function () {
  function engine(cofold) {
    return globalThis.Hyb2EngineParameters[cofold ? "cplcofold" : "cplfold"];
  }

  function topology(cofold, value = engine(cofold).defaults.allowPseudoknot) {
    return typeof value === "boolean" ? value : null;
  }

  function readTopology(cofold, value) {
    const enabled = topology(cofold, value);
    if (enabled === null) throw new TypeError("allowPseudoknot must be a boolean.");
    return enabled;
  }

  function readEnergyModel(cofold, value) {
    const { models, defaultModel } = engine(cofold);
    const model = String(value || defaultModel).toUpperCase();
    if (!models.some(option => option.value === model)) {
      throw new Error("Choose a supported " + (cofold ? "CPLcofold" : "CPLfold") + " energy model: " + models.map(option => option.value).join(", ") + ".");
    }
    return model;
  }

  function readKernel(value) {
    const { options, defaultKernel } = globalThis.Hyb2SupportKernels;
    const kernel = value == null || value === "" ? defaultKernel : value;
    if (!options.some(option => option.value === kernel)) {
      throw new Error("Choose a supported CPLfold read-support kernel: " + options.map(option => option.value).join(", ") + ".");
    }
    return kernel;
  }

  // An invalid saved form has neither radio selected and can still be corrected.
  function formTopology(form) {
    return topology(form?.cplfoldMode === "cofold", form?.cplfoldAllowPseudoknot);
  }

  function valueOr(value, fallback) {
    return value == null || value === "" ? fallback : String(value);
  }

  function profileKey(form = {}) {
    const cofold = form.cplfoldMode === "cofold", { defaults, defaultModel } = engine(cofold);
    const pk = formTopology(form);
    return [cofold ? "cofold" : "single", valueOr(form.cplfoldEvidence, "hyb-blocks"),
      form.cplfoldEvidence === "none" ? "none" : valueOr(form.cplfoldKernel, globalThis.Hyb2SupportKernels.defaultKernel),
      valueOr(form.cplfoldBeam, defaults.beamSize), valueOr(form.cplfoldMaxPhase1, defaults.maxPhase1),
      valueOr(form.cplfoldEnergyDelta, defaults.energyDelta), valueOr(form.cplfoldEnergyModel, defaultModel).toUpperCase(),
      valueOr(form.cplfoldAlpha, defaults.alpha), valueOr(form.cplfoldBeta, defaults.beta),
      pk === null ? "invalid" : pk ? "pseudoknot" : "nested"].join("|");
  }

  // cplfold.cli accepts an unpruned beam (0) and has no browser search-budget caps.
  function localInteger(flag, value, minimum) {
    const integer = Number.isInteger(value) || (typeof value === "string" && /^[+-]?\d+$/.test(value.trim()))
      ? BigInt(value) : null;
    if (integer === null || integer < minimum) throw new Error(flag + " must be an integer >= " + minimum + ".");
    return flag + " " + integer;
  }

  function localFloat(flag, value, nonnegative) {
    const number = typeof value === "string" && /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i.test(value.trim())
      ? Number(value) : value;
    if (!Number.isFinite(number)) throw new Error(flag + " must be finite.");
    if (nonnegative && number < 0) throw new Error(flag + " must be nonnegative.");
    const token = String(number);
    // argparse can mistake a separate negative exponent-form value for another flag.
    return flag + (token.startsWith("-") && token.includes("e") ? "=" : " ") + token;
  }

  function localCommand(form = {}, guided) {
    const cofold = form.cplfoldMode === "cofold", { defaults } = engine(cofold);
    const { cplfoldBeam: beam = defaults.beamSize, cplfoldEnergyDelta: delta = defaults.energyDelta,
      cplfoldMaxPhase1: phase1 = defaults.maxPhase1, cplfoldAlpha: alpha = defaults.alpha, cplfoldBeta: beta = defaults.beta } = form;
    const pk = readTopology(cofold, form.cplfoldAllowPseudoknot);
    const command = [cofold ? "bin/cplcofold" : "bin/cplfold", "--sequence-file", "cplfold-input.fasta"];
    if (cofold) command.push("--mode", pk ? "pseudoknot" : "standard");
    if (guided) {
      readKernel(form.cplfoldKernel);
      command.push("--bonus-matrix-file", "cplfold-bonus-matrix.tsv");
    }
    command.push(localInteger("--beam", beam, 0), localFloat("--delta", delta, true), localInteger("--max-phase1", phase1, 1),
      "--max-phase2", "1", "--model", readEnergyModel(cofold, form.cplfoldEnergyModel), localFloat("--beta", beta, cofold));
    if (guided) command.push(localFloat("--alpha", alpha, cofold));
    if (!pk) command.push("--no-pseudoknot");
    return command.join(" ");
  }

  return { readTopology, readEnergyModel, readKernel, formTopology, profileKey, localCommand };
}());
