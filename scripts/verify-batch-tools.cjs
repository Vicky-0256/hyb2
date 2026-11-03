'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const root=path.resolve(__dirname,'..');
const context=vm.createContext({window:{},self:{},console,TextEncoder,Uint8Array,Uint32Array,Int32Array,Float64Array,Blob,setTimeout,clearTimeout});
for(const file of ['analysis-data.js','deseq.js','batch-tools.js','region-panels.js','structure-editor.js','results.js'])vm.runInContext(fs.readFileSync(path.join(root,'web',file),'utf8'),context,{filename:file});
const B=context.window.Hyb2Batch,E=context.window.Hyb2StructureEditor;
const record={id:'r',lineNumber:3,raw:['r','ACGUGC','old','one',1,3,2,4,0,'two',4,6,1,3,0,0,'Type_1','extra'].join('\t')};
const fasta={mapping:{one:'one',two:'two'},sequences:[{id:'one',sequence:'AACGTAC'},{id:'two',sequence:'GCTAAA'}]};
assert.deepEqual(Array.from(B.prepareEnergy(record,fasta)),['ACG','GCU']);
assert.throws(()=>B.prepareEnergy(record,{mapping:{},sequences:[]}),/No mapped/);
assert.throws(()=>B.prepareEnergy({...record,raw:record.raw.replace('\t2\t4\t','\t4\t2\t')},fasta),/antisense/);
assert.throws(()=>B.prepareEnergy(record,{...fasta,sequences:[{id:'one',sequence:'ANCGTAC'},{id:'two',sequence:'GCTAAA'}]}),/ambiguous/);
const source='# comment\r\n\r\n'+record.raw+'\r\ninvalid\n';
const updated=B.rewriteEnergy(source,new Map([[3,{raw:record.raw,energy:-2.5}]]));
assert.equal(updated,source.replace('\told\t','\t-2.50\t'));
assert.equal(B.rewriteEnergy('\ufeff'+record.raw+'\n',new Map([[1,{raw:record.raw,energy:0}]])),'\ufeff'+record.raw.replace('\told\t','\t0.00\t')+'\n');
assert.throws(()=>B.rewriteEnergy(source,new Map([[3,{raw:'wrong',energy:1}]])),/changed/);
const interactions=Array.from({length:15},(_,i)=>({direction:'up',x:200+i*10,y:500,padj:(15-i)/100,log2FoldChange:2})).concat([{direction:'down',x:10,y:20,padj:.01,log2FoldChange:-2}]);
const regions=B.topRegions(interactions);
assert.equal(regions.length,11);assert.equal(regions[0].x,340);assert.equal(regions[10].x1,1);assert.equal(regions[10].rank,1);
const samples=[{id:'a',label:'A <unsafe>',condition:'A',rnaX:'r',rnaY:'r',arms:new Int32Array([10,20,20,30,10,20,20,30])},{id:'b',label:'B',condition:'B',rnaX:'r',rnaY:'r',arms:new Int32Array([1000,1009,1200,1209])}];
const batch=B.mapBatch(samples,[regions[10]]);
assert.equal(batch.panels[0].samples[0].cells.length,4);
assert.ok(batch.panels[0].samples[0].cells.every(c=>c[2]===2));
assert.equal(batch.panels[0].samples[1].cells.length,0);
assert.equal(batch.cap,2);assert.equal(B.histogramQuantile(new Map([[1,1],[4,1]]),.95),3.8499999999999996);
const result={labels:{A:'Alpha',B:'Beta'},datasets:[],summary:{parameters:{}},completedAt:'now'};
// bins start at 0: contacts at nt 1-9 (bin 0) stay in a window clamped to 1,
// while the drawn axis and interactions.tsv keep the 1-based lower bound
{
  assert.equal(regions[10].binX1,0);assert.equal(regions[10].binY1,0);
  const nearStart=B.mapBatch([{id:'s',label:'S',condition:'A',rnaX:'r',rnaY:'r',arms:new Int32Array([1,5,3,8])}],[regions[10]]);
  assert.equal(JSON.stringify(nearStart.panels[0].samples[0].cells),'[[0,0,1]]');
  assert.match(B.mapSvg(nearStart.panels[0],nearStart.cap,{A:'A',B:'B'},nearStart.quantile),/>1<\/text>/);
}
// colour as Lau/contact_density_map_zoom.R: pmin(count, quant) on limits
// c(0, ceiling(quant)); an integer quantile reaches black, a fractional one
// stops at a dark grey and the legend says so
{
  const panel={region:regions[10],samples:[{id:'a',label:'A',condition:'A',rnaX:'r',rnaY:'r',cells:[[10,20,9]]}]};
  const whole=B.mapSvg(panel,2,{A:'A',B:'B'},2);
  assert.ok(whole.includes('fill="rgb(0,0,0)"'));assert.match(whole,/common white–black cap 2</);
  const fractional=B.mapSvg(panel,4,{A:'A',B:'B'},3.8499999999999996);
  assert.ok(!fractional.includes('fill="rgb(0,0,0)"'));
  assert.match(fractional,/white–black scale 0–4, counts clipped at 3\.85 \(darkest grey, not black\)/);
}
const files=B.mapFiles(batch,result),drawing=files.find(f=>f.path.endsWith('.svg')).text;
assert.ok(drawing.includes('A &lt;unsafe&gt;'));assert.ok(drawing.includes('No contacts in this window'));
assert.ok(files.find(f=>f.path==='preview.html'));assert.ok(files.find(f=>f.path.endsWith('.tsv')).text.includes('\t2\n'));
const zip=context.window.Hyb2Results.zipBytes(files,'test');assert.equal(zip[0],80);assert.equal(zip[1],75);
const prediction={sequence:'GGGAAACCC',dotBracket:'(((...)))',pairs:[{left:1,right:9},{left:2,right:8},{left:3,right:7}]};
const data=E.initial(prediction);assert.equal(data.points.length,9);assert.ok(data.points.every(p=>Number.isFinite(p.x)&&Number.isFinite(p.y)));
assert.equal(E.validate(data,prediction.sequence,prediction.dotBracket).pairs.length,3);
assert.throws(()=>E.validate({...data,sequence:'AA'},prediction.sequence,prediction.dotBracket),/different/);
assert.throws(()=>E.validate({...data,pairs:[{left:1,right:9,color:'#000000',width:2},{left:1,right:8,color:'#000000',width:2}]},prediction.sequence,prediction.dotBracket),/overlapping/);
data.annotations.push({base:1,text:'<script>bad</script>',dx:20,dy:-20,color:'#000000'});
assert.ok(E.svg(data).includes('&lt;script&gt;'));assert.ok(!E.svg(data).includes('<script>'));
assert.ok(E.ct(data).includes('1\tG\t0\t2\t9\t1'));
const session={data,undo:[JSON.parse(JSON.stringify(data))],redo:[]};session.data.title='changed';E.undo(session);assert.notEqual(session.data.title,'changed');E.redo(session);assert.equal(session.data.title,'changed');
console.log('Batch energy, Top 10 map counts/ranking/exports, drawing validation and undo checks passed.');

