<script setup lang="ts">
import {onMounted,ref,watch} from 'vue';
import {backend,type Device,type PairIdentity,type PairInspectResult} from '../bridge';
import {confirmReviewedReply} from '../package-flow';
import {verifySavedPair,type PairCompletion} from '../pairing-lifecycle';
const props=defineProps<{devices:Device[];droppedPath?:string;dropSequence?:number;rotationDevice?:Device}>();
const emit=defineEmits<{paired:[result:PairCompletion];legacy:[]}>();
const identity=ref<PairIdentity>();
const sourceIP=ref(''),identityWarning=ref(''),saved=ref(false),stage=ref('');
const name=ref(''),existing=ref(''),requestFile=ref(''),requestJSON=ref('');
const replyPath=ref(''),review=ref<PairInspectResult>();
const expectedSSH=ref(''),expectedCA=ref(''),confirmed=ref(false);
const busy=ref(false),checking=ref(false),error=ref(''),notice=ref('');
async function refreshIdentity(){checking.value=true;try{identity.value=await backend().PairLocalIdentity();sourceIP.value=identity.value.source_ip;name.value=identity.value.name;identityWarning.value='';}catch(e){identity.value=undefined;identityWarning.value=`${String(e)}。可手动输入；生成时会核对本机 Tailscale 网卡地址。`;}finally{checking.value=false;}}
onMounted(()=>void refreshIdentity());
watch(()=>props.rotationDevice,device=>{existing.value=device?.id||'';},{immediate:true});
watch(()=>props.dropSequence,()=>{if(props.droppedPath)void inspect(props.droppedPath);},{immediate:true});
async function create(){busy.value=true;error.value='';try{const result=existing.value?await backend().CreatePairRequest(existing.value):await backend().PairCreateRequest(sourceIP.value.trim(),name.value);if(result.request_file){requestFile.value=result.request_file;requestJSON.value='request_json' in result&&typeof result.request_json==='string'?result.request_json:'';notice.value='公开请求已生成，把这一个 JSON 交给源电脑。私钥保留在本机。';}}catch(e){error.value=String(e);}finally{busy.value=false;}}
async function choose(){try{const path=await backend().PickPairPackage();if(path)await inspect(path);}catch(e){error.value=String(e);}}
async function inspect(path:string){if(busy.value){error.value='请完成当前操作后再导入回应。';return;}busy.value=true;saved.value=false;error.value='';notice.value='';confirmed.value=false;expectedSSH.value='';expectedCA.value='';review.value=undefined;try{const result=await backend().PairInspectReply(path);if(props.rotationDevice&&result.device.request_id!==props.rotationDevice.id)throw new Error('回应不属于所选设备。请导出该设备原请求并让源电脑生成新回应。');review.value=result;replyPath.value=path;}catch(e){error.value=String(e);}finally{busy.value=false;}}
async function finish(){
 if(saved.value)return;
 busy.value=true;error.value='';stage.value='正在固定信任并保存…';
 try{
  await confirmReviewedReply(confirmed.value,review.value,expectedSSH.value,expectedCA.value,async(ssh,ca)=>{
   if(props.rotationDevice)await backend().RotateDeviceTrust(props.rotationDevice.id,replyPath.value,ssh,ca);
   else await backend().PairImportReply(replyPath.value,ssh,ca);
  });
  saved.value=true;stage.value='资料已保存，正在验收连接…';
  const devices=await backend().Devices();
  const device=devices.find(d=>d.id===review.value?.device.request_id);
  if(!device)throw new Error('连接资料已保存，但设备列表尚未读到记录，请刷新设备页。无需重复配对。');
  const result=await verifySavedPair(device,backend());
  if(props.rotationDevice)result.message=result.message.replace('配对资料已保存','信任资料已轮换，旧版本已保留');
  notice.value=result.message;emit('paired',result);
 }catch(e){error.value=String(e);}finally{busy.value=false;stage.value='';}
}
async function copyRequest(){try{await navigator.clipboard.writeText(requestFile.value);notice.value='公开请求路径已复制；将该文件交给源电脑即可。';}catch{notice.value='请选中公开请求路径复制。';}}
</script>

