// The web RNA Structure page's IRIS code (web/iris-core.js) on a fixture,
// for test_iris.py.
//
//   node web_iris_reference.js request.json   (prints JSON)
//
// request: {sequence, arms: [{oneStart, oneEnd, twoStart, twoEnd}],
//           parameters: {K, C, fraction, kRange},
//           folds: {"l-r,l-r,...": {dotBracket, energy}}}
// The browser folds each constraint set with ViennaRNA WebAssembly; here
// the folds come from the request (the desktop's ViennaRNA, keyed by the
// constraint pairs), so the rest of the pipeline is the web's own code.
"use strict";
const fs = require("fs");
const path = require("path");

const iris = require(path.resolve(__dirname, "..", "..", "..", "web", "iris-core.js"));
const request = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));

function key(pairs) {
  return pairs.map(function (p) { return p.left + "-" + p.right; }).join(",");
}

function pairsOf(dotBracket) {
  const stack = [], pairs = [];
  for (let i = 0; i < dotBracket.length; i++) {
    if (dotBracket[i] === "(") stack.push(i);
    else if (dotBracket[i] === ")") pairs.push({ left: stack.pop() + 1, right: i + 1 });
  }
  return pairs.sort(function (a, b) { return a.left - b.left; });
}

(async function () {
  const out = {};
  const p = iris.parameters(request.parameters);
  out.parameters = p;
  const n = request.sequence.length;
  const support = iris.supportMatrix(n, request.arms);
  out.support = Array.from(support);
  const stems = iris.collectStems(request.sequence, support, p);
  out.stems = stems;
  const constraints = iris.collectConstraints(stems);
  out.constraints = constraints.map(key);
  const missing = out.constraints.filter(function (k) { return !(k in request.folds); });
  if (missing.length) {
    out.missingFolds = missing;
  } else {
    const stages = [];
    const job = { sequence: request.sequence, parameters: request.parameters, evidenceArms: request.arms };
    const result = await iris.run(job, async function (sequence, pairs) {
      const fold = request.folds[key(pairs)];
      return { dotBracket: fold.dotBracket, pairs: pairsOf(fold.dotBracket), energy: fold.energy,
        constraints: pairs.map(function (q) { return { left: q.left, right: q.right }; }) };
    }, function (stage, percent) { stages.push([stage, percent]); });
    out.result = {
      members: result.candidates.map(function (c) {
        return { dotBracket: c.dotBracket, energy: c.energy, weight: c.weight, coefficient: c.coefficient, constraints: key(c.constraints) };
      }),
      ensemble: result.ensemble, selectedCandidate: result.selectedCandidate, stages: stages
    };
  }
  process.stdout.write(JSON.stringify(out));
}()).catch(function (error) {
  process.stdout.write(JSON.stringify({ error: error.message }));
});
