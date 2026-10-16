/*
 * HYB2 differential contact test in the browser: a JavaScript port of HYB2
 * Desktop's numpy DESeq2 port (pipeline/hyb2analysis/deseq.py and
 * differential.py), which follows DESeq2 1.46 for a design with one
 * two-level factor:
 *
 *   - median-of-ratios size factors;
 *   - gene-wise dispersion (Cox-Reid adjusted likelihood, Armijo line search,
 *     grid search when it does not converge);
 *   - parametric trend asymptDisp + extraPois / mean, local fit fallback;
 *   - prior variance of the log dispersions (for 1-3 residual degrees of
 *     freedom computed by numerical convolution, as the Desktop port does);
 *   - MAP dispersion, outliers kept at the gene-wise value;
 *   - IRLS (ridge 1e-6, minmu 0.5) and the Wald test;
 *   - Cook's distance filtering for cells with three or more replicates;
 *   - independent filtering on the mean of normalised counts (lowess) at
 *     alpha 0.01 and Benjamini-Hochberg.
 *
 * Then, as hyb2_compare: cells are the 10-nt contact bins of every sample
 * (rows with a summed count > minCount are tested), significant cells
 * (padj < cutoff) are merged greedily by padj into interactions within
 * `radius` nt in both x and y, per direction.
 *
 * Everything works on typed arrays so a whole viral genome (about a million
 * 10-nt cells) fits; it runs in deseq.worker.js off the main thread.  Plain
 * script: defines Hyb2Deseq on window / self / globalThis and module.exports.
 */
