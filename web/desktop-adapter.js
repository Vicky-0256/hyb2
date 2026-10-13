/*
 * Boundary between the browser UI and the future Tauri shell.
 *
 * The web build remains functional without a native bridge: callers fall
 * back to the existing browser download path. The desktop bundle can replace
 * these methods with native dialog and task implementations without changing
 * the analysis pages.
 */
(function () {
  "use strict";

  var nativeBridge = window.__TAURI__ || null;
  var advertisedCapabilities = window.__HYB2_DESKTOP_CAPABILITIES__ || (nativeBridge && nativeBridge.capabilities) || {};
  var invoke = nativeBridge && nativeBridge.core && nativeBridge.core.invoke;
  var desktopEnabled = !!invoke;

  window.Hyb2Desktop = {
    enabled: desktopEnabled,
    nativeBridge: nativeBridge,
    // Runtime diagnostics are not proof that this Web controller dispatches
    // native jobs. Keep browser limits until a dispatcher is integrated.
    advertisedCapabilities: advertisedCapabilities,
    capabilities: {
      cplfoldNative: desktopEnabled && advertisedCapabilities.cplfoldNative === true,
      numbaJit: desktopEnabled && advertisedCapabilities.numbaJit === true
    },
    saveFile: async function () {
      return false;
    },
    openFile: async function () {
      return null;
    },
    startTask: async function () {
      throw new Error("The native task bridge is not available in this build.");
    },
    predictCplfold: async function (request) {
      if (!invoke) {
        return { ok: false, status: "unavailable", error: "The native task bridge is not available in this build." };
      }
      return invoke("cplfold_predict", { request: request || {} });
    }
  };
  // Capability claims are opt-in. The desktop shell may not know them until
  // its runtime detector completes, so probe IPC once and promote only when
  // the detector explicitly reports an available native capability.
  if (desktopEnabled && !advertisedCapabilities.cplfoldNative && typeof invoke === "function") {
    invoke("runtime_detect").then(function (snapshot) {
      var list = snapshot && snapshot.result && snapshot.result.capabilities || [];
      var available = function (id) { return list.some(function (item) { return item && item.id === id && item.status === "available"; }); };
      window.Hyb2Desktop.capabilities.cplfoldNative = available("structure.cplfold_native");
      window.Hyb2Desktop.capabilities.numbaJit = available("structure.cplfold_numba");
    }).catch(function () { /* Browser worker remains the safe fallback. */ });
  }
})();
