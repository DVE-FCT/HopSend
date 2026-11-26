import type {BrowseError,PathVersion,PathSelection} from './bridge';

export interface PublishedSelectionState {busy:boolean;starting:boolean;review:string;version?:PathVersion;error?:BrowseError;destination:string}
export const newPublishedSelection=():PublishedSelectionState=>({busy:false,starting:false,review:'',destination:''});
interface SelectionAPI {
 InspectPublishedPath(id:string,share:string,path:string):Promise<PathSelection>;
 ReleasePublishedPath(review:string):Promise<void>;
 PickDestination(name:string):Promise<string>;
 StartPublishedDownload(review:string,destination:string):Promise<string>;
}

export function createPublishedSelection(state:PublishedSelectionState,api:SelectionAPI,onCreated:(id:string)=>void){
 let epoch=0,disposed=false;
 const current=(n:number)=>!disposed&&n===epoch;
 const release=(review:string)=>{if(review)void api.ReleasePublishedPath(review).catch(()=>{});};
 function invalidate(){epoch++;release(state.review);Object.assign(state,newPublishedSelection(),{version:undefined,error:undefined});}
 return {
  invalidate,
  async select(device:string,share:string,path:string){
   if(disposed)return;
   invalidate();const turn=epoch;state.busy=true;
   try{
    const result=await api.InspectPublishedPath(device,share,path);
    if(!current(turn)){release(result.review_id);return;}
    if(result.error){state.error=result.error;return;}
    const v=result.version;
    if(!v||!result.review_id||v.share_id!==share||v.path!==path||v.name!==path.split('/').at(-1)||!Number.isSafeInteger(v.size_bytes)||v.size_bytes<0||!/^[a-f0-9]{64}$/.test(v.manifest_id)||!/^[a-f0-9]{64}$/.test(v.content_id)||!['file','folder'].includes(v.payload_kind)||!Number.isFinite(Date.parse(v.published_at))){release(result.review_id);throw new Error('已发布版本响应不一致，未选择下载。');}
    state.version=v;state.review=result.review_id;
   }catch(error){if(current(turn))state.error={code:'version_failed',message:String(error)};}
   finally{if(current(turn))state.busy=false;}
  },
  async download(){
   if(disposed||state.starting||!state.version||!state.review)return;
   const turn=epoch,review=state.review,name=state.version.name;state.starting=true;state.error=undefined;
   try{
    const destination=await api.PickDestination(name);
    if(!current(turn)||!destination)return;
    state.destination=destination;
    const id=await api.StartPublishedDownload(review,destination);
    onCreated(id);
    if(current(turn)){state.review='';state.version=undefined;}
   }catch(error){if(current(turn))state.error={code:'download_failed',message:String(error)};}
   finally{if(current(turn))state.starting=false;}
  },
  dispose(){invalidate();disposed=true;},
 };
}
