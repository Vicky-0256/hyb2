"use strict";
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const root=path.resolve(__dirname,'..'),workers=[];
class Worker {constructor(url){this.url=url;workers.push(this);} postMessage(m){this.message=m;} terminate(){this.terminated=true;} }
const ctx=vm.createContext({window:{},Worker,console});
for(const name of ['comrades-analysis.js','analysis-pages.js','structure-controller.js','structure-ui.js']) vm.runInContext(fs.readFileSync(path.join(root,'web',name),'utf8'),ctx);
const sequence='GGCGCGGCACCGUCCGCGGAACAAACGG';
const records=[{id:'s1',index:0,rnaOne:'synthetic',rnaTwo:'synthetic',rnaOneStart:103,rnaOneEnd:108,rnaTwoStart:113,rnaTwoEnd:118},
{id:'s2',index:1,rnaOne:'synthetic',rnaTwo:'synthetic',rnaOneStart:110,rnaOneEnd:114,rnaTwoStart:123,rnaTwoEnd:127}];
const state={summary:{rnaNames:['synthetic'],fileName:'synthetic.hyb'},activePage:'structure',
 reference:{mapping:{synthetic:'synthetic'},mapped:{synthetic:{referenceId:'synthetic',length:128}}},
 view:{structureRecords:records,structureEvidence:{guided:true,rnas:['synthetic'],records,fasta:{mapping:{synthetic:'synthetic'},sequences:[{id:'synthetic',sequence:'A'.repeat(100)+sequence}]}}},
 structure:{engine:'iris',source:'reference',rna:'synthetic',start:'101',end:'128',workflow:'custom',constraintMode:'none',evidenceLayout:'single',minimumLoop:'3',temperature:'37',irisK:'2',irisC:'20',irisFraction:'0.75',irisKRange:'4 5 6 7',status:'idle',viewMode:'folded'}};
const page=ctx.window.Hyb2Pages.renderStructure(state);
assert.match(page, /value="iris" selected/);
assert.match(page,/Predict IRIS ensemble/);
assert.match(page,/limit 300 nt/);
assert.doesNotMatch(page,/data-key="cplfoldAlpha"|data-key="cplfoldBeta"|data-key="constraintMode"/);
const prepared=ctx.window.Hyb2Pages.getStructureSequence(state);
assert.equal(prepared.sequence,sequence);
assert.equal(prepared.assembly.evidenceArms[0].oneStart,3);
assert.equal(prepared.assembly.evidenceArms[0].twoStart,13);
fs.writeFileSync('/tmp/hyb2-iris-ui-state.json',JSON.stringify(state));
const api={render(){},showToast(){}};
ctx.window.Hyb2Structure.predict(state,api);
const worker=workers.at(-1);
assert.ok(worker.url.includes('structure.worker.js'));
assert.equal(worker.message.type,'iris-fold');
assert.equal(worker.message.parameters.K,'2');
assert.equal(worker.message.evidenceArms.length,2);
const members=[{dotBracket:'('.repeat(4)+'.'.repeat(20)+')'.repeat(4),pairs:[],energy:-2,weight:0.7,unpaired:20},{dotBracket:'.'.repeat(28),pairs:[],energy:-1,weight:0.3,unpaired:28}];
worker.onmessage({data:{type:'complete',result:{engine:'IRIS',model:'IRIS test',sequence,candidates:members,selectedCandidate:0,...members[0],ensemble:{meanEnergy:-1.7,candidateCount:2,testedEnsembles:1},parameters:worker.message.parameters}}});
assert.equal(state.structure.status,'complete');
assert.equal(state.structure.result.rawHybArms.length,2);
assert.match(ctx.window.Hyb2Pages.renderStructure(state),/IRIS weighted ensemble/);
ctx.window.Hyb2StructureUI.handleAction('select-iris-candidate',state,api,{dataset:{candidateIndex:'1'}});
assert.equal(state.structure.result.selectedCandidate,1);
assert.equal(state.structure.result.dotBracket,members[1].dotBracket);
const report=ctx.window.Hyb2StructureUI.structureReport(state,state.structure.result);
assert.equal(report.iris.members.length,2);
assert.equal(report.iris.selectedMember,1);
assert.equal(report.prediction.mfe,null);
assert.equal(report.methodScope.irisBrowserAdaptation,true);
console.log('iris-ui-controller-export: ok');
let exported=null;
ctx.window.Hyb2StructureUI.handleAction('download-iris-ensemble',state,{...api,download(name,text){exported={name,text};}},null);
assert.equal(exported.name,'iris-ensemble.tsv');
assert.match(exported.text,/2\tyes\t0.3/);
// Exercise the production provider prefetch, not only a preassembled UI fixture.
(async function(){
 for(const name of ['analysis-data.js','data-provider.js','page-data.js']) vm.runInContext(fs.readFileSync(path.join(root,'web',name),'utf8'),ctx);
 const provider=new ctx.window.Hyb2Provider.BrowserProvider({data:ctx.window.Hyb2Data});
 provider.addDataset({id:'synthetic',primary:true,summary:{...state.summary,validRecords:2},records});
 provider.setReference(state.view.structureEvidence.fasta);
 const prefetched={...state,view:null,structure:{...state.structure,status:'idle',result:null}};
 const controller=ctx.window.Hyb2PageData.createController({state:prefetched,onUpdate(){}});
 controller.setProvider(provider);
 await controller.setSelection({datasetIds:['synthetic']});
 prefetched.structure={...state.structure,status:'idle',result:null};
 for(let i=0;i<5;i++){const task=controller.ensure('structure');if(!task)break;await task;}
 assert.equal(prefetched.view.structureEvidence.guided,true);
 assert.equal(prefetched.view.structureEvidence.records.length,2);
 console.log('iris-provider-evidence-prefetch: ok');
})().catch(e=>{console.error(e);process.exitCode=1;});
