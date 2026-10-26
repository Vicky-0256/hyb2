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
if(fs.existsSync('/tmp/hyb2-iris-reference-support.json')){
 const fixture=JSON.parse(fs.readFileSync('/tmp/hyb2-iris-test-result.json','utf8'));
 const expected=JSON.parse(fs.readFileSync('/tmp/hyb2-iris-reference-support.json','utf8'));
 const actual=iris.supportMatrix(fixture.sequence.length,fixture.rawHybArms);
 assert.equal(actual.length,expected.length);
 assert.ok(actual.every((x,i)=>Math.abs(x-expected[i])<1e-12));
 console.log('iris-original-support-matrix: ok');
}
console.log('iris-core: ok');
