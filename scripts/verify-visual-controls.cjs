"use strict";
const assert = require("node:assert/strict"), fs = require("node:fs"), path = require("node:path"), vm = require("node:vm");
const root = path.resolve(__dirname, "..");
const source = name => fs.readFileSync(path.join(root, "web", name), "utf8");
const context = vm.createContext({window: {}, Number, Math, Intl, console});
vm.runInContext(source("analysis-ui.js").replace("window.Hyb2UI = {", "window.Hyb2UI = { changeComparisonView, comparisonView, getComparisonGeometry,"), context);
const ui = context.window.Hyb2UI;
const result = {binSize: 10, xMin: 0, xMax: 990, yMin: 100, yMax: 1090};
let view = ui.changeComparisonView(result, "effect", .5, .5, .5, 0, 0);
assert.equal(view.xStart, 250); assert.equal(view.xEnd, 740);
view = ui.changeComparisonView(result, "effect", 1, .5, .5, -100, 100);
assert.equal(view.xStart, 0); assert.equal(view.yEnd, 1090);
for(let i=0;i<20;i++) view = ui.changeComparisonView(result, "effect", .5, .5, .5, 0, 0);
assert.equal(view.xStart, view.xEnd, "zoom stops at one bin");
view = ui.changeComparisonView(result, "effect", 10000, .5, .5, 0, 0);
assert.equal(view.xStart, 0); assert.equal(view.xEnd, 990);
assert.equal(ui.comparisonView(result, "conserved"), null, "maps keep separate viewports");
assert.equal(ui.comparisonView({...result}, "effect"), null, "new results reset views");
vm.runInContext(source("ui-kit.js"), context);
const chimera = context.window.Hyb2ChimeraTypes;
for(let i=1;i<=13;i++) {
 assert.doesNotMatch(chimera.label("Type_"+i), /Type_/);
 assert.match(chimera.diagram("Type_"+i), /<svg/);
}
assert.match(chimera.label("Type_1"), /5′ arm first/);
assert.match(chimera.label("Type_2"), /3′ arm first/);
assert.match(chimera.label("Type_13"), /Touching/);
assert.match(chimera.label("Type_7"), /Overlap/);
assert.equal((chimera.guide().match(/<svg/g)||[]).length, 14);
vm.runInContext(source("deseq-card.js").replace("window.Hyb2DeseqCard = {", "window.Hyb2DeseqCard = { changeMapView, geometry,"), context);
const card = context.window.Hyb2DeseqCard;
const differential = { summary: { maxCoordinate: 990 }, significant: {x:[20,900],y:[30,900],log2FoldChange:[1,-1],padj:[.001,.0001]} };
const comparison = {deseq: {result:differential,show:"both"}};
assert.equal(card.panelSvg(comparison).drawn,2);
const dv = card.changeMapView(differential,.5,0,0,0,0);
assert.equal(dv.span,500);
assert.equal(card.panelSvg(comparison).drawn,1,"zoomed SVG omits off-screen cells");
const dg = card.geometry(760,820,1000,dv);
assert.equal(dg.x(0),dg.left);
assert.equal(dg.y(500),dg.top,"differential map preserves its upward Y axis");
let redraws=0;
card.handleAction({comparison},"deseq-zoom",{dataset:{zoom:"reset"}},{render(){redraws++;}});
assert.equal(redraws,1);
assert.equal(card.panelSvg(comparison).drawn,2);
const escape = str => String(str).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/"/g,"&quot;");
class Node {
 constructor(name) { this.name=name; this.attrs={}; this.children=[]; this.textContent=""; }
 setAttribute(k,v) { this.attrs[k]=v; }
 appendChild(n) { this.children.push(n); }
 get outerHTML() { return `<${this.name} ${Object.entries(this.attrs).map(([k,v])=>`${k}="${escape(v)}"`).join(" ")}>${escape(this.textContent)}${this.children.map(n=>n.outerHTML).join("")}</${this.name}>`; }
}
context.document = {documentElement: {}, createElementNS: (_,name)=>new Node(name)};
context.getComputedStyle = ()=>({getPropertyValue: name=>({"--surface-subtle":"#ffffff","--ink":"#222222","--primary":"#3C5488","--accent":"#E64B35"}[name] || "#666666")});
vm.runInContext(source("structure-ui.js"), context);
const structure = {sequence:"ACGUACGUACGU", pairs:[{left:1,right:12,leftBase:"A",rightBase:"U",type:"A–U"}], rawHybArms:[{recordId:"synthetic",oneStart:1,oneEnd:3,twoStart:9,twoEnd:12}]};
const svg = context.window.Hyb2StructureUI.buildStructureDiagram(structure,null,"arc");
const predicted = svg.children.find(n=>n.attrs["data-arc-source"]==="prediction");
const raw = svg.children.find(n=>n.attrs["data-arc-source"]==="raw-hyb");
assert.ok(predicted && raw);
assert.ok(Number(predicted.attrs.d.split(" ")[5]) < 244);
assert.ok(Number(raw.attrs.d.split(" ")[5]) > 244);
assert.match(raw.outerHTML,/arm midpoints, not a predicted base pair/);
const absent = context.window.Hyb2StructureUI.buildStructureDiagram({...structure,rawHybArms:null},null,"arc");
assert.doesNotMatch(absent.outerHTML,/data-arc-source="raw-hyb"/);
assert.match(absent.outerHTML,/not attached/);
if (process.argv.includes("--preview")) fs.writeFileSync("/tmp/hyb2-visual-preview.html",`<!doctype html><html><head><meta charset="utf-8"><style>${source("styles.css")} body{padding:30px} .preview{max-width:1100px;margin:auto} details{display:block}</style></head><body><main class="preview"><h1>Structure Arc · synthetic display fixture</h1>${svg.outerHTML}<h1>Chimera pattern guide</h1>${chimera.guide().replace('<details class=', '<details open class=')}</main></body></html>`);
console.log("visual-controls: ok");