// Exercise the async UI lifecycle: a reused worker, full-source export,
// cancellation during input reading, and stale comparison rejection.
const lifecycleTest=(async()=>{
 let workers=0, renders=0, downloaded=null;
 class TestWorker {
  constructor(){workers++;this.dead=false;}
  terminate(){this.dead=true;}
  postMessage(message){setTimeout(()=>{if(!this.dead)this.onmessage({data:{type:'result',version:'test',energies:new Float64Array(message.pairs.length).fill(-1.25)}});},1);}
 }
 context.Worker=TestWorker;
 const list=Array.from({length:130},(_,i)=>({...record,id:'r'+i,lineNumber:i+2,raw:record.raw.replace(/^r\t/,'r'+i+'\t')}));
 const source='# retained\n'+list.map(r=>r.raw).join('\n')+'\n';
 const state={summary:{},fasta,file:{name:'test.hyb',text:async()=>source}};
 const api={queryHybrids:async()=>({rows:list}),render(){renders++;B.renderEnergy(state);},download(name,text){downloaded={name,text};},showToast(){}};
 B.handleAction(state,{dataset:{featureAction:'batch-energy-run'}},api);
 const wait=async predicate=>{for(let i=0;i<100;i++){if(predicate())return;await new Promise(r=>setTimeout(r,10));}throw new Error('Async batch test timeout');};
 await wait(()=>!state.hybEnergy.running);
 assert.equal(state.hybEnergy.result.metadata.updated,130);assert.equal(workers,1,'reuse WASM worker across chunks');assert.ok(renders>2);
 B.handleAction(state,{dataset:{featureAction:'batch-energy-hyb'}},api);
 assert.ok(downloaded.text.startsWith('# retained\n'));assert.equal((downloaded.text.match(/\t-1.25\t/g)||[]).length,130);
 let release;
 api.queryHybrids=()=>new Promise(resolve=>{release=resolve;});
 B.handleAction(state,{dataset:{featureAction:'batch-energy-run'}},api);
 B.handleAction(state,{dataset:{featureAction:'batch-energy-cancel'}},api);
 release({rows:list});await new Promise(r=>setTimeout(r,20));
 assert.equal(state.hybEnergy.result,null);assert.equal(workers,1);assert.equal(state.hybEnergy.running,false);
 context.window.Hyb2DeseqCard={inputKey:()=> 'new'};
 const cmp={comparison:{deseq:{result:{inputKey:'old'}}}};
 B.handleAction(cmp,{dataset:{featureAction:'batch-maps-run'}},{render(){},getArms(){throw new Error('must not read stale data');}});
 assert.match(cmp.comparison.topMaps.message,/again after changing/);
 console.log('Batch worker reuse, cancellation, full-file output and stale-result guards passed.');
})().catch(error=>{console.error(error);process.exitCode=1;});

