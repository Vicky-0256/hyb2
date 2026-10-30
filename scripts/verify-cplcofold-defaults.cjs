'use strict';
// CPLcofold setup: region 2 defaults to region 1's most-supported partner
// window, and the sequence panel marks the chain break on both rows.
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const root=path.resolve(__dirname,'..');
const context=vm.createContext({window:{},self:{},console});
vm.runInContext(fs.readFileSync(path.join(root,'web','analysis-data.js'),'utf8'),context,{filename:'analysis-data.js'});
const D=context.window.Hyb2Data;
const rec=(s1,e1,s2,e2,r1='A',r2='A')=>({rnaOne:r1,rnaOneStart:s1,rnaOneEnd:e1,rnaTwo:r2,rnaTwoStart:s2,rnaTwoEnd:e2});
const records=[rec(10,30,5000,5020),rec(40,60,5100,5130),rec(5050,5070,20,45),rec(50,70,9000,9020),
  rec(10,30,15,40),            // both arms inside region 1: no partner
  rec(300,320,5000,5020),      // arm outside region 1
  rec(10,30,40,60,'A','B')];   // another RNA
const w=D.partnerWindow(records,'A',1,201);
assert.equal(w.rna,'A');assert.equal(w.start,5000);assert.equal(w.end,5200);assert.equal(w.records,3);
assert.equal(D.partnerWindow(records,'A',1000,2000),null);
assert.equal(D.partnerWindow([],'A',1,201),null);
// A window past the RNA's last mapped base slides back onto the RNA.
const edge=D.partnerWindow([rec(10,30,10700,10720),rec(12,32,10780,10807)],'A',1,201);
assert.equal(edge.end,10807);assert.equal(edge.start,10607);
const other=D.partnerWindow([rec(10,30,40,60,'A','B'),rec(15,35,45,65,'A','B')],'A',1,201);
assert.equal(other.rna,'B');assert.equal(other.records,2);

// The Contact Map draws one dataset, never the header's merge.
{
  const datasets=[{id:'primary',primary:true,label:'ZAFR_REP1',rnaNames:['AFR']},{id:'c1',label:'ZILM_REP2',rnaNames:['ILM']}];
  const state={datasets,selection:{datasetIds:['primary','c1']},summary:{rnaNames:['AFR','ILM']},contact:{datasetId:'',rnaX:'ILM',rnaY:'ILM'}};
  let chosen=D.contactDataset(state);
  assert.equal(chosen.id,'primary');assert.equal(state.contact.rnaX,'AFR','RNA X follows the dataset');
  state.contact.datasetId='c1';chosen=D.contactDataset(state);
  assert.equal(chosen.id,'c1');assert.deepEqual(Array.from(chosen.rnas),['ILM']);assert.equal(state.contact.rnaY,'ILM');
  state.contact.datasetId='';state.selection={datasetIds:['c1']};
  assert.equal(D.contactDataset(state).id,'c1','a one-dataset scope is the default');
}

const source=fs.readFileSync(path.join(root,'web','analysis-pages.js'),'utf8');
const start=source.indexOf('  function renderAlignedSequence(');
const end=source.indexOf('\n  }\n',start)+4;
const render=vm.runInContext('(function(){const escape=s=>String(s);'+source.slice(start,end)+';return renderAlignedSequence;})()',vm.createContext({}));
const html=render('ACGUACGUAC'+'GGGCCC','((((....))'+'..((..',null,'x',10);
assert.equal((html.match(/class="seq-break"/g)||[]).length,2,'the break is marked on the sequence and dot-bracket rows');
assert.match(html,/ACGUACGUAC<span class="seq-break"[^>]*>&amp;<\/span> GGGCCC/);
assert.doesNotMatch(render('ACGU','....',null,'x',0),/seq-break/);
console.log('Contact Map dataset choice, CPLcofold defaults: partner window from HYB records and the sequence-panel chain break passed.');