<template>
 <section class="pair-wizard" aria-label="桌面配对向导">
  <div class="wizard-heading"><div><span class="wizard-kicker">本机作为客户端</span><h2>{{rotationDevice?`轮换信任 · ${rotationDevice.name}`:'四步完成首次配对'}}</h2><p>{{rotationDevice?'用原请求获取新回应，重新核对身份后切换。设备 ID 和历史任务保持不变。':'生成公开请求，导入回应，核对身份，再保存设备。'}}</p></div><span class="wizard-tag">私钥不离开本机</span></div>
  <p v-if="rotationDevice" class="wizard-note">旧任务仍固定使用旧凭据；远端撤销旧授权后，这些任务可能需要重新授权或创建新任务。轮换不会自动迁移、重启或删除任务，也不会删除旧凭据。</p>
  <div class="wizard-grid">
   <article class="wizard-card"><div class="wizard-title"><b>1</b><h3>确认本机并生成请求</h3></div>
    <div class="wizard-local"><span>本机 Tailscale IPv4</span><strong>{{identity?.source_ip||'可手动填写下方地址'}}</strong><button @click="refreshIdentity" :disabled="checking||busy">{{checking?'读取中…':'刷新地址'}}</button></div>
    <label>本机地址<input v-model="sourceIP" :disabled="busy||checking" spellcheck="false" placeholder="100.x.x.x" aria-label="本机 Tailscale IPv4"></label>
    <p v-if="identityWarning" class="wizard-note" role="status">{{identityWarning}}</p>
    <label>设备显示名称<input v-model="name" :disabled="busy" aria-label="本机配对名称"></label>
    <label>请求用途<select v-model="existing" :disabled="busy||!!rotationDevice"><option value="">首次连接：创建新请求</option><option v-for="d in devices" :key="d.id" :value="d.id">重新导出原请求：{{d.name}}</option></select></label>
    <button class="secondary" @click="create" :disabled="busy||checking||!sourceIP.trim()||!name.trim()">{{existing?'导出原公开请求':'生成配对请求'}}</button>
    <template v-if="requestFile"><p class="wizard-path">{{requestFile}}</p><button class="text-button" @click="copyRequest">复制文件路径</button><details v-if="requestJSON"><summary>公开请求 JSON</summary><pre>{{requestJSON}}</pre></details></template>
    <p class="wizard-note">把生成的 JSON 发给源电脑。源电脑确认共享并授权后，会返回一个 ZIP 或 .hopsend-pair。</p>
   </article>
   <article class="wizard-card"><div class="wizard-title"><b>2</b><h3>导入源电脑的回应</h3></div><div class="wizard-drop" @dragover.prevent @drop.prevent><strong>拖入 .hopsend-pair</strong><span>或选择源电脑返回的 reply.zip</span><button @click="choose" :disabled="busy">选择回应 / 配对包</button></div><p class="wizard-note">导入前只读公开设备信息；没有核对指纹时不会保存凭据或设备。</p><button class="text-button" @click="emit('legacy')" :disabled="busy">兼容旧 JSON 连接配置</button></article>
  </div>
  <article v-if="review" class="wizard-review"><div class="wizard-title"><b>3</b><h3>通过可信渠道核对完整指纹</h3><span class="wizard-tag">资料已检查 · 身份待核对</span></div>
   <dl><div><dt>源设备</dt><dd>{{review.device.name}}</dd></div><div><dt>Agent 地址</dt><dd>{{review.device.agent_url}}</dd></div><div><dt>SSH 连接</dt><dd>{{review.device.ssh_user}} · {{review.device.ssh_address}}</dd></div><div><dt>共享</dt><dd>{{review.device.share_id}}</dd></div></dl>
   <div class="wizard-fingerprints"><div><label>包内 SSH 主机指纹（待核对）<code>{{review.device.host_fingerprint}}</code></label><label>从源电脑核对后填入 SSH 指纹<input v-model="expectedSSH" :disabled="busy" spellcheck="false" autocomplete="off" aria-label="可信 SSH 指纹" placeholder="SHA256:…"></label></div><div><label>包内 CA SHA-256（待核对）<code>{{review.device.ca_sha256}}</code></label><label>从源电脑核对后填入 CA 哈希<input v-model="expectedCA" :disabled="busy" spellcheck="false" autocomplete="off" aria-label="可信 CA 哈希" placeholder="64 位 SHA-256"></label></div></div>
   <p class="wizard-note">包内指纹和设备名不能自行证明身份。请在源电脑界面核对以上两项，不要只从此处复制后当作可信来源。</p>
   <label class="wizard-confirm"><input type="checkbox" v-model="confirmed" :disabled="busy">已通过可信渠道核对 SSH 主机指纹和 CA 哈希</label>
   <div class="wizard-finish"><span class="wizard-step-four">4 · {{rotationDevice?'切换信任并验收':'配对并验收'}}</span><button class="primary" @click="finish" :disabled="busy||saved||!confirmed||!expectedSSH.trim()||!expectedCA.trim()"><span v-if="busy" class="spinner"/>{{busy?stage:saved?'资料已保存':rotationDevice?'确认轮换并验收':'完成配对并验收'}}</button><span>托管设备将自动准备 Agent；其他设备仅验证 HTTPS 与 SSH 身份</span></div>
  </article>
  <p v-if="error" class="message error" role="alert">{{error}}</p><p v-if="notice" class="message success" role="status">{{notice}}</p>
 </section>