// Configurable Lau -q, including R's endpoint probabilities. The raw counts
// are invariant: only the common colour cap and export provenance change.
assert.equal(B.contrastQuantile('0.8'),.8);
for(const bad of ['', 'NaN', -1, 1.01, Infinity])assert.throws(()=>B.contrastQuantile(bad),/quantile/);
const lowest=B.mapBatch(samples,[regions[10]],null,0),highest=B.mapBatch(samples,[regions[10]],null,1);
assert.equal(lowest.quantile,1);assert.equal(highest.quantile,2);
assert.equal(JSON.stringify(lowest.panels),JSON.stringify(highest.panels));
const customFiles=B.mapFiles(lowest,result);
assert.equal(JSON.parse(customFiles.find(f=>f.path==='parameters.json').text).quantile,0);
assert.match(customFiles.find(f=>f.path==='preview.html').text,/shared 0% count cap/);
assert.throws(()=>B.mapBatch(samples,[regions[10]],null,'oops'),/quantile/);
const controlState={comparison:{}};
assert.equal(B.handleInput(controlState,{dataset:{feature:'batch-map-quantile'},value:'0.75'}),true);
assert.equal(controlState.comparison.topMapQuantile,'0.75');
assert.match(B.renderMaps(controlState.comparison,false),/value="0.75"/);

// Exact Lau colour-map control points and interpolation (Java truncates RGB).
for(const [fraction,color] of [[0,'#ffffff'],[.2,'#4747ff'],[.55,'#1cff47'],[.65,'#ffff47'],[.9,'#ff4747'],[1,'#b64747']])assert.equal(E.supportColor(fraction*100,100),color);
assert.equal(E.supportColor(10,100),'#a3a3ff');
const supported={...prediction,constraintMode:'hyb-guided',nucleotideSupport:[20,55,100,0,0,0,100,55,20]};
const evidenceDrawing=E.initial(supported);
assert.equal(evidenceDrawing.styles[1].fill,'#4747ff');
assert.equal(evidenceDrawing.styles[4].fill,'#ffffff');
assert.equal(evidenceDrawing.evidence.kind,'RNAcofold supporting observations');
// IRIS and CPLfold are coloured by COMRADES support scored from their RNAcofold evidence.
const scoredByComrades=E.evidenceFor({...prediction,engine:'CPLfold',nucleotideSupport:[9,9,9,9,9,9,9,9,9],comradesEvidence:{pairs:[{one:2,two:8,count:3},{one:8,two:2,count:1},{one:4,two:6,count:7}]}});
assert.deepEqual(Array.from(scoredByComrades.values),[0,4,0,0,0,0,0,4,0]);assert.equal(scoredByComrades.maximum,4);
assert.equal(E.evidenceFor({...prediction,engine:'CPLfold',nucleotideSupport:[1,1,1,1,1,1,1,1,1]}),null);
assert.equal(E.evidenceFor({...prediction,nucleotideSupport:[]}),null);
const notes=E.autoAnnotations(evidenceDrawing,'helices');assert.equal(notes.length,1);assert.equal(notes[0].bases.length,6);
const loops=E.autoAnnotations(evidenceDrawing,'hairpins');assert.equal(loops.length,1);assert.equal(loops[0].bases.join(','),'4,5,6');
assert.equal(E.autoAnnotations(evidenceDrawing,'ends').length,2);
assert.throws(()=>E.autoAnnotations({...evidenceDrawing,pairs:[{left:1,right:7},{left:2,right:9}]},'hairpins'),/non-crossing/);
evidenceDrawing.annotations=notes.concat(loops,[{kind:'region',base:2,end:5,bases:[2,3,4,5],text:'region',dx:25,dy:-35,color:'#E64B35'},{kind:'probe',base:5,text:'probe',marker:'triangle',dx:25,dy:-35,color:'#4DBBD5'}]);
assert.equal(E.validate(evidenceDrawing,prediction.sequence,prediction.dotBracket).annotations.length,4);
const editorSvg=E.svg(evidenceDrawing);assert.match(editorSvg,/stroke-opacity="0.25"/);assert.match(editorSvg,/RNAcofold supporting observations/);assert.match(editorSvg,/Hairpin 1/);
const invalidDrawing=JSON.parse(JSON.stringify(evidenceDrawing));invalidDrawing.annotations[2].end=99;
assert.throws(()=>E.validate(invalidDrawing,prediction.sequence,prediction.dotBracket),/highlight/);
const anchorBefore=E.anchor(evidenceDrawing,notes[0]);notes[0].bases.forEach(i=>{evidenceDrawing.points[i-1].x+=10;});
assert.ok(Math.abs(E.anchor(evidenceDrawing,notes[0]).x-anchorBefore.x-10)<1e-9);
console.log('Configurable quantiles, Lau evidence colours, region/probe validation and automatic annotations passed.');