(function (root, factory) {
  "use strict";
  const api = factory();
  if (typeof module === "object" && module && module.exports) {
    module.exports = api;
  }
  if (root) {
    root.Hyb2Deseq = api;
  }
}(typeof window !== "undefined" ? window : (typeof self !== "undefined" ? self : globalThis), function () {
  "use strict";

  const MIN_DISP = 1e-8;
  const MIN_MU = 0.5;
  const KAPPA_0 = 1.0;
  const DISP_TOL = 1e-6;
  const MAXIT = 100;
  const BETA_TOL = 1e-8;
  const LAMBDA = 1e-6;
  const LARGE = 30.0;
  const EPSILON = 1e-4;
  const LN2 = Math.log(2);
  const EPS = 2.220446049250313e-16;
  const DEFAULTS = { minCount: 2, radius: 100, alpha: 0.01, padjCutoff: 0.05, binSize: 10 };
  const KEY_SHIFT = 33554432; // 2^25: key = xBin * 2^25 + yBin (exact below 2^53)

  // ---- special functions -----------------------------------------------------

  const f64 = new Float64Array(1);
  const u32 = new Uint32Array(f64.buffer);
  const LITTLE = new Uint8Array(new Uint16Array([1]).buffer)[0] === 1;
  const HI = LITTLE ? 1 : 0;
  const LO = LITTLE ? 0 : 1;

  function highWord(x) {
    f64[0] = x;
    return u32[HI] | 0;
  }

  function lowWord(x) {
    f64[0] = x;
    return u32[LO] | 0;
  }

  function withLowZero(x) {
    f64[0] = x;
    u32[LO] = 0;
    return f64[0];
  }

  // log Gamma(x) for x > 0: fdlibm e_lgamma_r.c (the algorithm of most C
  // libraries' lgamma).
  const LG = {
    a0: 7.72156649015328655494e-02, a1: 3.22467033424113591611e-01, a2: 6.73523010531292681824e-02,
    a3: 2.05808084325167332806e-02, a4: 7.38555086081402883957e-03, a5: 2.89051383673415629091e-03,
    a6: 1.19270763183362067845e-03, a7: 5.10069792153511336608e-04, a8: 2.20862790713908385557e-04,
    a9: 1.08011567247583939954e-04, a10: 2.52144565451257326939e-05, a11: 4.48640949618915160150e-05,
    tc: 1.46163214496836224576e+00, tf: -1.21486290535849611461e-01, tt: -3.63867699703950536541e-18,
    t0: 4.83836122723810047042e-01, t1: -1.47587722994593911752e-01, t2: 6.46249402391333854778e-02,
    t3: -3.27885410759859649565e-02, t4: 1.79706750811820387126e-02, t5: -1.03142241298341437450e-02,
    t6: 6.10053870246291332635e-03, t7: -3.68452016781138256760e-03, t8: 2.25964780900612472250e-03,
    t9: -1.40346469989232843813e-03, t10: 8.81081882437654011382e-04, t11: -5.38595305356740546715e-04,
    t12: 3.15632070903625950361e-04, t13: -3.12754168375120860518e-04, t14: 3.35529192635519073543e-04,
    u0: -7.72156649015328655494e-02, u1: 6.32827064025093366517e-01, u2: 1.45492250137234768737e+00,
    u3: 9.77717527963372745603e-01, u4: 2.28963728064692451092e-01, u5: 1.33810918536787660377e-02,
    v1: 2.45597793713041134822e+00, v2: 2.12848976379893395361e+00, v3: 7.69285150456672783825e-01,
    v4: 1.04222645593369134254e-01, v5: 3.21709242282423911810e-03,
    s0: -7.72156649015328655494e-02, s1: 2.14982415960608852501e-01, s2: 3.25778796408930981787e-01,
    s3: 1.46350472652464452805e-01, s4: 2.66422703033638609560e-02, s5: 1.84028451407337715652e-03,
    s6: 3.19475326584100867617e-05,
    r1: 1.39200533467621045958e+00, r2: 7.21935547567138069525e-01, r3: 1.71933865632803078993e-01,
    r4: 1.86459191715652901344e-02, r5: 7.77942496381893596434e-04, r6: 7.32668430744625636189e-06,
    w0: 4.18938533204672725052e-01, w1: 8.33333333333329678849e-02, w2: -2.77777777728775536470e-03,
    w3: 7.93650558643019558500e-04, w4: -5.95187557450339963135e-04, w5: 8.36339918996282139126e-04,
    w6: -1.63092934096575273989e-03
  };

  function lgamma(x) {
    if (!(x > 0)) {
      if (x === 0) {
        return Infinity;
      }
      return NaN; // not needed by the test (all arguments are positive)
    }
    const C = LG;
    const hx = highWord(x);
    const ix = hx & 0x7fffffff;
    const lx = lowWord(x);
    if (ix >= 0x7ff00000) {
      return x * x;
    }
    if (ix < 0x3b900000) {
      return -Math.log(x);
    }
    let r;
    if ((((ix - 0x3ff00000) | lx) === 0) || (((ix - 0x40000000) | lx) === 0)) {
      return 0;
    }
    if (ix < 0x40000000) {
      let y;
      let i;
      if (ix <= 0x3feccccc) {
        r = -Math.log(x);
        if (ix >= 0x3FE76944) {
          y = 1 - x; i = 0;
        } else if (ix >= 0x3FCDA661) {
          y = x - (C.tc - 1); i = 1;
        } else {
          y = x; i = 2;
        }
      } else {
        r = 0;
        if (ix >= 0x3FFBB4C3) {
          y = 2.0 - x; i = 0;
        } else if (ix >= 0x3FF3B4C4) {
          y = x - C.tc; i = 1;
        } else {
          y = x - 1; i = 2;
        }
      }
      if (i === 0) {
        const z = y * y;
        const p1 = C.a0 + z * (C.a2 + z * (C.a4 + z * (C.a6 + z * (C.a8 + z * C.a10))));
        const p2 = z * (C.a1 + z * (C.a3 + z * (C.a5 + z * (C.a7 + z * (C.a9 + z * C.a11)))));
        const p = y * p1 + p2;
        r += (p - 0.5 * y);
      } else if (i === 1) {
        const z = y * y;
        const w = z * y;
        const p1 = C.t0 + w * (C.t3 + w * (C.t6 + w * (C.t9 + w * C.t12)));
        const p2 = C.t1 + w * (C.t4 + w * (C.t7 + w * (C.t10 + w * C.t13)));
        const p3 = C.t2 + w * (C.t5 + w * (C.t8 + w * (C.t11 + w * C.t14)));
        const p = z * p1 - (C.tt - w * (p2 + y * p3));
        r += (C.tf + p);
      } else {
        const p1 = y * (C.u0 + y * (C.u1 + y * (C.u2 + y * (C.u3 + y * (C.u4 + y * C.u5)))));
        const p2 = 1 + y * (C.v1 + y * (C.v2 + y * (C.v3 + y * (C.v4 + y * C.v5))));
        r += (-0.5 * y + p1 / p2);
      }
      return r;
    }
    if (ix < 0x40200000) {
      const i = Math.trunc(x);
      const y = x - i;
      const p = y * (C.s0 + y * (C.s1 + y * (C.s2 + y * (C.s3 + y * (C.s4 + y * (C.s5 + y * C.s6))))));
      const q = 1 + y * (C.r1 + y * (C.r2 + y * (C.r3 + y * (C.r4 + y * (C.r5 + y * C.r6)))));
      r = 0.5 * y + p / q;
      let z = 1;
      switch (i) {
        case 7: z *= (y + 6.0); // falls through
        case 6: z *= (y + 5.0); // falls through
        case 5: z *= (y + 4.0); // falls through
        case 4: z *= (y + 3.0); // falls through
        case 3: z *= (y + 2.0);
          r += Math.log(z);
          break;
        default:
          break;
      }
      return r;
    }
    if (ix < 0x43900000) {
      const t = Math.log(x);
      const z = 1 / x;
      const y = z * z;
      const w = C.w0 + z * (C.w1 + y * (C.w2 + y * (C.w3 + y * (C.w4 + y * (C.w5 + y * C.w6)))));
      return (x - 0.5) * (t - 1) + w;
    }
    return x * (Math.log(x) - 1);
  }

  // erfc(x): fdlibm s_erf.c
  const EF = {
    erx: 8.45062911510467529297e-01, efx: 1.28379167095512586316e-01,
    pp0: 1.28379167095512558561e-01, pp1: -3.25042107247001499370e-01, pp2: -2.84817495755985104766e-02,
    pp3: -5.77027029648944159157e-03, pp4: -2.37630166566501626084e-05,
    qq1: 3.97917223959155352819e-01, qq2: 6.50222499887672944485e-02, qq3: 5.08130628187576562776e-03,
    qq4: 1.32494738004321644526e-04, qq5: -3.96022827877536812320e-06,
    pa0: -2.36211856075265944077e-03, pa1: 4.14856118683748331666e-01, pa2: -3.72207876035701323847e-01,
    pa3: 3.18346619901161753674e-01, pa4: -1.10894694282396677476e-01, pa5: 3.54783043256182359371e-02,
    pa6: -2.16637559486879084300e-03,
    qa1: 1.06420880400844228286e-01, qa2: 5.40397917702171048937e-01, qa3: 7.18286544141962662868e-02,
    qa4: 1.26171219808761642112e-01, qa5: 1.36370839120290507362e-02, qa6: 1.19844998467991074170e-02,
    ra0: -9.86494403484714822705e-03, ra1: -6.93858572707181764372e-01, ra2: -1.05586262253232909814e+01,
    ra3: -6.23753324503260060396e+01, ra4: -1.62396669462573470355e+02, ra5: -1.84605092906711035994e+02,
    ra6: -8.12874355063065934246e+01, ra7: -9.81432934416914548592e+00,
    sa1: 1.96512716674392571292e+01, sa2: 1.37657754143519042600e+02, sa3: 4.34565877475229228821e+02,
    sa4: 6.45387271733267880336e+02, sa5: 4.29008140027567833386e+02, sa6: 1.08635005541779435134e+02,
    sa7: 6.57024977031928170135e+00, sa8: -6.04244152148580987438e-02,
    rb0: -9.86494292470009928597e-03, rb1: -7.99283237680523006574e-01, rb2: -1.77579549177547519889e+01,
    rb3: -1.60636384855821916062e+02, rb4: -6.37566443368389627722e+02, rb5: -1.02509513161107724954e+03,
    rb6: -4.83519191608651397019e+02,
    sb1: 3.03380607434824582924e+01, sb2: 3.25792512996573918826e+02, sb3: 1.53672958608443695994e+03,
    sb4: 3.19985821950859553908e+03, sb5: 2.55305040643316442583e+03, sb6: 4.74528541206955367215e+02,
    sb7: -2.24409524465858183362e+01
  };

  function erfc(x) {
    const C = EF;
    const hx = highWord(x);
    const ix = hx & 0x7fffffff;
    if (ix >= 0x7ff00000) {
      return ((hx >>> 31) << 1) + 1 / x;
    }
    if (ix < 0x3feb0000) {
      if (ix < 0x3c700000) {
        return 1 - x;
      }
      const z = x * x;
      const r = C.pp0 + z * (C.pp1 + z * (C.pp2 + z * (C.pp3 + z * C.pp4)));
      const s = 1 + z * (C.qq1 + z * (C.qq2 + z * (C.qq3 + z * (C.qq4 + z * C.qq5))));
      const y = r / s;
      if (hx < 0x3fd00000) {
        return 1 - (x + x * y);
      }
      let rr = x * y;
      rr += (x - 0.5);
      return 0.5 - rr;
    }
    if (ix < 0x3ff40000) {
      const s = Math.abs(x) - 1;
      const P = C.pa0 + s * (C.pa1 + s * (C.pa2 + s * (C.pa3 + s * (C.pa4 + s * (C.pa5 + s * C.pa6)))));
      const Q = 1 + s * (C.qa1 + s * (C.qa2 + s * (C.qa3 + s * (C.qa4 + s * (C.qa5 + s * C.qa6)))));
      if (hx >= 0) {
        return 1 - C.erx - P / Q;
      }
      return 1 + (C.erx + P / Q);
    }
    if (ix < 0x403c0000) {
      const ax = Math.abs(x);
      const s = 1 / (ax * ax);
      let R;
      let S;
      if (ix < 0x4006DB6D) {
        R = C.ra0 + s * (C.ra1 + s * (C.ra2 + s * (C.ra3 + s * (C.ra4 + s * (C.ra5 + s * (C.ra6 + s * C.ra7))))));
        S = 1 + s * (C.sa1 + s * (C.sa2 + s * (C.sa3 + s * (C.sa4 + s * (C.sa5 + s * (C.sa6 + s * (C.sa7 + s * C.sa8)))))));
      } else {
        if (hx < 0 && ix >= 0x40180000) {
          return 2;
        }
        R = C.rb0 + s * (C.rb1 + s * (C.rb2 + s * (C.rb3 + s * (C.rb4 + s * (C.rb5 + s * C.rb6)))));
        S = 1 + s * (C.sb1 + s * (C.sb2 + s * (C.sb3 + s * (C.sb4 + s * (C.sb5 + s * (C.sb6 + s * C.sb7))))));
      }
      const z = withLowZero(ax);
      const r = Math.exp(-z * z - 0.5625) * Math.exp((z - ax) * (z + ax) + R / S);
      if (hx > 0) {
        return r / ax;
      }
      return 2 - r / ax;
    }
    if (hx > 0) {
      return 0;
    }
    return 2;
  }

  function digamma(x) {
    let result = 0.0;
    while (x < 6.0) {
      result -= 1.0 / x;
      x += 1.0;
    }
    const f = 1.0 / (x * x);
    return result + Math.log(x) - 0.5 / x - f * (1.0 / 12 - f * (1.0 / 120 - f * (1.0 / 252 - f * (
      1.0 / 240 - f * (1.0 / 132 - f * (691.0 / 32760 - f / 12.0))))));
  }

  function trigamma(x) {
    let result = 0.0;
    while (x < 6.0) {
      result += 1.0 / (x * x);
      x += 1.0;
    }
    const f = 1.0 / (x * x);
    return result + 1.0 / x + f / 2 + f / x * (1.0 / 6 - f * (1.0 / 30 - f * (1.0 / 42 - f * (1.0 / 30 - f * 5.0 / 66))));
  }

  // ---- numpy-compatible reductions ---------------------------------------------

  // np.sum of a contiguous float64 array (numpy's pairwise summation).
  function pairwiseSum(a, start, n) {
    if (n < 8) {
      let res = 0.0;
      for (let i = 0; i < n; i += 1) {
        res += a[start + i];
      }
      return res;
    }
    if (n <= 128) {
      let r0 = a[start], r1 = a[start + 1], r2 = a[start + 2], r3 = a[start + 3];
      let r4 = a[start + 4], r5 = a[start + 5], r6 = a[start + 6], r7 = a[start + 7];
      let i = 8;
      const end = n - (n % 8);
      for (; i < end; i += 8) {
        const k = start + i;
        r0 += a[k]; r1 += a[k + 1]; r2 += a[k + 2]; r3 += a[k + 3];
        r4 += a[k + 4]; r5 += a[k + 5]; r6 += a[k + 6]; r7 += a[k + 7];
      }
      let res = ((r0 + r1) + (r2 + r3)) + ((r4 + r5) + (r6 + r7));
      for (; i < n; i += 1) {
        res += a[start + i];
      }
      return res;
    }
    let n2 = Math.floor(n / 2);
    n2 -= n2 % 8;
    return pairwiseSum(a, start, n2) + pairwiseSum(a, start + n2, n - n2);
  }

  function npSum(a) {
    return pairwiseSum(a, 0, a.length);
  }

  function npMean(a) {
    return npSum(a) / a.length;
  }

  function sortedCopy(a) {
    const s = Float64Array.from(a);
    s.sort();
    return s;
  }

  function npMedian(a) {
    const n = a.length;
    if (!n) {
      return NaN;
    }
    const s = sortedCopy(a);
    if (Number.isNaN(s[n - 1])) {
      return NaN;
    }
    const h = n >> 1;
    return n % 2 ? s[h] : (s[h - 1] + s[h]) / 2;
  }

  function mad(x) {
    const med = npMedian(x);
    const d = new Float64Array(x.length);
    for (let i = 0; i < x.length; i += 1) {
      d[i] = Math.abs(x[i] - med);
    }
    return 1.4826 * npMedian(d);
  }

  function linspace(start, stop, num) {
    const out = new Float64Array(num);
    const step = (stop - start) / (num - 1);
    for (let i = 0; i < num; i += 1) {
      out[i] = i * step + start;
    }
    out[num - 1] = stop;
    return out;
  }

  // Solve a small dense system with partial pivoting (LAPACK dgesv order).
  function solve(a, b) {
    const n = b.length;
    const m = a.map(function (row) { return row.slice(); });
    const x = b.slice();
    const perm = [];
    for (let k = 0; k < n; k += 1) {
      let p = k;
      for (let i = k + 1; i < n; i += 1) {
        if (Math.abs(m[i][k]) > Math.abs(m[p][k])) {
          p = i;
        }
      }
      perm.push(p);
      if (m[p][k] === 0) {
        throw new Error("singular matrix");
      }
      if (p !== k) {
        const t = m[p]; m[p] = m[k]; m[k] = t;
      }
      const inv = 1 / m[k][k];
      for (let i = k + 1; i < n; i += 1) {
        m[i][k] *= inv;
      }
      for (let i = k + 1; i < n; i += 1) {
        for (let j = k + 1; j < n; j += 1) {
          m[i][j] -= m[i][k] * m[k][j];
        }
      }
    }
    for (let k = 0; k < n; k += 1) {
      const p = perm[k];
      if (p !== k) {
        const t = x[p]; x[p] = x[k]; x[k] = t;
      }
    }
    for (let i = 0; i < n; i += 1) {
      for (let j = 0; j < i; j += 1) {
        x[i] -= m[i][j] * x[j];
      }
    }
    for (let i = n - 1; i >= 0; i -= 1) {
      for (let j = i + 1; j < n; j += 1) {
        x[i] -= m[i][j] * x[j];
      }
      x[i] /= m[i][i];
    }
    return x;
  }

  function inv2(a00, a01, a10, a11, out) {
    const det = a00 * a11 - a01 * a10;
    out[0] = a11 / det;
    out[1] = -a01 / det;
    out[2] = -a10 / det;
    out[3] = a00 / det;
    return out;
  }

  // ---- dispersion likelihood (src/DESeq2.cpp) -------------------------------------
  // y, mu: row-major n x m; row offset o.  group[j] in {0, 1}.

  function logPost(logAlpha, y, mu, o, m, group, priorMean, priorVar, usePrior) {
    const alpha = Math.exp(logAlpha);
    const r = 1.0 / alpha;
    let ll = 0.0;
    for (let j = 0; j < m; j += 1) {
      const yj = y[o + j];
      const mj = mu[o + j];
      ll += lgamma(yj + r) - yj * Math.log(mj + r) - r * Math.log(1.0 + mj * alpha);
    }
    ll -= m * lgamma(r);
    let cr = 0.0;
    for (let g = 0; g < 2; g += 1) {
      let s = 0.0;
      for (let j = 0; j < m; j += 1) {
        if (group[j] === g) {
          s += 1.0 / (1.0 / mu[o + j] + alpha);
        }
      }
      cr += Math.log(s);
    }
    let prior = 0.0;
    if (usePrior) {
      const d = logAlpha - priorMean;
      prior = -0.5 * (d * d) / priorVar;
    }
    return ll - 0.5 * cr + prior;
  }

  function dlogPost(logAlpha, y, mu, o, m, group, priorMean, priorVar, usePrior) {
    const alpha = Math.exp(logAlpha);
    const r = 1.0 / alpha;
    let s = 0.0;
    const dg = digamma(r);
    for (let j = 0; j < m; j += 1) {
      const yj = y[o + j];
      const mj = mu[o + j];
      s += dg + Math.log(1.0 + mj * alpha) - mj * alpha / (1.0 + mj * alpha) -
        digamma(yj + r) + yj / (mj + r);
    }
    const ll = r * r * s;
    let cr = 0.0;
    for (let g = 0; g < 2; g += 1) {
      let sw = 0.0;
      let sdw = 0.0;
      for (let j = 0; j < m; j += 1) {
        if (group[j] === g) {
          const t = 1.0 / mu[o + j] + alpha;
          sw += 1.0 / t;
          sdw -= 1.0 / (t * t);
        }
      }
      cr += sdw / sw;
    }
    let prior = 0.0;
    if (usePrior) {
      prior = -(logAlpha - priorMean) / priorVar;
    }
    return (ll - 0.5 * cr) * alpha + prior;
  }

  // rows: optional index list; returns typed arrays over those rows.
  function fitDisp(y, mu, m, group, rows, logAlpha0, priorMean, priorVar, usePrior, minLogAlpha, tick) {
    const nrow = rows ? rows.length : logAlpha0.length;
    const logAlpha = new Float64Array(nrow);
    const iters = new Int32Array(nrow);
    const initialLp = new Float64Array(nrow);
    const lastLp = new Float64Array(nrow);
    for (let k = 0; k < nrow; k += 1) {
      if (tick && (k & 0xffff) === 0) {
        tick(k, nrow);
      }
      const o = (rows ? rows[k] : k) * m;
      let a = logAlpha0[k];
      const pm = priorMean ? priorMean[k] : 0;
      let lp = logPost(a, y, mu, o, m, group, pm, priorVar, usePrior);
      let dlp = dlogPost(a, y, mu, o, m, group, pm, priorVar, usePrior);
      let kappa = KAPPA_0;
      initialLp[k] = lp;
      let accepts = 0;
      let it = 0;
      for (let t = 0; t < MAXIT; t += 1) {
        it += 1;
        const aPropose = a + kappa * dlp;
        if (aPropose < -30.0) {
          kappa = (-30.0 - a) / dlp;
        }
        if (aPropose > 10.0) {
          kappa = (10.0 - a) / dlp;
        }
        const thetaKappa = -logPost(a + kappa * dlp, y, mu, o, m, group, pm, priorVar, usePrior);
        const thetaHatKappa = -lp - kappa * EPSILON * dlp * dlp;
        if (thetaKappa <= thetaHatKappa) {
          accepts += 1;
          a = a + kappa * dlp;
          const lpnew = logPost(a, y, mu, o, m, group, pm, priorVar, usePrior);
          const change = lpnew - lp;
          if (change < DISP_TOL) {
            lp = lpnew;
            break;
          }
          if (a < minLogAlpha) {
            break;
          }
          lp = lpnew;
          dlp = dlogPost(a, y, mu, o, m, group, pm, priorVar, usePrior);
          kappa = Math.min(kappa * 1.1, KAPPA_0);
          if (accepts % 5 === 0) {
            kappa = kappa / 2.0;
          }
        } else {
          kappa = kappa / 2.0;
        }
      }
      iters[k] = it;
      lastLp[k] = lp;
      logAlpha[k] = a;
    }
    return { logAlpha: logAlpha, iters: iters, initialLp: initialLp, lastLp: lastLp };
  }

  // _grid_refit: returns alpha (not log) for each listed row
  function gridRefit(y, mu, m, group, rows, priorMean, priorVar, usePrior, ncol) {
    const grid = linspace(Math.log(1e-8), Math.log(Math.max(10.0, ncol)), 20);
    const n = grid.length;
    const delta = grid[1] - grid[0];
    const out = new Float64Array(rows.length);
    for (let k = 0; k < rows.length; k += 1) {
      const o = rows[k] * m;
      const pm = priorMean ? priorMean[k] : 0;
      let best = -Infinity;
      let aHat = grid[0];
      for (let t = 0; t < n; t += 1) {
        const lp = logPost(grid[t], y, mu, o, m, group, pm, priorVar, usePrior);
        if (lp > best) {
          best = lp;
          aHat = grid[t];
        }
      }
      best = -Infinity;
      const lo = aHat - delta;
      const step = 2.0 * delta / (n - 1);
      let res = lo;
      for (let t = 0; t < n; t += 1) {
        const a = lo + step * t;
        const lp = logPost(a, y, mu, o, m, group, pm, priorVar, usePrior);
        if (lp > best) {
          best = lp;
          res = a;
        }
      }
      out[k] = Math.exp(res);
    }
    return out;
  }

  // ---- IRLS for the GLM coefficients (fitBeta) ------------------------------------

  function dnbinomLog(y, size, mu) {
    if (y === 0) {
      return size * (size < mu ? Math.log(size / (size + mu)) : Math.log1p(-mu / (size + mu)));
    }
    return (lgamma(y + size) - lgamma(size) - lgamma(y + 1.0) +
      size * Math.log(size / (size + mu)) + y * Math.log(mu / (size + mu)));
  }

  // Design: x[j] = [1, two[j]]; nf[j] = size factor j (same for every row).
  function fitBeta(y, n, m, sf, two, disp, beta0, lam, tick, wantHat) {
    const beta = new Float64Array(n * 2);
    const variance = new Float64Array(n * 2);
    const hat = wantHat ? new Float64Array(n * m) : null;
    const iters = new Int32Array(n);
    const mu = new Float64Array(m);
    const wv = new Float64Array(m);
    const ia = new Float64Array(4);
    for (let i = 0; i < n; i += 1) {
      if (tick && (i & 0xffff) === 0) {
        tick(i, n);
      }
      const o = i * m;
      const alpha = disp[i];
      let b0 = beta0[2 * i];
      let b1 = beta0[2 * i + 1];
      for (let j = 0; j < m; j += 1) {
        const eta = b0 + two[j] * b1;
        mu[j] = Math.max(sf[j] * Math.exp(eta), MIN_MU);
      }
      let devOld = 0.0;
      let it = 0;
      for (let t = 0; t < MAXIT; t += 1) {
        it += 1;
        let a00 = 0, a01 = 0, a10 = 0, a11 = 0, bb0 = 0, bb1 = 0;
        for (let j = 0; j < m; j += 1) {
          const w = mu[j] / (1.0 + alpha * mu[j]);
          const z = Math.log(mu[j] / sf[j]) + (y[o + j] - mu[j]) / mu[j];
          const xj1 = two[j];
          bb0 += 1 * w * z;
          bb1 += xj1 * w * z;
          a00 += 1 * w * 1;
          a01 += 1 * w * xj1;
          a10 += xj1 * w * 1;
          a11 += xj1 * w * xj1;
        }
        a00 += lam;
        a11 += lam;
        inv2(a00, a01, a10, a11, ia);
        b0 = ia[0] * bb0 + ia[1] * bb1;
        b1 = ia[2] * bb0 + ia[3] * bb1;
        if (Math.abs(b0) > LARGE || Math.abs(b1) > LARGE) {
          it = MAXIT;
          break;
        }
        for (let j = 0; j < m; j += 1) {
          const eta = b0 + two[j] * b1;
          mu[j] = Math.max(sf[j] * Math.exp(eta), MIN_MU);
        }
        let dev = 0.0;
        for (let j = 0; j < m; j += 1) {
          dev += -2.0 * dnbinomLog(y[o + j], 1.0 / alpha, mu[j]);
        }
        const conv = Math.abs(dev - devOld) / (Math.abs(dev) + 0.1);
        if (Number.isNaN(conv)) {
          it = MAXIT;
          break;
        }
        if (t > 0 && conv < BETA_TOL) {
          break;
        }
        devOld = dev;
      }
      iters[i] = it;
      beta[2 * i] = b0;
      beta[2 * i + 1] = b1;
      let x00 = 0, x01 = 0, x10 = 0, x11 = 0;
      for (let j = 0; j < m; j += 1) {
        wv[j] = mu[j] / (1.0 + alpha * mu[j]);
        const xj1 = two[j];
        x00 += 1 * wv[j] * 1;
        x01 += 1 * wv[j] * xj1;
        x10 += xj1 * wv[j] * 1;
        x11 += xj1 * wv[j] * xj1;
      }
      inv2(x00 + lam, x01, x10, x11 + lam, ia);
      const xt = [x00, x01, x10, x11];
      for (let k = 0; k < 2; k += 1) {
        let v = 0.0;
        for (let l = 0; l < 2; l += 1) {
          for (let q = 0; q < 2; q += 1) {
            v += ia[2 * k + l] * xt[2 * l + q] * ia[2 * q + k];
          }
        }
        variance[2 * i + k] = v;
      }
      for (let j = 0; hat && j < m; j += 1) {
        const xj = [1, two[j]];
        let h = 0.0;
        for (let k = 0; k < 2; k += 1) {
          for (let l = 0; l < 2; l += 1) {
            h += xj[k] * ia[2 * k + l] * xj[l];
          }
        }
        hat[o + j] = wv[j] * h;
      }
    }
    return { beta: beta, variance: variance, hat: hat, iters: iters };
  }

  // fitNbinomGLMsOptim for one row (projected Fisher scoring, log2 scale)
  function optimRow(k, nf, two, alpha, start) {
    const m = k.length;
    const r = 1.0 / alpha;
    const lam = LAMBDA;
    const muOf = function (b) {
      const out = new Float64Array(m);
      for (let j = 0; j < m; j += 1) {
        out[j] = nf[j] * Math.pow(2, b[0] + two[j] * b[1]);
      }
      return out;
    };
    const objective = function (b) {
      const mu = muOf(b);
      let ll = 0;
      for (let j = 0; j < m; j += 1) {
        ll += dnbinomLog(k[j], r, mu[j]);
      }
      const val = -(ll - 0.5 * (lam * b[0] * b[0] + lam * b[1] * b[1]));
      return Number.isFinite(val) ? val : 1e300;
    };
    const clip = function (v) { return Math.min(LARGE, Math.max(-LARGE, v)); };
    const L2 = LN2 * LN2;
    let b = [clip(start[0]), clip(start[1])];
    let f = objective(b);
    let converged = false;
    for (let iter = 0; iter < 200; iter += 1) {
      const mu = muOf(b);
      let g0 = 0, g1 = 0, i00 = 0, i01 = 0, i11 = 0;
      for (let j = 0; j < m; j += 1) {
        // numpy: (ln2 * x.T) @ v and (ln2 ** 2 * (x.T * w)) @ x
        const resid = r * (k[j] - mu[j]) / (r + mu[j]);
        g0 += (LN2 * 1) * resid;
        g1 += (LN2 * two[j]) * resid;
        const w = mu[j] * r / (r + mu[j]);
        i00 += (L2 * (1 * w)) * 1;
        i01 += (L2 * (1 * w)) * two[j];
        i11 += (L2 * (two[j] * w)) * two[j];
      }
      const grad = [-g0 + lam * b[0], -g1 + lam * b[1]];
      const info = [[i00 + lam, i01], [i01, i11 + lam]];
      let step;
      try {
        step = solve(info, grad);
      } catch (error) {
        step = grad;
      }
      let t = 1.0;
      let cand;
      let fc;
      for (;;) {
        cand = [clip(b[0] - t * step[0]), clip(b[1] - t * step[1])];
        fc = objective(cand);
        if (fc <= f || t < 1e-10) {
          break;
        }
        t /= 2;
      }
      const moved = Math.max(Math.abs(cand[0] - b[0]), Math.abs(cand[1] - b[1]));
      const fOld = f;
      b = cand;
      f = fc;
      if (Math.abs(fOld - f) <= 1e-12 * (Math.abs(f) + 1e-12) || moved < 1e-10) {
        converged = true;
        break;
      }
    }
    const mu = muOf(b);
    let x00 = 0, x01 = 0, x11 = 0;
    for (let j = 0; j < m; j += 1) {
      const muj = Math.max(mu[j], MIN_MU);
      const w = muj / (1.0 + alpha * muj);
      x00 += w;
      x01 += w * two[j];
      x11 += w * two[j] * two[j];
    }
    const ridge = lam / (LN2 * LN2);
    const inv = new Float64Array(4);
    inv2(x00 + ridge, x01, x01, x11 + ridge, inv);
    const xt = [x00, x01, x01, x11];
    const se = [0, 0];
    for (let kk = 0; kk < 2; kk += 1) {
      let v = 0.0;
      for (let l = 0; l < 2; l += 1) {
        for (let q = 0; q < 2; q += 1) {
          v += inv[2 * kk + l] * xt[2 * l + q] * inv[2 * q + kk];
        }
      }
      se[kk] = Math.sqrt(Math.max(v, 0.0)) / LN2;
    }
    return { beta: b, se: se, converged: converged };
  }

  // ---- R helpers -------------------------------------------------------------------

  // p.adjust(p, "BH") over the listed entries; others are NaN.
  function pAdjustBH(p, use) {
    const n0 = p.length;
    const out = new Float64Array(n0).fill(NaN);
    let n = 0;
    for (let i = 0; i < n0; i += 1) {
      if ((!use || use[i]) && !Number.isNaN(p[i])) {
        n += 1;
      }
    }
    if (!n) {
      return out;
    }
    const idx = new Uint32Array(n);
    let k = 0;
    for (let i = 0; i < n0; i += 1) {
      if ((!use || use[i]) && !Number.isNaN(p[i])) {
        idx[k] = i;
        k += 1;
      }
    }
    const order = sortIndexByValue(idx, p);
    // order is ascending in p; rank k (1-based) -> n / k * p, cumulative min from the top
    let running = Infinity;
    for (let r = n; r >= 1; r -= 1) {
      const i = order[r - 1];
      const v = n / r * p[i];
      if (v < running) {
        running = v;
      }
      out[i] = Math.min(1.0, running);
    }
    return out;
  }

  // indices sorted by value (ascending, ties by index)
  // indices sorted by value (ascending, ties by position in `indices`, which
  // callers pass in increasing order): stable LSD radix sort on the IEEE bits,
  // valid for non-negative values (p-values); a comparison sort otherwise.
  function sortIndexByValue(indices, values) {
    const n = indices.length;
    let nonNegative = true;
    for (let k = 0; k < n; k += 1) {
      if (!(values[indices[k]] >= 0)) {
        nonNegative = false;
        break;
      }
    }
    if (!nonNegative || n < 64) {
      const arr = Array.from(indices);
      arr.sort(function (a, b) { return values[a] - values[b] || a - b; });
      return Uint32Array.from(arr);
    }
    const bits = new Float64Array(n);
    const words = new Uint32Array(bits.buffer);
    for (let k = 0; k < n; k += 1) {
      bits[k] = values[indices[k]] + 0; // -0 -> +0
    }
    let pos = new Uint32Array(n);
    let tmp = new Uint32Array(n);
    for (let k = 0; k < n; k += 1) {
      pos[k] = k;
    }
    const counts = new Uint32Array(65537);
    // four 16-bit digits, least significant first
    for (let pass = 0; pass < 4; pass += 1) {
      const word = pass < 2 ? LO : HI;
      const shift = pass % 2 ? 16 : 0;
      counts.fill(0);
      for (let k = 0; k < n; k += 1) {
        counts[((words[2 * pos[k] + word] >>> shift) & 0xffff) + 1] += 1;
      }
      for (let d = 1; d <= 65536; d += 1) {
        counts[d] += counts[d - 1];
      }
      for (let k = 0; k < n; k += 1) {
        const digit = (words[2 * pos[k] + word] >>> shift) & 0xffff;
        tmp[counts[digit]] = pos[k];
        counts[digit] += 1;
      }
      const swap = pos;
      pos = tmp;
      tmp = swap;
    }
    const out = new Uint32Array(n);
    for (let k = 0; k < n; k += 1) {
      out[k] = indices[pos[k]];
    }
    return out;
  }

  function quantile7(sorted, probs) {
    const n = sorted.length;
    const out = new Float64Array(probs.length);
    for (let k = 0; k < probs.length; k += 1) {
      const h = (n - 1) * probs[k];
      let lo = Math.floor(h + 4 * EPS);
      lo = Math.min(Math.max(lo, 0), n - 1);
      const hi = Math.min(Math.max(lo + 1, 0), n - 1);
      let frac = h - lo;
      if (Math.abs(frac) < 4 * EPS) {
        frac = 0.0;
      }
      out[k] = sorted[lo] + frac * (sorted[hi] - sorted[lo]);
    }
    return out;
  }

  // R lowess (clowess); x sorted.
  function lowess(xIn, yIn, f, nsteps, delta) {
    const x = Float64Array.from(xIn);
    const y = Float64Array.from(yIn);
    const n = x.length;
    f = f == null ? 2 / 3 : f;
    nsteps = nsteps == null ? 3 : nsteps;
    if (delta == null) {
      delta = 0.01 * (x[n - 1] - x[0]);
    }
    const ys = new Float64Array(n);
    if (n < 2) {
      ys.set(y);
      return ys;
    }
    const rw = new Float64Array(n);
    const res = new Float64Array(n);
    const ns = Math.max(2, Math.min(n, Math.trunc(f * n + 1e-7)));

    function lowest(xs, nleft, nright, userw) {
      const rng = x[n - 1] - x[0];
      const h = Math.max(xs - x[nleft], x[nright] - xs);
      const h9 = 0.999 * h;
      const h1 = 0.001 * h;
      const w = res;
      let a = 0.0;
      let j = nleft;
      while (j < n) {
        w[j] = 0.0;
        const r = Math.abs(x[j] - xs);
        if (r <= h9) {
          w[j] = r <= h1 ? 1.0 : Math.pow(1.0 - Math.pow(r / h, 3), 3);
          if (userw) {
            w[j] *= rw[j];
          }
          a += w[j];
        } else if (x[j] > xs) {
          break;
        }
        j += 1;
      }
      const nrt = j - 1;
      if (a <= 0.0) {
        return null;
      }
      for (let k = nleft; k <= nrt; k += 1) {
        w[k] /= a;
      }
      if (h > 0.0) {
        a = 0;
        for (let k = nleft; k <= nrt; k += 1) {
          a += w[k] * x[k];
        }
        let b = xs - a;
        let c = 0;
        for (let k = nleft; k <= nrt; k += 1) {
          c += w[k] * Math.pow(x[k] - a, 2);
        }
        if (Math.sqrt(c) > 0.001 * rng) {
          b /= c;
          for (let k = nleft; k <= nrt; k += 1) {
            w[k] *= b * (x[k] - a) + 1.0;
          }
        }
      }
      let s = 0;
      for (let k = nleft; k <= nrt; k += 1) {
        s += w[k] * y[k];
      }
      return s;
    }

    let it = 1;
    while (it <= nsteps + 1) {
      let nleft = 0;
      let nright = ns - 1;
      let last = -1;
      let i = 0;
      for (;;) {
        if (nright < n - 1) {
          const d1 = x[i] - x[nleft];
          const d2 = x[nright + 1] - x[i];
          if (d1 > d2) {
            nleft += 1;
            nright += 1;
            continue;
          }
        }
        const val = lowest(x[i], nleft, nright, it > 1);
        ys[i] = val === null ? y[i] : val;
        if (last < i - 1) {
          const denom = x[i] - x[last];
          for (let j = last + 1; j < i; j += 1) {
            const alpha = (x[j] - x[last]) / denom;
            ys[j] = alpha * ys[i] + (1.0 - alpha) * ys[last];
          }
        }
        last = i;
        const cut = x[last] + delta;
        i = last + 1;
        while (i < n) {
          if (x[i] > cut) {
            break;
          }
          if (x[i] === x[last]) {
            ys[i] = ys[last];
            last = i;
          }
          i += 1;
        }
        i = Math.max(last + 1, i - 1);
        if (last >= n - 1) {
          break;
        }
      }
      const absRes = new Float64Array(n);
      for (let k = 0; k < n; k += 1) {
        res[k] = y[k] - ys[k];
        absRes[k] = Math.abs(res[k]);
      }
      const sc = npSum(absRes) / n;
      if (it > nsteps) {
        break;
      }
      const ar = sortedCopy(absRes);
      const m1 = Math.floor(n / 2);
      const cmad = n % 2 === 0 ? 3.0 * (ar[m1] + ar[n - m1 - 1]) : 6.0 * ar[m1];
      if (cmad < 1e-7 * sc) {
        break;
      }
      const c9 = 0.999 * cmad;
      const c1 = 0.001 * cmad;
      for (let k = 0; k < n; k += 1) {
        const r = absRes[k];
        rw[k] = r <= c1 ? 1.0 : (r <= c9 ? Math.pow(1.0 - Math.pow(r / cmad, 2), 2) : 0.0);
      }
      it += 1;
    }
    return ys;
  }

  // predict(loess(y ~ x, span), at) for one predictor (interpolated surface)
  function loess(xIn, yIn, at, span, cell) {
    cell = cell == null ? 0.2 : cell;
    const n = xIn.length;
    const order = Array.from({ length: n }, function (_, i) { return i; })
      .sort(function (a, b) { return xIn[a] - xIn[b] || a - b; });
    const xs = new Float64Array(n);
    const ys = new Float64Array(n);
    order.forEach(function (k, i) { xs[i] = xIn[k]; ys[i] = yIn[k]; });
    const q = Math.floor(n * span);
    const fc = Math.floor(n * span * cell);
    const margin = 0.005 * Math.max(xs[n - 1] - xs[0], 1e-10 * Math.max(Math.abs(xs[0]), Math.abs(xs[n - 1])) + 1e-30);
    const vertices = new Set([xs[0] - margin, xs[n - 1] + margin]);
    const stack = [[0, n - 1, xs[0] - margin, xs[n - 1] + margin]];
    while (stack.length) {
      const item = stack.pop();
      const lo = item[0], hi = item[1], left = item[2], right = item[3];
      if (hi - lo + 1 <= fc) {
        continue;
      }
      const mid = Math.floor((lo + hi + 2) / 2) - 1;
      const split = xs[mid];
      if (split === left || split === right) {
        continue;
      }
      vertices.add(split);
      stack.push([lo, mid, left, split]);
      stack.push([mid + 1, hi, split, right]);
    }
    const vx = Float64Array.from(vertices);
    vx.sort();
    const d = new Float64Array(n);
    const w = new Float64Array(n);
    const t = new Float64Array(n);
    const tmp = new Float64Array(n);
    function local(x0) {
      for (let i = 0; i < n; i += 1) {
        d[i] = Math.abs(xs[i] - x0);
      }
      const ds = sortedCopy(d);
      const h = Math.max(ds[q - 1], 1e-12);
      for (let i = 0; i < n; i += 1) {
        w[i] = d[i] < h ? Math.pow(1 - Math.pow(d[i] / h, 3), 3) : 0.0;
        t[i] = xs[i] - x0;
      }
      const powT = function (i, e) {
        if (e === 0) { return 1; }
        if (e === 1) { return t[i]; }
        if (e === 2) { return t[i] * t[i]; }
        return Math.pow(t[i], e);
      };
      const a = [];
      for (let r = 0; r < 3; r += 1) {
        const row = [];
        for (let c = 0; c < 3; c += 1) {
          for (let i = 0; i < n; i += 1) {
            tmp[i] = w[i] * powT(i, r + c);
          }
          row.push(npSum(tmp));
        }
        a.push(row);
      }
      const b = [];
      for (let r = 0; r < 3; r += 1) {
        for (let i = 0; i < n; i += 1) {
          tmp[i] = w[i] * ys[i] * powT(i, r);
        }
        b.push(npSum(tmp));
      }
      const coef = solve(a, b);
      return [coef[0], coef[1]];
    }
    const fits = Array.from(vx, function (v) { return local(v); });
    const out = new Float64Array(at.length);
    for (let k = 0; k < at.length; k += 1) {
      const v = at[k];
      // searchsorted(vx, v, side="right") - 1, clipped to [0, len - 2]
      let lo = 0;
      let hi = vx.length;
      while (lo < hi) {
        const mid = (lo + hi) >> 1;
        if (vx[mid] <= v) {
          lo = mid + 1;
        } else {
          hi = mid;
        }
      }
      const i = Math.min(Math.max(lo - 1, 0), vx.length - 2);
      const width = vx[i + 1] - vx[i];
      const tt = (v - vx[i]) / width;
      out[k] = (Math.pow(1 - tt, 2) * (1 + 2 * tt) * fits[i][0] + Math.pow(tt, 2) * (3 - 2 * tt) * fits[i + 1][0] +
        (tt * Math.pow(1 - tt, 2) * fits[i][1] - Math.pow(tt, 2) * (1 - tt) * fits[i + 1][1]) * width);
    }
    return out;
  }

  // ---- prior variance for few residual degrees of freedom ---------------------------

  // estimateDispersionsPriorVar for 1 <= m - p <= 3: the exact density of
  // log(chisq(df)/df) + N(0, v) on the Desktop port's grid, by numerical
  // convolution.  Only the CDF at the histogram edges is needed, so it is
  // computed as base * cumulative-kernel at those points (same quantity as
  // cumsum(convolve(base, kernel, "same")), without the full convolution).
  function priorVarFewDf(residuals, df) {
    const brks = new Float64Array(41);
    for (let i = 0; i < 41; i += 1) {
      brks[i] = (i - 20) / 2.0;
    }
    const obsCounts = new Float64Array(40);
    let nObs = 0;
    for (let k = 0; k < residuals.length; k += 1) {
      const v = residuals[k];
      if (!(v > brks[0] && v < brks[40])) {
        continue;
      }
      let b = Math.floor((v - brks[0]) * 2);
      b = Math.min(Math.max(b, 0), 39);
      while (b > 0 && v < brks[b]) {
        b -= 1;
      }
      while (b < 39 && v >= brks[b + 1]) {
        b += 1;
      }
      obsCounts[b] += 1;
      nObs += 1;
    }
    const obsDens = new Float64Array(40);
    for (let b = 0; b < 40; b += 1) {
      obsDens[b] = obsCounts[b] / (nObs * 0.5);
    }
    const step = 0.005;
    const start = -40.0;
    const delta = (start + step) - start;
    const nt = Math.ceil(((12.0 + step) - start) / step);
    const t = new Float64Array(nt);
    for (let i = 0; i < nt; i += 1) {
      t[i] = start + i * delta;
    }
    const k = df / 2.0;
    const base = new Float64Array(nt);
    const lgk = lgammaExact(k);
    for (let i = 0; i < nt; i += 1) {
      base[i] = Math.exp((Math.log(df) + t[i]) * k - df * Math.exp(t[i]) / 2 - k * Math.log(2.0) - lgk);
    }
    const baseSum = npSum(base) * step;
    for (let i = 0; i < nt; i += 1) {
      base[i] /= baseSum;
    }
    // xp: cell edges of the grid (len nt + 1)
    const xp = new Float64Array(nt + 1);
    for (let i = 0; i < nt; i += 1) {
      xp[i] = t[i] - step / 2;
    }
    xp[nt] = t[nt - 1] + step / 2;
    // for each break: interval j with xp[j] <= brk < xp[j + 1]
    const brkIndex = new Int32Array(41);
    for (let b = 0; b < 41; b += 1) {
      let lo = 0;
      let hi = nt + 1;
      while (lo < hi) {
        const mid = (lo + hi) >> 1;
        if (xp[mid] <= brks[b]) {
          lo = mid + 1;
        } else {
          hi = mid;
        }
      }
      brkIndex[b] = lo - 1;
    }
    const grid = linspace(0.0, 8.0, 200);
    const kls = new Float64Array(grid.length);
    const cdfAt = new Map();
    const baseCum = new Float64Array(nt + 1);
    for (let i = 0; i < nt; i += 1) {
      baseCum[i + 1] = baseCum[i] + base[i];
    }
    for (let gi = 0; gi < grid.length; gi += 1) {
      const v = grid[gi];
      cdfAt.clear();
      let cdf;
      if (v === 0) {
        // cumsum(base) * step
        let acc = 0;
        const cum = new Float64Array(nt + 1);
        for (let i = 0; i < nt; i += 1) {
          acc += base[i];
          cum[i + 1] = acc * step;
        }
        cdf = function (idx) { return cum[idx]; };
      } else {
        const sd = Math.sqrt(v);
        const half = Math.ceil(8 * sd / step);
        const K = 2 * half + 1;
        const kernel = new Float64Array(K);
        for (let i = 0; i < K; i += 1) {
          const z = (i - half) * step / sd;
          kernel[i] = Math.exp(-0.5 * (z * z));
        }
        const ksum = npSum(kernel);
        for (let i = 0; i < K; i += 1) {
          kernel[i] /= ksum;
        }
        const kc = new Float64Array(K);
        let acc = 0;
        for (let i = 0; i < K; i += 1) {
          acc += kernel[i];
          kc[i] = acc;
        }
        const KC = function (idx) { return idx < 0 ? 0 : (idx >= K - 1 ? kc[K - 1] : kc[idx]); };
        // cdf[c] = step * sum_{i < c} dens[i];  dens[i] = sum_j base[j] kernel[i + half - j]
        cdf = function (c) {
          if (cdfAt.has(c)) {
            return cdfAt.get(c);
          }
          let s = 0;
          if (c > 0) {
            const jLo = Math.max(0, -half);
            const jHi = Math.min(nt - 1, c - 1 + half);
            for (let j = jLo; j <= jHi; j += 1) {
              const upper = KC(c - 1 + half - j);
              const lower = KC(half - j - 1);
              if (upper !== lower) {
                s += base[j] * (upper - lower);
              }
            }
          }
          const value = s * step;
          cdfAt.set(c, value);
          return value;
        };
      }
      const edges = new Float64Array(41);
      for (let b = 0; b < 41; b += 1) {
        const j = brkIndex[b];
        const x0 = xp[j];
        const x1 = xp[j + 1];
        const f0 = cdf(j);
        const f1 = cdf(j + 1);
        const slope = (f1 - f0) / (x1 - x0);
        edges[b] = slope * (brks[b] - x0) + f0;
      }
      const mass = new Float64Array(40);
      for (let b = 0; b < 40; b += 1) {
        mass[b] = Math.max(0, edges[b + 1] - edges[b]);
      }
      const total = npSum(mass);
      const randDens = new Float64Array(40);
      for (let b = 0; b < 40; b += 1) {
        randDens[b] = total > 0 ? mass[b] / (total * 0.5) : mass[b];
      }
      let small = Infinity;
      for (let b = 0; b < 40; b += 1) {
        if (obsDens[b] > 0 && obsDens[b] < small) { small = obsDens[b]; }
        if (randDens[b] > 0 && randDens[b] < small) { small = randDens[b]; }
      }
      const terms = new Float64Array(40);
      for (let b = 0; b < 40; b += 1) {
        terms[b] = obsDens[b] * (Math.log(obsDens[b] + small) - Math.log(randDens[b] + small));
      }
      kls[gi] = npSum(terms);
    }
    const fine = linspace(0.0, 8.0, 1000);
    const smooth = loess(grid, kls, fine, 0.2);
    let best = 0;
    for (let i = 1; i < smooth.length; i += 1) {
      if (smooth[i] < smooth[best]) {
        best = i;
      }
    }
    return Math.max(fine[best], 0.25);
  }

  // lgamma for the few non-integer half-integer arguments (k = df / 2)
  function lgammaExact(x) {
    return lgamma(x);
  }

  // ---- dispersion trend ----------------------------------------------------------------

  function parametricFit(means, disps) {
    let coefs = [0.1, 1.0];
    const n = means.length;
    for (let outer = 0; outer < 11; outer += 1) {
      const good = [];
      for (let i = 0; i < n; i += 1) {
        const resid = disps[i] / (coefs[0] + coefs[1] / means[i]);
        if (resid > 1e-4 && resid < 15) {
          good.push(i);
        }
      }
      const g = good.length;
      const yv = new Float64Array(g);
      const xv = new Float64Array(g);
      good.forEach(function (i, k) { yv[k] = disps[i]; xv[k] = 1.0 / means[i]; });
      const tmp = new Float64Array(g);
      const tmp2 = new Float64Array(g);
      let c = coefs.slice();
      let converged = false;
      const deviance = function (cc) {
        for (let k = 0; k < g; k += 1) {
          const mu = cc[0] + cc[1] * xv[k];
          if (!Number.isFinite(mu) || !(mu > 0)) {
            return null;
          }
          tmp[k] = Math.log(yv[k] / mu) - (yv[k] - mu) / mu;
        }
        return -2.0 * npSum(tmp);
      };
      let devOld = deviance(c);
      if (devOld === null) {
        throw new Error("parametric dispersion fit failed");
      }
      for (let glm = 0; glm < 25; glm += 1) {
        const sums = [0, 0, 0, 0, 0];
        for (let k = 0; k < g; k += 1) {
          const mu = c[0] + c[1] * xv[k];
          tmp2[k] = 1.0 / Math.pow(mu, 2);
        }
        sums[0] = npSum(tmp2);
        for (let k = 0; k < g; k += 1) { tmp[k] = tmp2[k] * xv[k]; }
        sums[1] = npSum(tmp);
        for (let k = 0; k < g; k += 1) { tmp[k] = tmp2[k] * xv[k] * xv[k]; }
        sums[2] = npSum(tmp);
        for (let k = 0; k < g; k += 1) { tmp[k] = tmp2[k] * yv[k]; }
        sums[3] = npSum(tmp);
        for (let k = 0; k < g; k += 1) { tmp[k] = tmp2[k] * xv[k] * yv[k]; }
        sums[4] = npSum(tmp);
        const sw = sums[0], swx = sums[1], swxx = sums[2], swy = sums[3], swxy = sums[4];
        const det = sw * swxx - swx * swx;
        let next = [(swxx * swy - swx * swxy) / det, (sw * swxy - swx * swy) / det];
        let dev = deviance(next);
        let halvings = 0;
        while (dev === null) {
          halvings += 1;
          if (halvings > 25) {
            throw new Error("parametric dispersion fit failed");
          }
          next = [(next[0] + c[0]) / 2, (next[1] + c[1]) / 2];
          dev = deviance(next);
        }
        c = next;
        if (Math.abs(dev - devOld) / (Math.abs(dev) + 0.1) < 1e-8) {
          converged = true;
          break;
        }
        devOld = dev;
      }
      const old = coefs;
      coefs = c;
      if (!(coefs[0] > 0 && coefs[1] > 0)) {
        throw new Error("parametric dispersion fit failed");
      }
      const l0 = Math.log(coefs[0] / old[0]);
      const l1 = Math.log(coefs[1] / old[1]);
      if (l0 * l0 + l1 * l1 < 1e-6 && converged) {
        return coefs;
      }
    }
    throw new Error("parametric dispersion fit did not converge");
  }

  // localDispersionFit: locfit(log(disp) ~ log(mean), weights = mean)
  function localFit(means, disps, query, alphaSpan, cut) {
    alphaSpan = alphaSpan == null ? 0.7 : alphaSpan;
    cut = cut == null ? 0.8 : cut;
    const keep = [];
    for (let i = 0; i < disps.length; i += 1) {
      if (disps[i] >= MIN_DISP * 10) {
        keep.push(i);
      }
    }
    if (!keep.length) {
      return new Float64Array(query.length).fill(MIN_DISP);
    }
    const n = keep.length;
    const x = new Float64Array(n);
    const yv = new Float64Array(n);
    const w0 = new Float64Array(n);
    keep.forEach(function (i, k) { x[k] = Math.log(means[i]); yv[k] = Math.log(disps[i]); w0[k] = means[i]; });
    const kk = Math.max(0, Math.min(n, Math.trunc(n * alphaSpan)) - 1);
    const d = new Float64Array(n);
    const w = new Float64Array(n);
    const tmp = new Float64Array(n);
    function vertex(x0) {
      for (let i = 0; i < n; i += 1) {
        d[i] = Math.abs(x[i] - x0);
      }
      let h = sortedCopy(d)[kk];
      if (h <= 0) {
        let mx = 0;
        for (let i = 0; i < n; i += 1) { mx = Math.max(mx, d[i]); }
        h = mx || 1.0;
      }
      for (let i = 0; i < n; i += 1) {
        const u = d[i] / h;
        w[i] = u < 1 ? w0[i] * Math.pow(1 - Math.pow(u, 3), 3) : 0.0;
      }
      const sw = [];
      for (let p = 0; p < 5; p += 1) {
        for (let i = 0; i < n; i += 1) {
          const t = x[i] - x0;
          tmp[i] = w[i] * (p === 0 ? 1 : (p === 1 ? t : (p === 2 ? t * t : Math.pow(t, p))));
        }
        sw.push(npSum(tmp));
      }
      const sy = [];
      for (let p = 0; p < 3; p += 1) {
        for (let i = 0; i < n; i += 1) {
          const t = x[i] - x0;
          tmp[i] = w[i] * yv[i] * (p === 0 ? 1 : (p === 1 ? t : t * t));
        }
        sy.push(npSum(tmp));
      }
      let coef;
      try {
        coef = solve([[sw[0], sw[1], sw[2]], [sw[1], sw[2], sw[3]], [sw[2], sw[3], sw[4]]], sy);
      } catch (error) {
        coef = [sw[0] > 0 ? sy[0] / sw[0] : npMean(yv), 0.0, 0.0];
      }
      return [coef[0], coef[1], h];
    }
    let lo = Infinity;
    let hi = -Infinity;
    for (let i = 0; i < n; i += 1) {
      lo = Math.min(lo, x[i]);
      hi = Math.max(hi, x[i]);
    }
    const verts = new Map();
    verts.set(lo, vertex(lo));
    if (hi > lo) {
      verts.set(hi, vertex(hi));
      const stack = [[lo, hi]];
      while (stack.length) {
        const pair = stack.pop();
        const a = pair[0];
        const b = pair[1];
        if ((b - a) > cut * Math.min(verts.get(a)[2], verts.get(b)[2]) && verts.size < 4096) {
          const mid = (a + b) / 2;
          verts.set(mid, vertex(mid));
          stack.push([a, mid], [mid, b]);
        }
      }
    }
    const xs = Float64Array.from(verts.keys());
    xs.sort();
    const out = new Float64Array(query.length);
    if (xs.length === 1) {
      return out.fill(Math.exp(verts.get(lo)[0]));
    }
    // quadratic coefficient of lm(y ~ x + x^2, weights = w0): locfit's
    // parametric component, which it uses to extrapolate
    let curvature = 0.0;
    {
      const xm = npSum(Float64Array.from(x, function (v, i) { return w0[i] * v; })) / npSum(w0);
      const gw = [];
      const gy = [];
      for (let p = 0; p < 5; p += 1) {
        for (let i = 0; i < n; i += 1) { tmp[i] = w0[i] * Math.pow(x[i] - xm, p); }
        gw.push(npSum(tmp));
      }
      for (let p = 0; p < 3; p += 1) {
        for (let i = 0; i < n; i += 1) { tmp[i] = w0[i] * yv[i] * Math.pow(x[i] - xm, p); }
        gy.push(npSum(tmp));
      }
      try {
        curvature = solve([[gw[0], gw[1], gw[2]], [gw[1], gw[2], gw[3]], [gw[2], gw[3], gw[4]]], gy)[2];
      } catch (error) {
        curvature = 0.0;
      }
      if (!Number.isFinite(curvature)) {
        curvature = 0.0;
      }
    }
    for (let k = 0; k < query.length; k += 1) {
      const lq = Math.log(query[k]);
      let a0 = 0;
      let a1 = xs.length;
      while (a0 < a1) { // searchsorted left
        const mid = (a0 + a1) >> 1;
        if (xs[mid] < lq) {
          a0 = mid + 1;
        } else {
          a1 = mid;
        }
      }
      if (lq < lo || lq > hi) {
        // outside the data range locfit continues the boundary vertex's value
        // and slope with the curvature of the global weighted quadratic
        const edge = lq < lo ? lo : hi;
        const fe = verts.get(edge);
        const dx = lq - edge;
        out[k] = Math.exp(fe[0] + fe[1] * dx + curvature * dx * dx);
        continue;
      }
      const i = Math.min(Math.max(a0 - 1, 0), xs.length - 2);
      const a = xs[i];
      const b = xs[i + 1];
      const fa = verts.get(a);
      const fb = verts.get(b);
      const length = b - a;
      const t = (lq - a) / length;
      const t2 = t * t;
      const t3 = t2 * t;
      const val = (2 * t3 - 3 * t2 + 1) * fa[0] + (t3 - 2 * t2 + t) * length * fa[1] +
        (-2 * t3 + 3 * t2) * fb[0] + (t3 - t2) * length * fb[1];
      out[k] = Math.exp(val);
    }
    return out;
  }

  // ---- the whole test ---------------------------------------------------------------------

  function trimmedMeanRow(values, trim) {
    const n = values.length;
    const lo = Math.floor(n * trim);
    if (lo === 0) {
      let s = 0;
      for (let i = 0; i < n; i += 1) { s += values[i]; }
      return s / n;
    }
    const s = values.slice().sort(function (a, b) { return a - b; });
    let acc = 0;
    for (let i = lo; i < n - lo; i += 1) { acc += s[i]; }
    return acc / (n - 2 * lo);
  }

  // DESeq() then results(contrast = one vs two, alpha) on a row-major
  // integer matrix (n rows x m samples, Float64Array).  log2FoldChange is
  // condition one over condition two.
  // ---- row-parallel tasks ---------------------------------------------------------
  // The per-row stages (gene-wise and MAP dispersion, IRLS) are yielded as
  // tasks by the step generators below.  runTask runs one on this thread;
  // splitTask / mergeTask cut one into contiguous row ranges for helper
  // workers (deseq.worker.js).  Rows are independent, so the result is the
  // same bit for bit however a task is split.

  function runTask(task, tick) {
    const a = task.args;
    if (task.kind === "fitDisp") {
      return fitDisp(a.y, a.mu, a.m, a.group, null, a.logAlpha0, a.priorMean, a.priorVar, a.usePrior, a.minLogAlpha, tick);
    }
    if (task.kind === "fitBeta") {
      return fitBeta(a.y, a.disp.length, a.m, a.sf, a.two, a.disp, a.beta0, a.lam, tick, a.wantHat);
    }
    throw new Error("unknown task " + task.kind);
  }

  const ROW_FIELDS = {
    fitDisp: { y: "m", mu: "m", logAlpha0: 1, priorMean: 1 },
    fitBeta: { y: "m", disp: 1, beta0: 2 }
  };
  const RESULT_FIELDS = {
    fitDisp: { logAlpha: 1, iters: 1, initialLp: 1, lastLp: 1 },
    fitBeta: { beta: 2, variance: 2, hat: "m", iters: 1 }
  };

  function splitTask(task, parts) {
    const n = task.rows;
    const m = task.args.m;
    const k = Math.max(1, Math.min(parts, Math.ceil(n / 2048)));
    const out = [];
    for (let p = 0; p < k; p += 1) {
      const start = Math.floor(n * p / k);
      const end = Math.floor(n * (p + 1) / k);
      const args = Object.assign({}, task.args);
      Object.keys(ROW_FIELDS[task.kind]).forEach(function (field) {
        const width = ROW_FIELDS[task.kind][field] === "m" ? m : ROW_FIELDS[task.kind][field];
        if (args[field]) {
          args[field] = args[field].slice(start * width, end * width);
        }
      });
      const transfer = Object.keys(ROW_FIELDS[task.kind]).filter(function (field) { return args[field]; })
        .map(function (field) { return args[field].buffer; });
      out.push({ kind: task.kind, rows: end - start, args: args, transfer: transfer });
    }
    return out;
  }

  function mergeTask(task, results) {
    const merged = {};
    const m = task.args.m;
    Object.keys(RESULT_FIELDS[task.kind]).forEach(function (field) {
      if (!results[0][field]) {
        merged[field] = null;
        return;
      }
      const width = RESULT_FIELDS[task.kind][field] === "m" ? m : RESULT_FIELDS[task.kind][field];
      const out = new results[0][field].constructor(task.rows * width);
      let offset = 0;
      results.forEach(function (part) {
        out.set(part[field], offset);
        offset += part[field].length;
      });
      merged[field] = out;
    });
    return merged;
  }

  function taskTransfers(value) {
    const list = [];
    Object.keys(value || {}).forEach(function (key) {
      const v = value[key];
      if (v && v.buffer instanceof ArrayBuffer && list.indexOf(v.buffer) < 0 && v.byteLength === v.buffer.byteLength) {
        list.push(v.buffer);
      }
    });
    return list;
  }

  function driveSync(steps, progress) {
    let step = steps.next();
    while (!step.done) {
      const task = step.value;
      step = steps.next(runTask(task, function (k, total) { progress(task.stage, task.label, k / total); }));
    }
    return step.value;
  }

  // runner(task) -> Promise of the task's result (e.g. split over workers)
  function driveAsync(steps, runner) {
    return new Promise(function (resolve, reject) {
      function advance(value) {
        let step;
        try {
          step = steps.next(value);
        } catch (error) {
          reject(error);
          return;
        }
        if (step.done) {
          resolve(step.value);
          return;
        }
        Promise.resolve(runner(step.value)).then(advance, reject);
      }
      advance(undefined);
    });
  }

  function deseqTwoConditions(counts, n, m, conditionOne, options) {
    const opts = options || {};
    const progress = typeof opts.progress === "function" ? opts.progress : function () {};
    return driveSync(deseqSteps(counts, n, m, conditionOne, opts), progress);
  }

  function* deseqSteps(counts, n, m, conditionOne, options) {
    const opts = options || {};
    const alpha = opts.alpha == null ? DEFAULTS.alpha : opts.alpha;
    const cooksCutoff = opts.cooksCutoff !== false;
    const progress = typeof opts.progress === "function" ? opts.progress : function () {};
    const y = counts;
    const one = Array.from(conditionOne, Boolean);
    const nOne = one.filter(Boolean).length;
    if (nOne < 1 || m - nOne < 1 || m < 3 || one.length !== m) {
      throw new Error("need samples in both conditions and at least three in total");
    }
    const group = new Int32Array(m);
    const two = new Float64Array(m);
    for (let j = 0; j < m; j += 1) {
      group[j] = one[j] ? 0 : 1;
      two[j] = one[j] ? 0 : 1;
    }
    const p = 2;
    const timings = {};
    let t0 = now();

    // size factors
    progress("size", "size factors");
    const loggeo = new Float64Array(n);
    for (let i = 0; i < n; i += 1) {
      let s = 0;
      for (let j = 0; j < m; j += 1) {
        s += Math.log(y[i * m + j]);
      }
      loggeo[i] = s / m;
    }
    const sf = new Float64Array(m);
    for (let j = 0; j < m; j += 1) {
      const vals = [];
      for (let i = 0; i < n; i += 1) {
        const yij = y[i * m + j];
        if (Number.isFinite(loggeo[i]) && yij > 0) {
          vals.push(Math.log(yij) - loggeo[i]);
        }
      }
      sf[j] = vals.length ? Math.exp(npMedian(Float64Array.from(vals))) : NaN;
      if (!(sf[j] > 0 && Number.isFinite(sf[j]))) {
        // estimateSizeFactorsForMatrix: "every gene contains at least one zero"
        throw new Error("every cell has a zero count in at least one sample, so size factors (log geometric means) " +
          "cannot be computed; check that every sample has hybrids for this RNA pair");
      }
    }
    const norm = new Float64Array(n * m);
    const baseMean = new Float64Array(n);
    const baseVar = new Float64Array(n);
    for (let i = 0; i < n; i += 1) {
      let s = 0;
      for (let j = 0; j < m; j += 1) {
        const v = y[i * m + j] / sf[j];
        norm[i * m + j] = v;
        s += v;
      }
      const mean = s / m;
      baseMean[i] = mean;
      let ss = 0;
      for (let j = 0; j < m; j += 1) {
        const dlt = norm[i * m + j] - mean;
        ss += dlt * dlt;
      }
      baseVar[i] = ss / (m - 1);
    }

    // gene-wise dispersion
    let xim = 0;
    {
      const inv = new Float64Array(m);
      for (let j = 0; j < m; j += 1) { inv[j] = 1.0 / sf[j]; }
      xim = npMean(inv);
    }
    const maxDisp = Math.max(10.0, m);
    let mu = new Float64Array(n * m);
    const alphaInit = new Float64Array(n);
    const sizes = [0, 0];
    for (let j = 0; j < m; j += 1) { sizes[group[j]] += 1; }
    for (let i = 0; i < n; i += 1) {
      const o = i * m;
      const gm = [0, 0];
      for (let j = 0; j < m; j += 1) {
        gm[group[j]] += norm[o + j];
      }
      gm[0] /= sizes[0];
      gm[1] /= sizes[1];
      const moments = (baseVar[i] - xim * baseMean[i]) / Math.pow(baseMean[i], 2);
      let rs = 0;
      for (let j = 0; j < m; j += 1) {
        const lin = gm[group[j]];
        const mu1 = Math.max(1.0, lin);
        rs += (Math.pow(norm[o + j] - mu1, 2) - mu1) / Math.pow(mu1, 2);
        mu[o + j] = Math.max(lin * sf[j], MIN_MU);
      }
      const rough = Math.max(rs / (m - p), 0.0);
      const lowest = Math.min(rough, moments);
      alphaInit[i] = Number.isNaN(lowest) ? NaN : Math.min(Math.max(lowest, MIN_DISP), maxDisp);
    }
    timings.sizeFactors = now() - t0;
    t0 = now();
    const logInit = new Float64Array(n);
    for (let i = 0; i < n; i += 1) { logInit[i] = Math.log(alphaInit[i]); }
    progress("genewise", "gene-wise dispersion", 0);
    let gene = yield { kind: "fitDisp", stage: "genewise", label: "gene-wise dispersion", rows: n,
      args: { y: y, mu: mu, m: m, group: group, logAlpha0: logInit, priorMean: null, priorVar: 1.0, usePrior: false, minLogAlpha: Math.log(MIN_DISP / 10) } };
    const dispGene = new Float64Array(n);
    const refitRows = [];
    for (let i = 0; i < n; i += 1) {
      let dg = Math.min(Math.exp(gene.logAlpha[i]), maxDisp);
      if (gene.lastLp[i] < gene.initialLp[i] + Math.abs(gene.initialLp[i]) / 1e6) {
        dg = alphaInit[i];
      }
      dispGene[i] = dg;
      const conv = gene.iters[i] < MAXIT && gene.iters[i] !== 1;
      if (!conv && dg > MIN_DISP * 10) {
        refitRows.push(i);
      }
    }
    if (refitRows.length) {
      const refit = gridRefit(y, mu, m, group, refitRows, null, 1.0, false, m);
      refitRows.forEach(function (i, k) { dispGene[i] = refit[k]; });
    }
    gene = null;
    for (let i = 0; i < n; i += 1) {
      dispGene[i] = Math.min(Math.max(dispGene[i], MIN_DISP), maxDisp);
    }
    timings.genewise = now() - t0;
    t0 = now();

    // trend
    progress("trend", "dispersion trend");
    const useFit = [];
    for (let i = 0; i < n; i += 1) {
      if (dispGene[i] > 100 * MIN_DISP) {
        useFit.push(i);
      }
    }
    const fitMeans = Float64Array.from(useFit, function (i) { return baseMean[i]; });
    const fitDisps = Float64Array.from(useFit, function (i) { return dispGene[i]; });
    let coefs = [NaN, NaN];
    let fitType = "parametric";
    const dispFit = new Float64Array(n);
    try {
      coefs = parametricFit(fitMeans, fitDisps);
      for (let i = 0; i < n; i += 1) {
        dispFit[i] = coefs[0] + coefs[1] / baseMean[i];
      }
    } catch (error) {
      coefs = [NaN, NaN];
      fitType = "local";
      dispFit.set(localFit(fitMeans, fitDisps, baseMean));
    }
    const residAbove = [];
    for (let i = 0; i < n; i += 1) {
      if (dispGene[i] >= MIN_DISP * 100) {
        residAbove.push(Math.log(dispGene[i]) - Math.log(dispFit[i]));
      }
    }
    const residArray = Float64Array.from(residAbove);
    const varLogDisp = Math.pow(mad(residArray), 2);
    const df = m - p;
    let priorVar;
    if (df <= 3) {
      priorVar = priorVarFewDf(residArray, df);
    } else {
      priorVar = Math.max(varLogDisp - trigamma(df / 2.0), 0.25);
    }
    timings.trend = now() - t0;
    t0 = now();

    // MAP
    const logDispInit = new Float64Array(n);
    const logDispFit = new Float64Array(n);
    for (let i = 0; i < n; i += 1) {
      logDispInit[i] = Math.log(dispGene[i] > 0.1 * dispFit[i] ? dispGene[i] : dispFit[i]);
      logDispFit[i] = Math.log(dispFit[i]);
    }
    progress("map", "MAP dispersion", 0);
    let map = yield { kind: "fitDisp", stage: "map", label: "MAP dispersion", rows: n,
      args: { y: y, mu: mu, m: m, group: group, logAlpha0: logDispInit, priorMean: logDispFit, priorVar: priorVar, usePrior: true, minLogAlpha: Math.log(MIN_DISP / 10) } };
    const dispMap = new Float64Array(n);
    const mapRefit = [];
    for (let i = 0; i < n; i += 1) {
      dispMap[i] = Math.exp(map.logAlpha[i]);
      if (map.iters[i] >= MAXIT) {
        mapRefit.push(i);
      }
    }
    if (mapRefit.length) {
      const refit = gridRefit(y, mu, m, group, mapRefit, Float64Array.from(mapRefit, function (i) { return logDispFit[i]; }), priorVar, true, m);
      mapRefit.forEach(function (i, k) { dispMap[i] = refit[k]; });
    }
    map = null;
    mu = null;
    const disp = new Float64Array(n);
    const outlierLimit = 2.0 * Math.sqrt(varLogDisp);
    for (let i = 0; i < n; i += 1) {
      const dm = Math.min(Math.max(dispMap[i], MIN_DISP), maxDisp);
      const outlier = Math.log(dispGene[i]) > Math.log(dispFit[i]) + outlierLimit;
      disp[i] = outlier ? dispGene[i] : dm;
    }
    timings.map = now() - t0;
    t0 = now();

    // Wald test
    progress("wald", "Wald test", 0);
    // beta0: least squares of log(norm + 0.1) on [1, two]
    const beta0 = new Float64Array(n * 2);
    for (let i = 0; i < n; i += 1) {
      let s0 = 0;
      let s1 = 0;
      for (let j = 0; j < m; j += 1) {
        const v = Math.log(norm[i * m + j] + 0.1);
        if (group[j] === 0) { s0 += v; } else { s1 += v; }
      }
      const g0 = s0 / sizes[0];
      beta0[2 * i] = g0;
      beta0[2 * i + 1] = s1 / sizes[1] - g0;
    }
    const lam = LAMBDA / (LN2 * LN2);
    const bigCells = [0, 1].filter(function (g) { return sizes[g] >= 3; });
    const fit = yield { kind: "fitBeta", stage: "wald", label: "Wald test", rows: n,
      args: { y: y, m: m, sf: sf, two: two, disp: disp, beta0: beta0, lam: lam, wantHat: cooksCutoff && bigCells.length > 0 } };
    const lfc = new Float64Array(n);
    const se = new Float64Array(n);
    const stat = new Float64Array(n);
    const pvalue = new Float64Array(n);
    const betaConv = new Uint8Array(n);
    let optimRows = 0;
    const beta2 = new Float64Array(n * 2);
    for (let i = 0; i < n; i += 1) {
      const b0 = fit.beta[2 * i] / LN2;
      const b1 = fit.beta[2 * i + 1] / LN2;
      const v0 = fit.variance[2 * i];
      const v1 = fit.variance[2 * i + 1];
      let se1 = Math.sqrt(Math.max(v1, 0.0)) / LN2;
      let conv = fit.iters[i] < MAXIT;
      const stable = Number.isFinite(fit.beta[2 * i]) && Number.isFinite(fit.beta[2 * i + 1]);
      let lb0 = b0;
      let lb1 = b1;
      if (!conv || !stable || !(v0 > 0 && v1 > 0)) {
        optimRows += 1;
        const start = stable && Math.abs(b0) < LARGE && Math.abs(b1) < LARGE ? [b0, b1] : [beta0[2 * i], beta0[2 * i + 1]];
        const rowCounts = Array.from(y.subarray(i * m, i * m + m));
        const res = optimRow(rowCounts, sf, two, disp[i], start);
        lb0 = res.beta[0];
        lb1 = res.beta[1];
        se1 = res.se[1];
        conv = res.converged;
      }
      beta2[2 * i] = lb0;
      beta2[2 * i + 1] = lb1;
      betaConv[i] = conv ? 1 : 0;
      lfc[i] = -lb1;
      se[i] = se1;
      stat[i] = lfc[i] / se[i];
      pvalue[i] = 2.0 * (erfc(Math.abs(stat[i]) / Math.SQRT2) * 0.5);
    }

    // Cook's distance (cells with three or more replicates)
    const maxCooks = new Float64Array(n).fill(NaN);
    let cooksFiltered = 0;
    if (cooksCutoff && bigCells.length) {
      const cutoff = qf99(p, m - p);
      const cooks = new Float64Array(m);
      for (let i = 0; i < n; i += 1) {
        const o = i * m;
        let v = 0;
        bigCells.forEach(function (g) {
          const cell = [];
          for (let j = 0; j < m; j += 1) {
            if (group[j] === g) { cell.push(norm[o + j]); }
          }
          const nc = cell.length;
          const trim = nc <= 3.5 ? 1 / 3 : (nc <= 23.5 ? 1 / 4 : 1 / 8);
          const scale = nc <= 3.5 ? 2.04 : (nc <= 23.5 ? 1.86 : 1.51);
          const cm = trimmedMeanRow(cell, trim);
          v = Math.max(v, scale * trimmedMeanRow(cell.map(function (c) { return Math.pow(c - cm, 2); }), trim));
        });
        const meanAll = baseMean[i];
        const rdisp = Math.max((v - meanAll) / Math.pow(meanAll, 2), 0.04);
        let mx = -Infinity;
        let argmax = 0;
        for (let j = 0; j < m; j += 1) {
          const muFit = sf[j] * Math.pow(2, beta2[2 * i] + two[j] * beta2[2 * i + 1]);
          const pear = Math.pow(y[o + j] - muFit, 2) / (muFit + rdisp * Math.pow(muFit, 2));
          const h = fit.hat[o + j];
          cooks[j] = pear / p * h / Math.pow(1.0 - h, 2);
          if (cooks[j] > cooks[argmax]) {
            argmax = j;
          }
          if (bigCells.indexOf(group[j]) >= 0 && cooks[j] > mx) {
            mx = cooks[j];
          }
        }
        maxCooks[i] = mx;
        if (mx > cutoff) {
          const outCount = y[o + argmax];
          let above = 0;
          for (let j = 0; j < m; j += 1) {
            if (y[o + j] > outCount) { above += 1; }
          }
          if (above < 3) {
            pvalue[i] = NaN;
            cooksFiltered += 1;
          }
        }
      }
    }
    timings.wald = now() - t0;
    t0 = now();

    progress("filter", "independent filtering");
    const filtered = independentFiltering(baseMean, pvalue, alpha);
    timings.filter = now() - t0;
    return {
      baseMean: baseMean, log2FoldChange: lfc, lfcSE: se, stat: stat, pvalue: pvalue,
      padj: filtered.padj, dispGeneEst: dispGene, dispFit: dispFit, dispersion: disp,
      maxCooks: maxCooks, sizeFactors: sf, dispPriorVar: priorVar, varLogDispEsts: varLogDisp,
      trend: coefs, fitType: fitType, filterThreshold: filtered.threshold, betaConv: betaConv,
      optimRows: optimRows, cooksFiltered: cooksFiltered, timings: timings
    };
  }

  function qf99(d1, d2) {
    if (d1 === 2) {
      return d2 / 2.0 * (Math.pow(0.01, -2.0 / d2) - 1.0);
    }
    throw new Error("only two-condition designs are supported");
  }

  function independentFiltering(filt, pvalue, alpha) {
    const n = filt.length;
    let zeros = 0;
    for (let i = 0; i < n; i += 1) {
      if (filt[i] === 0) { zeros += 1; }
    }
    const lower = zeros / n;
    const upper = lower < 0.95 ? 0.95 : 1.0;
    const theta = linspace(lower, upper, 50);
    const cutoffs = quantile7(sortedCopy(filt), theta);
    // rank the tested p-values once (ascending); for each cutoff count BH
    // rejections: the largest rank k (within the subset) with n / k * p < alpha
    let nValid = 0;
    for (let i = 0; i < n; i += 1) {
      if (!Number.isNaN(pvalue[i])) { nValid += 1; }
    }
    const valid = new Uint32Array(nValid);
    let k = 0;
    for (let i = 0; i < n; i += 1) {
      if (!Number.isNaN(pvalue[i])) { valid[k] = i; k += 1; }
    }
    const order = sortIndexByValue(valid, pvalue);
    const filtByP = new Float64Array(order.length);
    const pByP = new Float64Array(order.length);
    for (let r = 0; r < order.length; r += 1) {
      filtByP[r] = filt[order[r]];
      pByP[r] = pvalue[order[r]];
    }
    const numRej = new Float64Array(cutoffs.length);
    for (let c = 0; c < cutoffs.length; c += 1) {
      const cut = cutoffs[c];
      let size = 0;
      for (let r = 0; r < order.length; r += 1) {
        if (filtByP[r] >= cut) { size += 1; }
      }
      let rank = 0;
      let best = 0;
      for (let r = 0; r < order.length; r += 1) {
        if (filtByP[r] >= cut) {
          rank += 1;
          if (Math.min(1.0, size / rank * pByP[r]) < alpha) {
            best = rank;
          }
        }
      }
      numRej[c] = best;
    }
    const fit = lowess(theta, numRej, 1 / 5);
    let j = 0;
    let maxRej = -Infinity;
    for (let c = 0; c < numRej.length; c += 1) { maxRej = Math.max(maxRej, numRej[c]); }
    if (maxRej > 10) {
      const residual = [];
      for (let c = 0; c < numRej.length; c += 1) {
        if (numRej[c] > 0) { residual.push(Math.pow(numRej[c] - fit[c], 2)); }
      }
      const meanSq = residual.length ? npMean(Float64Array.from(residual)) : 0;
      let fitMax = -Infinity;
      for (let c = 0; c < fit.length; c += 1) { fitMax = Math.max(fitMax, fit[c]); }
      const thresh = fitMax - Math.sqrt(meanSq);
      const levels = [thresh, 0.9 * fitMax, 0.8 * fitMax];
      for (let l = 0; l < levels.length; l += 1) {
        let found = -1;
        for (let c = 0; c < numRej.length; c += 1) {
          if (numRej[c] > levels[l]) { found = c; break; }
        }
        if (found >= 0) {
          j = found;
          break;
        }
      }
    }
    const use = new Uint8Array(n);
    for (let i = 0; i < n; i += 1) {
      use[i] = filt[i] >= cutoffs[j] ? 1 : 0;
    }
    return { padj: pAdjustBH(pvalue, use), threshold: cutoffs[j], numRej: numRej, theta: theta, cutoffs: cutoffs };
  }

  function now() {
    return typeof performance !== "undefined" && performance && performance.now ? performance.now() : Date.now();
  }

  // ---- contact cells --------------------------------------------------------------------

  // HYB2 coverage rule (plot_hybrids_3.awk USE_ENTIRE_HYBRIDS=1): every
  // record adds 1 to each binSize x binSize cell covered by arm X x arm Y.
  // arms: Int32Array/Array [xStart, xEnd, yStart, yEnd] per record (inclusive).
  // Returns sparse cells sorted by key: { key: Float64Array, x, y: Int32Array, count: Float64Array }.
  function binArms(arms, binSize) {
    const b = binSize || DEFAULTS.binSize;
    const records = Math.floor(arms.length / 4);
    let maxX = 0;
    let maxY = 0;
    for (let r = 0; r < records; r += 1) {
      maxX = Math.max(maxX, arms[4 * r + 1]);
      maxY = Math.max(maxY, arms[4 * r + 3]);
    }
    const nx = Math.floor(maxX / b) + 1;
    const ny = Math.floor(maxY / b) + 1;
    if (nx * ny <= 16000000) {
      const grid = new Uint32Array(nx * ny);
      let cells = 0;
      for (let r = 0; r < records; r += 1) {
        const x0 = Math.floor(arms[4 * r] / b);
        const x1 = Math.floor(arms[4 * r + 1] / b);
        const y0 = Math.floor(arms[4 * r + 2] / b);
        const y1 = Math.floor(arms[4 * r + 3] / b);
        for (let xb = x0; xb <= x1; xb += 1) {
          const row = xb * ny;
          for (let yb = y0; yb <= y1; yb += 1) {
            if (grid[row + yb] === 0) { cells += 1; }
            grid[row + yb] += 1;
          }
        }
      }
      const out = { key: new Float64Array(cells), x: new Int32Array(cells), y: new Int32Array(cells), count: new Float64Array(cells) };
      let k = 0;
      for (let xb = 0; xb < nx; xb += 1) {
        for (let yb = 0; yb < ny; yb += 1) {
          const v = grid[xb * ny + yb];
          if (v) {
            out.key[k] = xb * KEY_SHIFT + yb;
            out.x[k] = xb * b;
            out.y[k] = yb * b;
            out.count[k] = v;
            k += 1;
          }
        }
      }
      return out;
    }
    const counts = new Map();
    for (let r = 0; r < records; r += 1) {
      const x0 = Math.floor(arms[4 * r] / b);
      const x1 = Math.floor(arms[4 * r + 1] / b);
      const y0 = Math.floor(arms[4 * r + 2] / b);
      const y1 = Math.floor(arms[4 * r + 3] / b);
      for (let xb = x0; xb <= x1; xb += 1) {
        for (let yb = y0; yb <= y1; yb += 1) {
          const key = xb * KEY_SHIFT + yb;
          counts.set(key, (counts.get(key) || 0) + 1);
        }
      }
    }
    return cellsFromMap(counts, b);
  }

  function cellsFromMap(counts, b) {
    const keys = Float64Array.from(counts.keys());
    keys.sort();
    const out = { key: keys, x: new Int32Array(keys.length), y: new Int32Array(keys.length), count: new Float64Array(keys.length) };
    for (let k = 0; k < keys.length; k += 1) {
      const xb = Math.floor(keys[k] / KEY_SHIFT);
      out.x[k] = xb * b;
      out.y[k] = (keys[k] - xb * KEY_SHIFT) * b;
      out.count[k] = counts.get(keys[k]);
    }
    return out;
  }

  // Sparse cells from x/y/count columns (e.g. a contact.txt or DataProvider bins).
  function cellsFromColumns(x, y, count, binSize) {
    const b = binSize || DEFAULTS.binSize;
    let maxX = 0;
    let maxY = 0;
    for (let i = 0; i < x.length; i += 1) {
      if (x[i] < 0 || y[i] < 0 || x[i] % b || y[i] % b) {
        throw new Error("cell coordinates must be non-negative multiples of the bin size");
      }
      maxX = Math.max(maxX, x[i]);
      maxY = Math.max(maxY, y[i]);
    }
    const nx = maxX / b + 1;
    const ny = maxY / b + 1;
    if (nx * ny <= 8000000) {
      const grid = new Float64Array(nx * ny);
      let cells = 0;
      for (let i = 0; i < x.length; i += 1) {
        const at = (x[i] / b) * ny + y[i] / b;
        if (grid[at] === 0 && Number(count[i]) !== 0) { cells += 1; }
        grid[at] += Number(count[i]);
      }
      const out = { key: new Float64Array(cells), x: new Int32Array(cells), y: new Int32Array(cells), count: new Float64Array(cells) };
      let k = 0;
      for (let at = 0; at < grid.length && k < cells; at += 1) {
        if (grid[at] !== 0) {
          const xb = Math.floor(at / ny);
          const yb = at - xb * ny;
          out.key[k] = xb * KEY_SHIFT + yb;
          out.x[k] = xb * b;
          out.y[k] = yb * b;
          out.count[k] = grid[at];
          k += 1;
        }
      }
      return out;
    }
    const counts = new Map();
    for (let i = 0; i < x.length; i += 1) {
      const key = (x[i] / b) * KEY_SHIFT + y[i] / b;
      counts.set(key, (counts.get(key) || 0) + Number(count[i]));
    }
    return cellsFromMap(counts, b);
  }

  // contact.txt text -> sparse cells (header and # lines skipped)
  function parseContactText(text, binSize) {
    const x = [];
    const y = [];
    const c = [];
    const lines = String(text).split("\n");
    for (let i = 0; i < lines.length; i += 1) {
      const line = lines[i];
      const ch = line.charCodeAt(0);
      if (!(ch >= 48 && ch <= 57)) {
        continue;
      }
      const parts = line.trim().split(/\s+/);
      if (parts.length !== 3) {
        throw new Error("expected x, y, count on every line");
      }
      x.push(Number(parts[0]));
      y.push(Number(parts[1]));
      c.push(Number(parts[2]));
    }
    return cellsFromColumns(x, y, c, binSize);
  }

  // Union of every sample's cells -> { key, x, y, table (row-major n x m) }
  function countTable(samples) {
    const m = samples.length;
    let total = 0;
    samples.forEach(function (s) { total += s.key.length; });
    const all = new Float64Array(total);
    let o = 0;
    samples.forEach(function (s) { all.set(s.key, o); o += s.key.length; });
    all.sort();
    let n = 0;
    for (let i = 0; i < all.length; i += 1) {
      if (i === 0 || all[i] !== all[i - 1]) {
        all[n] = all[i];
        n += 1;
      }
    }
    const keys = all.slice(0, n);
    const table = new Float64Array(n * m);
    samples.forEach(function (s, j) {
      let r = 0;
      for (let k = 0; k < s.key.length; k += 1) {
        while (keys[r] < s.key[k]) { r += 1; }
        table[r * m + j] += s.count[k];
      }
    });
    return { key: keys, table: table, n: n, m: m };
  }

  // Greedy split (DESeq_interaction_split_select.py): cells visited by
  // increasing padj (ties in input order); a cell is kept unless a kept
  // cell of the same direction lies within radius in both x and y.
  // Returns the kept positions into the given arrays, up first then down.
  // Kept cells are bucketed on a (radius + 1) grid, so only the 3 x 3
  // neighbouring buckets are searched.
  function splitInteractions(x, y, lfc, padj, radius) {
    const n = x.length;
    const order = Array.from({ length: n }, function (_, i) { return i; });
    order.sort(function (a, b) { return padj[a] - padj[b] || a - b; });
    const kept = [];
    const width = radius + 1;
    [1, -1].forEach(function (direction) {
      const grid = new Map();
      order.forEach(function (index) {
        const s = lfc[index] > 0 ? 1 : (lfc[index] < 0 ? -1 : 0);
        if (s !== direction) {
          return;
        }
        const xi = x[index];
        const yi = y[index];
        const bx = Math.floor(xi / width);
        const by = Math.floor(yi / width);
        for (let gx = bx - 1; gx <= bx + 1; gx += 1) {
          const column = grid.get(gx);
          if (!column) {
            continue;
          }
          for (let gy = by - 1; gy <= by + 1; gy += 1) {
            const bucket = column.get(gy);
            if (!bucket) {
              continue;
            }
            for (let k = 0; k < bucket.length; k += 2) {
              if (Math.abs(bucket[k] - xi) <= radius && Math.abs(bucket[k + 1] - yi) <= radius) {
                return;
              }
            }
          }
        }
        let column = grid.get(bx);
        if (!column) {
          column = new Map();
          grid.set(bx, column);
        }
        let bucket = column.get(by);
        if (!bucket) {
          bucket = [];
          column.set(by, bucket);
        }
        bucket.push(xi, yi);
        kept.push(index);
      });
    });
    return kept;
  }

  // the quadratic reference implementation, for the checks
  function splitInteractionsPlain(x, y, lfc, padj, radius) {
    const n = x.length;
    const order = Array.from({ length: n }, function (_, i) { return i; });
    order.sort(function (a, b) { return padj[a] - padj[b] || a - b; });
    const kept = [];
    [1, -1].forEach(function (direction) {
      const cx = [];
      const cy = [];
      order.forEach(function (index) {
        const s = lfc[index] > 0 ? 1 : (lfc[index] < 0 ? -1 : 0);
        if (s !== direction) {
          return;
        }
        const xi = x[index];
        const yi = y[index];
        for (let k = 0; k < cx.length; k += 1) {
          if (Math.abs(cx[k] - xi) <= radius && Math.abs(cy[k] - yi) <= radius) {
            return;
          }
        }
        cx.push(xi);
        cy.push(yi);
        kept.push(index);
      });
    });
    return kept;
  }

  // compare(samples, conditionOne, options) -> full result (see README):
  // samples: [{ name, cells: { key, x, y, count } }] (from binArms / parseContactText)
  function compare(samples, conditionOne, options) {
    const opts = options || {};
    const progress = typeof opts.progress === "function" ? opts.progress : function () {};
    return driveSync(compareSteps(samples, conditionOne, opts), progress);
  }

  // compareAsync(samples, conditionOne, options, runner): as compare, with the
  // row-parallel tasks handed to runner (see driveAsync)
  function compareAsync(samples, conditionOne, options, runner) {
    return driveAsync(compareSteps(samples, conditionOne, options || {}), runner);
  }

  function* compareSteps(samples, conditionOne, options) {
    const opts = Object.assign({}, DEFAULTS, options || {});
    const progress = typeof opts.progress === "function" ? opts.progress : function () {};
    const started = now();
    const m = samples.length;
    const one = Array.from(conditionOne, Boolean);
    const nOne = one.filter(Boolean).length;
    if (one.length !== m) {
      throw new Error("every sample needs a condition");
    }
    if (nOne < 1 || m - nOne < 1 || m < 3) {
      throw new Error("a comparison needs both conditions and at least three samples");
    }
    if (nOne < 2 && m - nOne < 2) {
      throw new Error("at least one condition needs two or more replicates");
    }
    if (!Number.isInteger(opts.minCount) || opts.minCount < 0) {
      throw new Error("min count must be a non-negative integer");
    }
    if (!Number.isInteger(opts.radius) || opts.radius < 0) {
      throw new Error("merge range must be a non-negative integer");
    }
    if (!(opts.padjCutoff > 0 && opts.padjCutoff <= 1)) {
      throw new Error("the padj cutoff must be in (0, 1]");
    }
    progress("table", "building the count table");
    let t0 = now();
    const full = countTable(samples.map(function (s) { return s.cells; }));
    const keepRows = [];
    for (let i = 0; i < full.n; i += 1) {
      let s = 0;
      for (let j = 0; j < m; j += 1) {
        const v = full.table[i * m + j];
        if (!Number.isSafeInteger(v) || v < 0) {
          throw new Error("counts must be non-negative integers");
        }
        s += v;
      }
      if (s > opts.minCount) {
        keepRows.push(i);
      }
    }
    const n = keepRows.length;
    if (n < 2) {
      throw new Error("fewer than two cells pass the minimum count; nothing to test");
    }
    const table = new Float64Array(n * m);
    const x = new Int32Array(n);
    const y = new Int32Array(n);
    const b = opts.binSize;
    keepRows.forEach(function (i, r) {
      table.set(full.table.subarray(i * m, i * m + m), r * m);
      const xb = Math.floor(full.key[i] / KEY_SHIFT);
      x[r] = xb * b;
      y[r] = (full.key[i] - xb * KEY_SHIFT) * b;
    });
    const tableMs = now() - t0;
    t0 = now();
    progress("deseq", "DESeq2 Wald test on " + n.toLocaleString("en-US") + " cells");
    const res = yield* deseqSteps(table, n, m, one, { alpha: opts.alpha, progress: progress });
    const testMs = now() - t0;

    progress("split", "merging significant cells");
    const padj = res.padj;
    const lfc = res.log2FoldChange;
    const sig = [];
    for (let i = 0; i < n; i += 1) {
      if (padj[i] < opts.padjCutoff) {
        sig.push(i);
      }
    }
    sig.sort(function (a, c) { return padj[a] - padj[c] || a - c; });
    const sx = Int32Array.from(sig, function (i) { return x[i]; });
    const sy = Int32Array.from(sig, function (i) { return y[i]; });
    const slfc = Float64Array.from(sig, function (i) { return lfc[i]; });
    const spadj = Float64Array.from(sig, function (i) { return padj[i]; });
    const sbase = Float64Array.from(sig, function (i) { return res.baseMean[i]; });
    const kept = splitInteractions(sx, sy, slfc, spadj, opts.radius);
    const interactions = kept.map(function (k) {
      return { direction: slfc[k] > 0 ? "up" : "down", x: sx[k], y: sy[k], log2FoldChange: slfc[k], padj: spadj[k], baseMean: sbase[k] };
    });
    let up = 0;
    let sigUp = 0;
    let sigDown = 0;
    interactions.forEach(function (row) { if (row.direction === "up") { up += 1; } });
    for (let k = 0; k < slfc.length; k += 1) {
      if (slfc[k] > 0) { sigUp += 1; } else if (slfc[k] < 0) { sigDown += 1; }
    }
    let withPadj = 0;
    let maxCoordinate = 0;
    for (let i = 0; i < n; i += 1) {
      if (!Number.isNaN(padj[i])) { withPadj += 1; }
      maxCoordinate = Math.max(maxCoordinate, x[i], y[i]);
    }
    const summary = {
      samples: samples.map(function (s, j) { return { name: s.name, condition: one[j] ? "one" : "two" }; }),
      parameters: { minCount: opts.minCount, range: opts.radius, alpha: opts.alpha, padjCutoff: opts.padjCutoff, binSize: b },
      cellsTotal: full.n,
      cellsTested: n,
      cellsWithPadj: withPadj,
      significant: sig.length,
      significantUp: sigUp,
      significantDown: sigDown,
      interactions: interactions.length,
      interactionsUp: up,
      interactionsDown: interactions.length - up,
      sizeFactors: Array.from(res.sizeFactors),
      dispersionTrend: res.fitType === "parametric"
        ? { type: "parametric", asymptDisp: res.trend[0], extraPois: res.trend[1] }
        : { type: "local" },
      dispPriorVar: res.dispPriorVar,
      filterThreshold: res.filterThreshold,
      optimRows: res.optimRows,
      cooksFiltered: res.cooksFiltered,
      maxCoordinate: maxCoordinate,
      engine: "Hyb2Deseq (JavaScript port of HYB2 Desktop's numpy DESeq2 1.46 Wald test)",
      timings: {
        tableMs: Math.round(tableMs), testMs: Math.round(testMs), totalMs: Math.round(now() - started),
        stagesMs: Object.fromEntries(Object.keys(res.timings).map(function (key) { return [key, Math.round(res.timings[key])]; }))
      }
    };
    const out = {
      summary: summary,
      significant: { x: sx, y: sy, log2FoldChange: slfc, padj: spadj, baseMean: sbase },
      interactions: interactions
    };
    if (opts.keepAll) {
      out.all = { x: x, y: y, table: table, m: m, result: res };
    }
    return out;
  }

  // ---- exports -------------------------------------------------------------------------

  function fmt(value) {
    if (value == null || Number.isNaN(value)) {
      return "NA";
    }
    return String(value);
  }

  // HYB2 Desktop interactions.tsv
  function interactionsToTsv(interactions) {
    const rows = ["direction\tx\ty\tlog2FoldChange\tpadj\tbaseMean"];
    (interactions || []).forEach(function (row) {
      rows.push([row.direction, row.x, row.y, fmt(row.log2FoldChange), fmt(row.padj), fmt(row.baseMean)].join("\t"));
    });
    return rows.join("\n") + "\n";
  }

  // significant cells in padj order (x y log2FoldChange padj baseMean)
  function significantToTsv(significant) {
    const rows = ["x\ty\tlog2FoldChange\tpadj\tbaseMean"];
    const s = significant || { x: [] };
    for (let i = 0; i < s.x.length; i += 1) {
      rows.push([s.x[i], s.y[i], fmt(s.log2FoldChange[i]), fmt(s.padj[i]), fmt(s.baseMean[i])].join("\t"));
    }
    return rows.join("\n") + "\n";
  }

  // The rows Hyb2ArcMap.parseInteractions returns for interactions.tsv
  function arcMapInteractions(interactions, name) {
    return {
      name: name || "differential test",
      rows: (interactions || []).map(function (row) {
        return { x: row.x, y: row.y, padj: row.padj, log2FoldChange: row.log2FoldChange, up: row.direction === "up" };
      })
    };
  }

  return {
    DEFAULTS: DEFAULTS,
    KEY_SHIFT: KEY_SHIFT,
    lgamma: lgamma,
    erfc: erfc,
    digamma: digamma,
    trigamma: trigamma,
    pairwiseSum: npSum,
    median: npMedian,
    lowess: lowess,
    loess: loess,
    pAdjustBH: pAdjustBH,
    priorVarFewDf: priorVarFewDf,
    parametricFit: parametricFit,
    localFit: localFit,
    deseqTwoConditions: deseqTwoConditions,
    compareAsync: compareAsync,
    runTask: runTask,
    splitTask: splitTask,
    mergeTask: mergeTask,
    taskTransfers: taskTransfers,
    independentFiltering: independentFiltering,
    binArms: binArms,
    cellsFromColumns: cellsFromColumns,
    parseContactText: parseContactText,
    countTable: countTable,
    splitInteractions: splitInteractions,
    splitInteractionsPlain: splitInteractionsPlain,
    compare: compare,
    interactionsToTsv: interactionsToTsv,
    significantToTsv: significantToTsv,
    arcMapInteractions: arcMapInteractions
  };
}));
