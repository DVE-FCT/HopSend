<script setup lang="ts">
import {computed,onUnmounted,reactive,ref,watch} from 'vue';
import {backend,type Device,type RemoteEntry} from '../bridge';
import {childPath,createRemoteBrowser,newBrowserState,parentPath} from '../remote-browser';
import {createPublishedSelection,newPublishedSelection} from '../published-selection';
import Icon from './Icon.vue';

const props=defineProps<{devices:Device[];deviceId:string;purpose?:'download'|'upload-directory';preferredShare?:string}>();
const emit=defineEmits<{device:[id:string];share:[id:string];directory:[value:{share:string;path:string}];diagnose:[id:string];created:[id:string]}>();
const uploading=computed(()=>props.purpose==='upload-directory');
const open=ref(false),selection=ref<RemoteEntry>(),preparing=ref(false),preparationError=ref('');
const state=reactive(newBrowserState());
const published=reactive(newPublishedSelection());
const versionSelection=createPublishedSelection(published,{
 InspectPublishedPath:(...args)=>backend().InspectPublishedPath(...args),
 ReleasePublishedPath:id=>backend().ReleasePublishedPath(id),
 PickDestination:name=>backend().PickDestination(name),
 StartPublishedDownload:(...args)=>backend().StartPublishedDownload(...args),
},id=>emit('created',id));
const browser=createRemoteBrowser(state,{
 BrowseDeviceShares:async id=>{const result=await backend().BrowseDeviceShares(id);return uploading.value?{...result,shares:result.shares.filter(s=>s.access!=='read_only')}:result;},
 BrowseDeviceEntries:(...args)=>backend().BrowseDeviceEntries(...args),
});
const currentDevice=computed(()=>props.devices.find(d=>d.id===props.deviceId));
const crumbs=computed(()=>state.path==='.'?[]:state.path.split('/').map((name,i,parts)=>({name,path:parts.slice(0,i+1).join('/')})));
const configuration=computed(()=>JSON.stringify([currentDevice.value||null,props.purpose,props.preferredShare]));
const accessLabels={read_only:'只读',read_write:'读写',write_only:'仅上传'};
const bytes=(n:number)=>n<1024?`${n} B`:n<1024**2?`${(n/1024).toFixed(1)} KiB`:n<1024**3?`${(n/1024**2).toFixed(1)} MiB`:`${(n/1024**3).toFixed(2)} GiB`;
let alive=true,prepareGeneration=0;
watch([open,configuration],()=>{
 selection.value=undefined;preparationError.value='';prepareGeneration++;preparing.value=false;
 versionSelection.invalidate();
 browser.invalidate();
 if(open.value&&props.deviceId)void reloadShares();
},{immediate:true});
function chooseDevice(event:Event){emit('device',(event.target as HTMLSelectElement).value);}
function resetSelection(){selection.value=undefined;versionSelection.invalidate();}
function reloadShares(){return browser.device(props.deviceId,uploading.value?props.preferredShare:currentDevice.value?.share_id,uploading.value);}
function chooseShare(event:Event){resetSelection();const id=(event.target as HTMLSelectElement).value;if(uploading.value){emit('share',id);return;}void browser.share(id);}
function navigate(path:string){resetSelection();void browser.navigate(path);}
function inspect(entry:RemoteEntry){selection.value=entry;void versionSelection.select(props.deviceId,state.share,childPath(state.path,entry.name));}
function choose(entry:RemoteEntry){if(entry.directory)navigate(childPath(state.path,entry.name));else if(!uploading.value)inspect(entry);}
function useDirectory(){if(state.loaded&&!state.error&&uploading.value){emit('directory',{share:state.share,path:state.path==='.'?'':state.path});open.value=false;}}
watch(()=>state.error,error=>{if(error)resetSelection();});
async function prepare(){
 const device=currentDevice.value;if(!device?.manage_agent||preparing.value)return;
 const epoch=++prepareGeneration;preparing.value=true;preparationError.value='';
 try{const result=await backend().PrepareDevice(device.id);if(!alive||epoch!==prepareGeneration)return;if(!result.ready)throw new Error('设备准备未通过');await reloadShares();}
 catch(error){if(alive&&epoch===prepareGeneration)preparationError.value=String(error);}
 finally{if(alive&&epoch===prepareGeneration)preparing.value=false;}
}
onUnmounted(()=>{alive=false;prepareGeneration++;browser.dispose();versionSelection.dispose();});
</script>