// Energy engines and the worker pool: RNAcofold on several workers, records
// filtered by the Interactions filters, CPLcofold's batch message, and a
// header scope change (state.summary) that leaves the card alone.
lifecycleTest.then(async()=>{
 const posted=[];let created=0;
 class PoolWorker {
  constructor(url,options){created++;this.url=url;this.options=options;this.dead=false;}
  terminate(){this.dead=true;}
  postMessage(message){posted.push({url:this.url,module:!!(this.options&&this.options.type==='module'),message});setTimeout(()=>{if(!this.dead)this.onmessage({data:{type:'result',version:'test',energies:new Float64Array(message.pairs.length).fill(message.type==='cplfold-energy-batch'?-7.5:-2.25)}});},1);}
 }
 context.Worker=PoolWorker;context.window.navigator={hardwareConcurrency:4};
 const list=Array.from({length:300},(_,i)=>({...record,id:'p'+i,lineNumber:i+1,raw:record.raw.replace(/^r\t/,'p'+i+'\t')}));
 const source=list.map(r=>r.raw).join('\n')+'\n';
 const primarySummary={validRecords:300};
 const state={summary:primarySummary,primarySummary,fasta,file:{name:'pool.hyb',text:async()=>source},filters:{search:'p1'}};
 const queries=[];
 const api={queryHybrids:async query=>{queries.push(query);return {rows:query.filters?list.filter(r=>r.id.startsWith('p1')):list};},render(){B.renderEnergy(state);},download(){},downloadBlob(){},showToast(){}};
 const wait=async predicate=>{for(let i=0;i<300;i++){if(predicate())return;await new Promise(r=>setTimeout(r,10));}throw new Error('pool test timeout');};
 // RNAcofold, all records: 300 / 64 = 5 chunks on min(8, 4 - 1, 5) = 3 workers
 B.handleAction(state,{dataset:{featureAction:'batch-energy-run'}},api);
 await wait(()=>!state.hybEnergy.running);
 assert.equal(state.hybEnergy.result.metadata.updated,300);assert.equal(created,3,'three parallel RNAcofold workers');
 assert.ok(posted.every(p=>p.message.type==='cofold-batch'&&!p.module));
 assert.equal((state.hybEnergy.result.hyb.match(/\t-2.25\t/g)||[]).length,300,'every energy lands on its own line');
 // a header scope change keeps the result
 state.summary={validRecords:999};assert.ok(B.renderEnergy(state).includes('Download updated HYB'),'scope change keeps the energies');
 // CPLcofold on the filtered records only
 B.handleInput(state,{dataset:{feature:'batch-energy-setting',key:'engine'},value:'cplcofold'});
 B.handleInput(state,{dataset:{feature:'batch-energy-setting',key:'records'},value:'filtered'});
 created=0;posted.length=0;
 B.handleAction(state,{dataset:{featureAction:'batch-energy-run'}},api);
 await wait(()=>!state.hybEnergy.running);
 const filtered=list.filter(r=>r.id.startsWith('p1')).length;
 assert.equal(state.hybEnergy.result.metadata.updated,filtered);assert.equal(state.hybEnergy.result.metadata.unchanged,300-filtered);
 assert.equal(state.hybEnergy.result.metadata.engine,'CPLcofold, pseudoknots off');
 assert.ok(posted.every(p=>p.message.type==='cplfold-energy-batch'&&p.module&&p.message.allowPseudoknot===false&&/cplfold\.worker\.mjs/.test(p.url)));
 assert.ok(created>=1&&created<=3);assert.ok(queries.some(q=>q.filters&&q.filters.search==='p1'));
 assert.match(state.hybEnergy.result.audit,/not selected by the Interactions filters/);
 assert.equal((state.hybEnergy.result.hyb.match(/\t-7.50\t/g)||[]).length,filtered);
 console.log('Energy engines (RNAcofold pool, CPLcofold), filtered records and scope independence passed.');
}).catch(error=>{console.error(error);process.exit(1);});
