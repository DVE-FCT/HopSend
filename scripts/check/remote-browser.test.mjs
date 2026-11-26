import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createRemoteBrowser,newBrowserState,mergeRemotePage,childPath,parentPath} from '../../apps/desktop/frontend/src/remote-browser.ts';

const stamp='2026-11-26T12:00:00Z';
const entry=(name,directory=false)=>({name,directory,size_bytes:directory?0:1});
const page=(entries,{share='files',path='.',next=''}={})=>({share_id:share,path,entries,next_cursor:next,has_more:!!next});
const success=(p)=>({page:p,observed_at:stamp});
const shares=(items=[{id:'files',access:'read_only'}])=>({shares:items,observed_at:stamp});

test('explicit upload share never silently falls back to another readable share',async()=>{
 const state=newBrowserState();let calls=0;
 const browser=createRemoteBrowser(state,{BrowseDeviceShares:async()=>shares([{id:'write',access:'write_only'},{id:'rw',access:'read_write'}]),BrowseDeviceEntries:async()=>{calls++;return success(page([],{share:'rw'}));}});
 await browser.device('d','write',true);assert.equal(state.share,'write');assert.equal(calls,0);assert.equal(state.error.code,'browse_denied');
 await browser.device('d','unknown',true);assert.equal(calls,0);assert.equal(state.loaded,false);assert.equal(state.share,'');
 await browser.device('d','rw',true);assert.equal(calls,1);assert.equal(state.share,'rw');assert.equal(state.loaded,true);
});

test('1005 entries are traversed without duplicates and exact name order, including empty directories',async()=>{
 const state=newBrowserState();let calls=0;
 const browser=createRemoteBrowser(state,{
  BrowseDeviceShares:async()=>shares(),
  BrowseDeviceEntries:async(id,share,path,cursor,limit)=>{
   assert.equal(limit,100);calls++;
   if(path==='空目录')return success(page([],{path}));
   const offset=cursor?Number(cursor):0;
   return success(page(Array.from({length:Math.min(100,1005-offset)},(_,i)=>entry(`item-${String(offset+i).padStart(4,'0')}`)),{next:offset+100<1005?String(offset+100):''}));
  },
 });
 await browser.device('device','files');
 while(state.hasMore)await browser.next();
 assert.equal(calls,11);assert.equal(state.entries.length,1005);
 assert.equal(new Set(state.entries.map(e=>e.name)).size,1005);assert.equal(state.entries.at(-1).name,'item-1004');
 await browser.navigate('空目录');assert.equal(state.loaded,true);assert.deepEqual(state.entries,[]);assert.equal(state.hasMore,false);
});

test('directory change clears the whole traversal and explicit retry starts at page one',async()=>{
 const state=newBrowserState();const cursors=[];let changed=true;
 const browser=createRemoteBrowser(state,{BrowseDeviceShares:async()=>shares(),BrowseDeviceEntries:async(id,share,path,cursor)=>{
  cursors.push(cursor);return cursor&&changed?{observed_at:stamp,error:{code:'cursor_stale',message:'changed'}}:success(page([entry('a')],{next:'page2'}));
 }});
 await browser.device('d');await browser.next();
 assert.equal(state.error.code,'cursor_stale');assert.equal(state.loaded,false);assert.equal(state.hasMore,false);assert.deepEqual(state.entries,[]);
 changed=false;await browser.reload();assert.equal(cursors.at(-1),'');assert.equal(state.error,undefined);assert.equal(state.entries.length,1);
});

