// Golden outputs of web/structure-editor.js for the desktop port's tests
// (drawing_test.go). From the repository root:
//   node desktop/app/views/testdata/drawing_golden.js web desktop/app/views/testdata/drawing_golden.json <full outputs.json>
// The third file keeps every output in full, to compare when a hash differs.
const fs=require('fs'),path=require('path'),vm=require('vm');
const web=process.argv[2],out=process.argv[3];
globalThis.window=globalThis;globalThis.self=globalThis;
for(const f of ['region-panels.js','structure-editor.js'])vm.runInThisContext(fs.readFileSync(path.join(web,f),'utf8'),{filename:f});
const E=window.Hyb2StructureEditor;
const cases=[];
function add(name,result,edit){
  const d=E.initial(result);if(edit)edit(d,result);
  const o={name,result,drawing:d,svg:E.svg(d,null),svgSel:E.svg(d,[3,7]),eps:E.eps(d),ct:E.ct(d),bpseq:E.bpseq(d),json:JSON.stringify(d,null,2)};
  try{o.dbn=E.editedDbn(d);}catch(e){o.dbnError=e.message;}
  try{o.varna=E.varnaCommand(d);}catch(e){o.varnaError=e.message;}
  o.layouts={};
  for(const kind of ['radiate','simple','circular','linear'])for(const flat of [true,false])for(const spacing of [1,1.7])o.layouts[kind+'/'+flat+'/'+spacing]=E.layout(d.sequence,d.pairs,kind,{flat,spacing});
  cases.push(o);
}
const seq='GGGAAACCCAGCUUCGGCUGGAUCCGAAAGGAUCUAGCGCAAAGCGCUAAUUGGCCAAAAGGCCAAUUACGUACGUACGUA';
const dbn='(((...)))((((....))))((((((...))))))((((((...))))))..((((((((....))))))))........';
const pairs=E.pairsFrom(dbn);
add('plain',{sequence:seq,dotBracket:dbn,pairs,label:'ZIKV:1–82 test'});
const multi='((((..((((...))))..((((...))))...((((....))))..))))..((((....)))).....';
// a sequence that pairs the dot-bracket (G–C, C–G, some G·U and A·G)
const pt=E.pairsFrom(multi),chars=Array.from(multi,(c,i)=>'AUAC'[i%4]);
pt.forEach((p,k)=>{const kind=k%5;chars[p.left-1]=kind===3?'G':kind===4?'A':'GC'[k%2];chars[p.right-1]=kind===3?'U':kind===4?'G':'CG'[k%2];});
const mseq=chars.join('');
add('multi-styled',{sequence:mseq,dotBracket:multi,pairs:E.pairsFrom(multi),label:'Multi  loop\tRNA'},d=>{
  d.pairStyle='lw';d.font='serif';d.baseFontSize=14;d.numberColor='#aa0000';d.backboneWidth=2.5;d.titleColor='#123456';d.titleSize=22;d.numberInterval=5;
  d.styles[3]={fill:'#ff0000',text:'#ffffff',radius:11,outline:'#00ff00'};d.styles[4]={fill:'#ff0000',text:'#ffffff',radius:11,outline:'#00ff00'};d.styles[10]={fill:'#91d1c2',text:'#222222',radius:9};
  d.pairs[0].color='#e64b35';d.pairs[0].width=3.5;
  d.pairs.push({left:5,right:18,color:'#3C5488',width:2});d.pairs=d.pairs.filter(p=>!(p.left===7));
  d.annotations=d.annotations.concat(E.autoAnnotations(d,'ends'),E.autoAnnotations(d,'helices'));
  d.annotations.push({kind:'region',base:7,end:17,text:'',bases:[7,8,9,10,11,12,13,14,15,16,17],dx:25,dy:-35,color:'#4dbbd5',size:14,opacity:.4,width:20});
  d.annotations.push({kind:'region',base:30,end:30,text:'one',dx:25,dy:-35,color:'#4dbbd5',size:14,opacity:.25,width:32});
  d.annotations.push({kind:'probe',base:12,text:'1M7',dx:25,dy:-35,color:'#E64B35',size:12,marker:'arrow',direction:'out',intensity:1.5});
  d.annotations.push({kind:'probe',base:13,text:'',dx:-20,dy:30,color:'#E64B35',size:12,marker:'dot',direction:'in',intensity:1});
  d.annotations.push({kind:'probe',base:70,text:'end',dx:-20,dy:30,color:'#E64B35',size:12,marker:'triangle',direction:'in',intensity:2});
  d.annotations.push({kind:'text',base:20,text:'free <&> "it\'s"',dx:0,dy:0,color:'#3C5488',size:16,free:true,x:12.5,y:-40.25});
  d.annotations.push({kind:'text',base:7,text:'helix',bases:[7,17,8,16],dx:25,dy:-35,color:'#3C5488',size:14,anchorType:'helix'});
  E.colorMap(d,'1 0.5\n2 1.5\n# c\n10 3\n40 -1',{minimum:'',maximum:'',palette:'viridis',caption:'SHAPE'});
});
add('arc-cut',{sequence:seq,dotBracket:dbn,pairs,label:'cofold',cutpoint:30},d=>{
  d.points=E.layout(d.sequence,d.pairs,'linear',{});d.layout={kind:'linear',spacing:1,flat:true};d.pairCurve='arc';d.pairStyle='rnaviz';d.numbering=false;
  d.pairs.push({left:4,right:52,color:"#3C5488",width:2});d.pairs.push({left:5,right:75,color:'#3C5488',width:2});
  d.annotations=E.autoAnnotations(d,'ends');
});
add('evidence',{sequence:seq,dotBracket:dbn,pairs,label:'COMRADES',comradesEvidence:{pairs:[{one:1,two:9,count:5},{one:2,two:8,count:2},{one:12,two:19,count:9},{one:40,two:31,count:1}]}},d=>{d.title='Ev';});
add('evidence-heat-over',{sequence:seq,dotBracket:dbn,pairs,label:'H',comradesEvidence:{pairs:[{one:1,two:9,count:5},{one:12,two:19,count:9}]}},(d,r)=>{
  E.applyEvidence(d,E.evidenceFor(r),'heat');d.evidence.overrides=true;d.styles[2]={...d.styles[2],outline:'#000000'};d.pairStyle='none';
});
fs.writeFileSync(process.argv[4],JSON.stringify(cases));
// the committed golden file: inputs, rounded layouts and SHA-256 of each output
const crypto=require('crypto'),sha=t=>crypto.createHash('sha256').update(t,'utf8').digest('hex');
const keep=['radiate/true/1','radiate/false/1','radiate/true/1.7','simple/true/1','circular/true/1','linear/true/1.7'];
fs.writeFileSync(out,JSON.stringify(cases.map(c=>{const o={name:c.name,result:c.result,drawing:c.drawing,layouts:{},sha:{}};
  keep.forEach(k=>{o.layouts[k]=c.layouts[k].map(p=>[Math.round(p.x*1e6)/1e6,Math.round(p.y*1e6)/1e6]);});
  ['svg','svgSel','eps','ct','bpseq','json','dbn','varna'].forEach(k=>{if(c[k]!=null)o.sha[k]=sha(c[k]);});
  ['dbnError','varnaError'].forEach(k=>{if(c[k])o[k]=c[k];});
  return o;}))+'\n');
