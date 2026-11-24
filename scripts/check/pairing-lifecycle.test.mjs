import {test} from 'node:test';
import assert from 'node:assert/strict';
import {verifySavedPair,createHealthMonitor} from '../../apps/desktop/frontend/src/pairing-lifecycle.ts';

test('saved pairs use managed preparation or unmanaged authentication, never both',async()=>{
 const calls=[];
 const api={PrepareDevice:async(id)=>{calls.push(`prepare:${id}`);return {ready:true};},CheckDevice:async(id)=>{calls.push(`check:${id}`);return {};}};
 assert.equal((await verifySavedPair({id:'managed',manage_agent:true},api)).connected,true);
 assert.equal((await verifySavedPair({id:'external',manage_agent:false},api)).connected,true);
 assert.deepEqual(calls,['prepare:managed','check:external']);
});

test('network failures preserve saved identity and never rerun the import',async()=>{
 const device={id:'saved-device',manage_agent:true};
 const result=await verifySavedPair(device,{PrepareDevice:async()=>{throw new Error('offline');},CheckDevice:async()=>{throw new Error('wrong branch');}});
 assert.equal(result.device,device);assert.equal(result.connected,false);
 assert.match(result.message,/资料已保存.*连接未通过.*offline/);
 assert.equal((await verifySavedPair(device,{PrepareDevice:async()=>({ready:false})})).connected,false);
});

test('health probes are bounded, coalesced, throttled after failure, and stop cleanly',async()=>{
 let time=0,active=0,peak=0,calls=0;const release=[];const states=[];
 const monitor=createHealthMonitor(()=>['a','b','c'],async(id)=>{
   calls++;peak=Math.max(peak,++active);await new Promise(resolve=>release.push(resolve));active--;
   if(id==='b')throw new Error('bridge failure');
   return {reachable:true,observed_at:new Date(time).toISOString(),detail:'health'};
 },(id,state)=>states.push({id,...state}),()=>time);
 const first=monitor.refresh();assert.equal(calls,2);
 await monitor.refresh(true);assert.equal(calls,2);
 release.shift()();await new Promise(resolve=>setImmediate(resolve));assert.equal(calls,3);
 while(release.length)release.shift()();await first;
 assert.equal(peak,2);assert.equal(states.find(s=>s.id==='b'&&!s.loading).error,'Error: bridge failure');
 time=29_999;await monitor.refresh();assert.equal(calls,3);
 time=30_000;const second=monitor.refresh();assert.equal(calls,5);
 monitor.stop();while(release.length)release.shift()();await second;
 await monitor.refresh(true);assert.equal(calls,5);
 assert.equal(states.filter(s=>s.loading).length,5);
 assert.equal(states.filter(s=>!s.loading).length,3);
});

test('a trust change discards old in-flight observations for the same device ID',async()=>{
 const states=[];let release;
 const monitor=createHealthMonitor(()=>['same-id'],async()=>{await new Promise(resolve=>release=resolve);return {reachable:true,detail:'old trust'};},(id,state)=>states.push(state));
 const old=monitor.refresh();monitor.invalidate();release();await old;
 assert.equal(states.filter(state=>!state.loading).length,0);
 const current=monitor.refresh();release();await current;
 assert.equal(states.filter(state=>!state.loading).length,1);
});
