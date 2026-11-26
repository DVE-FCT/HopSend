<script setup lang="ts">
import {computed,reactive,ref} from 'vue';
import {backend,type Device} from '../bridge';
import {newBatchDraft,createBatchDraft} from '../batch-upload';
import RemoteBrowser from './RemoteBrowser.vue';
import Icon from './Icon.vue';
const props=defineProps<{devices:Device[]}>();
const emit=defineEmits<{started:[id:string];diagnose:[id:string]}>();
const draft=reactive(newBatchDraft()),browser=ref(''),notice=ref('');
const flow=createBatchDraft(draft,backend(),b=>{notice.value=`已创建批次，共 ${b.recipients.length} 台接收端。进度与控制见下方批次卡片。`;emit('started',b.id);});
const locked=computed(()=>draft.busy||draft.submitted);
const chosen=computed(()=>draft.targets.find(t=>t.device_id===browser.value));
const name=(id:string)=>props.devices.find(d=>d.id===id)?.name||'设备已移除';
function toggle(d:Device){if(locked.value)return;const index=draft.targets.findIndex(t=>t.device_id===d.id);if(index>=0){draft.targets.splice(index,1);if(browser.value===d.id)browser.value='';}else if(draft.targets.length<32){draft.targets.push({device_id:d.id,share_id:d.share_id||'inbox',parent_path:'',target:''});}}
async function choose(folder=false){try{const value=folder?await backend().PickFolder():await backend().PickFile('选择本批次发送的文件');if(value&&!locked.value)draft.source=value;}catch(e){draft.error=String(e);}}
function selectDropped(paths:string[]){if(locked.value){notice.value='当前批次已创建或正在准备，点击“新建另一批次”后再选择内容。';return;}if(paths.length!==1){draft.error='每批选择一个文件或文件夹，多个文件请放入一个文件夹。';return;}draft.source=paths[0]||'';draft.error='';}
defineExpose({selectDropped});
</script>
<template>
 <section class="panel create batch-create" :aria-busy="draft.busy">
  <div class="section-title"><h2>一发多收</h2><span>同一份快照 · 各设备独立确认</span></div>
  <p class="muted">一次选择本机内容，分别发送到已配对设备。单台失败不影响其他接收端；同名文件不会覆盖。</p>
  <div v-if="draft.error" class="message error" role="alert">{{draft.error}}</div>
  <div v-if="notice" class="message" role="status">{{notice}}</div>
  <label>本机文件或文件夹<div class="path-input"><input v-model="draft.source" :disabled="locked" aria-label="批次本机内容" placeholder="可拖入一个文件或文件夹"><button :disabled="locked" @click="choose()">选择文件</button><button :disabled="locked" @click="choose(true)">选择文件夹</button></div></label>
  <fieldset :disabled="locked" class="batch-device-picker"><legend>接收设备 · 已选择 {{draft.targets.length}} / 32</legend>
   <p v-if="!devices.length" class="muted">请先配对接收设备。</p>
   <label v-for="d in devices" :key="d.id" class="checkbox-label"><input type="checkbox" :checked="draft.targets.some(t=>t.device_id===d.id)" @change="toggle(d)" :aria-label="'批次接收设备 '+d.name">{{d.name}}<small>{{d.ssh_address}}</small></label>
  </fieldset>
  <section v-for="t in draft.targets" :key="t.device_id" class="batch-target" :aria-label="name(t.device_id)+'接收位置'">
   <div class="section-title"><h3><Icon name="device" />{{name(t.device_id)}}</h3><button :disabled="locked" class="text-button" @click="browser=browser===t.device_id?'':t.device_id">{{browser===t.device_id?'收起目录':'浏览接收目录'}}</button></div>
   <div class="form-grid"><label>共享 ID<input v-model="t.share_id" :disabled="locked" :aria-label="name(t.device_id)+'共享 ID'" @input="t.parent_path=''" /></label><label>共享内父目录<input v-model="t.parent_path" :disabled="locked" :aria-label="name(t.device_id)+'父目录'" placeholder="留空为共享根，目录需已存在" /></label><label>接收文件名<input v-model="t.target" :disabled="locked" :aria-label="name(t.device_id)+'文件名'" placeholder="留空沿用源文件名" /></label></div>
  </section>
  <RemoteBrowser v-if="chosen&&!locked" :key="chosen.device_id" :devices="devices.filter(d=>d.id===chosen?.device_id)" :device-id="chosen.device_id" :preferred-share="chosen.share_id" purpose="upload-directory" @share="value=>{if(chosen){chosen.share_id=value;chosen.parent_path='';}}" @directory="value=>{if(chosen){chosen.share_id=value.share;chosen.parent_path=value.path;}}" @diagnose="emit('diagnose',$event)" />
  <div class="create-footer"><span>{{draft.busy?'正在固定本机快照，完成后自动排队…':'每次最多准备 64 GiB 内容；最多 2 个接收任务同时传输。'}}<br><small>关闭应用后队列暂停，重新打开需手动继续。</small></span><button v-if="draft.submitted" @click="flow.reset();notice='';browser='';" class="secondary">新建另一批次</button><button v-else @click="flow.submit" :disabled="draft.busy||!draft.source||!draft.targets.length" class="primary"><span v-if="draft.busy" class="spinner"></span>{{draft.busy?'正在准备快照…':'创建批次并发送'}}</button></div>
 </section>
</template>
