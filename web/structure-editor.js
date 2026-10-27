/* Browser RNA drawing editor. Prediction and edited drawing remain separate. */
(function(root){
  'use strict';
  const NS='http://www.w3.org/2000/svg';
  const clone=value=>JSON.parse(JSON.stringify(value));
  const xml=value=>String(value).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  function layout(sequence,dbn,kind){
    const n=sequence.length;
    if(kind==='simple'){
      const xy=root.Hyb2RegionPanels.simpleLayout(dbn);
      return Array.from(sequence,(_,i)=>({x:xy.x[i]*2,y:xy.y[i]*2}));
    }
    return Array.from(sequence,(_,i)=>kind==='linear'?{x:i*24,y:0}:{x:Math.cos(i*2*Math.PI/n)*Math.max(80,n*4),y:Math.sin(i*2*Math.PI/n)*Math.max(80,n*4)});
  }
  // Lau plot_VARNA colour stops; ModeleColorMap interpolates RGB channels
  // and truncates to integer. Scores here are already computed by the engine.
  function supportColor(value, maximum, palette='lau') {
    if (!(value > 0) || !(maximum > 0)) return '#ffffff';
    const stops = palette === 'lau'
      ? [[0,'#FFFFFF'],[.2,'#4747FF'],[.55,'#1CFF47'],[.65,'#FFFF47'],[.9,'#FF4747'],[1,'#B64747']]
      : [[0,'#FFFFFF'],[.2,'#91D1C2'],[.55,'#4DBBD5'],[.9,'#E64B35'],[1,'#3C5488']];
    const t=Math.min(1,value/maximum);
    for(let i=1;i<stops.length;i++) if(t<=stops[i][0]){
      const f=(t-stops[i-1][0])/(stops[i][0]-stops[i-1][0]);
      return '#'+[1,3,5].map(k=>Math.floor(parseInt(stops[i-1][1].slice(k,k+2),16)*(1-f)+parseInt(stops[i][1].slice(k,k+2),16)*f).toString(16).padStart(2,'0')).join('');
    }
    return stops[stops.length-1][1];
  }
  function evidenceFor(result) {
    const values=Array.from({length:result.sequence.length},(_,i)=>Number.isFinite(Number(result.nucleotideSupport && result.nucleotideSupport[i])) ? Math.max(0,Number(result.nucleotideSupport && result.nucleotideSupport[i])) : 0);
    const maximum=values.reduce((a,b)=>Math.max(a,b),0);
    if(!maximum)return null;
    const kind=result.engine==='CPLfold'?'CPLfold bonus score':result.engine==='IRIS'?'IRIS support score':result.constraintMode==='hyb-guided'?'RNAcofold supporting observations':'Engine support score';
    return {values,maximum,kind,palette:'lau',source:'original prediction'};
  }
  function applyEvidence(drawing, evidence, palette='lau') {
    if(!evidence)throw new Error('This prediction has no experimental support scores.');
    drawing.evidence=clone(evidence);drawing.evidence.palette=palette;drawing.evidence.overrides=false;
    evidence.values.forEach((value,i)=>{
      const fill=supportColor(value,evidence.maximum,palette),r=parseInt(fill.slice(1,3),16),g=parseInt(fill.slice(3,5),16),b=parseInt(fill.slice(5,7),16);
      drawing.styles[i+1]={...(drawing.styles[i+1]||{radius:9}),fill,text:(.2126*r+.7152*g+.0722*b)<125?'#ffffff':'#222222'};
    });
  }
  function anchor(d,a) {
    const bases=a.bases || [a.base];
    return bases.reduce((p,i)=>({x:p.x+d.points[i-1].x/bases.length,y:p.y+d.points[i-1].y/bases.length}),{x:0,y:0});
  }
  function autoAnnotations(d,kind) {
    const note=(base,text,bases)=>({kind:'text',base,text,bases,dx:25,dy:-35,color:'#3C5488',auto:kind});
    if(kind==='ends')return [note(1,'5′',[1]),note(d.sequence.length,'3′',[d.sequence.length])];
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
      points:layout(result.sequence,result.dotBracket,'simple'),pairs:(result.pairs||[]).map(p=>({left:p.left,right:p.right,color:'#3C5488',width:2})),
      styles:{},annotations:[],background:'#ffffff',backbone:'#8491B4',numberInterval:10,title:result.label||'RNA structure',topologyEdited:false};
    const evidence=evidenceFor(result);
    if(evidence)applyEvidence(drawing,evidence);
    return drawing;
  }
  function validate(value,sequence,dbn){
    const color=v=>typeof v==='string'&&/^#[0-9a-f]{6}$/i.test(v);
    const pos=v=>Number.isInteger(v)&&v>=1&&v<=sequence.length;
    const finite=v=>Number.isFinite(v)&&Math.abs(v)<1e7;
    if(!value||value.schema!=='hyb2-drawing-1'||value.sequence!==sequence||value.dotBracket!==dbn)throw new Error('This drawing belongs to a different prediction.');
    if(!Array.isArray(value.points)||value.points.length!==sequence.length||value.points.some(p=>!p||!finite(p.x)||!finite(p.y)))throw new Error('Invalid nucleotide coordinates.');
    if(!color(value.background)||!color(value.backbone)||!Number.isInteger(value.numberInterval)||value.numberInterval<1||value.numberInterval>1000)throw new Error('Invalid drawing style.');
    if(typeof value.title!=='string'||value.title.length>500)throw new Error('Invalid title.');
    const used=new Set();
    if(!Array.isArray(value.pairs)||value.pairs.length>sequence.length/2)throw new Error('Invalid pair list.');
    for(const pair of value.pairs){if(!pos(pair.left)||!pos(pair.right)||pair.left>=pair.right||used.has(pair.left)||used.has(pair.right)||!color(pair.color)||!Number.isFinite(pair.width)||pair.width<.5||pair.width>10)throw new Error('Invalid or overlapping base pairs.');used.add(pair.left);used.add(pair.right);}
    if(!value.styles||typeof value.styles!=='object'||Array.isArray(value.styles))throw new Error('Invalid nucleotide styles.');
    for(const [key,s] of Object.entries(value.styles)){if(!pos(Number(key))||!s||!color(s.fill)||!color(s.text)||!Number.isFinite(s.radius)||s.radius<3||s.radius>24)throw new Error('Invalid nucleotide style.');}
    if(!Array.isArray(value.annotations)||value.annotations.length>1000||value.annotations.some(a=>!a||!pos(a.base)||!finite(a.dx)||!finite(a.dy)||typeof a.text!=='string'||a.text.length>500||!color(a.color)))throw new Error('Invalid annotation.');
    for(const a of value.annotations){
      if(a.kind && !['text','region','probe'].includes(a.kind))throw new Error('Unknown annotation type.');
      if(a.bases && (!Array.isArray(a.bases)||!a.bases.length||a.bases.length>sequence.length||a.bases.some(i=>!pos(i))))throw new Error('Invalid annotation anchors.');
      if(a.kind==='region' && (!pos(a.end)||a.end<a.base))throw new Error('Invalid highlight interval.');
      if(a.kind==='probe' && !['arrow','dot','triangle'].includes(a.marker))throw new Error('Invalid chemical probe marker.');
    }
    if(value.evidence){const e=value.evidence;
      if(!Array.isArray(e.values)||e.values.length!==sequence.length||e.values.some(v=>!Number.isFinite(v)||v<0)||!Number.isFinite(e.maximum)||e.maximum<=0||e.values.some(v=>v>e.maximum)||!['lau','brand'].includes(e.palette)||typeof e.kind!=='string'||e.kind.length>100)throw new Error('Invalid evidence colours.');
    }
    return clone(value);
  }
  function pairSignature(pairs){return pairs.map(p=>[p.left,p.right]).sort((a,b)=>a[0]-b[0]||a[1]-b[1]).map(p=>p.join('-')).join(',');}
  function ct(drawing){
    const mates=new Map();drawing.pairs.forEach(p=>{mates.set(p.left,p.right);mates.set(p.right,p.left);});
    return drawing.sequence.length+' HYB2 manually edited drawing; energy not recalculated\n'+Array.from(drawing.sequence,(base,i)=>[i+1,base,i?i:0,i+1<drawing.sequence.length?i+2:0,mates.get(i+1)||0,i+1].join('\t')).join('\n')+'\n';
  }
  function bounds(d){
    const points=d.points.concat(d.annotations.map(a=>({x:anchor(d,a).x+a.dx,y:anchor(d,a).y+a.dy})));
    let x=Infinity,y=Infinity,right=-Infinity,bottom=-Infinity;
    points.forEach(p=>{x=Math.min(x,p.x);y=Math.min(y,p.y);right=Math.max(right,p.x);bottom=Math.max(bottom,p.y);});
    // Include annotation text and numbering in the exported canvas.
    const pad=65+Math.max(0,...d.annotations.map(a=>a.text.length*7));
    return [x-pad,y-90,Math.max(d.evidence?500:200,right-x+pad*2),Math.max(160,bottom-y+180)];
  }
  function svg(d,selection,box){
    const b=box||bounds(d),parts=['<svg xmlns="'+NS+'" viewBox="'+b.join(' ')+'" width="1000" height="700" role="img" aria-label="Editable RNA drawing"><rect data-editor-background="true" x="'+b[0]+'" y="'+b[1]+'" width="'+b[2]+'" height="'+b[3]+'" fill="'+d.background+'"/><g font-family="sans-serif"><text x="'+(b[0]+20)+'" y="'+(b[1]+28)+'" font-size="18" fill="#222">'+xml(d.title)+'</text>'];
    const line=(p,q,color,width,extra)=>'<line x1="'+p.x+'" y1="'+p.y+'" x2="'+q.x+'" y2="'+q.y+'" stroke="'+color+'" stroke-width="'+width+'" '+(extra||'')+'/>';
    d.annotations.filter(a=>a.kind==='region').forEach(a=>{
      const points=d.points.slice(a.base-1,a.end);
      parts.push('<path d="'+points.map((p,i)=>(i?'L':'M')+p.x+','+p.y).join(' ')+'" fill="none" stroke="'+a.color+'" stroke-opacity="0.25" stroke-width="32" stroke-linecap="round" stroke-linejoin="round"/>');
      if(points.length===1)parts.push('<circle cx="'+points[0].x+'" cy="'+points[0].y+'" r="16" fill="'+a.color+'" fill-opacity="0.25"/>');
    });
    if(d.evidence){const e=d.evidence;
      parts.push('<text x="'+(b[0]+20)+'" y="'+(b[1]+48)+'" font-size="10" fill="#333">'+xml(e.kind)+' · original prediction · 0–'+xml(e.maximum)+(e.overrides?' · manual colours override':'')+'</text>');
      for(let i=0;i<40;i++)parts.push('<rect x="'+(b[0]+20+i*3)+'" y="'+(b[1]+55)+'" width="3" height="5" fill="'+supportColor(e.maximum*i/39,e.maximum,e.palette)+'"/>');
    }
    for(let i=1;i<d.points.length;i++)parts.push(line(d.points[i-1],d.points[i],d.backbone,1.5));
    d.pairs.forEach(p=>parts.push(line(d.points[p.left-1],d.points[p.right-1],p.color,p.width)));
    d.points.forEach((p,i)=>{const s=d.styles[i+1]||{fill:'#91D1C2',text:'#222222',radius:9};const selected=selection&&i+1>=selection[0]&&i+1<=selection[1];parts.push('<g data-editor-base="'+(i+1)+'" tabindex="0" role="button" aria-label="Move nucleotide '+(i+1)+' '+d.sequence[i]+'"><circle cx="'+p.x+'" cy="'+p.y+'" r="'+s.radius+'" fill="'+s.fill+'" stroke="'+(selected?'#E64B35':'#3C5488')+'" stroke-width="'+(selected?3:1)+'"/><text x="'+p.x+'" y="'+(p.y+4)+'" font-size="12" text-anchor="middle" fill="'+s.text+'">'+xml(d.sequence[i])+'</text></g>');if(i===0||i===d.points.length-1||(i+1)%d.numberInterval===0)parts.push('<text x="'+p.x+'" y="'+(p.y-s.radius-7)+'" text-anchor="middle" font-size="10" fill="#444">'+(i+1)+'</text>');});
    d.annotations.forEach((a,i)=>{const p=anchor(d,a),q={x:p.x+a.dx,y:p.y+a.dy};
      parts.push('<g data-editor-annotation="'+i+'" role="button" tabindex="0" aria-label="Annotation '+(i+1)+' '+xml(a.text)+'">'+line(p,q,a.color,1));
      if(a.kind==='probe'){
        if(a.marker==='dot')parts.push('<circle cx="'+q.x+'" cy="'+q.y+'" r="5" fill="'+a.color+'"/>');
        else if(a.marker==='triangle')parts.push('<path d="M'+q.x+','+(q.y-6)+' l-6,12 h12 Z" fill="'+a.color+'"/>');
        else {const dx=p.x-q.x,dy=p.y-q.y,len=Math.hypot(dx,dy)||1,ux=dx/len,uy=dy/len;
          parts.push('<path d="M'+p.x+','+p.y+' L'+(p.x-ux*10-uy*4)+','+(p.y-uy*10+ux*4)+' L'+(p.x-ux*10+uy*4)+','+(p.y-uy*10-ux*4)+' Z" fill="'+a.color+'"/>');}
      }
      parts.push('<text x="'+q.x+'" y="'+(q.y-8)+'" fill="'+a.color+'" font-size="14">'+xml(a.text)+'</text></g>');
    });
    return parts.join('')+'</g></svg>';
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
    const s=session(state),result=state.structure.result;
    target.style.height='auto';
    target.innerHTML='<div class="editor-controls"><p class="notice">Drawing editor · Layout, styles and annotations are saved separately from the prediction. Edited pairs do not recalculate its energy. Standard CT/DBN exports retain the prediction; use “Export edited CT” for edited pairs.</p><div class="control-grid">'+
      '<label class="control-field">Layout<select data-edit="layout"><option value="simple">RNA polygon</option><option value="circular">Circular</option><option value="linear">Linear</option></select></label><label class="control-field">From nucleotide<input data-edit="from" type="number" min="1" max="'+result.sequence.length+'" value="'+s.selection[0]+'"></label><label class="control-field">To nucleotide<input data-edit="to" type="number" min="1" max="'+result.sequence.length+'" value="'+s.selection[1]+'"></label><label class="control-field">Base fill<input data-edit="fill" type="color" value="#91D1C2"></label><label class="control-field">Base text<input data-edit="text" type="color" value="#222222"></label><label class="control-field">Base radius<input data-edit="radius" type="number" min="3" max="24" value="9"></label><label class="control-field">Pair colour<input data-edit="pairColor" type="color" value="#3C5488"></label><label class="control-field">Pair width<input data-edit="pairWidth" type="number" min="0.5" max="10" step="0.5" value="2"></label><label class="control-field">Number every<input data-edit="numbers" type="number" min="1" max="1000" value="'+s.data.numberInterval+'"></label><label class="control-field">Background<input data-edit="background" type="color" value="'+s.data.background+'"></label><label class="control-field">Backbone<input data-edit="backbone" type="color" value="'+s.data.backbone+'"></label><label class="control-field">Title<input data-edit="title" maxlength="500" value="'+xml(s.data.title)+'"></label><label class="control-field">Annotation<input data-edit="annotation" maxlength="500" placeholder="Text anchored to the first selected base"></label><label class="control-field">Annotation type<select data-edit="annotationKind"><option value="text">Text</option><option value="region">Region highlight</option><option value="probe">Chemical probe marker</option></select></label><label class="control-field">Probe marker<select data-edit="marker"><option value="arrow">Arrow</option><option value="dot">Dot</option><option value="triangle">Triangle</option></select></label><label class="control-field">Saved annotations<select data-edit="annotationList"><option value="">Select annotation</option></select></label><label class="control-field">Evidence palette<select data-edit="palette"><option value="lau">Lau / VARNA</option><option value="brand">Website palette</option></select></label></div><div class="inline-actions">'+
      [['layout','Apply layout'],['style','Style selected bases / pairs'],['annotation','Add annotation'],['updateAnnotation','Update selected annotation'],['removeAnnotation','Remove selected annotation'],['clearAnnotations','Clear annotations'],['evidence','Restore evidence colours'],['ends','Auto 5′ / 3′ labels'],['helices','Auto helix labels'],['hairpins','Auto hairpin labels'],['pair','Add pair between selection ends'],['unpair','Remove selected pairs'],['rotate','Rotate selection 90°'],['mirror','Mirror selection'],['undo','Undo'],['redo','Redo'],['fit','Fit'],['reset','Reset drawing'],['save','Save drawing JSON'],['load','Load drawing JSON'],['ct','Export edited CT'],['svg','Export edited SVG']].map(([action,label])=>'<button type="button" class="quiet-button" data-edit-action="'+action+'">'+label+'</button>').join('')+'</div><p class="card-note" data-editor-message role="status">Drag a base to move the selection or drag an annotation to reposition it; drag the background to pan; scroll to zoom. Focus a base and use arrow keys to move it (Shift: 10 units). SVG/PNG exports include the edited drawing.</p><input type="file" accept=".json,application/json" data-edit="file" hidden></div><div data-editor-canvas style="overflow:hidden;touch-action:none;background:white"></div>';
    const host=target.querySelector('[data-editor-canvas]'),field=name=>target.querySelector('[data-edit="'+name+'"]');
    const message=text=>{target.querySelector('[data-editor-message]').textContent=text;};
    const draw=()=>{
      host.innerHTML=svg(s.data,s.selection,s.view||bounds(s.data));
      const list=field('annotationList'),selected=list.value;
      list.innerHTML='<option value="">Select annotation</option>'+s.data.annotations.map((a,i)=>'<option value="'+i+'">'+xml((i+1)+': '+(a.kind||'text')+' · '+(a.text||'untitled'))+'</option>').join('');
      list.value=selected;
    };
    const selectAnnotation=index=>{const a=s.data.annotations[index];if(!a)return;field('annotation').value=a.text;field('annotationKind').value=a.kind||'text';field('marker').value=a.marker||'arrow';field('text').value=a.color;s.selection=[a.base,a.end||a.base];sync();field('annotationList').value=String(index);};
    const selected=()=>{const a=Number(field('from').value),b=Number(field('to').value);if(!Number.isInteger(a)||!Number.isInteger(b)||a<1||b<a||b>result.sequence.length)throw new Error('Select a valid nucleotide interval.');s.selection=[a,b];return s.selection;};
    const sync=()=>{field('from').value=s.selection[0];field('to').value=s.selection[1];};
    const move=(dx,dy)=>{for(let i=s.selection[0]-1;i<s.selection[1];i++){s.data.points[i].x+=dx;s.data.points[i].y+=dy;}};
    target.addEventListener('change',event=>{if(event.target.dataset.edit==='annotationList'&&event.target.value!==''){selectAnnotation(Number(event.target.value));draw();}if(['from','to'].includes(event.target.dataset.edit)){try{selected();draw();}catch(error){message(error.message);}}});
    target.addEventListener('click',async event=>{
      const button=event.target.closest('[data-edit-action]');if(!button)return;
      try{
        const action=button.dataset.editAction;
        if(action==='save'){api.download('rna-drawing.json',JSON.stringify(s.data,null,2),'application/json');return;}
        if(action==='load'){field('file').click();return;}
        if(action==='ct'){api.download('rna-edited-drawing.ct',ct(s.data),'text/plain');return;}
        if(action==='svg'){api.download('rna-edited-drawing.svg',svg(s.data,null),'image/svg+xml');return;}
        if(action==='undo')undo(s);
        else if(action==='redo')redo(s);
        else if(action==='fit')s.view=null;
        else {
          selected();const next=clone(s.data),a=s.selection[0],b=s.selection[1];
          if(action==='layout'){next.points=layout(next.sequence,next.dotBracket,field('layout').value);s.view=null;}
          if(action==='reset'){checkpoint(s);s.data=initial(result);s.view=null;draw();return;}
          if(action==='style'){
            if(next.evidence)next.evidence.overrides=true;
            const radius=Number(field('radius').value),width=Number(field('pairWidth').value);
            if(!Number.isFinite(radius)||radius<3||radius>24||!Number.isFinite(width)||width<.5||width>10)throw new Error('Radius must be 3–24 and pair width 0.5–10.');
            for(let i=a;i<=b;i++)next.styles[i]={fill:field('fill').value,text:field('text').value,radius};
            next.pairs.forEach(p=>{if((p.left>=a&&p.left<=b)||(p.right>=a&&p.right<=b)){p.color=field('pairColor').value;p.width=width;}});
            next.numberInterval=Number(field('numbers').value);next.background=field('background').value;next.backbone=field('backbone').value;next.title=field('title').value;
          }
          if(action==='evidence')applyEvidence(next,evidenceFor(result),field('palette').value);
          if(['ends','helices','hairpins'].includes(action)){
            next.annotations=next.annotations.filter(note=>note.auto!==action).concat(autoAnnotations(next,action));s.view=null;
          }
          if(action==='annotation'||action==='updateAnnotation'){
            const kind=field('annotationKind').value,text=field('annotation').value.trim();
            if(kind==='text'&&!text)throw new Error('Enter annotation text first.');
            const note={kind,base:a,text,dx:25,dy:-35,color:field('text').value};
            if(kind==='region'){note.end=b;note.bases=Array.from({length:b-a+1},(_,i)=>a+i);}
            if(kind==='probe')note.marker=field('marker').value;
            if(action==='annotation')next.annotations.push(note);
            else {const index=Number(field('annotationList').value);if(field('annotationList').value===''||!next.annotations[index])throw new Error('Select a saved annotation first.');note.dx=next.annotations[index].dx;note.dy=next.annotations[index].dy;next.annotations[index]=note;}
            s.view=null;
          }
          if(action==='removeAnnotation'){
            if(field('annotationList').value==='')throw new Error('Select a saved annotation first.');next.annotations.splice(Number(field('annotationList').value),1);
          }
          if(action==='clearAnnotations')next.annotations=[];
          if(action==='pair'){
            if(a===b)throw new Error('Choose two different endpoints.');
            if(next.pairs.some(p=>[p.left,p.right].some(v=>v===a||v===b)))throw new Error('An endpoint is already paired. Remove its existing pair first.');
            next.pairs.push({left:a,right:b,color:field('pairColor').value,width:Number(field('pairWidth').value)});
          }
          if(action==='unpair')next.pairs=next.pairs.filter(p=>!((p.left>=a&&p.left<=b)||(p.right>=a&&p.right<=b)));
          if(action==='rotate'||action==='mirror'){
            const points=next.points.slice(a-1,b),cx=points.reduce((v,p)=>v+p.x,0)/points.length,cy=points.reduce((v,p)=>v+p.y,0)/points.length;
            points.forEach(p=>{const x=p.x-cx,y=p.y-cy;p.x=cx+(action==='rotate'?-y:-x);p.y=cy+(action==='rotate'?x:y);});s.view=null;
          }
          next.topologyEdited=pairSignature(next.pairs)!==pairSignature(result.pairs||[]);
          validate(next,result.sequence,result.dotBracket);checkpoint(s);s.data=next;
        }
        sync();draw();message(s.data.topologyEdited?'Manually edited pairs: prediction energy does not describe this drawing.':'Drawing updated. Prediction and energy are unchanged.');
      }catch(error){message(error.message);}
    });
    field('file').addEventListener('change',async()=>{try{const file=field('file').files[0];if(!file)return;if(file.size>5000000)throw new Error('Drawing JSON exceeds 5 MB.');const data=validate(JSON.parse(await file.text()),result.sequence,result.dotBracket);data.topologyEdited=pairSignature(data.pairs)!==pairSignature(result.pairs||[]);checkpoint(s);s.data=data;s.view=null;draw();message('Drawing loaded.');}catch(error){message(error.message);}finally{field('file').value='';}});
    const point=(event,box)=>{const rect=host.querySelector('svg').getBoundingClientRect();const scale=Math.min(rect.width/box[2],rect.height/box[3]);return {x:box[0]+(event.clientX-rect.left-(rect.width-box[2]*scale)/2)/scale,y:box[1]+(event.clientY-rect.top-(rect.height-box[3]*scale)/2)/scale};};
    let drag=null;
    host.addEventListener('pointerdown',event=>{if(event.button!==0)return;event.preventDefault();const base=event.target.closest('[data-editor-base]'),annotation=event.target.closest('[data-editor-annotation]'),box=s.view||bounds(s.data);s.view=box.slice();if(annotation){checkpoint(s);selectAnnotation(Number(annotation.dataset.editorAnnotation));}if(base){const i=Number(base.dataset.editorBase);if(i<s.selection[0]||i>s.selection[1])s.selection=[i,i];sync();checkpoint(s);}drag={base:!!base,annotation:annotation?Number(annotation.dataset.editorAnnotation):null,last:point(event,box)};host.setPointerCapture(event.pointerId);draw();});
    host.addEventListener('pointermove',event=>{if(!drag)return;const p=point(event,s.view),dx=p.x-drag.last.x,dy=p.y-drag.last.y;if(drag.annotation!==null){s.data.annotations[drag.annotation].dx+=dx;s.data.annotations[drag.annotation].dy+=dy;drag.last=p;}else if(drag.base){move(dx,dy);drag.last=p;}else{s.view[0]-=dx;s.view[1]-=dy;}draw();});
    const end=()=>{drag=null;};host.addEventListener('pointerup',end);host.addEventListener('pointercancel',end);host.addEventListener('lostpointercapture',end);
    host.addEventListener('wheel',event=>{event.preventDefault();const box=s.view||bounds(s.data),p=point(event,box),factor=event.deltaY>0?1.1:1/1.1;if(box[2]*factor<50||box[2]*factor>1e7)return;s.view=[p.x+(box[0]-p.x)*factor,p.y+(box[1]-p.y)*factor,box[2]*factor,box[3]*factor];draw();},{passive:false});
    host.addEventListener('keydown',event=>{const base=event.target.closest('[data-editor-base]');if(!base||!['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(event.key))return;event.preventDefault();const i=Number(base.dataset.editorBase);if(i<s.selection[0]||i>s.selection[1])s.selection=[i,i];checkpoint(s);const step=event.shiftKey?10:1;move(event.key==='ArrowLeft'?-step:event.key==='ArrowRight'?step:0,event.key==='ArrowUp'?-step:event.key==='ArrowDown'?step:0);sync();draw();host.querySelector('[data-editor-base="'+i+'"]').focus();});
    draw();
  }
  root.Hyb2StructureEditor={supportColor,evidenceFor,applyEvidence,autoAnnotations,anchor,layout,initial,validate,svg,ct,undo,redo,session,mount};
})(typeof window!=='undefined'?window:self);
