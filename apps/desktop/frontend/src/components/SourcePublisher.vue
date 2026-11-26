<script setup lang="ts">
import {onMounted,onUnmounted,ref} from 'vue';
import {backend,type PublicationConfig,type PublicationJob} from '../bridge';
const open=ref(false),config=ref(''),share=ref(''),relative=ref(''),details=ref<PublicationConfig>(),job=ref<PublicationJob>(),error=ref(''),busy=ref(false);
const running=()=>job.value?.state==='publishing'||job.value?.state==='cancelling';
let timer:ReturnType<typeof setInterval>|undefined,alive=true,polling=false;
async function poll(){if(polling)return;polling=true;try{const v=await backend().PathPublicationStatus();if(alive)job.value=v;}catch(e){if(alive)error.value=String(e);}finally{polling=false;}}
async function read(){busy.value=true;error.value='';details.value=undefined;try{const v=await backend().ReadPublicationConfig(config.value);if(alive){details.value=v;share.value=v.shares[0]?.id||'';}}catch(e){if(alive)error.value=String(e);}finally{busy.value=false;}}
async function pick(){try{const p=await backend().PickFile('选择本机源端 Agent 配置 JSON');if(p&&alive){config.value=p;await read();}}catch(e){error.value=String(e);}}
async function start(){if(!details.value||busy.value||running())return;busy.value=true;error.value='';try{const v=await backend().StartPathPublication(config.value,details.value.revision,share.value,relative.value);if(alive)job.value=v;}catch(e){if(alive)error.value=String(e);}finally{busy.value=false;}}
async function choose(folder:boolean){if(!details.value)return;try{const p=await backend().PickPublicationSource(config.value,details.value.revision,share.value,folder);if(p&&alive)relative.value=p;}catch(e){error.value=String(e);}}
async function cancel(){if(!job.value?.id)return;try{await backend().CancelPathPublication(job.value.id);await poll();}catch(e){error.value=String(e);}}
onMounted(()=>{void poll();timer=setInterval(()=>{if(open.value||running())void poll();},1000);});
onUnmounted(()=>{alive=false;if(timer)clearInterval(timer);});
const statusNames:Record<string,string>={publishing:'正在生成快照并校验',cancelling:'正在取消并释放文件',completed:'发布完成',failed:'发布失败',cancelled:'已取消，旧版本保留'};
</script>
<template>
 <section class="panel source-publisher" aria-label="发布本机内容">
  <div class="publisher-heading"><div><h2>发布本机内容</h2><p>显式发布快照后，已授权设备才能从目录中选择版本下载。</p></div><button class="secondary" @click="open=!open" :aria-expanded="open">{{open?'收起发布':'准备发布'}}</button></div>
  <div v-if="open">
   <p class="hint">这是源设备本机操作。先完成源端准备并选择已有 Agent 配置；发布会复制内容并占用本机磁盘，不修改共享权限。文件更新后需要重新发布。</p>
   <label>本机 Agent 配置<div class="publisher-picker"><input v-model="config" placeholder="agent.local.json 或 agent.json" :disabled="busy||running()" @input="details=undefined"><button class="secondary" @click="pick" :disabled="busy||running()">选择配置</button><button class="secondary" @click="read" :disabled="!config||busy||running()">读取共享</button></div></label>
   <template v-if="details"><label>发布共享<select v-model="share" :disabled="busy||running()" @change="relative='' "><option v-for="s in details.shares" :key="s.id" :value="s.id">{{s.id}} · {{s.path}}</option></select></label><label>共享内文件或文件夹<div class="publisher-picker"><input v-model="relative" placeholder="例如：资料/报告.pdf 或 资料" :disabled="busy||running()"><button class="secondary" @click="choose(false)" :disabled="busy||running()">选择文件</button><button class="secondary" @click="choose(true)" :disabled="busy||running()">选择文件夹</button></div></label><p class="hint">相对路径使用 /，不能包含 .. 或链接。一次发布一个路径；大文件可能耗时较长，可取消且不会切换旧索引。</p><button @click="start" :disabled="!relative||!share||busy||running()">{{busy?'正在提交…':'发布此路径的快照'}}</button></template>
   <p v-if="error" class="message error" role="alert">{{error}}</p>
   <div v-if="job?.id" class="publisher-job" role="status"><strong>{{statusNames[job.state]||job.state}}</strong><p>{{job.path}}</p><button v-if="running()" class="secondary" @click="cancel" :disabled="job.state==='cancelling'">取消发布</button><p v-if="job.error" class="message error">{{job.error}}</p><template v-if="job.version"><p>版本 {{job.version.manifest_id}}</p><p>发布于 {{new Date(job.version.published_at).toLocaleString('zh-CN')}}；授权客户端重新查询该路径即可选择下载。</p></template></div>
  </div>
 </section>
</template>
<style scoped>
.source-publisher{padding:22px 25px;margin:20px 0}.publisher-heading{display:flex;align-items:center;justify-content:space-between;gap:18px}.publisher-heading h2{font-size:16px;margin:0}.publisher-heading p,.hint,.publisher-job p{color:var(--muted);font-size:12px;line-height:1.8;overflow-wrap:anywhere;margin:7px 0 12px}.source-publisher label{display:block;margin:14px 0}.publisher-picker{display:flex;gap:10px}.publisher-picker input{flex:1;min-width:0}.publisher-job{border:1px solid var(--line);border-radius:8px;padding:16px;margin-top:16px}.publisher-job strong{font-size:13px}.publisher-job p:last-child{margin-bottom:0}@media(max-width:1100px){.publisher-picker{flex-wrap:wrap}.publisher-picker input{flex-basis:100%}}
</style>
