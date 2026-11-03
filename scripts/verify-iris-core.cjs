'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs');
const iris=require('../web/iris-core.js');
const arms=[{oneStart:1,oneEnd:4,twoStart:9,twoEnd:12}];
const m=iris.supportMatrix(12,arms);
for(let i=0;i<12;i++)for(let j=0;j<12;j++)assert.equal(m[i*12+j],m[j*12+i]);
assert.ok(Math.abs(m[2*12+10]-Math.log(2))<1e-12,'Gaussian peaks use half-open interval midpoints');
assert.throws(()=>iris.supportMatrix(12,[{oneStart:0,oneEnd:4,twoStart:9,twoEnd:12}]),/coordinates/);
assert.equal(iris.constraintsFor([{l:0,r:8,k:4},{l:2,r:12,k:4}]),null,'overlapping stems cannot be hard constrained together');
assert.equal(iris.constraintsFor([{l:0,r:8,k:2},{l:4,r:12,k:2}]),null,'crossing stems must be rejected');
assert.deepEqual(iris.nnls([[2,0],[0,4]],[1,3]),[0.5,0.75]);
assert.deepEqual(iris.nnls([[2,0],[0,4]],[-1,3]),[0,0.75]);
const duplicate=iris.nnls([[2,2],[2,2]],[1,1]);
assert.ok(Math.abs(duplicate.reduce((a,b)=>a+b,0)-0.5)<1e-12,'singular faces have a nonnegative boundary solution');
const candidates=[
 {energy:-1,pairs:[{left:1,right:12}]},
 {energy:-2,pairs:[{left:1,right:12},{left:2,right:11}]},
 {energy:-3,pairs:[{left:5,right:10},{left:6,right:9}]}
];
assert.deepEqual(iris.cluster(candidates,2).map(c=>c.energy),[-2,-3]);
assert.throws(()=>iris.pick([candidates[0]],m,12,1),/zero range/);
// More than 50 stems can be cheap: these share bases, so none can combine.
const overlapping=Array.from({length:55},(_,i)=>({l:0,r:20+i,k:4}));
const jobs=iris.collectConstraints(overlapping);
assert.equal(jobs.length,55);
assert.deepEqual(jobs,overlapping.map(s=>iris.constraintsFor([s])));
assert.equal(iris.collectConstraints(Array.from({length:1000},(_,i)=>({l:0,r:20+i,k:4}))).length,1000);
assert.throws(()=>iris.collectConstraints(Array.from({length:1001},(_,i)=>({l:0,r:20+i,k:4}))),/1,000 constrained folds/);
// Compatible pairs, not just individual stems, count against the fold budget.
const separate=Array.from({length:45},(_,i)=>({l:i*12,r:i*12+8,k:4}));
assert.equal(iris.collectConstraints(separate.slice(0,44)).length,990);
assert.throws(()=>iris.collectConstraints(separate),/1,000 constrained folds/);
// Synthetic support reaches the former failure in collectStems itself.
const denseSupport=Float64Array.from({length:10000},(_,i)=>(Math.floor(i/100)+i%100)%7===0?10:0.1);
const denseStems=iris.collectStems('GC'.repeat(50),denseSupport,iris.parameters({}));
assert.ok(denseStems.length>50);
assert.throws(()=>iris.collectConstraints(denseStems),/1,000 constrained folds/);
assert.equal(iris.parameters({}).fraction,0.75,'retain the upstream default');
console.log('iris-fold-budget-without-stem-cap: ok');
if(fs.existsSync('/tmp/hyb2-iris-reference-support.json')){
 const fixture=JSON.parse(fs.readFileSync('/tmp/hyb2-iris-test-result.json','utf8'));
 const expected=JSON.parse(fs.readFileSync('/tmp/hyb2-iris-reference-support.json','utf8'));
 const actual=iris.supportMatrix(fixture.sequence.length,fixture.rawHybArms);
 assert.equal(actual.length,expected.length);
 assert.ok(actual.every((x,i)=>Math.abs(x-expected[i])<1e-12));
 console.log('iris-original-support-matrix: ok');
}
// cluster caches each row's nearest later neighbour; it must merge exactly
// as the former full-matrix scan (first minimum in row-major order).
function referenceCluster(candidates,C){
 if(candidates.length<=C) return candidates;
 const keys=c=>new Set(c.pairs.map(p=>p.left+':'+p.right));
 const dist=(a,b)=>{let same=0;for(const k of a) if(b.has(k)) same++;return a.size+b.size-2*same;};
 const sets=candidates.map(keys),n=candidates.length;
 const d=Array.from({length:n},()=>new Float64Array(n));
 for(let i=0;i<n;i++) for(let j=i+1;j<n;j++) d[i][j]=d[j][i]=dist(sets[i],sets[j]);
 const groups=new Map(candidates.map((_,i)=>[i,[i]]));
 while(groups.size>C){
  let best=Infinity,a=-1,b=-1;
  for(const i of groups.keys()) for(const j of groups.keys()) if(i<j&&d[i][j]<best){best=d[i][j];a=i;b=j;}
  const ga=groups.get(a),gb=groups.get(b);
  for(const j of groups.keys()) if(j!==a&&j!==b) d[a][j]=d[j][a]=(d[a][j]*ga.length+d[b][j]*gb.length)/(ga.length+gb.length);
  groups.set(a,ga.concat(gb));groups.delete(b);
 }
 return Array.from(groups.values(),idx=>candidates[idx.reduce((best,i)=>candidates[i].energy<candidates[best].energy?i:best,idx[0])]);
}
let seed=12345;
const rand=()=>((seed=Math.imul(seed,1103515245)+12345>>>0)/4294967296);
function randomCandidates(n,universe,maxPairs,energyLevels){
 return Array.from({length:n},(_,id)=>{
  const pairs=[];const used=new Set();const count=Math.floor(rand()*(maxPairs+1));
  for(let k=0;k<count;k++){const p=Math.floor(rand()*universe);if(!used.has(p)){used.add(p);pairs.push({left:p+1,right:p+200});}}
  return {id,energy:-Math.floor(rand()*energyLevels),pairs};
 });
}
for(let trial=0;trial<300;trial++){
 const n=2+Math.floor(rand()*60),universe=1+Math.floor(rand()*6);
 const cands=randomCandidates(n,universe,Math.min(universe,4),3);
 const C=1+Math.floor(rand()*Math.min(n,50));
 assert.deepEqual(iris.cluster(cands,C).map(c=>c.id),referenceCluster(cands,C).map(c=>c.id),'tie-heavy trial '+trial);
}
{
 const cands=randomCandidates(300,12,6,5);
 for(const C of [1,20,50]) assert.deepEqual(iris.cluster(cands,C).map(c=>c.id),referenceCluster(cands,C).map(c=>c.id),'n=300, C='+C);
 const large=randomCandidates(1000,40,10,8);
 const started=Date.now();
 const leaders=iris.cluster(large,20);
 const elapsed=Date.now()-started;
 assert.equal(leaders.length,20);
 assert.ok(elapsed<5000,'clustering 1000 candidates took '+elapsed+' ms');
 const refStarted=Date.now();
 assert.deepEqual(leaders.map(c=>c.id),referenceCluster(large,20).map(c=>c.id),'n=1000 matches the full scan');
 console.log('iris-cluster-row-minima: ok (n=1000: '+elapsed+' ms; full scan '+(Date.now()-refStarted)+' ms)');
}
console.log('iris-core: ok');
