"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
function element() {
  const classes = new Set();
  return { classList: { contains: name => classes.has(name), add: name => classes.add(name), remove: name => classes.delete(name) }, children: [], events: {}, setAttribute() {}, appendChild(child) { this.children.push(child); },
    insertBefore(child) { this.children.unshift(child); }, addEventListener(name, callback) { this.events[name] = callback; } };
}
const context = vm.createContext({ window: {}, document: { createElement: element }, WeakSet, Number, Math });
const source = fs.readFileSync(path.join(__dirname, "../web/structure-ui.js"), "utf8");
vm.runInContext(source.replace("window.Hyb2StructureUI = {", "window.Hyb2StructureUI = { bindFornaControls: bindFornaControls,"), context);
const target = element(), mount = element();
let scale = 1, translate = [10, 20], applied = 0, fits = 0;
const zoomer = {
  scale(value) { if (value === undefined) return scale; scale = value; return this; },
  translate(value) { if (value === undefined) return translate; translate = Array.from(value); return this; },
  scaleExtent() { return [0.1, 10]; }, event() {}
};
let resizes = 0;
const container = { setSize() { resizes++; }, zoomer, options: { svgW: 800, svgH: 560, svg: { call(event) { assert.equal(event, zoomer.event); applied++; } } }, centerView() { fits++; } };
context.window.Hyb2StructureUI.bindFornaControls(target, mount, container);
const buttons = target.children[0].children;
buttons[0].events.click();
assert.equal(scale, 1.25);
assert.deepEqual(translate, [-87.5, -45], "zoom keeps the viewport centre fixed");
buttons[1].events.click();
assert.equal(scale, 1);
assert.deepEqual(translate, [10, 20], "zoom in and out are reversible");
scale = 10;
buttons[0].events.click();
assert.equal(scale, 10, "zoom respects fornac limits");
buttons[2].events.click();
assert.equal(fits, 1);
let prevented = false;
mount.events.keydown({ key: "ArrowRight", preventDefault() { prevented = true; }, stopPropagation() {} });
assert.deepEqual(translate, [50, 20]);
assert.equal(prevented, true);
assert.equal(applied, 4, "buttons and keys dispatch the native zoom event");
(async function () {
  target.requestFullscreen = async () => { throw new Error("Browser denied fullscreen"); };
  await buttons[3].events.click();
  assert.equal(target.classList.contains("is-expanded"), true);
  assert.equal(buttons[3].textContent, "Exit expanded view");
  await buttons[3].events.click();
  assert.equal(target.classList.contains("is-expanded"), false);
  assert.match(target.children[1].textContent, /Scroll or pinch/);
  await buttons[3].events.click();
  target.onkeydown({ key: "Escape", preventDefault() {}, stopPropagation() {} });
  assert.equal(target.classList.contains("is-expanded"), false);
  assert.equal(buttons[3].textContent, "Full screen");
  assert.equal(resizes, 4);
  console.log("forna-view-controls: ok");
})().catch(error => { console.error(error); process.exitCode = 1; });
