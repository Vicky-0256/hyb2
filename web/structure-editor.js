/* Browser RNA drawing editor. Prediction and edited drawing remain separate. */
(function(root){
  'use strict';
  const NS='http://www.w3.org/2000/svg';
  const clone=value=>JSON.parse(JSON.stringify(value));
  const xml=value=>String(value).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const num=v=>String(Math.round(v*100)/100);
  // Lau plot_VARNA colour stops (ModeleColorMap interpolates RGB channels and
  // truncates to integer), the website palette and VARNA-style alternatives.
  const PALETTES={
    lau:[[0,'#FFFFFF'],[.2,'#4747FF'],[.55,'#1CFF47'],[.65,'#FFFF47'],[.9,'#FF4747'],[1,'#B64747']],
    brand:[[0,'#FFFFFF'],[.2,'#91D1C2'],[.55,'#4DBBD5'],[.9,'#E64B35'],[1,'#3C5488']],
    heat:[[0,'#FFFFFF'],[.35,'#FFE066'],[.7,'#F08C00'],[1,'#B00020']],
    bluered:[[0,'#2166AC'],[.5,'#F7F7F7'],[1,'#B2182B']],
    grey:[[0,'#FFFFFF'],[1,'#252525']],
    viridis:[[0,'#440154'],[.25,'#3B528B'],[.5,'#21918C'],[.75,'#5EC962'],[1,'#FDE725']]
  };
  const PAIR_STYLES=['simple','lw','rnaviz','none'],FONTS={'sans-serif':'Helvetica','serif':'Times-Roman','monospace':'Courier'};
  const LAYOUTS=['radiate','simple','circular','linear'],CANONICAL=new Set(['AU','UA','GC','CG']),WOBBLE=new Set(['GU','UG']);
  const DEFAULT_BASE={fill:'#91D1C2',text:'#222222',radius:9};
  function interpolate(stops,t){
    for(let i=1;i<stops.length;i++) if(t<=stops[i][0]){
      const f=(t-stops[i-1][0])/(stops[i][0]-stops[i-1][0]);
      return '#'+[1,3,5].map(k=>Math.floor(parseInt(stops[i-1][1].slice(k,k+2),16)*(1-f)+parseInt(stops[i][1].slice(k,k+2),16)*f).toString(16).padStart(2,'0')).join('');
    }
    return stops[stops.length-1][1];
  }
  // Scores here are already computed by the engine; zero is white.
  function supportColor(value, maximum, palette='lau') {
    if (!(value > 0) || !(maximum > 0)) return '#ffffff';
    return interpolate(PALETTES[palette]||PALETTES.brand,Math.min(1,value/maximum));
  }
  // Engine evidence spans 0–maximum; a user colour map carries its own minimum.
  function mapColor(e,value){
    if(e.minimum==null)return supportColor(value,e.maximum,e.palette);
    return interpolate(PALETTES[e.palette]||PALETTES.brand,Math.max(0,Math.min(1,(value-e.minimum)/(e.maximum-e.minimum))));
  }
  function textOn(fill){const r=parseInt(fill.slice(1,3),16),g=parseInt(fill.slice(3,5),16),b=parseInt(fill.slice(5,7),16);return (.2126*r+.7152*g+.0722*b)<125?'#ffffff':'#222222';}
  // COMRADES support for every engine: a base takes the number of HYB records
  // whose RNAcofold pair is the pair it forms in this structure (0 when
  // unpaired or unsupported), as comrades-analysis.js scoreStructurePairs does.
  // COMRADES(ViennaRNA) results carry it already; IRIS and CPLfold results
  // carry the RNAcofold evidence, scored here against the shown candidate.
  function comradesSupport(result){
    if(result.constraintMode==='hyb-guided'&&Array.isArray(result.nucleotideSupport))return Array.from({length:result.sequence.length},(_,i)=>Math.max(0,Number(result.nucleotideSupport[i])||0));
    const evidence=result.comradesEvidence;
    if(!evidence||!Array.isArray(evidence.pairs))return null;
    const counts=new Map();
    evidence.pairs.forEach(e=>{const key=Math.min(e.one,e.two)+':'+Math.max(e.one,e.two);counts.set(key,(counts.get(key)||0)+(Number(e.count)||0));});
    const values=Array(result.sequence.length).fill(0);
    (result.pairs||[]).forEach(p=>{const a=Math.min(p.left,p.right),b=Math.max(p.left,p.right),count=counts.get(a+':'+b)||0;if(count>0&&a>=1&&b<=values.length){values[a-1]=count;values[b-1]=count;}});
    return values;
  }
  function evidenceFor(result) {
    const values=comradesSupport(result);
    if(!values)return null;
    const maximum=values.reduce((a,b)=>Math.max(a,b),0);
    if(!maximum)return null;
    return {values,maximum,kind:'RNAcofold supporting observations',palette:'lau',source:'COMRADES support'};
  }
  function applyEvidence(drawing, evidence, palette='lau') {
    if(!evidence)throw new Error('This prediction has no experimental support scores.');
    drawing.evidence=clone(evidence);drawing.evidence.palette=palette;drawing.evidence.overrides=false;
    drawing.evidence.values.forEach((value,i)=>{
      if(value==null)return;
      const fill=mapColor(drawing.evidence,value);
      drawing.styles[i+1]={...(drawing.styles[i+1]||{radius:9}),fill,text:textOn(fill)};
    });
  }
  // VARNA-style colour map: one value per base, or "position value" lines.
  // Bases without a value keep their current colour.
  function parseValues(text,n){
    const lines=String(text).split(/\r?\n/).map(l=>l.trim()).filter(l=>l&&!/^#/.test(l)),values=Array(n).fill(null);
    const tokens=lines.map(l=>l.split(/[\s,;]+/).filter(Boolean));
    if(tokens.length&&tokens.every(t=>t.length===2)){
      tokens.forEach(([p,v])=>{const i=Number(p),x=Number(v);if(!Number.isInteger(i)||i<1||i>n||!Number.isFinite(x))throw new Error('Each "position value" line needs a position from 1 to '+n+' and a number.');values[i-1]=x;});
    } else {
      const flat=tokens.flat();
      if(flat.length!==n)throw new Error('Enter '+n+' values (one per base) or "position value" lines; found '+flat.length+'.');
      flat.forEach((v,i)=>{const x=Number(v);if(!Number.isFinite(x))throw new Error('Value '+(i+1)+' is not a number.');values[i]=x;});
    }
    if(!values.some(v=>v!=null))throw new Error('No colour-map values were entered.');
    return values;
  }
  function colorMap(drawing,text,options){
    const values=parseValues(text,drawing.sequence.length),known=values.filter(v=>v!=null);
    const minimum=options.minimum===''||options.minimum==null?Math.min(...known):Number(options.minimum);
    const maximum=options.maximum===''||options.maximum==null?Math.max(...known):Number(options.maximum);
    if(!Number.isFinite(minimum)||!Number.isFinite(maximum)||!(maximum>minimum))throw new Error('The colour map needs a maximum above its minimum.');
    if(!PALETTES[options.palette])throw new Error('Unknown colour-map palette.');
    applyEvidence(drawing,{values,minimum,maximum,kind:String(options.caption||'User values').slice(0,100),source:'user values'},options.palette);
  }
  function clearColors(drawing){
    delete drawing.evidence;
    Object.keys(drawing.styles).forEach(k=>{drawing.styles[k]={...drawing.styles[k],fill:DEFAULT_BASE.fill,text:DEFAULT_BASE.text};});
  }
  function crosses(a,b){return (a.left<b.left&&b.left<a.right&&a.right<b.right)||(b.left<a.left&&a.left<b.right&&b.right<a.right);}
  // Nested subset of the pairs; crossing pairs are still drawn but do not shape a layout.
  function pairTable(n,pairs){
    const kept=[],pt=new Int32Array(n+2);
    pairs.slice().sort((a,b)=>a.left-b.left).forEach(p=>{if(!kept.some(q=>crosses(p,q)))kept.push(p);});
    kept.forEach(p=>{pt[p.left]=p.right;pt[p.right]=p.left;});
    return pt;
  }
  function pairsFrom(dbn){
    const open='([{<',close=')]}>',stacks=[[],[],[],[]],pairs=[];
    Array.from(dbn).forEach((c,i)=>{const o=open.indexOf(c),k=close.indexOf(c);if(o>=0)stacks[o].push(i+1);else if(k>=0&&stacks[k].length)pairs.push({left:stacks[k].pop(),right:i+1});});
    return pairs.sort((a,b)=>a.left-b.left);
  }
  function dotBracket(n,pairs){
    const open='([{<',close=')]}>',levels=[],chars=Array(n).fill('.');
    pairs.slice().sort((a,b)=>a.left-b.left).forEach(p=>{
      let k=levels.findIndex(level=>!level.some(q=>crosses(p,q)));
      if(k<0){if(levels.length===open.length)throw new Error('These pairs cross too often for bracket notation.');levels.push([]);k=levels.length-1;}
      levels[k].push(p);chars[p.left-1]=open[k];chars[p.right-1]=close[k];
    });
    return chars.join('');
  }
  function loopVertices(pt,i,j){const v=[i];for(let k=i+1;k<j;){v.push(k);if(pt[k]>k){v.push(pt[k]);k=pt[k]+1;}else k++;}v.push(j);return v;}
  // VARNA's RADIATE idea: each loop is a circle whose chords are the backbone
  // steps and closing pairs, and each helix grows straight out of its loop.
  function radiate(n,pt,s,flat){
    const w=1.5*s,P=Array(n+1),TAU=2*Math.PI,theta=(e,r)=>2*Math.asin(Math.min(1,e/(2*r)));
    const add=(p,u,k)=>({x:p.x+u.x*k,y:p.y+u.y*k}),unit=(x,y)=>{const l=Math.hypot(x,y)||1;return {x:x/l,y:y/l};};
    const chords=v=>v.slice(1).map((x,t)=>pt[v[t]]===x&&v[t]<x?w:s);
    function circle(edges){
      const longest=edges.reduce((m,e,i)=>e>edges[m]?i:m,0),half=edges[longest]/2,total=r=>edges.reduce((a,e)=>a+theta(e,r),0);
      // A loop too small to surround its centre puts the centre beyond the longest chord.
      const outside=total(half)<TAU,f=outside?r=>edges.reduce((a,e,i)=>a+(i===longest?-1:1)*theta(e,r),0):r=>total(r)-TAU;
      let lo=half,hi=edges.reduce((a,b)=>a+b,0);
      for(let k=0;k<100;k++){const mid=(lo+hi)/2;if(outside?f(mid)<0:f(mid)>0)lo=mid;else hi=mid;}
      return {r:(lo+hi)/2,outside,longest};
    }
    function place(v,edges,C,r,start,sign){let a=start;for(let t=1;t<v.length;t++){a+=sign*theta(edges[t-1],r);if(!P[v[t]])P[v[t]]={x:C.x+r*Math.cos(a),y:C.y+r*Math.sin(a)};}}
    function branches(v,C){for(let t=0;t+1<v.length;t++){const k=v[t],l=v[t+1];if(pt[k]===l&&k<l)helix(k,l,C?unit((P[k].x+P[l].x)/2-C.x,(P[k].y+P[l].y)/2-C.y):{x:0,y:-1});}}
    function helix(k,l,u){while(pt[k+1]===l-1&&k+1<l-1){P[k+1]=add(P[k],u,s);P[l-1]=add(P[l],u,s);k++;l--;}loop(k,l,u);}
    function loop(i,j,u){
      if(j-i<2)return;
      const v=loopVertices(pt,i,j),edges=chords(v),all=edges.concat(w),c=circle(all);
      const h=Math.sqrt(Math.max(0,c.r*c.r-w*w/4))*(c.outside&&c.longest===all.length-1?-1:1),C=add({x:(P[i].x+P[j].x)/2,y:(P[i].y+P[j].y)/2},u,h);
      const a0=Math.atan2(P[i].y-C.y,P[i].x-C.x),aj=Math.atan2(P[j].y-C.y,P[j].x-C.x),walk=edges.reduce((a,e)=>a+theta(e,c.r),0);
      const miss=sign=>{const d=a0+sign*walk-aj;return Math.abs(Math.atan2(Math.sin(d),Math.cos(d)));};
      place(v,edges,C,c.r,a0,miss(1)<=miss(-1)?1:-1);branches(v,C);
    }
    const v=[];for(let k=1;k<=n;){v.push(k);if(pt[k]>k){v.push(pt[k]);k=pt[k]+1;}else k++;}
    const edges=chords(v);let round=!flat&&v.length>=3;
    if(round){
      const c=circle(edges.concat(2*w));
      if(c.outside)round=false;
      else{const C={x:0,y:0},a0=Math.PI/2+Math.asin(Math.min(1,w/c.r));P[v[0]]={x:c.r*Math.cos(a0),y:c.r*Math.sin(a0)};place(v,edges,C,c.r,a0,1);branches(v,C);}
    }
    // Flat exterior loop: each top-level domain moves right until it clears the
    // widest part of the domains before it.
    if(!round){
      let x=0,right=-Infinity;
      v.forEach((k,t)=>{
        if(t&&pt[v[t-1]]===k&&v[t-1]<k)return;
        if(!(pt[k]>k)){x=t?x+s:0;P[k]={x,y:0};right=Math.max(right,x);return;}
        const l=pt[k];P[k]={x:0,y:0};P[l]={x:w,y:0};helix(k,l,{x:0,y:-1});
        let min=Infinity,max=-Infinity;for(let i=k;i<=l;i++){min=Math.min(min,P[i].x);max=Math.max(max,P[i].x);}
        const dx=Math.max(t?x+s:0,right+s-min);for(let i=k;i<=l;i++)P[i].x+=dx;
        x=P[l].x;right=Math.max(right,max+dx);
      });
    }
    return P.slice(1).map(p=>p||{x:0,y:0});
  }
  function layout(sequence,structure,kind,options){
    const n=sequence.length,o={spacing:1,flat:true,rotation:0,...options};
    const pairs=typeof structure==='string'?pairsFrom(structure):structure||[],pt=pairTable(n,pairs),s=30*o.spacing;
    let points;
    if(kind==='simple'){
      const nested=Array.from({length:n},(_,i)=>pt[i+1]>i+1?'(':pt[i+1]?')':'.').join(''),xy=root.Hyb2RegionPanels.simpleLayout(nested);
      points=Array.from(sequence,(_,i)=>({x:xy.x[i]*s,y:xy.y[i]*s}));
    } else if(kind==='radiate')points=radiate(n,pt,s,o.flat);
    else if(kind==='linear')points=Array.from(sequence,(_,i)=>({x:i*s,y:0}));
    else {const r=Math.max(80,n*s/(2*Math.PI));points=Array.from(sequence,(_,i)=>({x:Math.cos(i*2*Math.PI/n-Math.PI/2)*r,y:Math.sin(i*2*Math.PI/n-Math.PI/2)*r}));}
    if(o.rotation)turn(points,points.map((_,i)=>i),o.rotation,centroid(points));
    return points;
  }
  function centroid(points){return {x:points.reduce((v,p)=>v+p.x,0)/points.length,y:points.reduce((v,p)=>v+p.y,0)/points.length};}
  function turn(points,indices,degrees,pivot){
    const a=degrees*Math.PI/180,cos=Math.cos(a),sin=Math.sin(a);
    indices.forEach(i=>{const p=points[i],x=p.x-pivot.x,y=p.y-pivot.y;p.x=pivot.x+x*cos-y*sin;p.y=pivot.y+x*sin+y*cos;});
  }
  function mirror(points,indices,pivot){indices.forEach(i=>{points[i].x=2*pivot.x-points[i].x;});}
  // The closing pair of the domain that holds base i: its own pair, else the
  // innermost pair around it (null in the exterior loop).
  function domainOf(d,i){
    const pt=pairTable(d.sequence.length,d.pairs);
    if(pt[i])return [Math.min(i,pt[i]),Math.max(i,pt[i])];
    for(let l=i-1;l>=1;l--)if(pt[l]>i)return [l,pt[l]];
    return null;
  }
  function parentOf(d,a,b){const pt=pairTable(d.sequence.length,d.pairs);for(let l=a-1;l>=1;l--)if(pt[l]>b)return [l,pt[l]];return null;}
  function helixOf(d,i){
    const pt=pairTable(d.sequence.length,d.pairs);
    if(!pt[i])throw new Error('Base '+i+' is unpaired; choose a paired base to anchor a helix annotation.');
    let l=Math.min(i,pt[i]),r=pt[l];while(l>1&&pt[l-1]===r+1){l--;r++;}
    const bases=[];while(pt[l]===r&&l<r){bases.push(l,r);l++;r--;}
    return bases;
  }
  function loopOf(d,i){
    const pt=pairTable(d.sequence.length,d.pairs);
    let l,r;
    if(pt[i]){l=Math.min(i,pt[i]);r=pt[l];}else{const c=domainOf(d,i);if(!c)throw new Error('Base '+i+' lies in the exterior loop; choose a base inside a loop.');[l,r]=c;}
    return loopVertices(pt,l,r);
  }
  function anchor(d,a) {
    if(a.free)return {x:a.x,y:a.y};
    const bases=a.bases || [a.base];
    return bases.reduce((p,i)=>({x:p.x+d.points[i-1].x/bases.length,y:p.y+d.points[i-1].y/bases.length}),{x:0,y:0});
  }
  function autoAnnotations(d,kind) {
    const note=(base,text,bases)=>({kind:'text',base,text,bases,dx:25,dy:-35,color:'#3C5488',auto:kind});
    if(kind==='ends'){
      const n=d.sequence.length,c=d.cutpoint;
      // A two-strand drawing labels both ends of each strand.
      return c?[note(1,'A 5′',[1]),note(c,'A 3′',[c]),note(c+1,'B 5′',[c+1]),note(n,'B 3′',[n])]:[note(1,'5′',[1]),note(n,'3′',[n])];
    }
    const pairs=d.pairs.slice().sort((a,b)=>a.left-b.left),keys=new Set(pairs.map(p=>p.left+':'+p.right));
    if(kind==='helices'){
      let rank=0;
      return pairs.filter(p=>!keys.has((p.left-1)+':'+(p.right+1))).map(p=>{
        const bases=[];for(let l=p.left,r=p.right;keys.has(l+':'+r);l++,r--){bases.push(l,r);}
        return note(p.left,'H'+(++rank),bases);
      });
    }
    // Hairpins require a nested structure. Do not mislabel pseudoknot loops.
    if(pairs.some((p,i)=>pairs.slice(i+1).some(q=>p.left<q.left&&q.left<p.right&&p.right<q.right)))throw new Error('Automatic hairpin labels require non-crossing pairs.');
    const paired=new Set(pairs.flatMap(p=>[p.left,p.right]));let rank=0;
    return pairs.filter(p=>p.right>p.left+1&&!Array.from({length:p.right-p.left-1},(_,i)=>p.left+i+1).some(i=>paired.has(i))).map(p=>note(p.left+1,'Hairpin '+(++rank),Array.from({length:p.right-p.left-1},(_,i)=>p.left+i+1)));
  }
  function initial(result){
    const drawing = {schema:'hyb2-drawing-1',sequence:result.sequence,dotBracket:result.dotBracket,
      points:layout(result.sequence,result.pairs||result.dotBracket,'radiate'),layout:{kind:'radiate',spacing:1,flat:true},pairs:(result.pairs||[]).map(p=>({left:p.left,right:p.right,color:'#3C5488',width:2})),
      styles:{},annotations:[],background:'#ffffff',backbone:'#8491B4',numberInterval:10,title:result.label||'RNA structure',topologyEdited:false};
    // CPLcofold: strand A is 1..cutpoint, strand B the rest; no backbone joins them.
    if(Number.isInteger(result.cutpoint)&&result.cutpoint>0&&result.cutpoint<result.sequence.length)drawing.cutpoint=result.cutpoint;
    const evidence=evidenceFor(result);
    if(evidence)applyEvidence(drawing,evidence);
    return drawing;
  }
  function validate(value,sequence,dbn){
    const color=v=>typeof v==='string'&&/^#[0-9a-f]{6}$/i.test(v);
    const pos=v=>Number.isInteger(v)&&v>=1&&v<=sequence.length;
    const finite=v=>Number.isFinite(v)&&Math.abs(v)<1e7;
    const within=(v,min,max)=>v==null||(Number.isFinite(v)&&v>=min&&v<=max);
    if(!value||value.schema!=='hyb2-drawing-1'||value.sequence!==sequence||value.dotBracket!==dbn)throw new Error('This drawing belongs to a different prediction.');
    if(!Array.isArray(value.points)||value.points.length!==sequence.length||value.points.some(p=>!p||!finite(p.x)||!finite(p.y)))throw new Error('Invalid nucleotide coordinates.');
    if(!color(value.background)||!color(value.backbone)||!Number.isInteger(value.numberInterval)||value.numberInterval<1||value.numberInterval>1000)throw new Error('Invalid drawing style.');
    if((value.pairStyle!=null&&!PAIR_STYLES.includes(value.pairStyle))||(value.pairCurve!=null&&!['line','arc'].includes(value.pairCurve))||(value.font!=null&&!FONTS[value.font])||
      !within(value.baseFontSize,6,30)||!within(value.titleSize,8,48)||!within(value.backboneWidth,0,10)||(value.numbering!=null&&typeof value.numbering!=='boolean')||
      [value.numberColor,value.titleColor].some(c=>c!=null&&!color(c)))throw new Error('Invalid drawing style.');
    if(value.cutpoint!=null&&(!Number.isInteger(value.cutpoint)||value.cutpoint<1||value.cutpoint>=sequence.length))throw new Error('Invalid strand break.');
    if(value.layout!=null&&(!LAYOUTS.includes(value.layout.kind)||!within(value.layout.spacing,.5,3)||typeof value.layout.flat!=='boolean'))throw new Error('Invalid layout settings.');
    if(typeof value.title!=='string'||value.title.length>500)throw new Error('Invalid title.');
    const used=new Set();
    if(!Array.isArray(value.pairs)||value.pairs.length>sequence.length/2)throw new Error('Invalid pair list.');
    for(const pair of value.pairs){if(!pos(pair.left)||!pos(pair.right)||pair.left>=pair.right||used.has(pair.left)||used.has(pair.right)||!color(pair.color)||!Number.isFinite(pair.width)||pair.width<.5||pair.width>10)throw new Error('Invalid or overlapping base pairs.');used.add(pair.left);used.add(pair.right);}
    if(!value.styles||typeof value.styles!=='object'||Array.isArray(value.styles))throw new Error('Invalid nucleotide styles.');
    for(const [key,s] of Object.entries(value.styles)){if(!pos(Number(key))||!s||!color(s.fill)||!color(s.text)||!Number.isFinite(s.radius)||s.radius<3||s.radius>24||(s.outline!=null&&!color(s.outline)))throw new Error('Invalid nucleotide style.');}
    if(!Array.isArray(value.annotations)||value.annotations.length>1000||value.annotations.some(a=>!a||!pos(a.base)||!finite(a.dx)||!finite(a.dy)||typeof a.text!=='string'||a.text.length>500||!color(a.color)))throw new Error('Invalid annotation.');
    for(const a of value.annotations){
      if(a.kind && !['text','region','probe'].includes(a.kind))throw new Error('Unknown annotation type.');
      if(a.bases && (!Array.isArray(a.bases)||!a.bases.length||a.bases.length>sequence.length||a.bases.some(i=>!pos(i))))throw new Error('Invalid annotation anchors.');
      if(a.kind==='region' && (!pos(a.end)||a.end<a.base))throw new Error('Invalid highlight interval.');
      if(a.kind==='probe' && !['arrow','dot','triangle'].includes(a.marker))throw new Error('Invalid chemical probe marker.');
      if(a.free!=null&&(typeof a.free!=='boolean'||(a.free&&(!finite(a.x)||!finite(a.y)))))throw new Error('Invalid free annotation position.');
      if(!within(a.size,6,48)||!within(a.opacity,.05,1)||!within(a.width,2,80)||!within(a.intensity,.25,4)||(a.direction!=null&&!['in','out'].includes(a.direction)))throw new Error('Invalid annotation style.');
    }
    if(value.evidence){const e=value.evidence,user=e.minimum!=null;
      if(!Array.isArray(e.values)||e.values.length!==sequence.length||!Number.isFinite(e.maximum)||!PALETTES[e.palette]||typeof e.kind!=='string'||e.kind.length>100||
        (user?!(Number.isFinite(e.minimum)&&e.maximum>e.minimum)||e.values.some(v=>v!=null&&!Number.isFinite(v)):(e.maximum<=0||e.values.some(v=>!Number.isFinite(v)||v<0||v>e.maximum))))throw new Error('Invalid evidence colours.');
    }
    return clone(value);
  }
  function pairSignature(pairs){return pairs.map(p=>[p.left,p.right]).sort((a,b)=>a[0]-b[0]||a[1]-b[1]).map(p=>p.join('-')).join(',');}
  function ct(drawing){
    const mates=new Map();drawing.pairs.forEach(p=>{mates.set(p.left,p.right);mates.set(p.right,p.left);});
    const n=drawing.sequence.length,c=drawing.cutpoint||0;
    // The chain break: the last base of strand A has no next, the first of B no previous.
    return n+' HYB2 manually edited drawing; energy not recalculated\n'+Array.from(drawing.sequence,(base,i)=>[i+1,base,i&&i!==c?i:0,i+1<n&&i+1!==c?i+2:0,mates.get(i+1)||0,i+1].join('\t')).join('\n')+'\n';
  }
  function bpseq(drawing){
    const mates=new Map();drawing.pairs.forEach(p=>{mates.set(p.left,p.right);mates.set(p.right,p.left);});
    return '# HYB2 manually edited drawing; energy not recalculated\n'+Array.from(drawing.sequence,(base,i)=>(i+1)+' '+base+' '+(mates.get(i+1)||0)).join('\n')+'\n';
  }
  function editedDbn(drawing){
    const c=drawing.cutpoint,cut=s=>c?s.slice(0,c)+'&'+s.slice(c):s;
    return '>'+drawing.title.replace(/\s+/g,' ')+' (edited drawing; energy not recalculated)\n'+cut(drawing.sequence)+'\n'+cut(dotBracket(drawing.sequence.length,drawing.pairs))+'\n';
  }
  function bounds(d){
    const points=d.points.concat(d.annotations.map(a=>({x:anchor(d,a).x+a.dx,y:anchor(d,a).y+a.dy})));
    let x=Infinity,y=Infinity,right=-Infinity,bottom=-Infinity;
    points.forEach(p=>{x=Math.min(x,p.x);y=Math.min(y,p.y);right=Math.max(right,p.x);bottom=Math.max(bottom,p.y);});
    // Include annotation text, numbering and linear-layout arcs in the exported canvas.
    const pad=65+Math.max(0,...d.annotations.map(a=>a.text.length*(a.size||14)/2)),arcs=d.pairCurve==='arc'?Math.max(0,...d.pairs.map(p=>Math.hypot(d.points[p.right-1].x-d.points[p.left-1].x,d.points[p.right-1].y-d.points[p.left-1].y)*.45)):0;
    return [x-pad,y-90-arcs,Math.max(d.evidence?500:200,right-x+pad*2),Math.max(160,bottom-y+180+arcs)];
  }
  // Number labels point away from the partner (paired) or the loop centre (unpaired).
  function numberDirection(d,i,mates){
    const p=d.points,a=p[Math.max(0,i-1)],b=p[Math.min(p.length-1,i+1)],len=Math.hypot(b.x-a.x,b.y-a.y)||1;
    let n={x:(b.y-a.y)/len,y:-(b.x-a.x)/len};
    if(d.pairCurve==='arc')return {x:-n.x,y:-n.y};
    const mate=mates.get(i+1),toward=mate?{x:p[mate-1].x-p[i].x,y:p[mate-1].y-p[i].y}:{x:(a.x+b.x)/2-p[i].x,y:(a.y+b.y)/2-p[i].y};
    if(n.x*toward.x+n.y*toward.y>0)n={x:-n.x,y:-n.y};
    return n;
  }
  // One scene feeds both the SVG and the EPS writers.
  function scene(d,selection,b){
    const items=[{t:'rect',x:b[0],y:b[1],w:b[2],h:b[3],fill:d.background,attrs:{'data-editor-background':'true'}},
      {t:'text',x:b[0]+20,y:b[1]+28,size:d.titleSize||18,fill:d.titleColor||'#222222',text:d.title}];
    const line=(p,q,stroke,width,opacity)=>({t:'line',x1:p.x,y1:p.y,x2:q.x,y2:q.y,stroke,width,opacity});
    d.annotations.filter(a=>a.kind==='region').forEach(a=>{
      const points=d.points.slice(a.base-1,a.end),opacity=a.opacity==null?.25:a.opacity,width=a.width||32;
      items.push({t:'path',points,stroke:a.color,opacity,width,round:true});
      if(points.length===1)items.push({t:'circle',cx:points[0].x,cy:points[0].y,r:width/2,fill:a.color,fillOpacity:opacity});
    });
    if(d.evidence){const e=d.evidence,min=e.minimum==null?0:e.minimum;
      items.push({t:'text',x:b[0]+20,y:b[1]+48,size:10,fill:'#333333',text:e.kind+' · '+e.source+' · '+num(min)+'–'+num(e.maximum)+(e.overrides?' · manual colours override':'')});
      for(let i=0;i<40;i++)items.push({t:'rect',x:b[0]+20+i*3,y:b[1]+55,w:3,h:5,fill:mapColor(e,min+(e.maximum-min)*i/39)});
    }
    const backbone=d.backboneWidth==null?1.5:d.backboneWidth;
    if(backbone>0)for(let i=1;i<d.points.length;i++)if(i!==d.cutpoint)items.push(line(d.points[i-1],d.points[i],d.backbone,backbone));
    const style=d.pairStyle||'simple';
    if(style!=='none')d.pairs.forEach(p=>{
      const a=d.points[p.left-1],c=d.points[p.right-1],mid={x:(a.x+c.x)/2,y:(a.y+c.y)/2},bases=(d.sequence[p.left-1]+d.sequence[p.right-1]).toUpperCase().replace(/T/g,'U');
      if(d.pairCurve==='arc'){const dx=c.x-a.x,dy=c.y-a.y;items.push({t:'quad',x1:a.x,y1:a.y,cx:mid.x+dy*.9,cy:mid.y-dx*.9,x2:c.x,y2:c.y,stroke:p.color,width:p.width});}
      else if(style==='rnaviz')items.push(line({x:a.x+(c.x-a.x)/3,y:a.y+(c.y-a.y)/3},{x:c.x-(c.x-a.x)/3,y:c.y-(c.y-a.y)/3},p.color,p.width));
      else items.push(line(a,c,p.color,p.width));
      // Leontis–Westhof glyphs for cis Watson–Crick pairs: canonical pairs are a
      // line, G·U wobbles add a filled dot and other pairs an open circle.
      if(style==='lw'&&!CANONICAL.has(bases))items.push({t:'circle',cx:mid.x,cy:mid.y,r:4,fill:WOBBLE.has(bases)?p.color:d.background,stroke:p.color,strokeWidth:1.5});
    });
    const mates=new Map();d.pairs.forEach(p=>{mates.set(p.left,p.right);mates.set(p.right,p.left);});
    const letter=d.baseFontSize||12,showNumbers=d.numbering!==false;
    d.points.forEach((p,i)=>{
      const s={...DEFAULT_BASE,...(d.styles[i+1]||{})},selected=selection&&i+1>=selection[0]&&i+1<=selection[1];
      items.push({t:'g',attrs:{'data-editor-base':i+1,tabindex:0,role:'button','aria-label':'Move nucleotide '+(i+1)+' '+d.sequence[i]},items:[
        {t:'circle',cx:p.x,cy:p.y,r:s.radius,fill:s.fill,stroke:selected?'#E64B35':s.outline||'#3C5488',strokeWidth:selected?3:1},
        {t:'text',x:p.x,y:p.y+letter/3,size:letter,anchor:'middle',fill:s.text,text:d.sequence[i]}]});
      if(showNumbers&&(i===0||i===d.points.length-1||(i+1)%d.numberInterval===0)){
        const n=numberDirection(d,i,mates),color=d.numberColor||'#444444';
        items.push(line({x:p.x+n.x*(s.radius+2),y:p.y+n.y*(s.radius+2)},{x:p.x+n.x*(s.radius+8),y:p.y+n.y*(s.radius+8)},color,.8));
        items.push({t:'text',x:p.x+n.x*(s.radius+16),y:p.y+n.y*(s.radius+16)+3.5,size:10,anchor:'middle',fill:color,text:String(i+1)});
      }
    });
    d.annotations.forEach((a,i)=>{const p=anchor(d,a),q={x:p.x+a.dx,y:p.y+a.dy},group=[],k=a.intensity||1;
      if(!a.free)group.push(line(p,q,a.color,1));
      if(a.kind==='probe'){
        if(a.marker==='dot')group.push({t:'circle',cx:q.x,cy:q.y,r:5*k,fill:a.color});
        else if(a.marker==='triangle')group.push({t:'path',points:[{x:q.x,y:q.y-6*k},{x:q.x-6*k,y:q.y+6*k},{x:q.x+6*k,y:q.y+6*k}],closed:true,fill:a.color});
        else {const out=a.direction==='out',tip=out?q:p,from=out?p:q,dx=tip.x-from.x,dy=tip.y-from.y,len=Math.hypot(dx,dy)||1,ux=dx/len,uy=dy/len;
          group.push({t:'path',points:[tip,{x:tip.x-ux*10*k-uy*4*k,y:tip.y-uy*10*k+ux*4*k},{x:tip.x-ux*10*k+uy*4*k,y:tip.y-uy*10*k-ux*4*k}],closed:true,fill:a.color});}
      }
      group.push({t:'text',x:q.x,y:q.y-(a.free?0:8),size:a.size||14,fill:a.color,text:a.text});
      items.push({t:'g',attrs:{'data-editor-annotation':i,role:'button',tabindex:0,'aria-label':'Annotation '+(i+1)+' '+a.text},items:group});
    });
    return items;
  }
  function svgItem(item){
    const attrs=o=>Object.entries(o||{}).map(([k,v])=>' '+k+'="'+xml(v)+'"').join('');
    const opacity=(name,v)=>v==null||v===1?'':' '+name+'="'+v+'"';
    switch(item.t){
      case 'g':return '<g'+attrs(item.attrs)+'>'+item.items.map(svgItem).join('')+'</g>';
      case 'rect':return '<rect'+attrs(item.attrs)+' x="'+num(item.x)+'" y="'+num(item.y)+'" width="'+num(item.w)+'" height="'+num(item.h)+'" fill="'+item.fill+'"/>';
      case 'line':return '<line x1="'+num(item.x1)+'" y1="'+num(item.y1)+'" x2="'+num(item.x2)+'" y2="'+num(item.y2)+'" stroke="'+item.stroke+'" stroke-width="'+item.width+'"'+opacity('stroke-opacity',item.opacity)+'/>';
      case 'quad':return '<path d="M'+num(item.x1)+','+num(item.y1)+' Q'+num(item.cx)+','+num(item.cy)+' '+num(item.x2)+','+num(item.y2)+'" fill="none" stroke="'+item.stroke+'" stroke-width="'+item.width+'"/>';
      case 'path':return '<path d="'+item.points.map((p,i)=>(i?'L':'M')+num(p.x)+','+num(p.y)).join(' ')+(item.closed?' Z':'')+'" fill="'+(item.fill||'none')+'"'+(item.stroke?' stroke="'+item.stroke+'"'+opacity('stroke-opacity',item.opacity)+' stroke-width="'+item.width+'"':'')+(item.round?' stroke-linecap="round" stroke-linejoin="round"':'')+'/>';
      case 'circle':return '<circle cx="'+num(item.cx)+'" cy="'+num(item.cy)+'" r="'+num(item.r)+'" fill="'+item.fill+'"'+opacity('fill-opacity',item.fillOpacity)+(item.stroke?' stroke="'+item.stroke+'" stroke-width="'+item.strokeWidth+'"':'')+'/>';
      case 'text':return '<text x="'+num(item.x)+'" y="'+num(item.y)+'" font-size="'+item.size+'"'+(item.anchor?' text-anchor="'+item.anchor+'"':'')+' fill="'+item.fill+'">'+xml(item.text)+'</text>';
    }
    return '';
  }
  function svg(d,selection,box){
    const b=box||bounds(d);
    return '<svg xmlns="'+NS+'" viewBox="'+b.map(num).join(' ')+'" width="1000" height="700" role="img" aria-label="Editable RNA drawing"><g font-family="'+(d.font||'sans-serif')+'">'+scene(d,selection,b).map(svgItem).join('')+'</g></svg>';
  }
  // Encapsulated PostScript for journals and vector editors; opacity is
  // flattened onto the background colour.
  function eps(d){
    const b=bounds(d),k=Math.min(1,800/Math.max(b[2],b[3])),W=Math.ceil(b[2]*k),H=Math.ceil(b[3]*k);
    const hex=c=>[1,3,5].map(i=>parseInt(c.slice(i,i+2),16)),bg=hex(d.background);
    const X=x=>((x-b[0])*k).toFixed(2),Y=y=>((b[1]+b[3]-y)*k).toFixed(2);
    const rgb=(c,o)=>{const a=o==null?1:o;return hex(c).map((v,i)=>((v*a+bg[i]*(1-a))/255).toFixed(3)).join(' ')+' setrgbcolor';};
    const str=t=>'('+String(t).replace(/[′’]/g,"'").replace(/[–—]/g,'-').replace(/·/g,'|').replace(/[^\x20-\x7e]/g,'?').replace(/[\\()]/g,'\\$&')+')';
    const font=FONTS[d.font]||'Helvetica',out=['%!PS-Adobe-3.0 EPSF-3.0','%%BoundingBox: 0 0 '+W+' '+H,'%%Title: '+d.title.replace(/[^\x20-\x7e]/g,'?'),'%%Creator: HYB2 Web Lite drawing editor','%%EndComments','1 setlinecap 1 setlinejoin'];
    const emit=item=>{
      switch(item.t){
        case 'g':item.items.forEach(emit);break;
        case 'rect':out.push(rgb(item.fill)+' newpath '+X(item.x)+' '+Y(item.y+item.h)+' '+(item.w*k).toFixed(2)+' '+(item.h*k).toFixed(2)+' rectfill');break;
        case 'line':out.push(rgb(item.stroke,item.opacity)+' '+(item.width*k).toFixed(2)+' setlinewidth newpath '+X(item.x1)+' '+Y(item.y1)+' moveto '+X(item.x2)+' '+Y(item.y2)+' lineto stroke');break;
        case 'quad':{const c1={x:item.x1+2/3*(item.cx-item.x1),y:item.y1+2/3*(item.cy-item.y1)},c2={x:item.x2+2/3*(item.cx-item.x2),y:item.y2+2/3*(item.cy-item.y2)};
          out.push(rgb(item.stroke)+' '+(item.width*k).toFixed(2)+' setlinewidth newpath '+X(item.x1)+' '+Y(item.y1)+' moveto '+X(c1.x)+' '+Y(c1.y)+' '+X(c2.x)+' '+Y(c2.y)+' '+X(item.x2)+' '+Y(item.y2)+' curveto stroke');break;}
        case 'path':{const path='newpath '+item.points.map((p,i)=>X(p.x)+' '+Y(p.y)+(i?' lineto':' moveto')).join(' ')+(item.closed?' closepath':'');
          if(item.fill)out.push(rgb(item.fill)+' '+path+' fill');
          if(item.stroke)out.push(rgb(item.stroke,item.opacity)+' '+(item.width*k).toFixed(2)+' setlinewidth '+path+' stroke');break;}
        case 'circle':out.push('newpath '+X(item.cx)+' '+Y(item.cy)+' '+(item.r*k).toFixed(2)+' 0 360 arc closepath gsave '+rgb(item.fill,item.fillOpacity)+' fill grestore'+(item.stroke?' '+rgb(item.stroke)+' '+(item.strokeWidth*k).toFixed(2)+' setlinewidth stroke':' newpath'));break;
        case 'text':out.push(rgb(item.fill)+' /'+font+' '+(item.size*k).toFixed(2)+' selectfont '+X(item.x)+' '+Y(item.y)+' moveto '+str(item.text)+(item.anchor==='middle'?' dup stringwidth pop 2 div neg 0 rmoveto':'')+' show');break;
      }
    };
    scene(d,null,b).forEach(emit);
    return out.concat(['showpage','%%EOF']).join('\n')+'\n';
  }
  function ranges(list){
    const sorted=list.slice().sort((a,b)=>a-b),out=[];
    sorted.forEach(i=>{const last=out[out.length-1];if(last&&i===last[1]+1)last[1]=i;else out.push([i,i]);});
    return out.map(([a,b])=>a===b?String(a):a+'-'+b).join(',');
  }
  // A VARNAcmd invocation that redraws this structure in the Java VARNA
  // application with the same colours, highlights and annotations. VARNA
  // computes its own layout, so dragged positions are not carried over.
  function varnaCommand(d){
    const q=v=>"'"+String(v).replace(/'/g,"'\\''")+"'",clean=t=>String(t).replace(/[:;,]/g,' ');
    const kind=(d.layout&&d.layout.kind)||'simple',args=[['-sequenceDBN',q(d.sequence)],['-structureDBN',q(dotBracket(d.sequence.length,d.pairs))],
      ['-algorithm',{radiate:'radiate',simple:'radiate',circular:'circular',linear:'line'}[kind]],['-flat',String(!d.layout||d.layout.flat!==false)],
      ['-spaceBetweenBases',num((d.layout&&d.layout.spacing)||1)],['-title',q(d.title)],['-titleColor',d.titleColor||'#222222'],['-titleSize',String(d.titleSize||18)],
      ['-bpStyle',d.pairStyle||'simple'],['-background',d.background],['-backbone',d.backbone],['-bp',(d.pairs[0]&&d.pairs[0].color)||'#3C5488'],
      ['-periodNum',String(d.numbering===false?0:d.numberInterval)],['-baseNum',d.numberColor||'#444444'],['-resolution','4.0'],['-o','hyb2-drawing.svg']];
    const e=d.evidence;
    if(e&&!e.overrides){const min=e.minimum==null?0:e.minimum,stops=PALETTES[e.palette]||PALETTES.brand;
      args.push(['-colorMap',q(e.values.map(v=>num(v==null?min:v)).join(';'))],['-colorMapMin',num(min)],['-colorMapMax',num(e.maximum)],
        ['-colorMapStyle',q(stops.map(([t,c])=>num(min+t*(e.maximum-min))+':'+c).join(','))],['-colorMapCaption',q(clean(e.kind))]);
    }
    const groups=new Map();
    Object.entries(d.styles).forEach(([i,s])=>{if(e&&!e.overrides&&e.values[i-1]!=null&&!s.outline)return;const key='fill='+s.fill+',outline='+(s.outline||'#3C5488')+',label='+s.text+',number='+(d.numberColor||'#444444');groups.set(key,(groups.get(key)||[]).concat(Number(i)));});
    Array.from(groups).forEach(([key,list],k)=>args.push(['-basesStyle'+(k+1),q(key)],['-applyBasesStyle'+(k+1)+'on',q(ranges(list))]));
    const regions=d.annotations.filter(a=>a.kind==='region'),probes=d.annotations.filter(a=>a.kind==='probe'),texts=d.annotations.filter(a=>a.kind!=='region'&&a.kind!=='probe'&&a.text);
    if(regions.length)args.push(['-highlightRegion',q(regions.map(a=>a.base+'-'+a.end+':radius='+num((a.width||32)/2)+',fill='+a.color+',outline='+a.color).join(';'))]);
    if(texts.length)args.push(['-annotations',q(texts.map(a=>clean(a.text)+':'+(a.free?'type=P,x='+num(a.x+a.dx)+',y='+num(a.y+a.dy):(a.anchorType==='helix'?'type=H':a.anchorType==='loop'?'type=L':'type=B')+',anchor='+a.base)+',color='+a.color+',size='+(a.size||14)).join(';'))]);
    if(probes.length)args.push(['-chemProb',q(probes.map(a=>{const i=Math.min(a.base,d.sequence.length-1);return i+'-'+(i+1)+':glyph='+(a.marker==='triangle'?'triangle':a.marker==='dot'?'dot':'arrow')+',dir='+(a.direction||'in')+',intensity='+num(a.intensity||1)+',color='+a.color;}).join(';'))]);
    return '#!/bin/sh\n# VARNA command generated by HYB2 Web Lite. Requires VARNA (GPL, varna.lri.fr) and Java.\n'+
      '# VARNA computes its own layout: dragged positions and the ViennaRNA polygon layout are not carried over.\n'+
      '# Colour-map stops and option spellings follow VARNA 3.9; check them against your VARNA version.\n'+
      (d.cutpoint?'# Two strands: A is 1-'+d.cutpoint+', B '+(d.cutpoint+1)+'-'+d.sequence.length+'; VARNA draws them as one chain.\n':'')+
      'java -cp VARNA.jar fr.orsay.lri.varna.applications.VARNAcmd \\\n'+args.map(a=>'  '+a.join(' ')).join(' \\\n')+'\n';
  }
  function session(state){
    const result=state.structure.result,key=result.sequence+'\n'+result.dotBracket;
    if(!state.structure.drawing||state.structure.drawing.key!==key)state.structure.drawing={key,data:initial(result),undo:[],redo:[],selection:[1,1],view:null};
    return state.structure.drawing;
  }
  function checkpoint(s){s.undo.push(clone(s.data));if(s.undo.length>50)s.undo.shift();s.redo=[];}
  function undo(s){if(s.undo.length){s.redo.push(clone(s.data));s.data=s.undo.pop();s.view=null;}}
  function redo(s){if(s.redo.length){s.undo.push(clone(s.data));s.data=s.redo.pop();s.view=null;}}
  function mount(target,state,api){
    const s=session(state),result=state.structure.result,d0=s.data,n=result.sequence.length;
    // The editor takes the full result width; leaving it re-renders the page without this class.
    const grid=target.closest&&target.closest('.structure-result-grid');if(grid)grid.classList.add('is-editing');
    const field=(label,name,input)=>'<label class="control-field">'+label+input.replace('<input','<input data-edit="'+name+'"').replace('<select','<select data-edit="'+name+'"').replace('<textarea','<textarea data-edit="'+name+'"')+'</label>';
    const select=(options,value)=>'<select>'+options.map(([v,l])=>'<option value="'+v+'"'+(v===value?' selected':'')+'>'+l+'</option>').join('')+'</select>';
    const tool=(action,label,title)=>'<button type="button" class="quiet-button" data-edit-action="'+action+'"'+(title?' title="'+title+'" aria-label="'+title+'"':'')+'>'+label+'</button>';
    const buttons=list=>'<div class="inline-actions">'+list.map(([action,label,title])=>tool(action,label,title)).join('')+'</div>';
    const section=(title,body,open)=>'<details class="editor-section"'+(open?' open':'')+'><summary>'+title+'</summary>'+body+'</details>';
    const layoutOptions=[['radiate','Radiate (VARNA)'],['simple','RNA polygon (ViennaRNA)'],['circular','Circular'],['linear','Linear with arcs']];
    target.style.height='auto';
    target.innerHTML='<div class="editor-root"><div class="editor-toolbar" role="toolbar" aria-label="Drawing editor tools">'+
        '<div class="editor-tool-group">'+tool('undo','↶ Undo','Undo (Ctrl/⌘ Z)')+tool('redo','↷ Redo','Redo (Ctrl/⌘ Shift Z)')+'</div>'+
        '<div class="editor-tool-group">'+tool('zoomOut','−','Zoom out')+tool('zoomIn','+','Zoom in')+tool('fit','Fit','Fit the drawing to the view')+'</div>'+
        '<div class="editor-tool-group"><label class="editor-inline-field">Layout '+select(layoutOptions,(d0.layout&&d0.layout.kind)||'radiate').replace('<select','<select data-edit="layout"')+'</label>'+tool('layout','Re-layout','Recompute all positions with this layout')+'</div>'+
        '<div class="editor-tool-group"><label class="editor-inline-field">Export '+select([['svg','SVG image'],['eps','EPS (PostScript)'],['json','Drawing JSON (re-loadable)'],['ct','Edited CT'],['bpseq','Edited BPSEQ'],['dbn','Edited DBN'],['varna','VARNA command']],'svg').replace('<select','<select data-edit="exportFormat"')+'</label>'+tool('export','Download')+tool('load','Load JSON','Load a saved drawing JSON')+'</div>'+
      '</div>'+
      '<div class="editor-status"><span class="editor-selection" data-editor-selection></span><span class="card-note" data-editor-message role="status"></span></div>'+
      '<div class="editor-shell"><div class="editor-canvas" data-editor-canvas></div><div class="editor-panel">'+
      section('Selection','<div class="control-grid">'+field('From','from','<input type="number" min="1" max="'+n+'" value="'+s.selection[0]+'">')+field('To','to','<input type="number" min="1" max="'+n+'" value="'+s.selection[1]+'">')+'</div>'+
        buttons([['domain','Domain','Select the helix and everything it closes'],['parent','Enclosing domain','Expand to the next enclosing domain'],['all','All','Select every base']])+
        buttons([['rotL','⟲ 15°','Rotate the selection 15° anticlockwise'],['rotR','⟳ 15°','Rotate the selection 15° clockwise'],['rot90','⟳ 90°','Rotate the selection 90° clockwise'],['mirror','Mirror','Mirror the selection']])+
        buttons([['pair','Pair the two ends','Add a pair between the first and last selected bases'],['unpair','Remove pairs','Remove pairs that touch the selection']]),true)+
      section('Selection colours','<div class="control-grid">'+field('Fill','fill','<input type="color" value="#91D1C2">')+field('Letter','text','<input type="color" value="#222222">')+field('Outline','outline','<input type="color" value="#3C5488">')+
        field('Radius','radius','<input type="number" min="3" max="24" value="9">')+field('Pair colour','pairColor','<input type="color" value="#3C5488">')+field('Pair width','pairWidth','<input type="number" min="0.5" max="10" step="0.5" value="2">')+'</div>'+
        '<p class="card-note">Changes apply to the selected bases and their pairs at once.</p>'+buttons([['resetStyle','Default colours for selection']]),true)+
      section('Drawing style','<div class="control-grid">'+field('Pair style','pairStyle',select([['simple','Simple line'],['lw','Leontis–Westhof'],['rnaviz','RNAViz'],['none','None']],d0.pairStyle||'simple'))+
        field('Pair shape','pairCurve',select([['line','Straight'],['arc','Arc']],d0.pairCurve||'line'))+field('Font','font',select([['sans-serif','Sans serif'],['serif','Serif'],['monospace','Monospace']],d0.font||'sans-serif'))+
        field('Letter size','letterSize','<input type="number" min="6" max="30" value="'+(d0.baseFontSize||12)+'">')+field('Number every','numbers','<input type="number" min="1" max="1000" value="'+d0.numberInterval+'">')+
        field('Numbering','numbering',select([['true','Shown'],['false','Hidden']],String(d0.numbering!==false)))+field('Number colour','numberColor','<input type="color" value="'+(d0.numberColor||'#444444')+'">')+
        field('Backbone','backbone','<input type="color" value="'+d0.backbone+'">')+field('Backbone width','backboneWidth','<input type="number" min="0" max="10" step="0.5" value="'+(d0.backboneWidth==null?1.5:d0.backboneWidth)+'">')+
        field('Background','background','<input type="color" value="'+d0.background+'">')+field('Title colour','titleColor','<input type="color" value="'+(d0.titleColor||'#222222')+'">')+
        field('Title size','titleSize','<input type="number" min="8" max="48" value="'+(d0.titleSize||18)+'">')+'</div>'+field('Title','title','<input maxlength="500" value="'+xml(d0.title)+'">')+
        '<div class="control-grid">'+field('Base spacing','spacing','<input type="number" min="0.5" max="3" step="0.1" value="'+((d0.layout&&d0.layout.spacing)||1)+'">')+
        field('Exterior loop','flat','<select><option value="true">Flat</option><option value="false"'+(d0.layout&&d0.layout.flat===false?' selected':'')+'>Circle</option></select>')+'</div>'+
        '<p class="card-note">Style changes apply at once. Spacing and exterior loop re-run the layout.</p>')+
      section('Annotations',field('Text','annotation','<input maxlength="500" placeholder="Label text">')+'<div class="control-grid">'+
        field('Type','annotationKind',select([['text','Text'],['region','Region highlight'],['probe','Chemical probe']],'text'))+
        field('Anchor','anchorType',select([['base','Selected bases'],['helix','Helix of “From”'],['loop','Loop of “From”'],['free','Free position']],'base'))+
        field('Colour','annotationColor','<input type="color" value="#3C5488">')+field('Text size','annotationSize','<input type="number" min="6" max="48" value="14">')+
        field('Probe marker','marker',select([['arrow','Arrow'],['dot','Dot'],['triangle','Triangle']],'arrow'))+field('Probe direction','direction',select([['in','Toward base'],['out','Away from base']],'in'))+
        field('Probe intensity','intensity','<input type="number" min="0.25" max="4" step="0.25" value="1">')+field('Highlight opacity','opacity','<input type="number" min="0.05" max="1" step="0.05" value="0.25">')+
        field('Highlight width','regionWidth','<input type="number" min="2" max="80" value="32">')+'</div>'+field('Saved annotations','annotationList','<select><option value="">Select annotation</option></select>')+
        buttons([['annotation','Add'],['updateAnnotation','Update'],['removeAnnotation','Remove'],['clearAnnotations','Clear all']])+
        buttons([['ends','5′ / 3′ labels'],['helices','Helix labels'],['hairpins','Hairpin labels']]))+
      section('Colour map','<div class="control-grid">'+field('Palette','palette',select([['lau','Lau / VARNA'],['brand','Website palette'],['heat','Heat'],['bluered','Blue–white–red'],['grey','Greyscale'],['viridis','Viridis']],(d0.evidence&&d0.evidence.palette)||'lau'))+
        field('Caption','mapCaption','<input maxlength="100" placeholder="User values">')+field('Minimum','mapMin','<input type="number" step="any" placeholder="auto">')+field('Maximum','mapMax','<input type="number" step="any" placeholder="auto">')+'</div>'+
        field('Values','mapValues','<textarea rows="3" placeholder="'+n+' values (one per base), or “position value” lines, e.g. SHAPE reactivities"></textarea>')+
        buttons([['colormap','Apply values'],['evidence','Evidence colours'],['clearColors','Clear colours']]))+
      '</div></div>'+
      '<p class="card-note editor-help">Click a base to select it · Shift-click or Shift-drag a box to extend the selection · double-click selects the base’s domain · drag a selected base to move the selection · drag the background to pan · Ctrl/⌘ + scroll or pinch to zoom · arrow keys nudge a focused base (Shift: 10 units). Layout, styles and annotations are saved separately from the prediction; edited pairs do not recalculate its energy.</p>'+
      '<input type="file" accept=".json,application/json" data-edit="file" hidden></div>';
    const host=target.querySelector('[data-editor-canvas]'),input=name=>target.querySelector('[data-edit="'+name+'"]');
    const message=text=>{target.querySelector('[data-editor-message]').textContent=text;};
    const number=(name,min,max,label)=>{const v=Number(input(name).value);if(!Number.isFinite(v)||v<min||v>max)throw new Error(label+' must be '+min+'–'+max+'.');return v;};
    const draw=()=>{
      host.innerHTML=svg(s.data,s.selection,s.view||bounds(s.data));
      target.querySelector('[data-edit-action="undo"]').disabled=!s.undo.length;target.querySelector('[data-edit-action="redo"]').disabled=!s.redo.length;
      const [a,b]=s.selection;target.querySelector('[data-editor-selection]').textContent=a===b?'Selected: base '+a+' ('+s.data.sequence[a-1]+')':'Selected: '+a+'–'+b+' ('+(b-a+1)+' nt)';
      const list=input('annotationList'),selected=list.value;
      list.innerHTML='<option value="">Select annotation</option>'+s.data.annotations.map((x,i)=>'<option value="'+i+'">'+xml((i+1)+': '+(x.kind||'text')+' · '+(x.text||'untitled'))+'</option>').join('');
      list.value=selected;
    };
    // The selection fields and colour pickers follow the first selected base.
    const sync=()=>{
      input('from').value=s.selection[0];input('to').value=s.selection[1];
      const st={...DEFAULT_BASE,...(s.data.styles[s.selection[0]]||{})},pair=s.data.pairs.find(p=>p.left===s.selection[0]||p.right===s.selection[0]);
      input('fill').value=st.fill;input('text').value=st.text;input('outline').value=st.outline||'#3C5488';input('radius').value=st.radius;
      if(pair){input('pairColor').value=pair.color;input('pairWidth').value=pair.width;}
    };
    const selected=()=>{const a=Number(input('from').value),b=Number(input('to').value);if(!Number.isInteger(a)||!Number.isInteger(b)||a<1||b<a||b>n)throw new Error('Select a valid nucleotide interval.');s.selection=[a,b];return s.selection;};
    const status=()=>s.data.topologyEdited?'Manually edited pairs: prediction energy does not describe this drawing.':'Prediction and energy are unchanged.';
    // Every edit goes through one validated, undoable commit.
    const commit=(change,note)=>{
      try{
        const next=clone(s.data);change(next);
        next.topologyEdited=pairSignature(next.pairs)!==pairSignature(result.pairs||[]);
        validate(next,result.sequence,result.dotBracket);checkpoint(s);s.data=next;sync();draw();message((note?note+' ':'')+status());
      }catch(error){message(error.message);}
    };
    const selectAnnotation=index=>{const a=s.data.annotations[index];if(!a)return;input('annotation').value=a.text;input('annotationKind').value=a.kind||'text';input('marker').value=a.marker||'arrow';input('annotationColor').value=a.color;
      input('anchorType').value=a.free?'free':a.anchorType||'base';input('annotationSize').value=a.size||14;input('direction').value=a.direction||'in';input('intensity').value=a.intensity||1;input('opacity').value=a.opacity==null?.25:a.opacity;input('regionWidth').value=a.width||32;
      s.selection=[a.base,a.end||a.base];sync();input('annotationList').value=String(index);};
    const select2=(range,note)=>{s.selection=range;sync();draw();message(note||'');};
    const move=(dx,dy)=>{for(let i=s.selection[0]-1;i<s.selection[1];i++){s.data.points[i].x+=dx;s.data.points[i].y+=dy;}};
    // A selected domain turns about the middle of its closing pair, as a VARNA helix does.
    const pivot=(data,a,b)=>{const pt=pairTable(n,data.pairs);return pt[a]===b?{x:(data.points[a-1].x+data.points[b-1].x)/2,y:(data.points[a-1].y+data.points[b-1].y)/2}:centroid(data.points.slice(a-1,b));};
    const relayout=note=>commit(next=>{const kind=input('layout').value,options={spacing:number('spacing',.5,3,'Base spacing'),flat:input('flat').value==='true'};
      next.points=layout(next.sequence,next.pairs,kind,options);next.layout={kind,...options};next.pairCurve=kind==='linear'?'arc':'line';input('pairCurve').value=next.pairCurve;s.view=null;},note);
    const zoom=factor=>{const box=s.view||bounds(s.data),c={x:box[0]+box[2]/2,y:box[1]+box[3]/2};if(box[2]*factor<50||box[2]*factor>1e7)return;s.view=[c.x-box[2]*factor/2,c.y-box[3]*factor/2,box[2]*factor,box[3]*factor];draw();};
    const SELECTION_STYLE={fill:'fill',text:'text',outline:'outline',radius:'radius',pairColor:'color',pairWidth:'width'};
    const GLOBAL_STYLE=['pairStyle','pairCurve','font','letterSize','numbers','numbering','numberColor','backbone','backboneWidth','background','title','titleColor','titleSize'];
    target.addEventListener('change',event=>{
      const name=event.target.dataset.edit;if(!name||name==='file')return;
      if(name==='annotationList'){if(event.target.value!==''){selectAnnotation(Number(event.target.value));draw();}return;}
      if(name==='from'||name==='to'){try{selected();sync();draw();}catch(error){message(error.message);}return;}
      if(name==='layout'||name==='spacing'||name==='flat'){relayout('Layout applied.');return;}
      if(SELECTION_STYLE[name])return commit(next=>{
        const [a,b]=selected(),key=SELECTION_STYLE[name],value=name==='radius'?number('radius',3,24,'Base radius'):name==='pairWidth'?number('pairWidth',.5,10,'Pair width'):event.target.value;
        if(name==='pairColor'||name==='pairWidth'){const touched=next.pairs.filter(p=>(p.left>=a&&p.left<=b)||(p.right>=a&&p.right<=b));if(!touched.length)throw new Error('No pair touches the selection.');touched.forEach(p=>{p[key]=value;});return;}
        if(name==='fill'&&next.evidence)next.evidence.overrides=true;
        for(let i=a;i<=b;i++)next.styles[i]={...DEFAULT_BASE,...(next.styles[i]||{}),[key]:value};
      },'Applied to '+s.selection[0]+'–'+s.selection[1]+'.');
      if(GLOBAL_STYLE.includes(name))return commit(next=>{
        next.pairStyle=input('pairStyle').value;next.pairCurve=input('pairCurve').value;next.font=input('font').value;next.baseFontSize=number('letterSize',6,30,'Letter size');
        next.numberInterval=number('numbers',1,1000,'Number interval');next.numbering=input('numbering').value==='true';next.numberColor=input('numberColor').value;
        next.backbone=input('backbone').value;next.backboneWidth=number('backboneWidth',0,10,'Backbone width');next.background=input('background').value;
        next.title=input('title').value;next.titleColor=input('titleColor').value;next.titleSize=number('titleSize',8,48,'Title size');
      },'Style updated.');
      if(name==='palette'&&s.data.evidence)return commit(next=>applyEvidence(next,next.evidence,event.target.value),'Palette changed.');
    });
    const download=format=>{
      const files={svg:['rna-edited-drawing.svg',()=>svg(s.data,null),'image/svg+xml'],eps:['rna-edited-drawing.eps',()=>eps(s.data),'application/postscript'],json:['rna-drawing.json',()=>JSON.stringify(s.data,null,2),'application/json'],
        ct:['rna-edited-drawing.ct',()=>ct(s.data),'text/plain'],bpseq:['rna-edited-drawing.bpseq',()=>bpseq(s.data),'text/plain'],dbn:['rna-edited-drawing.dbn',()=>editedDbn(s.data),'text/plain'],
        varna:['rna-drawing-varna.sh',()=>varnaCommand(s.data),'text/x-shellscript']},f=files[format];
      api.download(f[0],f[1](),f[2]);message(format==='varna'?'VARNA command downloaded. Run it next to VARNA.jar to redraw this structure in VARNA.':f[0]+' downloaded.');
    };
    target.addEventListener('click',event=>{
      const button=event.target.closest('[data-edit-action]');if(!button)return;
      const action=button.dataset.editAction;
      try{
        if(action==='export')return download(input('exportFormat').value);
        if(action==='load')return input('file').click();
        if(action==='undo'||action==='redo'){(action==='undo'?undo:redo)(s);sync();draw();return message(action==='undo'?'Undone.':'Redone.');}
        if(action==='zoomIn'||action==='zoomOut')return zoom(action==='zoomIn'?1/1.25:1.25);
        if(action==='fit'){s.view=null;return draw();}
        if(action==='layout')return relayout('Positions recomputed.');
        if(action==='all')return select2([1,n]);
        if(action==='domain'||action==='parent'){
          const [a,b]=selected(),found=action==='domain'?domainOf(s.data,a):parentOf(s.data,a,b);
          if(!found)throw new Error(action==='domain'?'Base '+a+' lies in the exterior loop, outside any domain.':'No pair encloses the selection.');
          return select2(found);
        }
        const [a,b]=selected(),indices=Array.from({length:b-a+1},(_,i)=>a-1+i);
        const rotate=degrees=>commit(next=>turn(next.points,indices,degrees,pivot(next,a,b)));
        if(action==='rotL')return rotate(-15);
        if(action==='rotR')return rotate(15);
        if(action==='rot90')return rotate(90);
        if(action==='mirror')return commit(next=>mirror(next.points,indices,pivot(next,a,b)));
        if(action==='resetStyle')return commit(next=>{for(let i=a;i<=b;i++)delete next.styles[i];if(next.evidence)next.evidence.overrides=true;});
        if(action==='evidence')return commit(next=>applyEvidence(next,evidenceFor(result),input('palette').value),'Evidence colours restored.');
        if(action==='colormap')return commit(next=>colorMap(next,input('mapValues').value,{minimum:input('mapMin').value,maximum:input('mapMax').value,palette:input('palette').value,caption:input('mapCaption').value}),'Colour map applied.');
        if(action==='clearColors')return commit(clearColors,'Colours cleared.');
        if(['ends','helices','hairpins'].includes(action))return commit(next=>{next.annotations=next.annotations.filter(note=>note.auto!==action).concat(autoAnnotations(next,action));s.view=null;});
        if(action==='annotation'||action==='updateAnnotation')return commit(next=>{
          const kind=input('annotationKind').value,text=input('annotation').value.trim(),anchorType=input('anchorType').value;
          if(kind==='text'&&!text)throw new Error('Enter annotation text first.');
          if(anchorType==='free'&&kind!=='text')throw new Error('Only text annotations can take a free position.');
          const note={kind,base:a,text,dx:25,dy:-35,color:input('annotationColor').value,size:number('annotationSize',6,48,'Text size')};
          if(kind==='region'){note.end=b;note.bases=Array.from({length:b-a+1},(_,i)=>a+i);note.opacity=number('opacity',.05,1,'Highlight opacity');note.width=number('regionWidth',2,80,'Highlight width');}
          else if(anchorType==='helix'||anchorType==='loop'){note.bases=anchorType==='helix'?helixOf(next,a):loopOf(next,a);note.anchorType=anchorType;note.base=Math.min(...note.bases);}
          else if(anchorType==='free'){const p=anchor(next,{base:a});note.free=true;note.x=p.x+25;note.y=p.y-35;note.dx=0;note.dy=0;}
          else if(b>a)note.bases=Array.from({length:b-a+1},(_,i)=>a+i);
          if(kind==='probe'){note.marker=input('marker').value;note.direction=input('direction').value;note.intensity=number('intensity',.25,4,'Probe intensity');}
          if(action==='annotation')next.annotations.push(note);
          else {const index=Number(input('annotationList').value);if(input('annotationList').value===''||!next.annotations[index])throw new Error('Select a saved annotation first.');if(!note.free){note.dx=next.annotations[index].dx;note.dy=next.annotations[index].dy;}next.annotations[index]=note;}
        },action==='annotation'?'Annotation added.':'Annotation updated.');
        if(action==='removeAnnotation')return commit(next=>{if(input('annotationList').value==='')throw new Error('Select a saved annotation first.');next.annotations.splice(Number(input('annotationList').value),1);},'Annotation removed.');
        if(action==='clearAnnotations')return commit(next=>{next.annotations=[];},'Annotations cleared.');
        if(action==='pair')return commit(next=>{
          if(a===b)throw new Error('Select two or more bases: the first and last are paired.');
          if(next.pairs.some(p=>[p.left,p.right].some(v=>v===a||v===b)))throw new Error('Base '+a+' or '+b+' is already paired. Remove its pair first.');
          next.pairs.push({left:a,right:b,color:input('pairColor').value,width:number('pairWidth',.5,10,'Pair width')});
        },'Pair '+a+'–'+b+' added.');
        if(action==='unpair')return commit(next=>{const before=next.pairs.length;next.pairs=next.pairs.filter(p=>!((p.left>=a&&p.left<=b)||(p.right>=a&&p.right<=b)));if(next.pairs.length===before)throw new Error('No pair touches the selection.');},'Pairs removed.');
      }catch(error){message(error.message);}
    });
    input('file').addEventListener('change',async()=>{try{const file=input('file').files[0];if(!file)return;if(file.size>5000000)throw new Error('Drawing JSON exceeds 5 MB.');const data=validate(JSON.parse(await file.text()),result.sequence,result.dotBracket);data.topologyEdited=pairSignature(data.pairs)!==pairSignature(result.pairs||[]);checkpoint(s);s.data=data;s.view=null;sync();draw();message('Drawing loaded.');}catch(error){message(error.message);}finally{input('file').value='';}});
    // Undo/redo shortcuts, except while typing in a text field.
    target.addEventListener('keydown',event=>{
      if(!(event.metaKey||event.ctrlKey)||/^(INPUT|TEXTAREA|SELECT)$/.test(event.target.tagName))return;
      const key=event.key.toLowerCase();if(key!=='z'&&key!=='y')return;
      event.preventDefault();(key==='y'||event.shiftKey?redo:undo)(s);sync();draw();
    });
    const point=(event,box)=>{const rect=host.querySelector('svg').getBoundingClientRect();const scale=Math.min(rect.width/box[2],rect.height/box[3]);return {x:box[0]+(event.clientX-rect.left-(rect.width-box[2]*scale)/2)/scale,y:box[1]+(event.clientY-rect.top-(rect.height-box[3]*scale)/2)/scale};};
    let drag=null,lastPress=null;
    host.addEventListener('pointerdown',event=>{
      if(event.button!==0)return;
      const base=event.target.closest('[data-editor-base]'),annotation=event.target.closest('[data-editor-annotation]'),box=s.view||bounds(s.data);s.view=box.slice();
      if(annotation)selectAnnotation(Number(annotation.dataset.editorAnnotation));
      if(base){const i=Number(base.dataset.editorBase);if(event.shiftKey)s.selection=[Math.min(s.selection[0],i),Math.max(s.selection[1],i)];else if(i<s.selection[0]||i>s.selection[1])s.selection=[i,i];sync();}
      event.preventDefault();
      // Double-click (two presses on one base within 400 ms) selects its domain.
      if(base&&!event.shiftKey){const i=Number(base.dataset.editorBase),now=Date.now();if(lastPress&&lastPress.i===i&&now-lastPress.t<400){const found=domainOf(s.data,i);lastPress=null;if(found){select2(found,'Selected the domain '+found[0]+'–'+found[1]+'.');return;}}lastPress={i,t:now};}
      const kind=annotation?'annotation':base?'base':event.shiftKey?'box':'pan';
      drag={kind,annotation:annotation?Number(annotation.dataset.editorAnnotation):null,start:point(event,box),last:point(event,box),moved:false};
      host.setPointerCapture(event.pointerId);draw();
    });
    host.addEventListener('pointermove',event=>{
      if(!drag)return;const p=point(event,s.view),dx=p.x-drag.last.x,dy=p.y-drag.last.y;
      if(!drag.moved&&Math.hypot(p.x-drag.start.x,p.y-drag.start.y)<1)return;
      // One undo step per drag, taken when the drag really moves something.
      if(!drag.moved&&(drag.kind==='base'||drag.kind==='annotation'))checkpoint(s);
      drag.moved=true;
      if(drag.kind==='annotation'){s.data.annotations[drag.annotation].dx+=dx;s.data.annotations[drag.annotation].dy+=dy;drag.last=p;}
      else if(drag.kind==='base'){move(dx,dy);drag.last=p;}
      else if(drag.kind==='pan'){s.view[0]-=dx;s.view[1]-=dy;}
      else {drag.last=p;}
      draw();
      if(drag.kind==='box'){const x=Math.min(drag.start.x,p.x),y=Math.min(drag.start.y,p.y);host.querySelector('svg').insertAdjacentHTML('beforeend','<rect x="'+x+'" y="'+y+'" width="'+Math.abs(p.x-drag.start.x)+'" height="'+Math.abs(p.y-drag.start.y)+'" fill="#4DBBD5" fill-opacity="0.12" stroke="#4DBBD5" stroke-dasharray="4 3"/>');}
    });
    const end=()=>{
      if(drag&&drag.kind==='box'&&drag.moved){
        const x0=Math.min(drag.start.x,drag.last.x),x1=Math.max(drag.start.x,drag.last.x),y0=Math.min(drag.start.y,drag.last.y),y1=Math.max(drag.start.y,drag.last.y);
        const inside=s.data.points.map((p,i)=>p.x>=x0&&p.x<=x1&&p.y>=y0&&p.y<=y1?i+1:0).filter(Boolean);
        if(inside.length)select2([Math.min(...inside),Math.max(...inside)],'Selected '+Math.min(...inside)+'–'+Math.max(...inside)+' (first to last base inside the box).');else draw();
      } else if(drag&&drag.moved&&drag.kind!=='pan')message('Moved. '+status());
      drag=null;
    };
    host.addEventListener('pointerup',end);host.addEventListener('pointercancel',end);
    // Plain scrolling keeps scrolling the page; Ctrl/⌘ + scroll and trackpad pinches zoom.
    host.addEventListener('wheel',event=>{if(!event.ctrlKey&&!event.metaKey)return;event.preventDefault();const box=s.view||bounds(s.data),p=point(event,box),factor=event.deltaY>0?1.1:1/1.1;if(box[2]*factor<50||box[2]*factor>1e7)return;s.view=[p.x+(box[0]-p.x)*factor,p.y+(box[1]-p.y)*factor,box[2]*factor,box[3]*factor];draw();},{passive:false});
    host.addEventListener('keydown',event=>{const base=event.target.closest('[data-editor-base]');if(!base||!['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(event.key))return;event.preventDefault();const i=Number(base.dataset.editorBase);if(i<s.selection[0]||i>s.selection[1])s.selection=[i,i];checkpoint(s);const step=event.shiftKey?10:1;move(event.key==='ArrowLeft'?-step:event.key==='ArrowRight'?step:0,event.key==='ArrowUp'?-step:event.key==='ArrowDown'?step:0);sync();draw();host.querySelector('[data-editor-base="'+i+'"]').focus();});
    // Say why a structure with HYB evidence stays uncoloured.
    const support=comradesSupport(result),evidencePairs=result.comradesEvidence?result.comradesEvidence.pairs.length:0;
    sync();draw();message(support&&!support.some(v=>v>0)?'COMRADES support: none of the '+(result.pairs||[]).length+' pairs in this structure matches '+(evidencePairs?'any of the '+evidencePairs+' RNAcofold evidence pairs':'the RNAcofold evidence')+', so every base scores 0 and keeps the default colour.':status());
  }
  root.Hyb2StructureEditor={supportColor,mapColor,evidenceFor,applyEvidence,parseValues,colorMap,clearColors,autoAnnotations,anchor,layout,pairsFrom,dotBracket,domainOf,parentOf,helixOf,loopOf,initial,validate,svg,eps,ct,bpseq,editedDbn,varnaCommand,undo,redo,session,mount};
})(typeof window!=='undefined'?window:self);
