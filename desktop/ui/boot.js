/*
 * Runs before every other script (CSP forbids inline scripts): points the
 * embedded analysis pages at ./analysis/, keeps the website from mounting
 * itself and applies the colour theme so the first paint is correct.
 */
(function () {
  "use strict";
  window.HYB2_ASSET_BASE = "./analysis/";
  window.HYB2_AUTO_MOUNT = false;
  var THEME_KEY = "hyb2.desktop.theme";
  function stored() {
    try { return window.localStorage.getItem(THEME_KEY) || "system"; } catch (_) { return "system"; }
  }
  function systemDark() {
    return !!(window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
  }
  function apply(choice) {
    var value = choice || stored();
    var dark = value === "dark" || (value === "system" && systemDark());
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    document.documentElement.dataset.themeChoice = value;
  }
  window.Hyb2Theme = {
    key: THEME_KEY,
    get: stored,
    set: function (choice) {
      try { window.localStorage.setItem(THEME_KEY, choice); } catch (_) {}
      apply(choice);
    },
    apply: apply
  };
  apply();
  if (window.matchMedia) {
    var query = window.matchMedia("(prefers-color-scheme: dark)");
    var listener = function () { if (stored() === "system") apply("system"); };
    if (query.addEventListener) query.addEventListener("change", listener);
    else if (query.addListener) query.addListener(listener);
  }
}());