</template>

<style scoped>
.pair-wizard{padding:24px}.wizard-heading{display:flex;justify-content:space-between;gap:18px;margin-bottom:24px}.wizard-heading h2{margin:8px 0}.wizard-heading p,.wizard-note{font-size:13px;line-height:1.7;color:var(--muted)}.wizard-kicker{font-size:12px;color:var(--accent)}.wizard-tag{align-self:start;font-size:12px;color:var(--accent);background:var(--surface-soft);border:1px solid var(--line);border-radius:20px;padding:6px 12px;white-space:nowrap}.wizard-grid{display:grid;grid-template-columns:1fr 1fr;gap:20px}.wizard-card,.wizard-review{border:1px solid var(--line);border-radius:12px;padding:22px;background:var(--surface)}.wizard-review{margin-top:20px;background:var(--surface-soft)}.wizard-title{display:flex;gap:10px;align-items:center;margin-bottom:18px}.wizard-title b{display:grid;place-items:center;width:28px;height:28px;background:var(--surface-soft);color:var(--accent);border-radius:8px}.wizard-title h3{font-size:16px;margin:0}.wizard-title .wizard-tag{margin-left:auto}.pair-wizard label{display:block;margin:14px 0;font-size:13px}.pair-wizard label>input,.pair-wizard select{display:block;width:100%;margin-top:8px;box-sizing:border-box}.wizard-local{display:flex;flex-wrap:wrap;align-items:center;gap:12px;font-size:12px;color:var(--muted)}.wizard-local strong{font-variant-numeric:tabular-nums;color:var(--text)}.wizard-local button{margin-left:auto}.wizard-path,pre{overflow-wrap:anywhere;white-space:pre-wrap;padding:12px;background:var(--surface-soft);border-radius:8px;font-size:12px}.wizard-drop{--wails-drop-target:drop;display:flex;flex-direction:column;align-items:center;gap:16px;padding:40px 12px;margin-top:24px;border:1.5px dashed var(--line);border-radius:10px;background:var(--surface-soft)}.wizard-drop span{color:var(--muted);font-size:12px}.wizard-review dl{display:grid;grid-template-columns:1fr 1fr;gap:18px}.wizard-review dt{font-size:12px;color:var(--muted)}.wizard-review dd{margin:6px 0 0;font-size:14px;overflow-wrap:anywhere}.wizard-fingerprints{display:grid;grid-template-columns:1fr 1fr;gap:20px}.wizard-fingerprints code{display:block;overflow-wrap:anywhere;user-select:text;padding:12px;line-height:1.6;margin-top:8px;border:1px solid var(--line);border-radius:8px;background:var(--surface);font-size:12px}.wizard-confirm{display:flex!important;gap:9px;align-items:center}.wizard-confirm input{width:auto!important;margin:0!important}.wizard-finish{display:flex;align-items:center;gap:16px;flex-wrap:wrap}.wizard-finish>span{font-size:12px;color:var(--muted)}.wizard-step-four{font-weight:600}.pair-wizard details{font-size:12px;color:var(--muted);margin-top:12px}@media(max-width:1100px){.wizard-grid,.wizard-fingerprints{grid-template-columns:1fr}.wizard-heading{flex-wrap:wrap}}
</style>
