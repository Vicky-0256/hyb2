// Included in the generated browser catalog for both the page and its workers.
// One prepared strand; length limits and pairing rules belong to the engine.
globalThis.Hyb2FoldText.readSequence = function (input, label = "RNA sequence") {
  const { sequence, invalid } = globalThis.Hyb2FoldText.read(input);
  if (invalid.length) throw new Error(label + " contains unsupported symbols: " + invalid.join(", "));
  return sequence;
};

// The raw input contract of reference.py: positive safe integers, or ASCII
// decimal digit text with optional surrounding ASCII whitespace. Check before
// Number can turn booleans/arrays or rounded decimal text into coordinates.
globalThis.Hyb2FoldText.coordinateOrNull = function (value) {
  if (typeof value === "string") {
    const text = value.replace(/^[ \t\r\n\f\v]+|[ \t\r\n\f\v]+$/g, "");
    if (!text || /[^0-9]/.test(text)) return null;
    value = Number(text);
  }
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 1 ? value : null;
};

// A physical ID is the target of a manual choice. An automatic HYB match
// uses the first header word *after* the file-wide pipeline substitutions.
(function (rules) {
  const headerOf = entry => entry.header ?? entry.id ?? "";
  // Python str.split(None, 1): Unicode White_Space plus the information separators.
  rules.fastaWord = header => header.match(/[^\p{White_Space}\x1c-\x1f]+/u)?.[0] || "";
  rules.formattedFasta = entries => entries.some(entry => entry.hyb2Formatted || headerOf(entry).includes(rules.fastaNames.trigger));
  const indexes = new WeakMap();
  rules.indexFasta = function (entries) {
    if (indexes.has(entries)) return indexes.get(entries);
    const formatted = rules.formattedFasta(entries);
    const byId = new Map(), byEffectiveId = new Map();
    for (const entry of entries) {
      let header = headerOf(entry);
      if (formatted) {
        for (const [old, replacement] of rules.fastaNames.replacements) header = header.replaceAll(old, replacement);
      }
      const name = rules.fastaWord(header);
      if (byEffectiveId.has(name)) throw new Error("Duplicate FASTA ID: " + name + ". Give each record a unique effective ID.");
      if (byId.has(entry.id)) throw new Error("Duplicate FASTA ID: " + entry.id + ". Manual targets must be unique.");
      byEffectiveId.set(name, entry);
      byId.set(entry.id, entry);
    }
    const index = {
      byId, byEffectiveId,
      find(rna, mapping) {
        if (!mapping) return byEffectiveId.get(rna) || null;
        const id = Object.hasOwn(mapping, rna) && mapping[rna]; // a physical record, not another HYB name
        return id && byId.get(id) || null;
      }
    };
    // Imported lists need not have the parser's immutable identity fields.
    if (Object.isFrozen(entries) && entries.every(e => ["id", "header", "hyb2Formatted"].every(
      key => Object.getOwnPropertyDescriptor(e, key)?.writable === false))) indexes.set(entries, index);
    return index;
  };
  rules.findFastaEntry = function (fasta, rna) {
    if (!fasta || !fasta.sequences) return null;
    return rules.indexFasta(fasta.sequences).find(rna, fasta.mapping);
  };
}(globalThis.Hyb2FoldText));

(function (rules) {
  function read(input, pasted, alphabet) {
    const invalid = new Set();
    let sequence = "", records = 0, removedNumbers = false;
    for (const line of String(input ?? "").split(/\r\n|[\r\n]/)) {
      let start = 0;
      while (start < line.length && rules.space.includes(line[start])) start++;
      if (pasted && line.startsWith(rules.header, start)) { records++; continue; }
      for (let base of line.slice(start)) {
        if (rules.space.includes(base)) continue;
        if (pasted && rules.numbers.includes(base)) { removedNumbers = true; continue; }
        if (base >= "a" && base <= "z") base = base.toUpperCase();
        base = rules.aliases[base] || base;
        sequence += base;
        if (!alphabet.includes(base)) invalid.add(base);
      }
    }
    return { sequence, records, removedNumbers, invalid: Array.from(invalid) };
  }
  rules.read = (input, pasted = false) => read(input, pasted, rules.alphabet);

  // One position per non-layout code point, 1-based and inclusive. Raw windows
  // preserve the entry's symbols; only folding normalizes them to RNA.
  const cache = new WeakMap();
  function bases(text) {
    return /[\uD800-\uDBFF]/.test(text) ? Array.from(text) : text;
  }
  function slice(bases, start, end) {
    const part = bases.slice(start - 1, end);
    return typeof part === "string" ? part : part.join("");
  }
  rules.reference = function (entry) {
    const raw = entry.sequence, alphabet = rules.alphabet, cached = cache.get(entry);
    if (cached && cached.raw === raw && cached.alphabet === alphabet) return cached.reference;
    const compact = String(raw ?? "").replace(new RegExp("[" + rules.space + "]+", "gu"), "");
    const rawBases = bases(compact);
    let folded;
    const reference = {
      length: rawBases.length,
      rawWindow(start, end) { return slice(rawBases, start, end); },
      window(start, end, cofold = false) {
        const region = entry.id + ":" + start + "-" + end;
        if (end > rawBases.length) throw new Error("Region " + region + " exceeds the mapped FASTA length of " + rawBases.length + " nt.");
        // Metadata and previews don't need a normalized copy of the reference.
        if (!folded) {
          const { sequence, invalid } = read(compact, false, alphabet);
          folded = { bases: sequence === compact ? rawBases : bases(sequence), invalid };
        }
        const text = slice(folded.bases, start, end), invalid = folded.invalid;
        if (invalid.length) {
          const used = cofold ? new Set(text) : null;
          const bad = used ? invalid.filter(base => used.has(base)) : invalid;
          const label = cofold ? "Selected region " + region : "The mapped FASTA sequence for " + entry.id;
          if (bad.length) throw new Error(label + " contains unsupported symbols: " + bad.join(", ") + ".");
        }
        return text;
      }
    };
    cache.set(entry, { raw, alphabet, reference });
    return reference;
  };
}(globalThis.Hyb2FoldText));