test('late device, directory and closed-view responses cannot publish results',async()=>{
 const state=newBrowserState();let oldDevice,oldPage;
 const browser=createRemoteBrowser(state,{BrowseDeviceShares:async(id)=>id==='old'?new Promise(resolve=>oldDevice=resolve):shares(),BrowseDeviceEntries:async(id,share,path)=>path==='slow'?new Promise(resolve=>oldPage=resolve):success(page([entry(id)],{path}))});
 const old=browser.device('old');await browser.device('current');oldDevice(shares());await old;
 assert.equal(state.device,'current');assert.equal(state.entries[0].name,'current');
 const slow=browser.navigate('slow');await browser.navigate('fast');oldPage(success(page([entry('stale')],{path:'slow'})));await slow;
 assert.equal(state.path,'fast');assert.equal(state.entries[0].name,'current');
 const closed=browser.navigate('slow');browser.invalidate();oldPage(success(page([entry('stale')],{path:'slow'})));await closed;
 assert.deepEqual(state.entries,[]);assert.equal(state.loaded,false);assert.equal(state.busy,false);
 browser.dispose();await browser.device('ignored');assert.equal(state.loaded,false);
});

test('write-only shares never issue entries calls',async()=>{
 const state=newBrowserState();let calls=0;
 const browser=createRemoteBrowser(state,{BrowseDeviceShares:async()=>shares([{id:'inbox',access:'write_only'}]),BrowseDeviceEntries:async()=>{calls++;throw new Error('forbidden');}});
 await browser.device('d');assert.equal(calls,0);assert.equal(state.error.code,'browse_denied');
 await browser.share('inbox');await browser.navigate('nested');assert.equal(calls,0);
});

test('share changes reject late results, including a refresh after credential configuration changes',async()=>{
 const state=newBrowserState();let release;
 const browser=createRemoteBrowser(state,{BrowseDeviceShares:async()=>shares([{id:'first',access:'read_only'},{id:'second',access:'read_write'}]),BrowseDeviceEntries:async(id,share)=>share==='first'?new Promise(resolve=>release=resolve):success(page([entry('second-only')],{share}))});
 const first=browser.device('same-device','first');
 await new Promise(resolve=>setImmediate(resolve));
 await browser.share('second');release(success(page([entry('old-first')],{share:'first'})));await first;
 assert.equal(state.share,'second');assert.equal(state.entries[0].name,'second-only');
 const old=browser.share('first');browser.invalidate();await browser.device('same-device','second');release(success(page([entry('old-credential')],{share:'first'})));await old;
 assert.equal(state.share,'second');assert.equal(state.entries[0].name,'second-only');
});

test('pagination validation refuses duplicate, wrong-scope and stalled pages',()=>{
 assert.throws(()=>mergeRemotePage([entry('b')],page([entry('b')]),'files','.','next'));
 assert.throws(()=>mergeRemotePage([],page([entry('a')],{share:'other'}),'files','.',''));
 assert.throws(()=>mergeRemotePage([],page([entry('a')],{path:'other'}),'files','.',''));
 assert.throws(()=>mergeRemotePage([],page([entry('a')],{next:'same'}),'files','.','same'));
 assert.throws(()=>mergeRemotePage([],page([],{next:'next'}),'files','.',''));
 for(const name of ['..','a/b','a\\b','C:drive'])assert.throws(()=>mergeRemotePage([],page([entry(name)]),'files','.',''));
 assert.equal(mergeRemotePage([entry('\uE000')],page([entry('😀')]),'files','.','next').length,2);
});

test('duplicate next clicks are coalesced; unsafe paths never reach backend',async()=>{
 const state=newBrowserState();let release,calls=0;
 const browser=createRemoteBrowser(state,{BrowseDeviceShares:async()=>shares(),BrowseDeviceEntries:async(id,share,path,cursor)=>{
  calls++;if(cursor)return new Promise(resolve=>release=resolve);return success(page([entry('a')],{next:'next'}));
 }});
 await browser.device('d');const next=browser.next();await browser.next();assert.equal(calls,2);
 release(success(page([entry('b')])));await next;assert.equal(state.entries.length,2);
 for(const path of ['../escape','/absolute','C:/outside','a//b','a\\b'])await browser.navigate(path);
 assert.equal(calls,2);assert.equal(state.error.code,'invalid_path');
 assert.equal(childPath('.','中文'),'中文');assert.equal(parentPath('中文/文件'), '中文');assert.equal(parentPath('中文'),'.');
});
