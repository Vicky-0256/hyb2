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
    if (job) { job.cancelled = true; if (job.worker) job.worker.terminate(); if (job.reject) job.reject(new Error('Cancelled')); jobs.delete(kind); if(job.status) {job.status.running=false;job.status.message='Cancelled; no partial output exported.';} }
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
  function energyState(state) {
    if (!state.hybEnergy || state.hybEnergy.summary !== state.summary || state.hybEnergy.fasta !== state.fasta || state.hybEnergy.mapping !== JSON.stringify(state.fasta && state.fasta.mapping)) {
      stop('energy');state.hybEnergy={summary:state.summary,fasta:state.fasta,mapping:JSON.stringify(state.fasta && state.fasta.mapping),running:false,message:'',result:null};
    }
    return state.hybEnergy;
  }
  function renderEnergy(state) {
    const s=energyState(state);
    return '<section class="data-card"><div class="card-title-row"><div><h2>HYB binding energies</h2><p class="card-note">Recalculate column 3 for all records in the current dataset, using mapped FASTA arms and RNAcofold at 37 °C. Table filters do not apply. Download a new HYB; the loaded dataset stays unchanged.</p></div></div><div class="inline-actions"><button class="button" data-feature-action="batch-energy-run"'+(s.running||!state.fasta?' disabled':'')+'>Calculate all binding energies</button>'+(s.running?'<button class="quiet-button" data-feature-action="batch-energy-cancel">Cancel</button>':'')+(s.result?'<button class="button button-secondary" data-feature-action="batch-energy-hyb">Download updated HYB</button><button class="quiet-button" data-feature-action="batch-energy-zip">Download HYB + audit ZIP</button>':'')+'</div><p class="card-note" role="status">'+esc(s.message||(!state.fasta?'Load and map a reference FASTA first.':'Unmapped, antisense, ambiguous or over-limit rows retain their original energy and are listed in the audit.'))+'</p></section>';
  }
  async function runEnergy(state,api) {
    stop('energy');const status=energyState(state), job={status,keepAlive:true};jobs.set('energy',job);status.running=true;status.result=null;
    const file=state.file, fasta=state.fasta;
    try {
      if(!fasta)throw new Error('Load a mapped reference FASTA first.');
      progress(status,'Reading all HYB records…',api);
      const data=await api.queryHybrids({offset:0,limit:null,order:'file'});
      if(job.cancelled)return;
      const records=data.rows||[];if(!records.length)throw new Error('No HYB records available.');
      let source;
      if(file && typeof file.text==='function') {
        source=/\.gz$/i.test(file.name)?await new Response(file.stream().pipeThrough(new DecompressionStream('gzip'))).text():await file.text();
      } else {source=records.map(r=>r.raw).join('\n')+'\n';}
      const updates=new Map(), audit=['line\tid\tstatus\told_energy\tnew_energy\tnote'];let done=0,skipped=0,version='';
      for(let start=0;start<records.length;start+=64){
        if(job.cancelled)return;
        if(state.summary!==status.summary || state.fasta!==fasta || JSON.stringify(fasta.mapping)!==status.mapping)throw new Error('Dataset or reference mapping changed; calculate again.');
        const chunk=records.slice(start,start+64), prepared=[];
        chunk.forEach((record,i)=>{const line=file?record.lineNumber:start+i+1;const old=record.raw.split('\t')[2];try{prepared.push({record,line,old,arms:prepareEnergy(record,fasta)});}catch(error){skipped++;audit.push([line,record.id,'unchanged',old,old,error.message].map(tsv).join('\t'));}});
        if(prepared.length){const reply=await callWorker(job,'folding-energy.worker.js',{type:'cofold-batch',pairs:prepared.map(p=>p.arms),temperature:37,minimumLoop:3});version=reply.version;
          if(!reply.energies||reply.energies.length!==prepared.length)throw new Error('Incomplete energy worker result');
          prepared.forEach((item,i)=>{const energy=reply.energies[i];if(!Number.isFinite(energy))throw new Error('Invalid energy returned');updates.set(item.line,{raw:item.record.raw,energy});audit.push([item.line,item.record.id,'updated',item.old,energy.toFixed(2),''].map(tsv).join('\t'));done++;});}
        progress(status,(Math.min(start+64,records.length))+' / '+records.length+' records checked; '+done+' energies calculated; '+skipped+' unchanged.',api);await pause();
      }
      if(job.cancelled)return;
      if(state.summary!==status.summary || state.fasta!==fasta || JSON.stringify(fasta.mapping)!==status.mapping)throw new Error('Dataset or reference mapping changed; calculate again.');
      const hyb=rewriteEnergy(source,updates);
      status.result={hyb,audit:audit.join('\n')+'\n',metadata:{engine:'ViennaRNA RNAcofold',version,temperature:37,minimumLoop:3,updated:done,unchanged:skipped,validRecords:records.length,sourceFile:file&&file.name,reference:fasta.fileName,mapping:fasta.mapping,maximumCombinedArmLength:2000,completedAt:new Date().toISOString()}};
      status.message=done+' energies updated; '+skipped+' valid rows unchanged. Comments and unparsed source lines are preserved. Download the audit for details.';
    }catch(error){if(!job.cancelled)status.message='Energy calculation failed: '+error.message;}
    finally{if(job.worker)job.worker.terminate();if(jobs.get('energy')===job){jobs.delete('energy');status.running=false;api.render();}}
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
      if(action.endsWith('-hyb'))api.download('hyb2-energies.hyb',result.hyb,'text/plain');
      else api.downloadBlob('hyb2-energies.zip',root.Hyb2Results.zip([{path:'energies.hyb',text:result.hyb},{path:'audit.tsv',text:result.audit},{path:'parameters.json',text:JSON.stringify(result.metadata,null,2)}],'hyb2-energies'));
    }
    return true;
  }
  root.Hyb2Batch={contrastQuantile,handleInput,prepareEnergy,rewriteEnergy,topRegions,histogramQuantile,mapBatch,mapSvg,mapFiles,renderEnergy,renderMaps,handleAction,stopAll};
})(typeof window!=='undefined'?window:self);
