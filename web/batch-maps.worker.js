"use strict";
importScripts('./batch-tools.js'+self.location.search);
self.onmessage=function(event){try{const result=self.Hyb2Batch.mapBatch(event.data.samples,event.data.regions,(done,total)=>self.postMessage({type:'progress',done,total}),event.data.probability);self.postMessage({type:'result',result});}catch(error){self.postMessage({type:'error',message:error.message});}};
