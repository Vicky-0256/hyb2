/* Local batch exports. Calculations use the same ViennaRNA and HYB binning
 * as the interactive tools. Kept DOM-free until a UI action is invoked. */
(function (root) {
  "use strict";
  const jobs = new Map();
  const esc = value => String(value == null ? "" : value).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
  const tsv = value => String(value == null ? "" : value).replace(/[\t\r\n]/g, " ");
  const pause = () => new Promise(resolve => setTimeout(resolve, 0));

  function prepareEnergy(record, fasta) {
    const cols = record.raw.split("\t");
    if (cols.length < 15) throw new Error("Incomplete HYB row");
    const arms = [[3, 6, 7], [9, 12, 13]].map(indices => {
      const name = cols[indices[0]].trim();
      const start = Number(cols[indices[1]]), end = Number(cols[indices[2]]);
      const entry = root.Hyb2Data.findFastaEntry(fasta, name);
      if (!entry) throw new Error("No mapped FASTA for " + name);
      if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 1 || end < start)
        throw new Error("Invalid or antisense reference interval");
      if (end > entry.sequence.length) throw new Error("Interval exceeds FASTA length");
      const sequence = entry.sequence.slice(start - 1, end).toUpperCase().replace(/T/g, "U");
      if (!/^[ACGU]+$/.test(sequence)) throw new Error("Unsupported or ambiguous reference bases");
      return sequence;
    });
    if (arms[0].length + arms[1].length > 2000) throw new Error("Combined arms exceed the 2,000 nt batch limit");
    return arms;
  }

  // Replace only column 3; preserve comments, blank lines, invalid rows,
  // extra columns and original line endings when the source File exists.
  function rewriteEnergy(source, updates) {
    let line = 0;
    return source.replace(/[^\r\n]+|\r\n|\n|\r/g, part => {
      if (/^[\r\n]/.test(part)) { line++; return part; }
      const update = updates.get(line + 1);
      if (!update) return part;
      const raw = line === 0 ? part.replace(/^\uFEFF/, "") : part;
      if (raw !== update.raw) throw new Error("Source row changed at line " + (line + 1));
      return part.replace(/^([^\t]*\t[^\t]*\t)[^\t]*/, (_, prefix) => prefix + update.energy.toFixed(2));
    });
  }

  function topRegions(interactions) {
    return ["up", "down"].flatMap(direction => interactions.filter(r => r.direction === direction &&
      Number.isFinite(r.padj) && r.padj >= 0 && Number.isFinite(r.x) && Number.isFinite(r.y))
      .map((row, index) => ({row, index})).sort((a, b) => a.row.padj - b.row.padj || a.index - b.index).slice(0, 10)
      .map((item, index) => ({direction, rank: index + 1, x: item.row.x, y: item.row.y,
        padj: item.row.padj, log2FoldChange: item.row.log2FoldChange,
        x1: Math.max(1, item.row.x - 150), x2: item.row.x + 150,
        y1: Math.max(1, item.row.y - 150), y2: item.row.y + 150})));
  }

  function contrastQuantile(value) {
    const number = typeof value === 'number' ? value : String(value == null ? '' : value).trim() === '' ? NaN : Number(value);
    if (!Number.isFinite(number) || number < 0 || number > 1) throw new Error('Contrast quantile must be a number from 0 to 1 (Lau -q; default 0.95).');
    return number;
  }

  function histogramQuantile(hist, probability) {
    const entries = Array.from(hist).sort((a, b) => a[0] - b[0]);
    const n = entries.reduce((sum, item) => sum + item[1], 0);
    if (!n) return 1;
    const position = (n - 1) * probability;
    const at = index => { let count = 0; for (const [value, amount] of entries) { count += amount; if (index < count) return value; } return entries[entries.length - 1][0]; };
    const lo = at(Math.floor(position)), hi = at(Math.ceil(position));
    return lo + (hi - lo) * (position - Math.floor(position));
  }

  // Full-map pooled configurable quantile, as Lau/contact_density_map_zoom.R;
  // retain every replicate separately, never average away a missing signal.
  function mapBatch(samples, regions, progress, probability = .95) {
    probability = contrastQuantile(probability);
    const histogram = new Map();
    const panels = regions.map(region => ({region, samples: []}));
    samples.forEach((sample, index) => {
      let contributions = 0;
      for (let j = 0; j < sample.arms.length; j += 4) {
        const a = Array.from(sample.arms.slice(j, j + 4));
        if (a.some(v => !Number.isSafeInteger(v) || v < 0) || a[1] < a[0] || a[3] < a[2]) throw new Error("Invalid contact coordinates");
        contributions += (Math.floor(a[1]/10)-Math.floor(a[0]/10)+1)*(Math.floor(a[3]/10)-Math.floor(a[2]/10)+1);
        if (contributions > 50000000) throw new Error("This replicate exceeds the 50 million contact contribution batch limit");
      }
      // Bound distinct cells before allocation, including sparse long references.
      const counts = new Map();
      for (let j = 0; j < sample.arms.length; j += 4) {
        for (let x = Math.floor(sample.arms[j]/10)*10; x <= sample.arms[j+1]; x += 10) {
          for (let y = Math.floor(sample.arms[j+2]/10)*10; y <= sample.arms[j+3]; y += 10) {
            const key = x + ':' + y;
            counts.set(key, (counts.get(key) || 0) + 1);
            if (counts.size > 1500000) throw new Error('This replicate exceeds the 1.5 million distinct contact cell batch limit');
          }
        }
      }
      const bins = {x: new Float64Array(counts.size), y: new Float64Array(counts.size), count: new Float64Array(counts.size)};
      let bin = 0;
      counts.forEach((value, key) => {const coords = key.split(':'); bins.x[bin] = Number(coords[0]); bins.y[bin] = Number(coords[1]); bins.count[bin++] = value;});
      counts.clear();
      for (const count of bins.count) histogram.set(count, (histogram.get(count) || 0) + 1);
      for (const panel of panels) {
        const r = panel.region, cells = [];
        for (let j = 0; j < bins.x.length; j++) {
          // ggplot geom_tile centres each tile on the contact.txt coordinate.
          if (bins.x[j] >= r.x1 && bins.x[j] <= r.x2 && bins.y[j] >= r.y1 && bins.y[j] <= r.y2)
            cells.push([bins.x[j], bins.y[j], bins.count[j]]);
        }
        panel.samples.push({id: sample.id, label: sample.label, condition: sample.condition,
          rnaX: sample.rnaX, rnaY: sample.rnaY, cells});
      }
      if (progress) progress(index + 1, samples.length);
    });
    const quantile = histogramQuantile(histogram, probability);
    return {panels, probability, quantile, cap: Math.max(1, Math.ceil(quantile)), binSize: 10};
  }

  function mapSvg(panel, cap, labels, quantile) {
    const r = panel.region, width = Math.max(1, panel.samples.length) * 300, height = 390;
    const title = 'Top ' + r.rank + ' · Higher in ' + labels[r.direction === 'up' ? 'A' : 'B'];
    let out = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 '+width+' '+height+'" width="'+width+'" height="'+height+'" role="img"><title>'+esc(title)+'</title><rect width="100%" height="100%" fill="white"/><g font-family="sans-serif" fill="#222"><text x="18" y="24" font-size="16">'+esc(title)+'</text><text x="18" y="45" font-size="12">padj '+esc(r.padj.toPrecision(4))+' · 10 nt bins · raw records · common white–black cap '+cap+'</text>';
    panel.samples.forEach((sample, index) => {
      const left = index * 300 + 48, top = 104, size = 220;
      const sx = x => left + (x - r.x1)/(r.x2-r.x1)*size;
      const sy = y => top + size - (y - r.y1)/(r.y2-r.y1)*size;
      out += '<text x="'+(index*300+12)+'" y="70" font-size="12">'+esc(sample.label)+'</text><text x="'+(index*300+12)+'" y="88" font-size="10">'+esc(sample.rnaX+' × '+sample.rnaY)+'</text><defs><clipPath id="panel'+index+'"><rect x="'+left+'" y="'+top+'" width="'+size+'" height="'+size+'"/></clipPath></defs><g clip-path="url(#panel'+index+')">';
      sample.cells.forEach(([x,y,count]) => {
        const lightness = 100 * (1 - Math.min(count,quantile == null ? cap : quantile)/cap);
        const luminance = lightness > 8 ? Math.pow((lightness+16)/116,3) : lightness/903.3;
        const grey = Math.round(255 * (luminance <= .0031308 ? 12.92*luminance : 1.055*Math.pow(luminance,1/2.4)-.055));
        out += '<rect x="'+sx(x-5)+'" y="'+sy(y+5)+'" width="'+(size*10/(r.x2-r.x1))+'" height="'+(size*10/(r.y2-r.y1))+'" fill="rgb('+grey+','+grey+','+grey+')"><title>'+x+', '+y+': '+count+'</title></rect>';
      });
      out += '</g><rect x="'+left+'" y="'+top+'" width="'+size+'" height="'+size+'" fill="none" stroke="#222"/><g font-size="10"><text x="'+left+'" y="345">'+r.x1+'</text><text x="'+(left+size)+'" y="345" text-anchor="end">'+r.x2+'</text><text x="'+(left-4)+'" y="'+(top+size)+'" text-anchor="end">'+r.y1+'</text><text x="'+(left-4)+'" y="'+(top+10)+'" text-anchor="end">'+r.y2+'</text><text x="'+left+'" y="365">'+esc('RNA X (nt) · condition '+sample.condition)+'</text></g>';
      if (!sample.cells.length) out += '<text x="'+(left+size/2)+'" y="215" text-anchor="middle" font-size="12">No contacts in this window</text>';
    });
    return out+'</g></svg>';
  }

  function mapFiles(batch, result) {
    const labels = result.labels || {A:'Condition A', B:'Condition B'};
    const files = [], rows = ['direction\trank\tx1\tx2\ty1\ty2\tpadj\tlog2FoldChange\tfile'];
    const html = [];
    batch.panels.forEach(panel => {
      const r = panel.region, name = r.direction+'-'+String(r.rank).padStart(2,'0')+'-'+r.x+'-'+r.y;
      const svg = mapSvg(panel,batch.cap,labels,batch.quantile);
      files.push({path:name+'.svg',text:svg});
      const cells = ['dataset_id\tlabel\tcondition\trna_x\trna_y\tx\ty\trecords'];
      panel.samples.forEach(s => s.cells.forEach(c => cells.push([s.id,s.label,s.condition,s.rnaX,s.rnaY].concat(c).map(tsv).join('\t'))));
      files.push({path:name+'.tsv',text:cells.join('\n')+'\n'});
      rows.push([r.direction,r.rank,r.x1,r.x2,r.y1,r.y2,r.padj,r.log2FoldChange,name+'.svg'].join('\t'));
      html.push('<section>'+svg+'</section>');
    });
    files.push({path:'interactions.tsv',text:rows.join('\n')+'\n'});
    files.push({path:'preview.html',text:'<!doctype html><meta charset="utf-8"><title>Top 10 local contact maps</title><style>body{font:16px sans-serif;margin:24px}section{margin:24px 0;overflow:auto;break-after:page}svg{max-width:100%;height:auto}@media print{section{overflow:visible}body{margin:0}}</style><h1>Top 10 local contact maps per direction</h1><p>Ranked by adjusted p-value; ±150 nt windows (lower bound 1), each replicate separately, shared '+(100*batch.probability)+'% count cap. Counts use each dataset’s own RNA coordinates; no sequence alignment. Print this page to save PDF.</p>'+html.join('')});
    files.push({path:'parameters.json',text:JSON.stringify({method:'Lau-style replicate contact maps',topPerDirection:10,flank:150,binSize:10,quantile:batch.probability,cap:batch.cap,uncappedQuantile:batch.quantile,labels,datasets:result.datasets,test:result.summary.parameters,completedAt:result.completedAt},null,2)});
    return files;
  }

  function stop(kind) {
    const job = jobs.get(kind);
    if (job) { job.cancelled = true; if (job.worker) job.worker.terminate(); (job.workers || []).forEach(worker => worker.terminate()); if (job.reject) job.reject(new Error('Cancelled')); (job.rejects ? Array.from(job.rejects) : []).forEach(reject => reject(new Error('Cancelled'))); jobs.delete(kind); if(job.status) {job.status.running=false;job.status.message='Cancelled; no partial output exported.';} }
  }
  function stopAll() { Array.from(jobs.keys()).forEach(stop); }
  function callWorker(job, name, message, onProgress) {
    return new Promise((resolve,reject) => {
      job.reject = reject;
      const worker = job.worker || new Worker((root.HYB2_ASSET_BASE || './')+name+'?build='+encodeURIComponent(root.HYB2_BUILD && root.HYB2_BUILD.commit || 'local'));
      job.worker=worker;
      worker.onerror=event=>{worker.terminate();job.worker=null;reject(new Error(event.message || 'Batch worker failed'));};
      worker.onmessage=event=>{const data=event.data;if(data.type==='progress'){if(onProgress)onProgress(data);return;}if(!job.keepAlive){worker.terminate();job.worker=null;}job.reject=null;if(data.type==='error')reject(new Error(data.message));else resolve(data);};
      worker.postMessage(message);
    });
  }
  function progress(status, message, api) {status.message=message;api.render();}
  function quietProgress(status, message, api) {
    status.message=message;
    const line=typeof document!=='undefined'&&document.querySelector?document.querySelector('[data-batch-energy-status]'):null;
    if(line)line.textContent=message;else api.render();
  }
  // The energy tools work on the primary file only, so a header scope change
  // (state.summary) must not reset or abort them.
  const primaryOf = state => state.primarySummary || state.summary;
  function energyState(state) {
    if (!state.hybEnergy || state.hybEnergy.summary !== primaryOf(state) || state.hybEnergy.fasta !== state.fasta || state.hybEnergy.mapping !== JSON.stringify(state.fasta && state.fasta.mapping)) {
      stop('energy');state.hybEnergy={summary:primaryOf(state),fasta:state.fasta,mapping:JSON.stringify(state.fasta && state.fasta.mapping),running:false,message:'',result:null};
    }
    return state.hybEnergy;
  }
  // Engines for column 3. RNAcofold is what the HYB2 pipeline (sam2hyb) runs;
  // CPLcofold runs as pure Python here (about 100x slower per record), so it
  // gets fewer, memory-heavier workers and is best run on filtered records.
  const ENERGY_ENGINES = {
    rnacofold: {label:'RNAcofold (ViennaRNA), as the HYB2 pipeline',name:'ViennaRNA RNAcofold',worker:'folding-energy.worker.js',module:false,chunk:64,maxWorkers:8,maxLength:2000,msPerRecord:15,
      message:pairs=>({type:'cofold-batch',pairs,temperature:37,minimumLoop:3})},
    cplcofold: {label:'CPLcofold, nested (Turner energies)',name:'CPLcofold, pseudoknots off',worker:'cplfold.worker.mjs',module:true,chunk:4,maxWorkers:4,maxLength:500,msPerRecord:1700,
      message:pairs=>({type:'cplfold-energy-batch',pairs,allowPseudoknot:false,energyModel:'DP09'})},
    'cplcofold-pk': {label:'CPLcofold with pseudoknots (experimental score)',name:'CPLcofold, pseudoknots allowed (DP09 ranking score)',worker:'cplfold.worker.mjs',module:true,chunk:4,maxWorkers:4,maxLength:500,msPerRecord:2300,
      message:pairs=>({type:'cplfold-energy-batch',pairs,allowPseudoknot:true,energyModel:'DP09'})}
  };
  function energySettings(state) {
    if (!state.hybEnergySettings) state.hybEnergySettings={engine:'rnacofold',records:'all'};
    return state.hybEnergySettings;
  }
  function energyEngine(state) { return ENERGY_ENGINES[energySettings(state).engine] || ENERGY_ENGINES.rnacofold; }
  function energyWorkerCount(engine, chunks) {
    const cores=Number(root.navigator && root.navigator.hardwareConcurrency)||2;
    return Math.max(1,Math.min(engine.maxWorkers,cores-1,chunks));
  }
  function duration(ms) {
    const seconds=Math.max(1,Math.round(ms/1000));
    return seconds<90?seconds+' s':seconds<5400?Math.round(seconds/60)+' min':(seconds/3600).toFixed(1)+' h';
  }
  function renderEnergy(state) {
    const s=energyState(state), settings=energySettings(state), engine=energyEngine(state);
    const file=state.file&&state.file.name?' ('+esc(state.file.name)+')':'';
    const records=Number(primaryOf(state)&&primaryOf(state).validRecords)||0;
    const workers=energyWorkerCount(engine,Infinity);
    const estimate=records?' All '+records.toLocaleString('en-US')+' records: about '+duration(records*engine.msPerRecord/workers)+' with '+workers+(workers===1?' worker':' parallel workers')+' here.':'';
    const select=(key,label,value,options)=>'<label class="control-field"><span>'+label+'</span><select data-feature="batch-energy-setting" data-key="'+key+'"'+(s.running?' disabled':'')+'>'+options.map(o=>'<option value="'+o[0]+'"'+(o[0]===value?' selected':'')+'>'+esc(o[1])+'</option>').join('')+'</select></label>';
    return '<section class="data-card"><div class="card-title-row"><div><h2>HYB binding energies</h2><p class="card-note">Recalculate column 3 (ΔG of the two arms, kcal/mol, 37 °C) for records of the primary file'+file+', from the mapped FASTA arms. Download a new HYB; the loaded dataset stays unchanged.</p></div></div>'+
      '<div class="control-grid">'+select('engine','Engine',settings.engine,Object.keys(ENERGY_ENGINES).map(key=>[key,ENERGY_ENGINES[key].label]))+
      select('records','Records',settings.records,[['all','All records of the primary file'],['filtered','Records matching the Interactions filters']])+'</div>'+
      '<p class="card-note">'+esc((settings.engine==='rnacofold'?'The HYB2 pipeline\'s method.':settings.engine==='cplcofold'?'Same Turner energy model as RNAcofold, beam search; runs as pure Python in this browser, so it is slow.':'Adds CPLfold\'s pseudoknot phase: its DP09 pseudoknot term is an experimental ranking score, not a validated free energy.')+estimate+(settings.engine!=='rnacofold'?' For many records, filter first or use HYB2 Desktop / the cplcofold command.':''))+'</p>'+
      '<div class="inline-actions"><button class="button" data-feature-action="batch-energy-run"'+(s.running||!state.fasta?' disabled':'')+'>Calculate binding energies</button>'+(s.running?'<button class="quiet-button" data-feature-action="batch-energy-cancel">Cancel</button>':'')+(s.result?'<button class="button button-secondary" data-feature-action="batch-energy-hyb">Download updated HYB</button><button class="quiet-button" data-feature-action="batch-energy-zip">Download HYB + audit ZIP</button>':'')+'</div><p class="card-note" role="status" data-batch-energy-status>'+esc(s.message||(!state.fasta?'Load and map a reference FASTA first.':'Unmapped, antisense, ambiguous, over-limit or unselected rows retain their original energy and are listed in the audit.'))+'</p></section>';
  }
  // Fold the prepared pairs on a pool of workers, chunk by chunk; energies
  // come back in input order whatever worker finishes first.
  async function foldPool(job, engine, prepared, onChunk) {
    const starts=[];for(let start=0;start<prepared.length;start+=engine.chunk)starts.push(start);
    const energies=new Array(prepared.length);let next=0,version='',failed=null;
    const size=energyWorkerCount(engine,starts.length);
    const url=(root.HYB2_ASSET_BASE || './')+engine.worker+'?build='+encodeURIComponent(root.HYB2_BUILD && root.HYB2_BUILD.commit || 'local');
    job.workers=[];job.rejects=new Set();
    const slot=async()=>{
      const worker=engine.module?new Worker(url,{type:'module'}):new Worker(url);job.workers.push(worker);
      while(next<starts.length&&!job.cancelled&&!failed){
        const start=starts[next++], slice=prepared.slice(start,start+engine.chunk);
        let reply;
        try {
          reply=await new Promise((resolve,reject)=>{job.rejects.add(reject);
            worker.onerror=event=>{job.rejects.delete(reject);reject(new Error(event.message||'Energy worker failed'));};
            worker.onmessage=event=>{const data=event.data;if(data.type==='progress')return;job.rejects.delete(reject);if(data.type==='error')reject(new Error(data.message));else resolve(data);};
            worker.postMessage(engine.message(slice.map(item=>item.arms)));});
        } catch (error) { failed=failed||error; throw error; }
        if(!reply.energies||reply.energies.length!==slice.length){failed=new Error('Incomplete energy worker result');throw failed;}
        slice.forEach((item,i)=>{energies[start+i]=reply.energies[i];});version=reply.version||version;onChunk(slice.length,size);
      }
    };
    try { await Promise.all(Array.from({length:size},slot)); }
    finally { job.workers.forEach(worker=>worker.terminate()); job.workers=[]; }
    return {energies,version,workers:size};
  }
  async function runEnergy(state,api) {
    stop('energy');const status=energyState(state), job={status};jobs.set('energy',job);status.running=true;status.result=null;
    const file=state.file, fasta=state.fasta, settings=Object.assign({},energySettings(state)), engine=energyEngine(state);
    const unchanged=()=>primaryOf(state)!==status.summary || state.fasta!==fasta || JSON.stringify(fasta.mapping)!==status.mapping;
    try {
      if(!fasta)throw new Error('Load a mapped reference FASTA first.');
      progress(status,'Reading the HYB records…',api);
      const primary=(state.datasets||[]).find(d=>d.primary);
      const query={offset:0,limit:null,order:'file'};
      if(primary)query.datasetIds=[primary.id];
      const all=await api.queryHybrids(query);
      const chosen=settings.records==='filtered'?await api.queryHybrids(Object.assign({},query,{filters:Object.assign({},state.filters||{})})):all;
      if(job.cancelled)return;
      // Only the primary file is rewritten: rows of other merged datasets
      // carry their own line numbers.
      const onPrimary=r=>!r.datasetId||!primary||r.datasetId===primary.id;
      const records=(all.rows||[]).filter(onPrimary);
      if(!records.length)throw new Error('No HYB records available.');
      const selected=new Set((chosen.rows||[]).filter(onPrimary).map(r=>r.lineNumber+'\u0000'+r.id));
      let source;
      if(file && typeof file.text==='function') {
        source=/\.gz$/i.test(file.name)?await new Response(file.stream().pipeThrough(new DecompressionStream('gzip'))).text():await file.text();
      } else {source=records.map(r=>r.raw).join('\n')+'\n';}
      if(job.cancelled)return;
      const audit=['line\tid\tstatus\told_energy\tnew_energy\tnote'], notes=new Array(records.length), prepared=[];
      records.forEach((record,i)=>{
        const line=file?record.lineNumber:i+1, old=record.raw.split('\t')[2];
        if(!selected.has(record.lineNumber+'\u0000'+record.id)){notes[i]={line,record,old,note:'not selected by the Interactions filters'};return;}
        try {
          const arms=prepareEnergy(record,fasta);
          if(arms[0].length+arms[1].length>engine.maxLength)throw new Error('Combined arms exceed the '+engine.maxLength.toLocaleString('en-US')+' nt '+(engine.module?'CPLcofold':'batch')+' limit');
          notes[i]={line,record,old,item:prepared.length};prepared.push({arms});
        } catch(error){notes[i]={line,record,old,note:error.message};}
      });
      const started=Date.now();let done=0,lastRender=0;
      // progress rewrites only the status line: a full page render per update
      // would hold the main thread and leave finished workers waiting
      const report=workers=>{const left=prepared.length-done, rate=done/(Date.now()-started);
        quietProgress(status,done.toLocaleString('en-US')+' / '+prepared.length.toLocaleString('en-US')+' energies calculated with '+engine.name+' on '+workers+(workers===1?' worker':' workers')+(done&&left?'; about '+duration(left/rate)+' left.':'.'),api);};
      progress(status,'Starting '+engine.name+' for '+prepared.length.toLocaleString('en-US')+' records…',api);
      const folded=prepared.length?await foldPool(job,engine,prepared,(count,workers)=>{done+=count;if(Date.now()-lastRender>400||done===prepared.length){lastRender=Date.now();if(unchanged())job.stale=true;report(workers);}}):{energies:[],version:'',workers:0};
      if(job.cancelled)return;
      if(job.stale||unchanged())throw new Error('Dataset or reference mapping changed; calculate again.');
      const updates=new Map();let updated=0,skipped=0;
      notes.forEach(entry=>{
        if(entry.item===undefined){skipped++;audit.push([entry.line,entry.record.id,'unchanged',entry.old,entry.old,entry.note].map(tsv).join('\t'));return;}
        const energy=folded.energies[entry.item];
        if(!Number.isFinite(energy))throw new Error('Invalid energy returned');
        updates.set(entry.line,{raw:entry.record.raw,energy});updated++;
        audit.push([entry.line,entry.record.id,'updated',entry.old,energy.toFixed(2),''].map(tsv).join('\t'));
      });
      const hyb=rewriteEnergy(source,updates);
      status.result={hyb,engine:settings.engine,audit:audit.join('\n')+'\n',metadata:{engine:engine.name,engineVersion:folded.version,temperature:37,minimumLoop:settings.engine==='rnacofold'?3:null,energyModel:settings.engine==='rnacofold'?'Turner 2004 (ViennaRNA default)':'CPLfold DP09',pseudoknots:settings.engine==='cplcofold-pk',records:settings.records==='filtered'?'interaction-filters':'all',filters:settings.records==='filtered'?Object.assign({},state.filters||{}):null,updated,unchanged:skipped,validRecords:records.length,workers:folded.workers,elapsedSeconds:Math.round((Date.now()-started)/100)/10,sourceFile:file&&file.name,reference:fasta.fileName,mapping:fasta.mapping,maximumCombinedArmLength:engine.maxLength,completedAt:new Date().toISOString()}};
      status.message=updated.toLocaleString('en-US')+' energies updated with '+engine.name+' in '+duration(Date.now()-started)+'; '+skipped.toLocaleString('en-US')+' valid rows unchanged. Comments and unparsed source lines are preserved. Download the audit for details.';
    }catch(error){if(!job.cancelled)status.message='Energy calculation failed: '+error.message;}
    finally{if(jobs.get('energy')===job){jobs.delete('energy');status.running=false;api.render();}}
  }


  async function runMaps(state,api) {
    stop('maps');const comparison=state.comparison,result=comparison.deseq.result;
    const status=comparison.topMaps={running:true,message:'Reading replicate contacts…'},job={status};jobs.set('maps',job);api.render();
    try{
      if(!result || result.inputKey!==root.Hyb2DeseqCard.inputKey(comparison))throw new Error('Run the differential test again after changing its inputs.');
      const probability=contrastQuantile(comparison.topMapQuantile == null ? .95 : comparison.topMapQuantile);
      const regions=topRegions(result.interactions);if(!regions.length)throw new Error('No significant merged interactions to export.');
      const samples=[];
      for(const dataset of result.datasets){const arms=await api.getArms({datasetIds:[dataset.id]},{});if(job.cancelled)return;samples.push(Object.assign({},dataset,{label:dataset.label||dataset.fileName||dataset.id,arms:root.Hyb2DeseqCard.collectArms(arms,dataset)}));}
      const reply=await callWorker(job,'batch-maps.worker.js',{samples,regions,probability},p=>progress(status,'Building maps: '+p.done+' / '+p.total+' replicates',api));
      if(job.cancelled)return;
      if(contrastQuantile(comparison.topMapQuantile == null ? .95 : comparison.topMapQuantile)!==probability || state.comparison!==comparison || comparison.deseq.result!==result || result.inputKey!==root.Hyb2DeseqCard.inputKey(comparison))throw new Error('Comparison changed during export. Run the export again.');
      api.downloadBlob('hyb2-top10-local-maps.zip',root.Hyb2Results.zip(mapFiles(reply.result,result),'hyb2-top10-local-maps'));
      status.message=reply.result.panels.length+' local maps exported (contrast quantile '+reply.result.probability+', count cap '+reply.result.cap+'), with each replicate, raw-count TSVs and printable preview.html.';
    }catch(error){if(!job.cancelled)status.message='Map export failed: '+error.message;}
    finally{if(jobs.get('maps')===job){jobs.delete('maps');status.running=false;api.render();}}
  }
  function renderMaps(comparison,stale) {
    const s=comparison.topMaps||{};
    return '<label class="control-field"><span>Local map contrast quantile (Lau -q)</span><input type="number" min="0" max="1" step="0.01" data-feature="batch-map-quantile" data-key="quantile" value="'+esc(comparison.topMapQuantile == null ? '0.95' : comparison.topMapQuantile)+'"'+(s.running?' disabled':'')+'><small>0–1; default 0.95. Lower values increase contrast. One shared cap across all replicates.</small></label><div class="inline-actions"><button class="button button-secondary" data-feature-action="batch-maps-run"'+(stale||s.running?' disabled':'')+'>Export Top 10 local maps per direction · ZIP</button>'+(s.running?'<button class="quiet-button" data-feature-action="batch-maps-cancel">Cancel export</button>':'')+'</div><p class="card-note" role="status">'+esc(s.message||'Each map shows all included replicates separately, ±150 nt around the interaction, in white and black with the selected common count cap. ZIP contains SVGs, count tables and a printable preview.')+'</p>';
  }
  function handleInput(state, element) {
    if (element.dataset.feature === 'batch-energy-setting') {
      const settings = energySettings(state);
      if (state.hybEnergy && state.hybEnergy.running) return true;
      if (element.dataset.key === 'engine' && ENERGY_ENGINES[element.value]) settings.engine = element.value;
      if (element.dataset.key === 'records' && (element.value === 'all' || element.value === 'filtered')) settings.records = element.value;
      return true;
    }
    if (element.dataset.feature !== 'batch-map-quantile' || !state.comparison) return false;
    if (state.comparison.topMaps && state.comparison.topMaps.running) return true;
    state.comparison.topMapQuantile = element.value;
    if (state.comparison.topMaps) state.comparison.topMaps.message = '';
    return true;
  }
  function handleAction(state,element,api) {
    const action=element.dataset.featureAction;
    if(!action||!action.startsWith('batch-'))return false;
    if(action==='batch-energy-run')runEnergy(state,api);
    else if(action==='batch-maps-run')runMaps(state,api);
    else if(action.endsWith('-cancel')){stop(action.includes('energy')?'energy':'maps');api.render();}
    else if(action==='batch-energy-hyb'||action==='batch-energy-zip'){
      const result=energyState(state).result;if(!result){api.showToast('Calculate energies first.');return true;}
      const base='hyb2-energies-'+(result.engine||'rnacofold');
      if(action.endsWith('-hyb'))api.download(base+'.hyb',result.hyb,'text/plain');
      else api.downloadBlob(base+'.zip',root.Hyb2Results.zip([{path:'energies.hyb',text:result.hyb},{path:'audit.tsv',text:result.audit},{path:'parameters.json',text:JSON.stringify(result.metadata,null,2)}],base));
    }
    return true;
  }
  root.Hyb2Batch={contrastQuantile,handleInput,prepareEnergy,rewriteEnergy,topRegions,histogramQuantile,mapBatch,mapSvg,mapFiles,renderEnergy,renderMaps,handleAction,stopAll};
})(typeof window!=='undefined'?window:self);