<template>
 <section class="panel remote-browser" :aria-label="uploading?'上传目标目录选择':'共享目录浏览'">
  <div class="browser-heading"><div><h2>{{uploading?'接收目录':'共享目录'}}</h2><p>{{uploading?'选择已存在的上传父目录，只写共享请手动输入路径。':'查看设备授权的文件与目录，浏览不发起传输。'}}</p></div><button class="secondary" :aria-expanded="open" aria-controls="remote-browser-content" @click="open=!open">{{open?'收起目录':uploading?'选择接收目录':'浏览共享目录'}}</button></div>
  <div v-if="open" id="remote-browser-content" :aria-busy="state.busy||preparing">
   <div class="browser-controls">
    <label>浏览设备<select :value="deviceId" @change="chooseDevice" :disabled="preparing" aria-label="浏览设备"><option value="" disabled>请先配对设备</option><option v-for="d in devices" :key="d.id" :value="d.id">{{d.name}}</option></select></label>
    <label>授权共享<select :value="state.share" @change="chooseShare" :disabled="!state.shares.length||preparing" aria-label="授权共享"><option v-if="!state.shares.length" value="">尚未读取共享</option><option v-else-if="!state.share" value="" disabled>请选择授权共享</option><option v-for="s in state.shares" :key="s.id" :value="s.id">{{s.id}} · {{accessLabels[s.access]}}</option></select></label>
    <button class="secondary" @click="resetSelection();reloadShares()" :disabled="!deviceId||state.busy||preparing"><Icon name="refresh"/>刷新共享</button>
   </div>
   <nav v-if="state.share&&(!uploading||state.loaded)" class="browser-path" aria-label="远端目录路径"><button @click="navigate('.')" :disabled="preparing">共享根目录</button><template v-for="c in crumbs" :key="c.path"><span>/</span><button @click="navigate(c.path)" :disabled="preparing">{{c.name}}</button></template><button v-if="state.path!=='.'" class="text-button" @click="navigate(parentPath(state.path))" :disabled="preparing">返回上级</button></nav>
   <div v-if="state.error" class="message error" role="alert"><span>{{state.error.message}}</span><p v-if="state.error.code==='cursor_stale'||state.error.code==='invalid_cursor'">目录已变化或游标失效，已清空旧页面，请从本目录第一页重读。</p><div class="browser-actions"><button v-if="state.share&&state.error.code!=='browse_unsupported'" @click="resetSelection();browser.reload()" :disabled="state.busy||preparing">重新读取目录</button><button v-if="currentDevice?.manage_agent" @click="prepare" :disabled="preparing||state.busy">{{preparing?'正在准备并验收…':'准备并验收设备'}}</button><button v-if="deviceId" @click="emit('diagnose',deviceId)">诊断此设备</button></div><p v-if="state.error.code==='browse_unsupported'">托管节点可尝试“准备并验收设备”；非托管节点请由所有者升级 Agent。</p></div>
   <p v-if="preparationError" class="message error" role="alert">{{preparationError}}</p>
   <p v-if="state.busy" class="browser-status" role="status"><span class="spinner"/>正在读取目录元数据…</p>
   <p v-if="!deviceId" class="browser-status">先在“设备与信任”配对一台设备，再展开读取共享。</p>
   <template v-if="state.loaded">
    <div class="browser-table-wrap"><table class="browser-table"><caption class="browser-sr">{{state.share}} / {{state.path}} 的目录项目</caption><thead><tr><th scope="col">名称</th><th scope="col">类型</th><th scope="col">大小</th><th v-if="!uploading" scope="col">版本</th></tr></thead><tbody><tr v-for="entry in state.entries" :key="entry.name" :class="{selected:selection?.name===entry.name}"><td><button class="browser-entry" @click="choose(entry)" :disabled="preparing||(uploading&&!entry.directory)" :aria-label="`${entry.directory?'打开目录':'查看文件'} ${entry.name}`"><Icon :name="entry.directory?'folder':'file'"/><span>{{entry.name}}</span></button></td><td>{{entry.directory?'目录':'文件'}}</td><td>{{entry.directory?'—':bytes(entry.size_bytes)}}</td><td v-if="!uploading"><button class="text-button" @click="inspect(entry)" :disabled="preparing" :aria-label="`查看已发布版本 ${entry.name}`">查看</button></td></tr></tbody></table></div>
    <p v-if="!state.entries.length" class="browser-status">此目录为空。</p>
    <footer class="browser-footer"><span>已加载 {{state.entries.length}} 项 · {{state.hasMore?'还有下一页':'当前目录读取结束'}}<small v-if="state.observedAt">读取于 {{new Date(state.observedAt).toLocaleTimeString('zh-CN')}}</small></span><button v-if="state.hasMore" class="secondary" @click="browser.next()" :disabled="state.busy||preparing">{{state.busy?'读取中…':'加载下一页'}}</button><button v-else class="text-button" @click="resetSelection();browser.reload()" :disabled="state.busy||preparing">重新读取目录</button></footer>
    <button v-if="uploading" class="directory-choice" @click="useDirectory" :disabled="state.busy||preparing">使用此目录：{{state.share}} / {{state.path==='.'?'共享根':state.path}}</button>
   </template>
   <aside v-if="!uploading&&selection&&!state.error" class="browser-selection" aria-label="已发布版本"><strong>{{selection.name}} · {{published.version?'已发布快照':'查询发布版本'}}</strong><p v-if="published.busy" role="status">正在查询已发布版本…</p><div v-if="published.error" class="message error" role="alert">{{published.error.message}}<p v-if="published.error.code==='path_unpublished'">源端尚未发布此文件或文件夹。请在源设备“设备与信任 → 发布本机内容”显式发布后重试；下方仍可使用已有清单 ID。</p><button class="secondary" @click="inspect(selection)">重新查询版本</button></div><template v-if="published.version"><p>{{published.version.path}} · {{published.version.payload_kind==='folder'?'文件夹快照':'文件快照'}} · {{bytes(published.version.size_bytes)}}</p><p>发布于 {{new Date(published.version.published_at).toLocaleString('zh-CN')}}<br>版本 {{published.version.manifest_id}}</p><p>下载固定此快照，可能与实时目录不同。选择本机保存位置后创建任务，不覆盖已有目标。</p><button @click="versionSelection.download()" :disabled="published.starting||!published.review">{{published.starting?'正在选择位置并创建任务…':'下载到…'}}</button></template><p v-if="published.destination">本机位置：{{published.destination}}</p></aside>
  </div>
 </section>
