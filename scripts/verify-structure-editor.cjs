'use strict';
// Drawing editor: VARNA-style layouts, styles, annotations, colour maps and exports.
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const root=path.resolve(__dirname,'..');
const context=vm.createContext({window:{},self:{},console});
for(const file of ['region-panels.js','structure-editor.js'])vm.runInContext(fs.readFileSync(path.join(root,'web',file),'utf8'),context,{filename:file});
const E=context.window.Hyb2StructureEditor;
const plain=value=>JSON.parse(JSON.stringify(value));
const dist=(p,q)=>Math.hypot(p.x-q.x,p.y-q.y);

// ViennaRNA MFE structures: a ZIKV 5′ region with multiloops and a tRNA cloverleaf.
const fixtures=[
  ['AGUUGUUGAUCUGUGUGAGUCAGACUGCGACAGUUCGAGUCUGAAGCGAGAGCUAACAACAGUAUCAACAGGUUUAAUUUGGAUUUGGAAACGAGAGUUUCUGGUCAUGAAAAACCCAAAGAAGAAAUCCGGAGGAUUCCGGAUUGUCAAUAUGCUAAAACGCGGAGUAGCCCGUGUAAACCCCUUGGGAGGUUUGAAGAG',
   '...(((((((((((((...(((((((.(((....))))))))))(((....))).)).)))).)))))))(((((......((((.((((((....)))))))))).....))))).......((((((((((....)))))))).))...........((((((......))))))(((((((...)).)))))......'],
  ['GCGGAUUUAGCUCAGUUGGGAGAGCGCCAGACUGAAGAUCUGGAGGUCCUGUGUUCGAUCCACAGAAUUCGCACCA',
   '(((((((..((((........)))).(((((.......))))).....(((((.......))))))))))))....']
];
for(const [sequence,dbn] of fixtures){
  const pairs=E.pairsFrom(dbn);
  for(const flat of [true,false]){
    const points=E.layout(sequence,pairs,'radiate',{flat});
    assert.equal(points.length,sequence.length);
    assert.ok(points.every(p=>Number.isFinite(p.x)&&Number.isFinite(p.y)),'finite radiate coordinates');
    // Backbone steps are 30 units (longer only between flat top-level domains)
    // and every pair spans 45 units.
    for(let i=1;i<points.length;i++){const step=dist(points[i-1],points[i]);assert.ok(Math.abs(step-30)<1e-6||(flat&&step>30),'backbone step '+i);}
    pairs.forEach(p=>assert.ok(Math.abs(dist(points[p.left-1],points[p.right-1])-45)<1e-6,'pair '+p.left+'-'+p.right));
    // No two bases sit on top of each other.
    let closest=Infinity;for(let i=0;i<points.length;i++)for(let j=i+1;j<points.length;j++)closest=Math.min(closest,dist(points[i],points[j]));
    assert.ok(closest>20,'bases overlap ('+closest.toFixed(1)+' units, flat='+flat+')');
  }
  const spaced=E.layout(sequence,pairs,'radiate',{spacing:2});
  assert.ok(Math.abs(dist(spaced[0],spaced[1])-60)<1e-6);
  assert.equal(E.dotBracket(sequence.length,pairs),dbn);
}
// The drawing the editor opens with must not pile bases on top of each other:
// it starts with Radiate, and the ViennaRNA polygon uses the same 30-unit step.
{
  const [sequence,dbn]=fixtures[0],opened=E.initial({sequence,dotBracket:dbn,pairs:E.pairsFrom(dbn),label:'t'});
  assert.equal(opened.layout.kind,'radiate');
  let closest=Infinity;opened.points.forEach((p,i)=>opened.points.slice(i+1).forEach(q=>{closest=Math.min(closest,dist(p,q));}));
  assert.ok(closest>20,'opening drawing overlaps ('+closest.toFixed(1)+')');
  const polygon=E.layout(sequence,dbn,'simple',{});assert.ok(Math.abs(dist(polygon[0],polygon[1])-30)<1e-6);
}
// Hairpins smaller than the closing pair put the loop centre inside the helix side.
const tight=E.layout('GGGACCC',E.pairsFrom('(((.)))'),'radiate',{});
assert.ok(tight.every(p=>Number.isFinite(p.x)&&Number.isFinite(p.y)));
assert.ok(Math.abs(dist(tight[2],tight[3])-30)<1e-6&&Math.abs(dist(tight[3],tight[4])-30)<1e-6);
// Crossing pairs keep their bracket level and are left out of the layout tree.
const knot=E.pairsFrom('((..[[..))..]]');
assert.equal(E.dotBracket(14,knot),'((..[[..))..]]');
assert.ok(E.layout('GGAACCAACCAAGG',knot,'radiate',{}).every(p=>Number.isFinite(p.x)));
const linear=E.layout('GGGAAACCC','(((...)))','linear',{});
assert.ok(linear.every((p,i)=>p.y===0&&p.x===i*30));
const turned=E.layout('GGGAAACCC','(((...)))','linear',{rotation:90});
assert.ok(Math.abs(turned[0].x-turned[8].x)<1e-6,'rotation turns the drawing');

