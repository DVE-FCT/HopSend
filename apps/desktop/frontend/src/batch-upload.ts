import type {BatchUploadTarget,UploadBatch,Task} from './bridge';

export const activeBatchStates=['queued','running','connecting','uploading','rechecking','verifying','publishing','waiting_network'];
export function batchMatchesFilter(b:UploadBatch,filter:string){
 return filter==='all'||(filter==='active'&&b.recipients.some(r=>activeBatchStates.includes(r.state)))||(filter==='completed'&&b.state==='completed')||(filter==='attention'&&b.recipients.some(r=>['failed','paused'].includes(r.state)));
}

// Queued / pre-session failures have no transfer-journal row yet. Include
// them in totals and log navigation without duplicating running journal tasks.
export function mergeBatchTasks(tasks:Task[],batches:UploadBatch[]):Task[]{
 const byID=new Map(tasks.map(t=>[t.id,t]));
 for(const b of batches)for(const r of b.recipients)byID.set(r.id,{...byID.get(r.id),...r,id:r.id,task_id:r.id,batch_id:b.id,direction:'upload',name:b.name,manifest_id:b.manifest_id});
 return [...byID.values()];
}

export function newBatchDraft(){return {source:'',targets:[] as BatchUploadTarget[],busy:false,error:'',submitted:false,key:'',signature:''};}
export type BatchDraft=ReturnType<typeof newBatchDraft>;
export function createBatchDraft(state:BatchDraft,api:{StartBatchUpload(key:string,source:string,targets:BatchUploadTarget[]):Promise<UploadBatch>},created:(batch:UploadBatch)=>void,newKey=()=>Array.from(crypto.getRandomValues(new Uint8Array(16)),n=>n.toString(16).padStart(2,'0')).join('')){
 return {
  async submit(){
   if(state.busy||state.submitted)return;
   state.error='';
   if(!state.source||state.targets.length<1||state.targets.length>32||state.targets.some(t=>!t.device_id||!t.share_id)||new Set(state.targets.map(t=>t.device_id)).size!==state.targets.length){state.error='请选择本机内容和 1～32 台不同的接收设备，并填写各自共享 ID。';return;}
   const source=state.source,targets=state.targets.map(t=>({...t})),signature=JSON.stringify([source,targets]);
   if(state.signature!==signature||!state.key){state.key=newKey();state.signature=signature;}
   state.busy=true;
   try{const batch=await api.StartBatchUpload(state.key,source,targets);state.submitted=true;created(batch);}
   catch(e){state.error=String(e);}
   finally{state.busy=false;}
  },
  reset(){if(state.busy)return;state.submitted=false;state.key='';state.signature='';state.error='';},
 };
}