</template>

<style scoped>
.remote-browser{padding:22px 25px;margin-bottom:20px}.browser-heading{display:flex;gap:20px;align-items:center;justify-content:space-between}.browser-heading h2{margin:0;font-size:16px}.browser-heading p,.browser-status,.browser-selection p{font-size:12px;color:var(--muted);line-height:1.8;margin-top:6px}.browser-controls{display:grid;grid-template-columns:1fr 1fr auto;gap:16px;align-items:end;margin:18px 0}.browser-controls label{min-width:0}.browser-path{display:flex;gap:5px;align-items:center;flex-wrap:wrap;margin:12px 0}.browser-path button{padding:5px 9px;max-width:100%;overflow-wrap:anywhere;text-align:left;font-size:12px}.browser-path>span{color:var(--muted)}.browser-table-wrap{max-height:380px;overflow:auto;border:1px solid var(--line);border-radius:8px}.browser-table{width:100%;border-collapse:collapse;table-layout:fixed;font-size:12px}.browser-table th{position:sticky;top:0;z-index:1;background:var(--surface-soft);color:var(--muted);font-weight:500;text-align:left;padding:10px 12px}.browser-table th:first-child{width:68%}.browser-table th:nth-child(2){width:14%}.browser-table td{padding:6px 12px;border-top:1px solid var(--line);color:var(--text)}.browser-table tr.selected{background:var(--accent-soft)}.browser-entry{border:0;background:transparent;border-radius:5px;padding:7px 2px;justify-content:start;text-align:left;width:100%;font-size:13px;min-width:0;box-shadow:none}.browser-entry span{overflow-wrap:anywhere}.browser-entry .icon{flex-shrink:0;color:var(--accent)}.browser-entry:hover{background:var(--surface-soft);color:var(--accent)}.browser-footer{display:flex;gap:14px;align-items:center;justify-content:space-between;margin-top:12px;font-size:12px;color:var(--muted)}.browser-footer small{display:block;margin-top:5px}.browser-actions{display:flex;gap:10px;flex-wrap:wrap;margin-top:10px}.remote-browser .message{display:block;margin:12px 0}.remote-browser .message p{font-size:12px;line-height:1.8;margin-top:6px}.browser-status{display:flex;gap:9px;align-items:center;padding:12px 0}.browser-selection{margin-top:16px;padding:14px 16px;border:1px solid var(--line);border-radius:8px;background:var(--surface-soft);overflow-wrap:anywhere}.browser-selection strong{font-size:13px}.browser-sr{position:absolute;width:1px;height:1px;padding:0;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}@media(max-width:1100px){.browser-controls{grid-template-columns:1fr 1fr}.browser-controls>button{grid-column:1/-1;justify-self:start}.browser-table th:first-child{width:58%}.browser-table th:nth-child(2){width:16%}}
</style>
<style scoped>
.browser-table th:first-child{width:51%}.browser-table th:nth-child(2){width:14%}.browser-table th:nth-child(3){width:20%}.browser-table th:nth-child(4){width:15%}.browser-selection .message{padding:12px}.browser-selection button{margin-top:8px}
</style>
