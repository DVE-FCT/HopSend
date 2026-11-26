import {test} from 'node:test';
import assert from 'node:assert/strict';
import {newBatchDraft,createBatchDraft,mergeBatchTasks} from '../../apps/desktop/frontend/src/batch-upload.ts';
const target=id=>({device_id:id,share_id:'inbox-'+id,parent_path:'资料',target:''});
test('a duplicate click never starts a second batch; every target is captured separately',async()=>{
 const state=newBatchDraft();state.source='C:\\source';state.targets=[target('a'),target('b')];
 const calls=[],created=[];let finish;
 const flow=createBatchDraft(state,{StartBatchUpload:async(...args)=>{calls.push(args);return new Promise(r=>finish=r);}},b=>created.push(b),()=> 'key');
 const pending=flow.submit();await flow.submit();state.targets[0].parent_path='changed';finish({id:'batch'});await pending;await flow.submit();
 assert.equal(calls.length,1);assert.equal(calls[0][2][0].parent_path,'资料');assert.equal(calls[0][2][1].share_id,'inbox-b');assert.equal(created.length,1);
});
test('retry after a lost reply reuses the request key; explicit new batch rotates it',async()=>{
 const state=newBatchDraft();state.source='C:\\source';state.targets=[target('a')];const keys=[];let calls=0,key=0;
 const flow=createBatchDraft(state,{StartBatchUpload:async k=>{keys.push(k);if(calls++===0)throw Error('lost reply');return {id:k};}},()=>{},()=>String(++key));
 await flow.submit();assert.match(state.error,/lost reply/);await flow.submit();assert.deepEqual(keys,['1','1']);
 flow.reset();await flow.submit();assert.deepEqual(keys,['1','1','2']);
});
test('invalid, duplicate and excessive targets never prepare content',async()=>{
 for(const targets of [[],[target('a'),target('a')],Array.from({length:33},(_,i)=>target(String(i))),[{...target('a'),share_id:''}]]){
  const state=newBatchDraft();state.source='source';state.targets=targets;let calls=0;
  await createBatchDraft(state,{StartBatchUpload:async()=>{calls++;}},()=>{}).submit();assert.equal(calls,0);assert.ok(state.error);
 }
});
test('changed destination after a failed creation gets a new identity',async()=>{
 const state=newBatchDraft();state.source='source';state.targets=[target('a')];const keys=[];let key=0;
 const flow=createBatchDraft(state,{StartBatchUpload:async k=>{keys.push(k);throw Error('denied');}},()=>{},()=>String(++key));
 await flow.submit();state.targets[0].parent_path='other';await flow.submit();assert.deepEqual(keys,['1','2']);
});
test('queued recipients and failures before session creation count exactly once with journal tasks',()=>{
 const b={id:'b',name:'file',manifest_id:'m',recipients:[{id:'existing',state:'completed'},{id:'queued',state:'queued'},{id:'denied',state:'failed'}]};
 const tasks=mergeBatchTasks([{id:'existing',state:'uploading'},{id:'normal',state:'paused'}],[b]);
 assert.equal(tasks.length,4);assert.equal(tasks.filter(t=>t.id==='existing').length,1);assert.equal(tasks.find(t=>t.id==='existing').state,'completed');
 assert.equal(tasks.find(t=>t.id==='denied').batch_id,'b');assert.equal(tasks.find(t=>t.id==='queued').task_id,'queued');assert.equal(tasks.find(t=>t.id==='normal').state,'paused');
});
