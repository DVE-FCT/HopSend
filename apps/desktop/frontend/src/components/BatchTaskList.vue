<script setup lang="ts">
import {ref} from 'vue';
import {backend,type UploadBatch} from '../bridge';
import Icon from './Icon.vue';
import {activeBatchStates as active,batchMatchesFilter as visible} from '../batch-upload';
defineProps<{batches:UploadBatch[];filter:string}>();
const emit=defineEmits<{changed:[];diagnose:[id:string]}>();
const busy=ref(''),error=ref('');
const words:Record<string,string>={queued:'排队中',running:'调度中',connecting:'正在连接',uploading:'上传中',paused:'已暂停',failed:'失败',attention:'部分接收端需处理',completed:'已完成',cancelled:'已取消',rechecking:'重新核验',verifying:'完整性校验',publishing:'正在发布',waiting_network:'等待网络'};
const word=(s:string)=>words[s]||s;
const size=(n:number)=>n>=1073741824?`${(n/1073741824).toFixed(2)} GiB`:n>=1048576?`${(n/1048576).toFixed(1)} MiB`:`${n} B`;
const ratio=(bytes:number,total:number,state:string)=>state==='completed'?100:total?Math.min(100,100*bytes/total):0;
async function control(batch:string,id:string,action:string){if(busy.value)return;busy.value=id||batch;error.value='';try{if(!id)await backend().ControlUploadBatch(batch,action);else if(action==='pause')await backend().PauseTask(id);else if(action==='resume')await backend().ResumeTask(id);else await backend().CancelTask(id);emit('changed');}catch(e){error.value=String(e);}finally{busy.value='';}}
</script>
<template>
 <div v-if="error" class="message error" role="alert">{{error}}</div>
 <article v-for="b in batches.filter(b=>visible(b,filter))" :key="b.id" class="panel batch-card" :aria-label="'上传批次 '+b.name">
  <div class="batch-heading"><div><div class="section-title"><h3><Icon name="upload" />{{b.name}}</h3><span :class="['badge',b.state]">{{word(b.state)}}</span></div><p class="muted">{{b.recipients.filter(r=>r.state==='completed').length}} / {{b.recipients.length}} 台完成 · 每台 {{size(b.size_bytes)}} · 同一版本 {{b.manifest_id.slice(0,12)}}</p></div><div class="task-actions"><button :disabled="!!busy||!b.recipients.some(r=>active.includes(r.state))" @click="control(b.id,'','pause')">全部暂停</button><button :disabled="!!busy||!b.recipients.some(r=>['paused','failed'].includes(r.state))" @click="control(b.id,'','resume')" class="primary">全部继续</button><button :disabled="!!busy||b.recipients.every(r=>['completed','cancelled'].includes(r.state))" @click="control(b.id,'','cancel')">取消未完成</button></div></div>
  <section v-for="r in b.recipients" :key="r.id" :id="'task-'+r.id" class="batch-recipient" :aria-label="r.device_name+'接收进度'" tabindex="-1">
   <div class="batch-recipient-title"><h4>{{r.device_name}}</h4><span :class="['badge',r.state]">{{word(r.state)}}</span><strong>{{ratio(r.verified_bytes,r.total_bytes,r.state).toFixed(1)}}%</strong></div>
   <p class="destination">{{r.share_id}} / {{r.parent_path?r.parent_path+' / ':''}}{{r.target}}</p>
   <div class="bar" role="progressbar" :aria-label="r.device_name+'已校验进度'" :aria-valuenow="Math.round(ratio(r.verified_bytes,r.total_bytes,r.state))" :aria-valuemin="0" :aria-valuemax="100"><i :style="{width:ratio(r.verified_bytes,r.total_bytes,r.state)+'%'}"></i></div>
   <div class="task-meta"><span>{{size(r.verified_bytes)}} / {{size(r.total_bytes)}} · {{r.completed_chunks}} 块已确认<span v-if="r.metrics_available"> · {{size(r.speed_bytes_per_second||0)}}/s</span></span><div class="task-actions"><button v-if="active.includes(r.state)" :disabled="!!busy" @click="control(b.id,r.id,'pause')">暂停</button><button v-if="['paused','failed'].includes(r.state)" :disabled="!!busy" @click="control(b.id,r.id,'resume')">继续</button><button v-if="!['completed','cancelled'].includes(r.state)" :disabled="!!busy" @click="control(b.id,r.id,'cancel')">取消</button><button v-if="r.state==='failed'" :disabled="!!busy" @click="emit('diagnose',r.device_id)">诊断设备</button></div></div>
   <p v-if="r.error" class="task-error">{{r.error}}</p>
  </section>
  <p class="batch-footnote">{{b.snapshot_references?`此快照仍被 ${b.snapshot_references} 个接收任务引用，保留以供继续。`:'快照活动引用已释放；本机副本保留为历史清理候选。'}}</p>
 </article>
</template>
