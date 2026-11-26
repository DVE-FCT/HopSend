import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createPublishedSelection,newPublishedSelection} from '../../apps/desktop/frontend/src/published-selection.ts';
const version=(path='资料/a.txt',share='chosen')=>({share_id:share,path,name:path.split('/').at(-1),manifest_id:'a'.repeat(64),content_id:'b'.repeat(64),size_bytes:7,payload_kind:'file',published_at:'2026-11-26T12:00:00Z'});
const defaults=()=>({InspectPublishedPath:async()=>({review_id:'review',version:version()}),ReleasePublishedPath:async()=>{},PickDestination:async()=> 'C:\\Downloads\\a.txt',StartPublishedDownload:async()=> 'task'});

test('unpublished and malformed path versions never offer a download',async()=>{
 for(const result of [{error:{code:'path_unpublished',message:'未发布'}},{review_id:'r',version:version('other')},{review_id:'r',version:{...version(),manifest_id:'wrong'}}]){
  const state=newPublishedSelection();let calls=0;
  const flow=createPublishedSelection(state,{...defaults(),InspectPublishedPath:async()=>result,StartPublishedDownload:async()=>{calls++;}},()=>{});
  await flow.select('d','chosen','资料/a.txt');await flow.download();assert.equal(calls,0);assert.ok(state.error);assert.equal(state.version,undefined);
 }
});
test('late version results are discarded and released after navigation or trust change',async()=>{
 const state=newPublishedSelection();let resolve;const released=[];
 const flow=createPublishedSelection(state,{...defaults(),InspectPublishedPath:async()=>new Promise(r=>resolve=r),ReleasePublishedPath:async id=>released.push(id)},()=>{});
 const request=flow.select('d','chosen','资料/a.txt');flow.invalidate();resolve({review_id:'old',version:version()});await request;
 assert.equal(state.version,undefined);assert.deepEqual(released,['old']);
});
test('save cancellation and navigation during the native dialog never create tasks',async()=>{
 const state=newPublishedSelection();let resolve,calls=0;
 const api={...defaults(),PickDestination:async()=>'',StartPublishedDownload:async()=>{calls++;return 'task';}};
 const flow=createPublishedSelection(state,api,()=>{});await flow.select('d','chosen','资料/a.txt');await flow.download();assert.equal(calls,0);assert.equal(state.review,'review');
 api.PickDestination=async()=>new Promise(r=>resolve=r);
 const waiting=flow.download();flow.invalidate();resolve('C:\\a.txt');await waiting;assert.equal(calls,0);
});
test('download uses the reviewed ticket exactly once, independent of the current listing',async()=>{
 const state=newPublishedSelection(),started=[],created=[];let choose;
 const flow=createPublishedSelection(state,{...defaults(),PickDestination:async name=>{assert.equal(name,'a.txt');return new Promise(r=>choose=r);},StartPublishedDownload:async(...args)=>{started.push(args);return 'task';}},id=>created.push(id));
 await flow.select('d','chosen','资料/a.txt');const operation=flow.download();await flow.download();choose('C:\\a.txt');await operation;await flow.download();
 assert.deepEqual(started,[['review','C:\\a.txt']]);assert.deepEqual(created,['task']);assert.equal(state.version,undefined);
});
test('creation failures retain the selected version for explicit retry; disposed flows do not publish results',async()=>{
 const state=newPublishedSelection();let calls=0;
 const flow=createPublishedSelection(state,{...defaults(),StartPublishedDownload:async()=>{calls++;throw new Error('offline');}},()=>{});
 await flow.select('d','chosen','资料/a.txt');await flow.download();assert.equal(calls,1);assert.equal(state.review,'review');assert.equal(state.error.code,'download_failed');
 await flow.download();assert.equal(calls,2);flow.dispose();await flow.select('d','chosen','资料/a.txt');assert.equal(state.version,undefined);
});