// Domains, helices and loops for selection and annotation anchors.
const prediction={sequence:'GGGAAACCCAGGAAACCA',dotBracket:'(((...)))((...))..',label:'t',pairs:E.pairsFrom('(((...)))((...))..')};
const data=E.initial(prediction);
assert.deepEqual(plain(E.domainOf(data,5)),[3,7]);
assert.deepEqual(plain(E.domainOf(data,1)),[1,9]);
assert.equal(E.domainOf(data,18),null);
assert.deepEqual(plain(E.parentOf(data,3,7)),[2,8]);
assert.deepEqual(plain(E.helixOf(data,2)).sort((a,b)=>a-b),[1,2,3,7,8,9]);
assert.deepEqual(plain(E.loopOf(data,5)),[3,4,5,6,7]);
assert.throws(()=>E.helixOf(data,5),/unpaired/);
assert.throws(()=>E.loopOf(data,18),/exterior/);

// Drawing style, annotation and colour-map fields validate and render.
const styled=plain(data);
Object.assign(styled,{pairStyle:'lw',pairCurve:'line',font:'serif',baseFontSize:14,numbering:true,numberColor:'#112233',titleColor:'#445566',titleSize:20,backboneWidth:0,layout:{kind:'radiate',spacing:1.2,flat:false}});
styled.styles[4]={fill:'#ff0000',text:'#ffffff',outline:'#00ff00',radius:10};
styled.annotations.push({kind:'text',free:true,x:10,y:20,base:1,dx:0,dy:0,text:'Free <b>',color:'#123456',size:20});
styled.annotations.push({kind:'probe',marker:'arrow',direction:'out',intensity:2,base:5,dx:20,dy:-20,text:'',color:'#ff0000'});
styled.annotations.push({kind:'region',base:2,end:4,bases:[2,3,4],dx:0,dy:0,text:'',color:'#00ff00',opacity:.5,width:20});
styled.annotations.push({kind:'text',base:1,bases:[1,2,3,7,8,9],anchorType:'helix',dx:10,dy:10,text:'P1',color:'#000000'});
E.validate(styled,prediction.sequence,prediction.dotBracket);
const image=E.svg(styled);
assert.match(image,/font-family="serif"/);
assert.match(image,/Free &lt;b&gt;/);
assert.match(image,/stroke-opacity="0.5" stroke-width="20"/);
assert.match(image,/stroke="#00ff00" stroke-width="1"/,'base outline colour');
assert.match(image,/fill="#112233">10</,'number colour');
assert.ok(!/stroke="#8491B4"/.test(image),'backbone width 0 hides the backbone');
assert.match(image,/<circle[^>]+r="4" fill="#ffffff" stroke="#3C5488"/,'LW open circle for the non-canonical A·C pair');
assert.throws(()=>E.validate({...styled,pairStyle:'fancy'},prediction.sequence,prediction.dotBracket),/style/);
assert.throws(()=>E.validate({...styled,annotations:[{...styled.annotations[0],x:NaN}]},prediction.sequence,prediction.dotBracket),/free/);
assert.throws(()=>E.validate({...styled,annotations:[{...styled.annotations[1],intensity:9}]},prediction.sequence,prediction.dotBracket),/annotation style/);
assert.throws(()=>E.validate({...styled,layout:{kind:'naview',spacing:1,flat:true}},prediction.sequence,prediction.dotBracket),/layout/);
const arcs=E.svg({...styled,pairCurve:'arc'});assert.match(arcs,/ Q/);
const viz=E.svg({...styled,pairStyle:'rnaviz'}),none=E.svg({...styled,pairStyle:'none'});
assert.ok((viz.match(/<line/g)||[]).length>(none.match(/<line/g)||[]).length);

