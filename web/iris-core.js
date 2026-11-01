/* SPDX-License-Identifier: GPL-3.0-or-later
 * Browser adaptation of JY-Zhou/IRIS v1.0 (Scoring, Generating, Picking).
 * Original source and licence: third_party/iris; differences: docs/iris-browser.md.
 */
(function (root) {
  "use strict";
  const EPS = 2 ** -23;
  const LIMITS = { length: 300, records: 2000, folds: 1000, candidates: 50, ensembles: 20000 };
  const VALID = new Set(["AU", "UA", "GC", "CG", "GU", "UG"]);
  function integer(value, min, max, name) {
    const n = Number(value);
    if (!Number.isInteger(n) || n < min || n > max) throw new Error(name + " must be an integer from " + min + " to " + max + ".");
    return n;
  }
  function parameters(value) {
    const p = value || {};
    const K = integer(p.K == null ? 2 : p.K, 1, 3, "IRIS ensemble size K");
    const C = integer(p.C == null ? 20 : p.C, K, LIMITS.candidates, "IRIS candidate count C");
    const fraction = Number(p.fraction == null ? 0.75 : p.fraction);
    if (!Number.isFinite(fraction) || fraction < 0 || fraction >= 1) throw new Error("IRIS support quantile must be at least 0 and below 1.");
    const kRange = Array.from(new Set(String(p.kRange || "4 5 6 7").trim().split(/[ ,]+/).map(k => integer(k, 2, 12, "IRIS stem length")))).sort((a,b)=>a-b);
    return { K, C, fraction, kRange };
  }
  function supportMatrix(n, arms) {
    if (!Array.isArray(arms) || !arms.length) throw new Error("IRIS requires HYB evidence within the selected reference region.");
    if (arms.length > LIMITS.records) throw new Error("IRIS browser limit: 2,000 evidence rows. Select a smaller region.");
    // Prepared HYB coordinates are 1-based inclusive; IRIS uses 0-based half-open blocks.
    const blocks = arms.map(a => [a.oneStart - 1, Number(a.oneEnd), a.twoStart - 1, Number(a.twoEnd)]);
    blocks.forEach(b => { if (b.some(x=>!Number.isInteger(x)) || b[0]<0 || b[2]<0 || b[1]<=b[0] || b[3]<=b[2] || b[1]>n || b[3]>n) throw new Error("IRIS evidence coordinates are outside the prepared sequence."); });
    const sd = blocks.reduce((s,b)=>s+b[1]-b[0]+b[3]-b[2],0)/(2*blocks.length*6);
    const matrix = new Float64Array(n*n);
    function gaussian(mu) {
      const vec = Float64Array.from({length:n},(_,i)=>Math.exp(-0.5*((i-mu)/sd)**2));
      const peak = Math.max(...vec);
      return vec.map(x=>x/peak);
    }
    for (const b of blocks) {
      const l = gaussian((b[0]+b[1])/2), r = gaussian((b[2]+b[3])/2);
      for (let i=0;i<n;i++) for(let j=0;j<n;j++) matrix[i*n+j] += l[i]*r[j]+r[i]*l[j];
    }
    for(let i=0;i<matrix.length;i++) matrix[i]=matrix[i]<EPS?0:Math.log1p(matrix[i]);
    return matrix;
  }
  function quantile(values, q) {
    const a = values.sort((a,b)=>a-b), x=(a.length-1)*q, lo=Math.floor(x);
    return a[lo]+(a[Math.ceil(x)]-a[lo])*(x-lo);
  }
  function collectStems(sequence, support, p) {
    const n=sequence.length, positive=Array.from(support).filter(x=>x>0);
    if (!positive.length) throw new Error("IRIS evidence support is empty.");
    const threshold=quantile(positive,p.fraction), stems=[];
    for(const k of p.kRange) for(let l=0;l+k<=n;l++) for(let r=l+k+4;r+k<=n;r++) {
      let valid=true, sum=0;
      for(let t=0;t<k;t++) if(!VALID.has(sequence[l+t]+sequence[r+k-1-t])) {valid=false;break;}
      if(!valid) continue;
      for(let i=l;i<l+k;i++) for(let j=r;j<r+k;j++) sum+=support[i*n+j];
      if(sum/(k*k)>threshold) stems.push({l,r,k});
    }
    stems.sort((a,b)=>b.k-a.k);
    const clean=[];
    for(const s of stems) if(!clean.some(t=>s.l>=t.l&&s.l+s.k<=t.l+t.k&&s.r>=t.r&&s.r+s.k<=t.r+t.k)) clean.push(s);
    if(!clean.length) throw new Error("IRIS found no supported canonical stems at these settings.");
    return clean;
  }
  function constraintsFor(stems) {
    const pairs=stems.flatMap(s=>Array.from({length:s.k},(_,i)=>({left:s.l+i+1,right:s.r+s.k-i})));
    const occupied=new Set();
    for(const p of pairs) {
      if(occupied.has(p.left)||occupied.has(p.right)) return null;
      occupied.add(p.left);occupied.add(p.right);
    }
    for(const a of pairs) for(const b of pairs) if(a.left<b.left&&b.left<a.right&&a.right<b.right) return null;
    return pairs;
  }
  function collectConstraints(stems) {
    const constraints=[];
    function add(group) {
      const pairs=constraintsFor(group);
      if(!pairs) return;
      // Bound actual folding jobs while enumerating, before allocating a large
      // list or starting any folds. Stem count alone does not measure the work.
      if(constraints.length>=LIMITS.folds) throw new Error("IRIS browser limit: more than 1,000 constrained folds. Select a smaller region or a higher support quantile.");
      constraints.push(pairs);
    }
    for(const stem of stems) add([stem]);
    for(let i=0;i<stems.length;i++) for(let j=i+1;j<stems.length;j++) add([stems[i],stems[j]]);
    return constraints;
  }
  function pairKeys(candidate) { return new Set(candidate.pairs.map(p=>p.left+":"+p.right)); }
  function distance(a,b) { let same=0; for(const k of a) if(b.has(k)) same++;return a.size+b.size-2*same; }
  function cluster(candidates, C) {
    if(candidates.length<=C) return candidates;
    const sets=candidates.map(pairKeys), n=candidates.length;
    // Average linkage, with cluster sizes used for each merge (Lance–Williams update).
    const d=Array.from({length:n},()=>new Float64Array(n));
    for(let i=0;i<n;i++) for(let j=i+1;j<n;j++) d[i][j]=d[j][i]=distance(sets[i],sets[j]);
    const groups=new Map(candidates.map((_,i)=>[i,[i]]));
    while(groups.size>C) {
      let best=Infinity, a=-1,b=-1;
      for(const i of groups.keys()) for(const j of groups.keys()) if(i<j&&d[i][j]<best) {best=d[i][j];a=i;b=j;}
      const ga=groups.get(a),gb=groups.get(b);
      for(const j of groups.keys()) if(j!==a&&j!==b) d[a][j]=d[j][a]=(d[a][j]*ga.length+d[b][j]*gb.length)/(ga.length+gb.length);
      groups.set(a,ga.concat(gb));groups.delete(b);
    }
    return Array.from(groups.values(),indices=>candidates[indices.reduce((best,i)=>candidates[i].energy<candidates[best].energy?i:best,indices[0])]);
  }
  function solve(matrix,rhs) {
    const n=rhs.length,a=matrix.map((row,i)=>row.concat(rhs[i]));
    for(let k=0;k<n;k++) {
      let pivot=k;for(let i=k+1;i<n;i++) if(Math.abs(a[i][k])>Math.abs(a[pivot][k])) pivot=i;
      if(Math.abs(a[pivot][k])<1e-10) return null;
      [a[k],a[pivot]]=[a[pivot],a[k]];
      const v=a[k][k];for(let j=k;j<=n;j++) a[k][j]/=v;
      for(let i=0;i<n;i++) if(i!==k) {const f=a[i][k];for(let j=k;j<=n;j++) a[i][j]-=f*a[k][j];}
    }
    return a.map(row=>row[n]);
  }
  // K <= 3: enumerate active sets to solve bounded least squares exactly;
  // singular faces have an equivalent solution on a lower-dimensional face.
  function nnls(gram,rhs) {
    const n=rhs.length;let best=new Array(n).fill(0),loss=0;
    for(let mask=1;mask<2**n;mask++) {
      const indices=Array.from({length:n},(_,i)=>i).filter(i=>mask&(1<<i));
      const x=solve(indices.map(i=>indices.map(j=>gram[i][j])),indices.map(i=>rhs[i]));
      if(!x||x.some(v=>v< -1e-9)) continue;
      const all=new Array(n).fill(0);indices.forEach((i,k)=>all[i]=Math.max(0,x[k]));
      let objective=0;for(let i=0;i<n;i++) {objective-=2*all[i]*rhs[i];for(let j=0;j<n;j++) objective+=all[i]*gram[i][j]*all[j];}
      if(objective<loss) {loss=objective;best=all;}
    }
    return best;
  }
  function pick(candidates,support,n,K) {
    if(candidates.length<K) throw new Error("IRIS generated only "+candidates.length+" candidate(s); reduce ensemble size K.");
    const sets=candidates.map(pairKeys), means=candidates.map(c=>c.pairs.reduce((s,p)=>s+support[(p.left-1)*n+p.right-1],0)/c.pairs.length);
    if(means.some(x=>!Number.isFinite(x))) throw new Error("IRIS cannot score an unpaired candidate.");
    const energies=candidates.map(c=>c.energy), range=Math.max(...means)-Math.min(...means);
    // log p(E) = -E/RT - log Z; common partition constants cancel in differences and argmax.
    const RT=0.00198717*310.15, logRange=(Math.max(...energies)-Math.min(...energies))/RT;
    if(range<=1e-12 || logRange<=1e-12) throw new Error("IRIS cannot identify an ensemble: candidate support or energy has zero range.");
    const lambda=logRange/range;
    const rhs=candidates.map((c,i)=>2*c.pairs.length*means[i]);
    const gram=sets.map(a=>sets.map(b=>{let count=0;for(const k of a) if(b.has(k)) count++;return 2*count;}));
    let best=null,count=0;
    function visit(indices,start) {
      if(indices.length<K) {for(let i=start;i<=candidates.length-(K-indices.length);i++) visit(indices.concat(i),i+1);return;}
      if(++count>LIMITS.ensembles) throw new Error("IRIS browser limit: too many candidate ensembles. Reduce C or K.");
      const weights=nnls(indices.map(i=>indices.map(j=>gram[i][j])),indices.map(i=>rhs[i]));
      const total=weights.reduce((a,b)=>a+b,0);if(total<=1e-12) return;
      const proportions=weights.map(x=>x/total);
      const energy=indices.reduce((s,i,k)=>s+proportions[k]*energies[i],0);
      const meanSupport=indices.reduce((s,i,k)=>s+proportions[k]*means[i],0);
      const score=-energy/RT+lambda*meanSupport;
      if(!best||score>best.score) best={indices,weights,proportions,energy,meanSupport,score};
    }
    visit([],0);
    if(!best) throw new Error("IRIS could not fit positive ensemble weights.");
    return {...best,lambda,testedEnsembles:count};
  }
  async function run(request, fold, progress) {
    const sequence=String(request.sequence||"").toUpperCase().replace(/T/g,"U");
    if(!/^[ACGU]+$/.test(sequence)||sequence.length>LIMITS.length) throw new Error("IRIS requires 1–300 canonical RNA nucleotides.");
    const p=parameters(request.parameters),n=sequence.length;
    progress("Redistributing HYB evidence",8);
    const support=supportMatrix(n,request.evidenceArms),stems=collectStems(sequence,support,p);
    const constraints=collectConstraints(stems);
    const candidates=[];
    for(let i=0;i<constraints.length;i++) {
      candidates.push(await fold(sequence,constraints[i]));
      progress("Generating IRIS candidates "+(i+1)+" / "+constraints.length,15+Math.round(55*(i+1)/constraints.length));
    }
    progress("Clustering candidate structures",74);
    const leaders=cluster(candidates,p.C);
    progress("Selecting the IRIS ensemble",85);
    const chosen=pick(leaders,support,n,p.K);
    const ensemble=chosen.indices.map((i,k)=>({...leaders[i],weight:chosen.proportions[k],coefficient:chosen.weights[k]})).sort((a,b)=>b.weight-a.weight);
    const selected=ensemble[0];
    return {...selected,engine:"IRIS",engineVersion:"1.0-browser.2",algorithm:"IRIS evidence-guided ensemble",
      model:"IRIS browser adaptation; Gaussian HYB support, constrained ViennaRNA candidates, average-linkage clustering and Bayesian ensemble selection",
      temperature:37,minimumLoop:3,constraintMode:"iris-ensemble",
      constraintSource:"iris-hyb-stems",evidenceMode:"hyb-blocks",evidenceSource:"iris-gaussian-support",
      candidates:ensemble,selectedCandidate:0,parameters:p,
      ensemble:{meanEnergy:chosen.energy,meanSupport:chosen.meanSupport,lambda:chosen.lambda,testedEnsembles:chosen.testedEnsembles,candidateCount:leaders.length,generatedCount:candidates.length},
      evidence:{inputRecords:request.evidenceArms.length,stemCount:stems.length},
      warnings:["Browser adaptation: HYB arms replace BAM input; C ≤ 50, so the upstream LASSO prefilter is not needed. Tie-breaking can differ from scikit-learn."]};
  }
  const api={run,parameters,supportMatrix,collectStems,constraintsFor,collectConstraints,cluster,nnls,pick,LIMITS};
  root.Hyb2Iris=api;
  if(typeof module!=="undefined"&&module.exports) module.exports=api;
}(typeof self!=="undefined"?self:globalThis));
