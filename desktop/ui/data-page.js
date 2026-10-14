/*
 * ① Data: references and the sample table. Import raw files or a sample
 * sheet, run / resume selected samples (with a disk check first), check and
 * relink inputs.
 */
(function () {
  "use strict";

  var HD = window.HD;
  var h = HD.h;
  var state = HD.state;
  var root = null;
  var selected = new Set();
  var inputStatus = Object.create(null);   // sample/reference id -> check_inputs item

  function stage(sample, name) {
    return (sample.stages && sample.stages[name]) || { status: "pending" };
  }
  function sampleDone(sample) {
    return stage(sample, "index").status === "succeeded";
  }
  function sampleProblem(sample) {
    var names = ["map", "hyb", "index"];
    for (var i = 0; i < names.length; i += 1) {
      var item = stage(sample, names[i]);
      if (item.status === "failed" || item.status === "interrupted" || item.status === "cancelled") return item;
      // A stage left "running" by a task that is no longer active was cut
      // off (application quit or crash): treat it as interrupted.
      if (item.status === "running" || item.status === "queued") {
        var task = item.task_id ? state.tasks[item.task_id] : null;
        if (!task || !HD.isActiveTask(task)) return Object.assign({}, item, { status: "interrupted" });
      }
    }
    return null;
  }

  function render(container) {
    root = container;
    draw();
  }

  function draw() {
    if (!root) return;
    HD.clear(root);
    root.appendChild(h("div", { className: "d-page-head" }, [
      h("div", {}, [
        h("h1", { className: "d-h1", text: "Data" }),
        h("p", { className: "d-muted", text: "References and samples of this project. Raw files are linked in place unless copied." })
      ]),
      h("div", { className: "d-actions" }, [
        h("button", { type: "button", className: "d-btn", text: "Check inputs", on: { click: checkInputs } }),
        h("button", { type: "button", className: "d-btn", text: "Import sample sheet…", on: { click: importSheet } }),
        h("button", { type: "button", className: "d-btn d-btn-primary", text: "Import raw files…", on: { click: importRaw } })
      ])
    ]));
    root.appendChild(renderReferences());
    root.appendChild(renderSamples());
  }

  // ---------------------------------------------------------------- references

  function renderReferences() {
    var card = h("section", { className: "d-card" });
    card.appendChild(h("div", { className: "d-card-head" }, [
      h("h2", { className: "d-h2", text: "References" }),
      h("button", { type: "button", className: "d-btn d-btn-small", text: "Add reference…", on: { click: addReference } })
    ]));
    if (!state.references.length) {
      card.appendChild(h("p", { className: "d-muted", text: "No reference yet. Add the FASTA the reads were (or will be) mapped to; sample sheets add theirs automatically." }));
      return card;
    }
    var rows = state.references.map(function (reference) {
      var check = inputStatus["reference:" + reference.id];
      var sequences = reference.sequences || [];
      return h("tr", {}, [
        h("td", {}, [h("strong", { text: reference.label || reference.id })]),
        h("td", { className: "d-wrap", title: reference.path, text: HD.basename(reference.path) }),
        h("td", { text: sequences.length === 1 ? sequences[0].name + " · " + HD.formatNumber(sequences[0].length) + " nt" : sequences.length + " sequences" }),
        h("td", {}, [h("span", { className: "d-badge", text: reference.mode || "linked" }), check && check.status !== "ok" ? HD.chip("failed", check.status) : null]),
        h("td", {}, check && check.status !== "ok" ? h("button", { type: "button", className: "d-btn d-btn-small", text: "Relink…", on: { click: function () { relink(check); } } }) : null)
      ]);
    });
    card.appendChild(h("div", { className: "d-table-wrap" }, h("table", { className: "d-table" }, [
      h("thead", {}, h("tr", {}, ["Reference", "File", "Sequences", "Storage", ""].map(function (label) { return h("th", { text: label }); }))),
      h("tbody", {}, rows)
    ])));
    return card;
  }

  async function addReference() {
    var paths = await HD.pickFiles("Choose a reference FASTA", false).catch(function (error) { HD.toast(HD.errorText(error), "error"); return []; });
    if (!paths.length) return;
    try {
      var result = await HD.op("add_reference", { source: paths[0], mode: "linked" });
      HD.toast((result.existing ? "Reference already registered: " : "Reference added: ") + ((result.reference && (result.reference.label || result.reference.id)) || HD.basename(paths[0])));
      await HD.refreshProject();
    } catch (error) {
      HD.toast("Could not add the reference: " + HD.errorText(error), "error");
    }
  }

  // ---------------------------------------------------------------- samples

  function renderSamples() {
    var card = h("section", { className: "d-card" });
    var samples = state.samples;
    selected.forEach(function (id) { if (!HD.sample(id)) selected.delete(id); });
    var problems = samples.filter(sampleProblem);
    var pending = samples.filter(function (sample) {
      var task = HD.sampleTask(sample);
      return !sampleDone(sample) && !(task && HD.isActiveTask(task));
    });
    card.appendChild(h("div", { className: "d-card-head" }, [
      h("h2", { className: "d-h2", text: "Samples (" + samples.length + ")" }),
      h("div", { className: "d-actions" }, [
        problems.length ? h("button", { type: "button", className: "d-btn d-btn-small", text: "Resume interrupted (" + problems.length + ")", on: { click: function () { resumeSamples(problems); } } }) : null,
        h("button", { type: "button", className: "d-btn d-btn-small", text: selected.size ? "Remove selected" : "Remove", disabled: !selected.size, on: { click: removeSelected } }),
        h("button", { type: "button", className: "d-btn d-btn-small d-btn-primary", disabled: !selected.size && !pending.length,
          text: selected.size ? "Run selected (" + selected.size + ")" : "Run all pending (" + pending.length + ")",
          on: { click: function () { runSamples(selected.size ? Array.from(selected).map(HD.sample).filter(Boolean) : pending); } } })
      ])
    ]));
    if (!samples.length) {
      card.appendChild(h("div", { className: "d-empty" }, [
        h("p", { text: "No samples yet." }),
        h("p", { className: "d-muted", text: "Import FASTQ / FASTA (optionally .gz), SAM or .hyb files, or a TSV/CSV sample sheet with columns name, condition, replicate, files, reference." })
      ]));
      return card;
    }
    var all = h("input", { type: "checkbox", "aria-label": "Select all samples", checked: selected.size === samples.length && samples.length > 0, on: { change: function () {
      if (this.checked) samples.forEach(function (sample) { selected.add(sample.id); }); else selected.clear();
      draw();
    } } });
    var head = h("tr", {}, [h("th", { className: "d-col-check" }, all)].concat(["Sample", "Condition", "Rep", "Raw file", "Map", ".hyb", "Index", "Hybrids", ""].map(function (label) { return h("th", { text: label }); })));
    var body = h("tbody", {}, samples.map(sampleRow));
    card.appendChild(h("div", { className: "d-table-wrap" }, h("table", { className: "d-table d-table-samples" }, [h("thead", {}, head), body])));
    return card;
  }

  function sampleRow(sample) {
    var reads = sample.reads || {};
    var check = inputStatus["sample:" + sample.id];
    var task = HD.sampleTask(sample);
    var active = task && HD.isActiveTask(task);
    var stats = state.stats[sample.id];
    var problem = active ? null : sampleProblem(sample);
    var checkbox = h("input", { type: "checkbox", "aria-label": "Select " + sample.name, checked: selected.has(sample.id), on: { change: function () {
      if (this.checked) selected.add(sample.id); else selected.delete(sample.id);
      draw();
    } } });
    var fileCell = h("td", { className: "d-file-cell", title: reads.path || "" }, [
      h("span", { className: "d-file-name", text: HD.basename(reads.path) }),
      h("small", { className: "d-muted", text: [reads.format, HD.formatBytes(reads.size_bytes), reads.mode || "linked"].filter(Boolean).join(" · ") }),
      check && check.status !== "ok" ? h("span", {}, [HD.chip("failed", check.status), " ", h("button", { type: "button", className: "d-btn d-btn-small", text: "Relink…", on: { click: function () { relink(check); } } })]) : null
    ]);
    var cells = [
      h("td", { className: "d-col-check" }, checkbox),
      h("td", {}, [h("button", { type: "button", className: "d-link", text: sample.name, title: "Edit sample", on: { click: function () { editSample(sample); } } }),
        sample.gene_id ? h("small", { className: "d-muted d-block", text: sample.gene_id }) : null]),
      h("td", { text: sample.condition || "—" }),
      h("td", { text: sample.replicate == null ? "—" : String(sample.replicate) }),
      fileCell
    ];
    if (active) {
      cells.push(h("td", { colSpan: 3, className: "d-progress-cell" }, HD.progressBar(task)));
    } else {
      ["map", "hyb", "index"].forEach(function (name) {
        var status = stage(sample, name).status;
        if ((status === "running" || status === "queued") && problem) status = "interrupted";
        cells.push(h("td", {}, HD.chip(status)));
      });
    }
    cells.push(h("td", { className: "d-num", text: stats ? HD.formatCompact(stats.records) : (HD.isReady(sample) ? "…" : "—") }));
    var actions = h("td", { className: "d-row-actions" });
    if (active) {
      actions.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "Cancel", on: { click: function () { HD.cancelTask(task.task_id).catch(function (error) { HD.toast(HD.errorText(error), "error"); }); } } }));
    } else if (problem) {
      actions.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "Resume", on: { click: function () { resumeSamples([sample]); } } }));
    } else if (!sampleDone(sample)) {
      actions.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "Run", on: { click: function () { runSamples([sample]); } } }));
    } else {
      actions.appendChild(h("button", { type: "button", className: "d-btn d-btn-small", text: "Explore", on: { click: function () {
        HD.setSelection({ kind: "sample", id: sample.id });
        HD.navigate("explore");
      } } }));
    }
    cells.push(actions);
    return h("tr", { className: active ? "d-row-active" : "" }, cells);
  }

  // ---------------------------------------------------------------- import raw files

  async function importRaw() {
    var paths = await HD.pickFiles("Choose raw read files (FASTQ, FASTA, .gz, SAM or .hyb)", true).catch(function (error) { HD.toast(HD.errorText(error), "error"); return []; });
    if (!paths.length) return;
    HD.modal("Import " + paths.length + (paths.length === 1 ? " file" : " files"), function (body, close) {
      body.appendChild(h("p", { className: "d-muted", text: "Each file becomes one sample. Replicates are numbered in the order below, after any existing replicates of the condition." }));
      var refSelect = h("select", { className: "d-select", id: "d-import-ref" });
      function fillRefs() {
        HD.clear(refSelect);
        refSelect.appendChild(h("option", { value: "", text: state.references.length ? "Choose…" : "Add a reference first" }));
        state.references.forEach(function (reference) { refSelect.appendChild(h("option", { value: reference.id, text: reference.label || reference.id })); });
        if (state.references.length) refSelect.value = state.referenceId || state.references[0].id;
      }
      fillRefs();
      var condition = h("input", { className: "d-input", id: "d-import-condition", placeholder: "e.g. AFR", autocomplete: "off" });
      var mode = h("select", { className: "d-select", id: "d-import-mode" }, [h("option", { value: "linked", text: "Link in place (recommended)" }), h("option", { value: "copied", text: "Copy into the project" })]);
      body.appendChild(h("div", { className: "d-form-grid" }, [
        h("label", { className: "d-label", for: "d-import-ref", text: "Reference" }),
        h("div", { className: "d-row" }, [refSelect, h("button", { type: "button", className: "d-btn d-btn-small", text: "Add…", on: { click: async function () { await addReference(); fillRefs(); } } })]),
        h("label", { className: "d-label", for: "d-import-condition", text: "Condition" }), condition,
        h("label", { className: "d-label", for: "d-import-mode", text: "Storage" }), mode
      ]));
      var names = paths.map(function (path) {
        return h("input", { className: "d-input d-input-compact", value: HD.basename(path).replace(/\.(fastq|fq|fasta|fa|sam|hyb)(\.gz)?$/i, ""), "aria-label": "Sample name for " + HD.basename(path) });
      });
      body.appendChild(h("div", { className: "d-table-wrap" }, h("table", { className: "d-table d-table-compact" }, [
        h("thead", {}, h("tr", {}, [h("th", { text: "Rep" }), h("th", { text: "Name" }), h("th", { text: "File" })])),
        h("tbody", {}, paths.map(function (path, index) {
          return h("tr", {}, [h("td", { text: String(index + 1) }), h("td", {}, names[index]), h("td", { className: "d-wrap", title: path, text: path })]);
        }))
      ])));
      var status = h("p", { className: "d-status-line", role: "status" });
      var actions = h("div", { className: "d-actions" }, [
        h("button", { type: "button", className: "d-btn", text: "Cancel", on: { click: function () { close(null); } } }),
        h("button", { type: "button", className: "d-btn d-btn-primary", text: "Import", on: { click: function () {
          if (!refSelect.value) { status.textContent = "Choose the reference for these samples (needed for mapping)."; return; }
          HD.busy(actions, async function () {
            var added = 0;
            // Continue the replicate numbering of an existing condition.
            var conditionName = condition.value.trim();
            var offset = state.samples.filter(function (sample) { return conditionName && sample.condition === conditionName; })
              .reduce(function (max, sample) { return Math.max(max, Number(sample.replicate) || 0); }, 0);
            var errors = [];
            for (var index = 0; index < paths.length; index += 1) {
              status.textContent = "Adding " + HD.basename(paths[index]) + "…";
              try {
                await HD.op("add_sample", {
                  source: paths[index], name: names[index].value.trim() || null, condition: condition.value.trim() || null,
                  replicate: offset + index + 1, reference_id: refSelect.value, mode: mode.value
                });
                added += 1;
              } catch (error) {
                errors.push(HD.basename(paths[index]) + ": " + HD.errorText(error));
              }
            }
            await HD.refreshProject();
            if (errors.length) {
              status.textContent = "Added " + added + "; " + errors.length + " failed:\n" + errors.join("\n");
            } else {
              HD.toast("Added " + added + (added === 1 ? " sample." : " samples."));
              close(true);
            }
          });
        } } })
      ]);
      body.appendChild(actions);
      body.appendChild(status);
    }, { wide: true });
  }

  // ---------------------------------------------------------------- sample sheet

  async function importSheet() {
    var paths = await HD.pickFiles("Choose a sample sheet (TSV or CSV)", false).catch(function (error) { HD.toast(HD.errorText(error), "error"); return []; });
    if (!paths.length) return;
    var sheet = paths[0];
    var preview;
    try {
      preview = await HD.op("import_sample_sheet", { sheet: sheet, dry_run: true });
    } catch (error) {
      HD.modal("Sample sheet problems", function (body) {
        body.appendChild(h("p", { text: HD.basename(sheet) + " cannot be imported:" }));
        body.appendChild(h("pre", { className: "d-pre", text: HD.errorText(error) }));
        body.appendChild(h("p", { className: "d-muted", text: "Expected columns: name, condition (or dataset/group), replicate, files (several files separated by ';' become replicates), reference, optional gene_id. Relative paths are resolved from the sheet's folder." }));
      }, { wide: true });
      return;
    }
    HD.modal("Import " + HD.basename(sheet), function (body, close) {
      var samples = preview.samples || [];
      body.appendChild(h("p", { className: "d-muted", text: samples.length + " samples, " + (preview.references || []).length + " reference(s). Nothing has been imported yet." }));
      body.appendChild(h("div", { className: "d-table-wrap d-table-scroll" }, h("table", { className: "d-table d-table-compact" }, [
        h("thead", {}, h("tr", {}, ["Name", "Condition", "Rep", "Format", "File", "Reference"].map(function (label) { return h("th", { text: label }); }))),
        h("tbody", {}, samples.map(function (sample) {
          return h("tr", {}, [
            h("td", { text: sample.name }), h("td", { text: sample.condition || "—" }), h("td", { text: sample.replicate == null ? "—" : String(sample.replicate) }),
            h("td", { text: sample.format }), h("td", { className: "d-wrap", title: sample.path, text: HD.basename(sample.path) }),
            h("td", { className: "d-wrap", title: sample.reference || "", text: sample.reference ? HD.basename(sample.reference) : "—" })
          ]);
        }))
      ])));
      var mode = h("select", { className: "d-select", id: "d-sheet-mode" }, [h("option", { value: "linked", text: "Link files in place" }), h("option", { value: "copied", text: "Copy files into the project" })]);
      body.appendChild(h("div", { className: "d-form-grid" }, [h("label", { className: "d-label", for: "d-sheet-mode", text: "Storage" }), mode]));
      var status = h("p", { className: "d-status-line", role: "status" });
      var actions = h("div", { className: "d-actions" }, [
        h("button", { type: "button", className: "d-btn", text: "Cancel", on: { click: function () { close(null); } } }),
        h("button", { type: "button", className: "d-btn d-btn-primary", text: "Import " + samples.length + " samples", on: { click: function () {
          HD.busy(actions, async function () {
            status.textContent = "Importing (fingerprinting files)…";
            try {
              var result = await HD.op("import_sample_sheet", { sheet: sheet, mode: mode.value });
              await HD.refreshProject();
              var skipped = result.skipped || [];
              HD.toast("Imported " + (result.samples || []).length + " samples" + (skipped.length ? ", " + skipped.length + " already in the project" : "") + ".");
              close(true);
            } catch (error) {
              status.textContent = "Import failed: " + HD.errorText(error);
            }
          });
        } } })
      ]);
      body.appendChild(actions);
      body.appendChild(status);
    }, { wide: true });
  }

  // ---------------------------------------------------------------- run / resume

  async function runSamples(samples) {
    var list = samples.filter(function (sample) {
      var task = HD.sampleTask(sample);
      return !(task && HD.isActiveTask(task));
    });
    if (!list.length) { HD.toast("Those samples are already running."); return; }
    var ids = list.map(function (sample) { return sample.id; });
    var estimate = null;
    try {
      estimate = await HD.op("disk_estimate", { sample_ids: ids, include_completed: true });
    } catch (error) {
      HD.toast("Disk estimate unavailable: " + HD.errorText(error), "error");
    }
    var go = await HD.modal("Run " + list.length + (list.length === 1 ? " sample" : " samples"), function (body, close) {
      body.appendChild(h("p", { text: list.map(function (sample) { return sample.name; }).join(", ") }));
      if (estimate) {
        var totals = estimate.totals || {};
        body.appendChild(h("dl", { className: "d-dl" }, [
          h("dt", { text: "Input" }), h("dd", { text: HD.formatBytes(totals.input_bytes) + (totals.uncompressed_bytes !== totals.input_bytes ? " (" + HD.formatBytes(totals.uncompressed_bytes) + " uncompressed)" : "") }),
          h("dt", { text: "Temporary peak" }), h("dd", { text: HD.formatBytes(totals.temp_peak_bytes) }),
          h("dt", { text: "Results" }), h("dd", { text: HD.formatBytes(totals.output_bytes) })
        ]));
        (estimate.volumes || []).forEach(function (volume) {
          body.appendChild(h("p", { className: volume.ok ? "d-muted" : "d-warning", text: (volume.ok ? "" : "Not enough space: ") + HD.formatBytes(volume.required_bytes) + " needed for " + volume.role + " on " + volume.path + ", " + HD.formatBytes(volume.free_bytes) + " free." }));
        });
      }
      body.appendChild(h("p", { className: "d-muted", text: "Each sample runs mapping → .hyb → analysis index as one task; tasks wait in the queue when the CPU / memory budget (Preferences) is used up." }));
      body.appendChild(h("div", { className: "d-actions" }, [
        h("button", { type: "button", className: "d-btn", text: "Cancel", on: { click: function () { close(false); } } }),
        h("button", { type: "button", className: "d-btn d-btn-primary", text: estimate && !estimate.fits ? "Run anyway" : "Run", on: { click: function () { close(true); } } })
      ]));
    });
    if (!go) return;
    var started = 0;
    for (var index = 0; index < list.length; index += 1) {
      try {
        var snapshot = await HD.startTask({ kind: "sample_pipeline", sample_id: list[index].id });
        HD.setTaskLabel(snapshot.task_id, "Pipeline · " + list[index].name);
        started += 1;
      } catch (error) {
        HD.toast("Could not start " + list[index].name + ": " + HD.errorText(error), "error");
      }
    }
    if (started) {
      HD.toast("Started " + started + (started === 1 ? " sample run." : " sample runs."));
      selected.clear();
      HD.refreshProject();
    }
  }

  // Resume = re-run the stored task request (Python resumes from checkpoints);
  // without a known task, start a fresh run for the sample.
  async function resumeSamples(samples) {
    for (var index = 0; index < samples.length; index += 1) {
      var sample = samples[index];
      var problem = sampleProblem(sample);
      try {
        if (problem && problem.task_id) {
          var snapshot = await HD.resumeTask(problem.task_id);
          HD.setTaskLabel(snapshot.task_id, "Pipeline · " + sample.name);
        } else {
          var started = await HD.startTask({ kind: "sample_pipeline", sample_id: sample.id });
          HD.setTaskLabel(started.task_id, "Pipeline · " + sample.name);
        }
      } catch (error) {
        HD.toast("Could not resume " + sample.name + ": " + HD.errorText(error), "error");
      }
    }
    HD.refreshProject();
  }

  async function removeSelected() {
    var list = Array.from(selected).map(HD.sample).filter(Boolean);
    if (!list.length) return;
    var ok = await HD.confirm("Remove samples", "Remove " + list.map(function (s) { return s.name; }).join(", ") + " from the project? Linked raw files are not touched; results in the project stay on disk.", "Remove");
    if (!ok) return;
    for (var index = 0; index < list.length; index += 1) {
      try { await HD.op("remove_sample", { sample_id: list[index].id }); } catch (error) { HD.toast(HD.errorText(error), "error"); }
    }
    selected.clear();
    await HD.refreshProject();
  }

  function editSample(sample) {
    HD.modal("Sample " + sample.name, function (body, close) {
      var name = h("input", { className: "d-input", id: "d-edit-name", value: sample.name });
      var condition = h("input", { className: "d-input", id: "d-edit-condition", value: sample.condition || "" });
      var replicate = h("input", { className: "d-input d-input-narrow", id: "d-edit-rep", type: "number", min: "1", value: sample.replicate == null ? "" : String(sample.replicate) });
      var reference = h("select", { className: "d-select", id: "d-edit-ref" }, [h("option", { value: "", text: "—" })].concat(state.references.map(function (item) { return h("option", { value: item.id, text: item.label || item.id }); })));
      reference.value = sample.reference_id || "";
      var gene = h("input", { className: "d-input", id: "d-edit-gene", value: sample.gene_id || "" });
      body.appendChild(h("div", { className: "d-form-grid" }, [
        h("label", { className: "d-label", for: "d-edit-name", text: "Name" }), name,
        h("label", { className: "d-label", for: "d-edit-condition", text: "Condition" }), condition,
        h("label", { className: "d-label", for: "d-edit-rep", text: "Replicate" }), replicate,
        h("label", { className: "d-label", for: "d-edit-ref", text: "Reference" }), reference,
        h("label", { className: "d-label", for: "d-edit-gene", text: "Gene id" }), gene
      ]));
      body.appendChild(h("p", { className: "d-muted d-wrap", text: (sample.reads || {}).path || "" }));
      var status = h("p", { className: "d-status-line", role: "status" });
      var actions = h("div", { className: "d-actions" }, [
        h("button", { type: "button", className: "d-btn", text: "Cancel", on: { click: function () { close(null); } } }),
        h("button", { type: "button", className: "d-btn d-btn-primary", text: "Save", on: { click: function () {
          var rep = replicate.value.trim() ? Math.floor(Number(replicate.value)) : null;
          HD.busy(actions, async function () {
            try {
              await HD.op("update_sample", { sample_id: sample.id, changes: {
                name: name.value.trim(), condition: condition.value.trim() || null, replicate: rep,
                reference_id: reference.value || null, gene_id: gene.value.trim() || null
              } });
              await HD.refreshProject();
              close(true);
            } catch (error) { status.textContent = HD.errorText(error); }
          });
        } } })
      ]);
      body.appendChild(actions);
      body.appendChild(status);
    });
  }

  // ---------------------------------------------------------------- check inputs

  async function checkInputs() {
    try {
      var result = await HD.op("check_inputs");
      inputStatus = Object.create(null);
      (result.items || []).forEach(function (item) { inputStatus[item.kind + ":" + item.id] = item; });
      draw();
      var summary = result.summary || {};
      if (result.all_ok) HD.toast("All " + (result.items || []).length + " input files are present and unchanged.");
      else HD.toast((summary.missing || 0) + " missing, " + (summary.changed || 0) + " changed. Use Relink on the marked rows.", "error");
    } catch (error) {
      HD.toast("Check failed: " + HD.errorText(error), "error");
    }
  }

  async function relink(item) {
    var paths = await HD.pickFiles("Locate " + HD.basename(item.path), false).catch(function () { return []; });
    if (!paths.length) return;
    try {
      if (item.kind === "reference") await HD.op("update_reference", { reference_id: item.id, source: paths[0] });
      else await HD.op("update_sample", { sample_id: item.id, changes: { reads_path: paths[0] } });
      delete inputStatus[item.kind + ":" + item.id];
      await HD.refreshProject();
      HD.toast("Relinked " + HD.basename(paths[0]) + ".");
    } catch (error) {
      HD.toast("Relink failed: " + HD.errorText(error), "error");
    }
  }

  // ---------------------------------------------------------------- wiring

  HD.on("project", draw);
  HD.on("stats", draw);
  HD.on("tasks", function () { if (state.page === "data") draw(); });
  HD.on("project-opened", function () { selected.clear(); inputStatus = Object.create(null); });

  HD.pages.data = { render: render, onShow: draw };
}());