// User colour maps: per-base values or "position value" lines, any range.
assert.deepEqual(plain(E.parseValues('1 -0.5\n3 2',4)),[-0.5,null,2,null]);
assert.deepEqual(plain(E.parseValues('0.1,0.2;0.3 0.4',4)),[0.1,0.2,0.3,0.4]);
assert.throws(()=>E.parseValues('1 2 3',4),/Enter 4 values/);
assert.throws(()=>E.parseValues('9 1',4),/position/);
const mapped=plain(data);E.colorMap(mapped,'1 -1\n2 1',{minimum:'',maximum:'',palette:'bluered',caption:'SHAPE'});
assert.equal(mapped.styles[1].fill,'#2166ac');assert.equal(mapped.styles[2].fill,'#b2182b');assert.equal(mapped.evidence.minimum,-1);
E.validate(mapped,prediction.sequence,prediction.dotBracket);
assert.match(E.svg(mapped),/SHAPE · user values · -1–1/);
assert.throws(()=>E.colorMap(plain(data),'1 1\n2 1',{palette:'lau'}),/maximum above/);
E.clearColors(mapped);assert.equal(mapped.evidence,undefined);assert.equal(mapped.styles[1].fill,'#91D1C2');

// Exports: EPS, BPSEQ, DBN and a VARNA command.
const postscript=E.eps(styled);
assert.match(postscript,/^%!PS-Adobe-3\.0 EPSF-3\.0\n%%BoundingBox: 0 0 \d+ \d+/);
assert.match(postscript,/\(Free <b>\) show/);assert.match(postscript,/\/Times-Roman /);assert.match(postscript,/%%EOF\n$/);
assert.ok(!/[^\x00-\x7e]/.test(postscript),'EPS is ASCII');
assert.match(E.eps({...data,title:'5′ (x)'}),/\(5' \\\(x\\\)\) show/);
assert.match(E.bpseq(data),/^# .*\n1 G 9\n/);
assert.equal(E.editedDbn(data).split('\n')[2],'(((...)))((...))..');
const command=E.varnaCommand({...styled,title:"it's"});
assert.match(command,/VARNAcmd/);assert.match(command,/-structureDBN '\(\(\(\.\.\.\)\)\)\(\(\.\.\.\)\)\.\.'/);
assert.match(command,/-algorithm radiate/);assert.match(command,/-flat false/);assert.match(command,/-bpStyle lw/);
assert.match(command,/-title 'it'\\''s'/);
assert.match(command,/-applyBasesStyle\d+on '4'/);
assert.match(command,/-highlightRegion '2-4:radius=10,fill=#00ff00,outline=#00ff00'/);
assert.match(command,/-annotations 'Free <b>:type=P,x=10,y=20,color=#123456,size=20;P1:type=H,anchor=1/);
assert.match(command,/-chemProb '5-6:glyph=arrow,dir=out,intensity=2,color=#ff0000'/);
const evidenceCommand=E.varnaCommand(mapped.evidence?mapped:(E.colorMap(mapped,'1 0\n2 4',{palette:'lau'}),mapped));
assert.match(evidenceCommand,/-colorMap '0;4;0;/);assert.match(evidenceCommand,/-colorMapStyle '0:#FFFFFF,0.8:#4747FF/);

// CPLcofold: two strands, no backbone across the break, break kept in CT/DBN.
{
  const two={sequence:'GGGAAACCCGGGUUUCCC',dotBracket:'(((...)))(((...)))',label:'duplex',cutpoint:9,pairs:E.pairsFrom('(((...)))(((...)))')};
  const d=E.initial(two);
  assert.equal(d.cutpoint,9);
  E.validate(d,two.sequence,two.dotBracket);
  assert.throws(()=>E.validate({...d,cutpoint:18},two.sequence,two.dotBracket),/strand break/);
  const lines=E.ct(d).split('\n');
  assert.equal(lines[9].split('\t')[3],'0','strand A ends at 9');
  assert.equal(lines[10].split('\t')[2],'0','strand B starts at 10');
  assert.equal(E.editedDbn(d).split('\n').slice(1,3).join('|'),'GGGAAACCC&GGGUUUCCC|(((...)))&(((...)))');
  const backbone=d.points.length-1;
  const drawn=(E.svg(d).match(/stroke="#8491B4"/g)||[]).length;
  assert.equal(drawn,backbone-1,'no backbone line across the break');
  assert.deepEqual(plain(E.autoAnnotations(d,'ends').map(n=>n.text)),['A 5′','A 3′','B 5′','B 3′']);
  assert.match(E.varnaCommand(d),/Two strands: A is 1-9, B 10-18/);
}

console.log('Drawing editor: Radiate/linear/circular layouts, domains, styles, annotations, colour maps and EPS/BPSEQ/DBN/VARNA exports passed.');
