<script setup lang="ts">
import {
  computed,
  nextTick,
  onMounted,
  onUnmounted,
  reactive,
  ref,
  watch,
} from "vue";
import {
  backend,
  type Device,
  type Task,
  type TaskEvent,
  type PreparationReport,
  type DeviceObservation,
} from "./bridge";
import UploadPanel from "./components/UploadPanel.vue";
import DiagnosticPanel from "./components/DiagnosticPanel.vue";
import Icon from "./components/Icon.vue";
import PairingWizard from "./components/PairingWizard.vue";
import {classifyDroppedFiles} from "./package-flow";
import {createHealthMonitor,type PairCompletion} from "./pairing-lifecycle";
const transferMode = ref("download");
const uploadPanel=ref<InstanceType<typeof UploadPanel>>();
const pairingOpen=ref(false),droppedPackage=ref("");
const rotatingDevice=ref<Device>();
const diagnosticDevice=ref(''),diagnosticSequence=ref(0),connectionFailure=ref('');
const packageDropSequence=ref(0);
let nativeDropRegistered=false;
const theme = ref(
  localStorage.getItem("hopsend-theme") === "dark" ? "dark" : "light",
);
function toggleTheme() {
  theme.value = theme.value === "light" ? "dark" : "light";
}
watch(
  theme,
  (value) => {
    document.documentElement.dataset.theme = value;
    localStorage.setItem("hopsend-theme", value);
  },
  { immediate: true },
);
const taskFilter = ref("all");
const history = ref<TaskEvent[]>([]);
const preparation = ref<PreparationReport>();
const preparing = ref("");
const operation = ref("");
const version = ref("");
const refreshedAt = ref("");
const refreshError = ref(false);
const clock = ref(Date.now());
const editor = ref<HTMLDialogElement>();
let previousOverflow = "";
const highlightedTask = ref("");
let highlightTimer: ReturnType<typeof setTimeout> | undefined;
const observations = reactive<
  Record<
    string,
    { loading: boolean; result?: DeviceObservation; error?: string }
  >
>({});
const observing = computed(() =>
  Object.values(observations).some((o) => o.loading),
);
const attentionStates = [
  "failed",
  "paused",
  "recoverable",
  "interrupted",
  "waiting_network",
];
const titles: Record<string, string> = {
  tasks: "传输任务",
  devices: "设备与信任",
  diagnostics: "网络诊断",
  logs: "活动记录",
};
const descriptions: Record<string, string> = {
  tasks: "让文件跨越设备，让每次传输都可靠。",
  devices: "管理已配对设备，连接前核对远端身份。",
  diagnostics: "从网络到文件，逐层定位连接问题。",
  logs: "查看任务状态变化，关闭后仍可追溯。",
};
const page = ref("tasks"),
  devices = ref<Device[]>([]),
  tasks = ref<Task[]>([]),
  selected = ref(""),
  manifest = ref(""),
  destination = ref(""),
  busy = ref(false),
  error = ref(""),
  notice = ref(""),
  showEditor = ref(false);
const empty = (): Device => ({
  id: "",
  name: "",
  agent_url: "",
  ssh_address: "",
  ssh_user: "",
  ca_file: "",
  token_file: "",
  key_file: "",
  known_hosts_file: "",
  share_id: "files",
  remote_agent_config: "",
  manage_agent: false,
});
const form = reactive(empty());
const entries = ref<{ time: string; text: string }[]>([]);
const attention = computed(
  () => tasks.value.filter((t) => attentionStates.includes(t.state)).length,
);
const breadcrumb = computed(() =>
  page.value === "tasks"
    ? `任务 / 活动 ${active.value.length} · 待处理 ${attention.value}`
    : `工作空间 / ${titles[page.value]}`,
);
const active = computed(() =>
  tasks.value.filter((t) =>
    [
      "connecting",
      "downloading",
      "uploading",
      "rechecking",
      "verifying",
      "publishing",
      "waiting_network",
    ].includes(t.state),
  ),
);
const visibleTasks = computed(() =>
  tasks.value.filter(
    (t) =>
      taskFilter.value === "all" ||
      (taskFilter.value === "active" &&
        active.value.some((a) => a.id === t.id)) ||
      (taskFilter.value === "completed" && t.state === "completed") ||
      (taskFilter.value === "attention" && attentionStates.includes(t.state)),
  ),
);
const totalSpeed = computed(() =>
  active.value.reduce((sum, t) => sum + (t.speed_bytes_per_second || 0), 0),
);
const eta = (n: number) =>
  !n
    ? "等待估算"
    : n < 60
      ? `${Math.ceil(n)} 秒`
      : n < 3600
        ? `${Math.ceil(n / 60)} 分钟`
        : `${(n / 3600).toFixed(1)} 小时`;
watch(page, (value) => {
  if (value === "devices") void observeDevices();
  if (value === "logs")
    void backend()
      .Activity()
      .then((v) => (history.value = v))
      .catch((e) => (error.value = String(e)));
});
watch(showEditor, async (open) => {
  await nextTick();
  if (open) {
    previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    editor.value?.showModal();
  } else {
    editor.value?.close();
    document.body.style.overflow = previousOverflow;
  }
});
const healthMonitor=createHealthMonitor(
  ()=>devices.value.map(d=>d.id),
  id=>backend().ObserveDevice(id),
  (id,state)=>{observations[id]=state.loading?{...observations[id],loading:true}:state;},
);
async function observeDevices(force = false) {
  await healthMonitor.refresh(force);
}
function invalidateObservations(){healthMonitor.invalidate();for(const id of Object.keys(observations))delete observations[id];}
function rotateTrust(device:Device){rotatingDevice.value=device;droppedPackage.value='';pairingOpen.value=true;page.value='devices';}
function togglePairing(){if(rotatingDevice.value){rotatingDevice.value=undefined;pairingOpen.value=true;}else pairingOpen.value=!pairingOpen.value;}
function diagnose(id:string){diagnosticDevice.value=id;diagnosticSequence.value++;page.value='diagnostics';}
async function paired(result:PairCompletion){
  invalidateObservations();devices.value=await backend().Devices();selected.value=result.device.id;pairingOpen.value=false;rotatingDevice.value=undefined;
  connectionFailure.value=result.connected?'':result.device.id;
  notice.value=result.message;page.value='devices';void observeDevices(true);
}
function observationLabel(id: string) {
  const o = observations[id];
  if (o?.loading) return "正在检查 HTTPS";
  if (!o?.result) return o?.error ? "检查失败" : "尚未检查";
  const stale = clock.value - Date.parse(o.result.observed_at) > 60_000;
  return o.result.reachable
    ? stale
      ? "上次 HTTPS 可达"
      : "HTTPS 可达"
    : stale
      ? "上次未响应"
      : "HTTPS 未响应";
}
function isRecentReachable(id: string) {
  const r = observations[id]?.result;
  return r?.reachable && clock.value - Date.parse(r.observed_at) <= 60_000;
}
function taskName(id: string) {
  return (
    tasks.value.find((t) => t.id === id)?.name || `任务 ${id.slice(0, 12)}`
  );
}
async function jumpToTask(id: string) {
  if (!tasks.value.some((t) => t.id === id)) return;
  page.value = "tasks";
  taskFilter.value = "all";
  highlightedTask.value = id;
  await nextTick();
  const target = document.getElementById(`task-${id}`);
  target?.scrollIntoView({
    behavior: matchMedia("(prefers-reduced-motion: reduce)").matches
      ? "instant"
      : "smooth",
    block: "center",
  });
  target?.focus({ preventScroll: true });
  if (highlightTimer) clearTimeout(highlightTimer);
  highlightTimer = setTimeout(() => (highlightedTask.value = ""), 3500);
}
const completed = computed(
  () => tasks.value.filter((t) => t.state === "completed").length,
);
const words: Record<string, string> = {
  connecting: "正在连接",
  pending: "准备中",
  downloading: "正在下载",
  uploading: "正在上传",
  rechecking: "核验已有分块",
  verifying: "全文件校验",
  publishing: "正在发布",
  completed: "已完成",
  paused: "已暂停",
  recoverable: "可继续",
  interrupted: "可继续",
  waiting_network: "等待网络",
  failed: "需要处理",
  cancelled: "已取消",
};
const word = (s: string) => words[s] || s;
const size = (n: number) => {
  if (!n) return "0 B";
  const u = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.max(0, Math.min(4, Math.floor(Math.log(n) / Math.log(1024))));
  return `${(n / 1024 ** i).toFixed(i ? 2 : 0)} ${u[i]}`;
};
const ratio = (t: Task) =>
  t.state === "completed"
    ? 100
    : t.total_bytes
      ? Math.min(100, (t.verified_bytes / t.total_bytes) * 100)
      : 0;
function log(text: string) {
  entries.value.unshift({ time: new Date().toLocaleTimeString("zh-CN"), text });
  entries.value = entries.value.slice(0, 50);
}
async function perform(action: () => Promise<void>, name = "action", failureDevice = '') {
  if (busy.value) return;
  error.value = "";
  busy.value = true;
  operation.value = name;
  try {
    await action();
  } catch (e) {
    if(failureDevice)connectionFailure.value=failureDevice;
    error.value = String(e);
    log(error.value);
    if (showEditor.value) {
      await nextTick();
      editor.value?.querySelector<HTMLElement>('[role="alert"]')?.focus();
    }
  } finally {
    busy.value = false;
    operation.value = "";
  }
}
async function refresh() {
  try {
    tasks.value = await backend().Tasks();
    refreshedAt.value = new Date().toLocaleTimeString("zh-CN");
    refreshError.value = false;
  } catch (e) {
    refreshError.value = true;
    throw e;
  }
}
async function load() {
  devices.value = await backend().Devices();
  if (!selected.value) selected.value = devices.value[0]?.id || "";
  await refresh();
  void observeDevices();
}
function edit(d?: Device) {
  error.value = "";
  Object.assign(form, empty(), d || {});
  showEditor.value = true;
  page.value = "devices";
}
function handleNativeDrop(paths:string[]) {
 const kind=classifyDroppedFiles(paths,page.value==='tasks'&&transferMode.value==='upload');
 if(kind==='package'){droppedPackage.value=paths[0]||'';packageDropSequence.value++;pairingOpen.value=true;page.value='devices';return;}
 if(kind==='multiple-packages'){error.value='一次只导入一个配对包，不与上传内容混放。';return;}
 if(kind==='upload'){uploadPanel.value?.selectDropped(paths);return;}
 notice.value='配对包可直接拖入；普通文件请在上传页添加。';
}
async function importPackage(){await perform(async()=>{const path=await backend().PickPairPackage();if(path){droppedPackage.value=path;packageDropSequence.value++;pairingOpen.value=true;page.value='devices';}},'import');}
async function importConfig() {
  await perform(async () => {
    const d = await backend().ImportDevice();
    if (d.name) {
      Object.assign(form, empty(), d);
      showEditor.value = true;
      page.value = "devices";
      notice.value = "配置已读入，请核对后保存。";
    }
  }, "import");
}
async function save() {
  await perform(async () => {
    const d = await backend().SaveDevice({ ...form });
    invalidateObservations();
    selected.value = d.id;
    showEditor.value = false;
    await load();
    notice.value = d.manage_agent
      ? "设备已保存，正在准备远端并执行网络验收……"
      : "配对信息已保存；启用自动准备后可在连接时恢复远端 Agent。";
    log(`保存设备：${d.name}`);
    if (d.manage_agent) {
      preparation.value = undefined;
      preparing.value = d.id;
      try {
        preparation.value = await backend().PrepareDevice(d.id);
        notice.value =
          "设备已就绪：环境、程序、HTTPS 认证与实际 SFTP 字节测试通过。";
        void observeDevices(true);
      } finally {
        preparing.value = "";
      }
    }
  }, "save");
}
const trustFields = [
  ["ca_file", "CA 证书公钥"],
  ["token_file", "API 凭据文件"],
  ["key_file", "SSH 私钥文件"],
  ["known_hosts_file", "已核对的主机记录"],
] as const;
async function pick(key: (typeof trustFields)[number][0], title: string) {
  await perform(async () => {
    const p = await backend().PickFile(title);
    if (p) form[key] = p;
  });
}
async function check(id: string) {
  connectionFailure.value='';
  await perform(async () => {
    const d = devices.value.find((d) => d.id === id);
    if (d?.manage_agent) {
      preparation.value = undefined;
      preparing.value = id;
      try {
        preparation.value = await backend().PrepareDevice(id);
        notice.value = "设备已就绪，传输前还会自动预检。";
        void observeDevices(true);
      } finally {
        preparing.value = "";
      }
      return;
    }
    const r = await backend().CheckDevice(id);
    notice.value = r.message;
    log(`${r.message} · ${r.observed_at}`);
    void observeDevices(true);
  }, `check:${id}`, id);
}
async function pickDestination() {
  await perform(async () => {
    const p = await backend().PickDestination("download.bin");
    if (p) destination.value = p;
  });
}
async function start() {
  await perform(async () => {
    if (!/^[a-f0-9]{64}$/.test(manifest.value.trim()))
      throw new Error("请填写源设备发布的 64 位清单 ID。");
    if (!destination.value) throw new Error("请选择目标文件。");
    const id = await backend().StartDownload(
      selected.value,
      manifest.value.trim(),
      destination.value,
    );
    log(`创建下载任务：${id.slice(0, 12)}`);
    await refresh();
  }, "download");
}
async function control(t: Task, action: "pause" | "resume" | "cancel") {
  await perform(async () => {
    if (action === "pause") await backend().PauseTask(t.id);
    else if (action === "resume") await backend().ResumeTask(t.id);
    else await backend().CancelTask(t.id);
    log(
      `${action === "pause" ? "暂停" : action === "resume" ? "继续" : "取消"}任务：${t.name}`,
    );
    await refresh();
  });
}
let timer: ReturnType<typeof setInterval> | undefined;
let healthTimer: ReturnType<typeof setInterval> | undefined;
onMounted(() => {
  if(window.runtime){window.runtime.OnFileDrop((_x,_y,paths)=>handleNativeDrop(paths),false);nativeDropRegistered=true;}
  void perform(load);
  void backend()
    .ApplicationInfo()
    .then((info) => (version.value = info.version))
    .catch((e) => log(`读取版本失败：${e}`));
  let ticks = 0;
  healthTimer=setInterval(()=>void observeDevices(true),30_000);
  timer = setInterval(() => {
    clock.value = Date.now();
    if (page.value === "logs" && ++ticks % 4 === 0)
      void backend()
        .Activity()
        .then((v) => (history.value = v))
        .catch((e) => (error.value = String(e)));
    void refresh().catch((e) => {
      error.value = String(e);
    });
  }, 750);
});
onUnmounted(() => {
  if(healthTimer)clearInterval(healthTimer);
  healthMonitor.stop();
  if(nativeDropRegistered)window.runtime?.OnFileDropOff();
  if (timer) clearInterval(timer);
  if (highlightTimer) clearTimeout(highlightTimer);
  if (showEditor.value) document.body.style.overflow = previousOverflow;
});
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">
        <img class="brand-icon" src="/hopsend.svg" alt="跃传图标" />
        <div>跃传 <small>HopSend</small></div>
      </div>
      <div class="side-label">工作空间</div>
      <button :class="{ chosen: page === 'tasks' }" @click="page = 'tasks'">
        <Icon name="transfer" />传输任务 <b>{{ tasks.length }}</b>
      </button>
      <button :class="{ chosen: page === 'devices' }" @click="page = 'devices'">
        <Icon name="device" />设备与信任 <b>{{ devices.length }}</b>
      </button>
      <button :class="{ chosen: page === 'logs' }" @click="page = 'logs'">
        <Icon name="activity" />活动记录
      </button>
      <button
        :class="{ chosen: page === 'diagnostics' }"
        @click="page = 'diagnostics'"
      >
        <Icon name="network" />网络诊断
      </button>
      <div class="side-bottom">
        <span class="dot"></span>任务保存在本机
        <p>跃传 HopSend</p>
        <small>{{ version || "正在读取版本…" }}</small>
      </div>
    </aside>
    <main>
      <header class="page-header">
        <div>
          <div class="eyebrow">{{ breadcrumb }}</div>
          <h1>{{ titles[page] }}</h1>
          <p>{{ descriptions[page] }}</p>
        </div>
        <div class="header-actions">
          <button
            class="icon-button"
            @click="toggleTheme"
            :aria-label="theme === 'light' ? '切换深色主题' : '切换浅色主题'"
            :title="theme === 'light' ? '切换深色主题' : '切换浅色主题'"
          >
            <Icon :name="theme === 'light' ? 'moon' : 'sun'" /></button
          ><button class="secondary" @click="importPackage" :disabled="busy">
            <span
              v-if="operation === 'import'"
              class="spinner"
              aria-hidden="true"
            ></span>
            <Icon v-else name="plus" />{{
              operation === "import" ? "正在导入…" : "导入回应 / 配对包"
            }}
          </button>
        </div>
      </header>
      <div v-if="error" class="message error" role="alert">
        <span>{{ error }}</span
        ><button
          class="text-button"
          aria-label="关闭错误提示"
          @click="error = ''"
        >
          <Icon name="close" />
        </button>
      </div>
      <div v-if="notice" class="message" role="status">
        <span>{{ notice }}</span
        ><button class="text-button" aria-label="关闭通知" @click="notice = ''">
          <Icon name="close" />
        </button>
      </div>
      <template v-if="page === 'tasks'">
        <div class="stats">
          <div>
            <label>活动任务</label
            ><strong>{{ active.length }} <span>项</span></strong>
          </div>
          <div>
            <label>已经完成</label
            ><strong>{{ completed }} <span>项</span></strong>
          </div>
          <div>
            <label>当前传输速度</label
            ><strong class="speed-stat"
              >{{ size(totalSpeed) }}<span>/ 秒</span></strong
            >
          </div>
          <button
            class="attention-stat"
            @click="taskFilter = 'attention'"
            :aria-label="`查看 ${attention} 项待处理任务`"
          >
            <label>待处理</label><strong>{{ attention }} <span>项</span></strong
            ><small>查看需要关注的任务 <Icon name="arrow" /></small>
          </button>
        </div>
        <div class="section-title">
          <div class="transfer-switch">
            <button
              :class="{ selected: transferMode === 'download' }"
              @click="transferMode = 'download'"
            >
              <Icon name="download" />从设备下载
            </button>
            <button
              :class="{ selected: transferMode === 'upload' }"
              @click="transferMode = 'upload'"
            >
              <Icon name="upload" />上传到设备
            </button>
          </div>
          <span>上传以接收端校验和发布结果为准</span>
        </div>
        <UploadPanel
          ref="uploadPanel"
          v-if="transferMode === 'upload'"
          :devices="devices"
          @started="
            (id) => {
              log('创建上传任务：' + id.slice(0, 12));
              void perform(refresh);
            }
          "
        />
        <section v-else class="panel create">
          <div class="section-title">
            <h2>新建下载</h2>
            <span>01 / 选择设备与文件版本</span>
          </div>
          <div class="create-grid">
            <label
              >源设备<select
                v-model="selected"
                aria-label="源设备"
                :disabled="busy"
              >
                <option disabled value="">请先添加设备</option>
                <option v-for="d in devices" :value="d.id" :key="d.id">
                  {{ d.name }}
                </option>
              </select></label
            ><label class="manifest"
              >清单 ID<input
                v-model="manifest"
                :disabled="busy"
                placeholder="粘贴源设备发布的 64 位清单 ID"
                aria-label="清单 ID"
                spellcheck="false"
            /></label>
          </div>
          <label
            >保存位置
            <div class="path-input">
              <input
                v-model="destination"
                :disabled="busy"
                placeholder="选择本机保存路径，已有文件不会被覆盖"
                aria-label="保存位置"
              /><button @click="pickDestination" :disabled="busy">
                选择文件位置
              </button>
            </div></label
          >
          <div class="create-footer">
            <span>依清单分块 · 每节点 2 个活动块 · SHA-256</span
            ><button
              class="primary"
              @click="start"
              :disabled="busy || !selected || !manifest || !destination"
            >
              <span
                v-if="operation === 'download'"
                class="spinner"
                aria-hidden="true"
              ></span
              >{{ operation === "download" ? "正在准备下载…" : "开始下载" }}
              <Icon v-if="operation !== 'download'" name="arrow" />
            </button>
          </div>
        </section>
        <div class="section-title list-title">
          <h2>
            全部任务 <span>{{ tasks.length }}</span>
          </h2>
          <button
            class="text-button"
            @click="perform(refresh)"
            :disabled="busy"
          >
            刷新状态
          </button>
        </div>
        <div v-if="!tasks.length" class="empty panel">
          <Icon name="download" />
          <h3>还没有传输任务</h3>
          <p>添加一台可信设备，填写清单 ID，开始第一次下载。</p>
          <button class="secondary" @click="edit()">添加设备</button>
        </div>
        <div v-if="tasks.length" class="filters" aria-label="任务筛选">
          <button
            v-for="f in [
              { id: 'all', name: '全部' },
              { id: 'active', name: '传输中' },
              { id: 'completed', name: '已完成' },
              { id: 'attention', name: '待处理' },
            ]"
            :key="f.id"
            :class="{ selected: taskFilter === f.id }"
            @click="taskFilter = f.id"
          >
            {{ f.name }}
          </button>
        </div>
        <div v-if="tasks.length && !visibleTasks.length" class="panel empty">
          <Icon name="file" />
          <h3>当前分类没有任务</h3>
          <p>切换分类可查看其他任务。</p>
        </div>
        <article
          v-for="t in visibleTasks"
          :key="t.id"
          :id="`task-${t.id}`"
          :class="[
            'task',
            'panel',
            t.state,
            { highlighted: highlightedTask === t.id },
          ]"
          tabindex="-1"
          :aria-label="t.name"
        >
          <div class="file-icon">
            <Icon :name="t.direction === 'upload' ? 'upload' : 'download'" />
          </div>
          <div class="task-body">
            <div class="task-heading">
              <h3 :title="t.name">{{ t.name }}</h3>
              <span class="muted task-direction">{{
                t.direction === "upload" ? "上传" : "下载"
              }}</span>
              <span :class="['badge', t.state]">{{ word(t.state) }}</span
              ><strong>{{ ratio(t).toFixed(1) }}%</strong>
            </div>
            <p class="destination" :title="t.destination">
              {{ t.destination }}
            </p>
            <div
              class="bar"
              role="progressbar"
              :aria-label="t.name + '已校验进度'"
              :aria-valuenow="Math.round(ratio(t))"
              :aria-valuemin="0"
              :aria-valuemax="100"
            >
              <i :style="{ width: ratio(t) + '%' }"></i>
            </div>
            <div class="task-meta">
              <span
                >{{ size(t.verified_bytes) }} / {{ size(t.total_bytes) }} ·
                {{ t.completed_chunks }} 块已确认</span
              ><span>版本 {{ t.manifest_id.slice(0, 12) }}</span>
            </div>
            <div v-if="t.metrics_available" class="task-metrics">
              <span
                >传输速度<b
                  >{{ size(t.speed_bytes_per_second || 0) }}/s</b
                ></span
              ><span
                >预计剩余<b>{{
                  t.state === "completed" ? "已完成" : eta(t.eta_seconds)
                }}</b></span
              ><span
                title="本次启动或继续以来，经 SFTP 实际传递的数据；含重传，不含协议开销。不会增加上方已校验进度。"
                >本次传输<b>{{ size(t.transferred_bytes || 0) }}</b></span
              >
            </div>
            <p v-if="t.error" class="task-error">{{ t.error }}</p>
          </div>
          <div class="task-actions">
            <button
              v-if="t.state === 'completed' && t.direction !== 'upload'"
              class="secondary"
              @click="perform(() => backend().RevealTask(t.id))"
              :disabled="busy"
            >
              <Icon name="folder" />打开文件夹
            </button>
            <button
              v-if="
                [
                  'connecting',
                  'downloading',
                  'uploading',
                  'rechecking',
                  'verifying',
                  'waiting_network',
                ].includes(t.state)
              "
              @click="control(t, 'pause')"
              :disabled="busy"
            >
              暂停</button
            ><button
              v-if="
                ['paused', 'recoverable', 'interrupted', 'failed'].includes(
                  t.state,
                )
              "
              class="primary"
              @click="control(t, 'resume')"
              :disabled="busy"
            >
              继续</button
            ><button
              v-if="
                [
                  'connecting',
                  'downloading',
                  'uploading',
                  'rechecking',
                  'waiting_network',
                ].includes(t.state)
              "
              class="text-button"
              @click="control(t, 'cancel')"
              :disabled="busy"
            >
              取消</button
            ><span v-if="t.state === 'completed'" class="verified"
              >✓ 完整校验通过</span
            >
          </div>
        </article>
      </template>
      <template v-if="page === 'devices'">
        <button class="secondary" @click="togglePairing">{{rotatingDevice?'返回首次配对':pairingOpen?'收起配对向导':'配对新设备'}}</button>
        <PairingWizard v-if="pairingOpen||!devices.length" :key="rotatingDevice?.id||'new'" class="panel" :devices="devices" :rotation-device="rotatingDevice" :dropped-path="droppedPackage" :drop-sequence="packageDropSequence" @legacy="importConfig" @paired="paired" />
        <div v-if="connectionFailure" class="message error" role="alert">设备资料已保留，连接验收未通过。<button class="text-button" @click="diagnose(connectionFailure)">诊断此设备</button></div>
        <div class="section-title">
          <h2>可信源设备</h2>
          <div class="header-actions">
            <button
              class="secondary"
              @click="observeDevices(true)"
              :disabled="observing"
            >
              <span v-if="observing" class="spinner" aria-hidden="true"></span
              >{{ observing ? "正在检查…" : "刷新连接状态" }}</button
            ><button class="primary" @click="edit()" :disabled="busy">
              ＋ 添加设备
            </button>
          </div>
        </div>
        <section v-if="preparing" class="panel preparation">
          <span class="spinner"></span>
          <h3>正在准备设备</h3>
          <p>
            自动检查环境、校验部署 Agent、启动并执行 HTTPS 与 SFTP
            实测。首次部署可能需要一两分钟。
          </p>
        </section>
        <section v-else-if="preparation" class="panel preparation">
          <div class="section-title">
            <h2>{{ preparation.ready ? "设备已就绪" : "设备准备未完成" }}</h2>
            <span>{{
              new Date(preparation.observed_at).toLocaleString("zh-CN")
            }}</span>
          </div>
          <div
            v-for="step in preparation.steps"
            :key="step.name"
            class="preparation-step"
          >
            <span :class="['badge', step.state]">{{
              step.state === "ok" ? "通过" : "失败"
            }}</span>
            <p>{{ step.detail }}</p>
          </div>
        </section>
        <div class="device-grid">
          <article v-for="d in devices" :key="d.id" class="panel device">
            <div class="device-top">
              <div class="device-icon"><Icon name="device" /></div>
              <span
                :class="[
                  'device-status',
                  { reachable: isRecentReachable(d.id) },
                ]"
                :title="
                  observations[d.id]?.result?.detail ||
                  observations[d.id]?.error
                "
                ><span
                  v-if="observations[d.id]?.loading"
                  class="spinner"
                  aria-hidden="true"
                ></span
                ><i v-else></i>{{ observationLabel(d.id) }}</span
              >
            </div>
            <h3>{{ d.name }}</h3>
            <p class="endpoint">
              <b>HTTPS</b><span>{{ d.agent_url }}</span>
            </p>
            <p class="endpoint">
              <b>SSH</b><span>{{ d.ssh_address }} · {{ d.ssh_user }}</span>
            </p>
            <p class="observation-time">
              {{
                observations[d.id]?.result
                  ? `观察于 ${new Date(observations[d.id]!.result!.observed_at).toLocaleTimeString("zh-CN")}`
                  : "只读健康检查，不启动远端程序"
              }}<span v-if="observations[d.id]?.result?.agent_version">
                · Agent {{ observations[d.id]!.result!.agent_version }}</span
              >
            </p>
            <small>{{
              d.manage_agent
                ? "已启用自动准备 · 连接时检查并恢复 Agent"
                : "仅保存配对信息 · 尚未启用自动准备"
            }}</small>
            <footer>
              <button class="secondary" @click="check(d.id)" :disabled="busy">
                <span
                  v-if="operation === `check:${d.id}`"
                  class="spinner"
                  aria-hidden="true"
                ></span>
                {{ d.manage_agent ? "准备并验收" : "检查连接" }}</button
              ><button class="text-button" @click="diagnose(d.id)">
                分层诊断</button
              ><button class="text-button" @click="edit(d)" :disabled="busy">
                编辑
              </button><button class="text-button" @click="rotateTrust(d)" :disabled="busy">
                轮换信任
              </button>
            </footer>
          </article>
        </div>
        <div v-if="!devices.length && !showEditor" class="empty panel">
          <h3>尚未保存设备</h3>
          <p>添加已完成配对的源设备，或导入仅含文件引用的配置。</p>
        </div>
        <dialog
          ref="editor"
          class="editor"
          aria-labelledby="editor-title"
          @close="showEditor = false"
          @cancel="busy && $event.preventDefault()"
        >
          <div class="section-title">
            <h2 id="editor-title">{{ form.id ? "编辑设备" : "添加设备" }}</h2>
            <button
              class="text-button"
              aria-label="关闭设备编辑"
              @click="showEditor = false"
              :disabled="busy"
            >
              <Icon name="close" />
            </button>
          </div>
          <div v-if="error" class="message error" role="alert" tabindex="-1">
            <span>{{ error }}</span>
          </div>
          <fieldset :disabled="busy">
            <div class="form-grid">
              <label
                >设备名称<input
                  v-model="form.name"
                  autofocus
                  aria-label="设备名称"
                  placeholder="例如：工作电脑" /></label
              ><label
                >共享 ID<input
                  v-model="form.share_id"
                  aria-label="共享 ID" /></label
              ><label
                >Agent HTTPS 地址<input
                  v-model="form.agent_url"
                  aria-label="Agent HTTPS 地址"
                  placeholder="https://设备地址:9443" /></label
              ><label
                >SSH 地址与端口<input
                  v-model="form.ssh_address"
                  aria-label="SSH 地址与端口"
                  placeholder="设备地址:22" /></label
              ><label
                >SSH 专用用户名<input
                  v-model="form.ssh_user"
                  aria-label="SSH 专用用户名"
              /></label>
            </div>
            <section class="preparation-options">
              <h3>连接前的设备准备</h3>
              <p>
                新设备先完成一次 SSH 与 HTTPS
                信任配对。已配对后，由本机自动部署、启动并测试远端 Agent。
              </p>
              <label
                >远端 Agent 配置路径<input
                  v-model="form.remote_agent_config"
                  aria-label="远端 Agent 配置路径"
                  placeholder="首次配对生成的远端配置绝对路径"
              /></label>
              <label class="checkbox-label"
                ><input
                  v-model="form.manage_agent"
                  type="checkbox"
                  aria-label="连接时自动准备远端 Agent"
                />连接时自动准备远端 Agent，并执行网络测试</label
              >
            </section>
            <div class="trust-note">
              证书、公钥和凭据都来自已核对的配对过程。选择现有文件，凭据内容保留在原文件中。
            </div>
            <label v-for="[key, title] in trustFields" :key="key"
              >{{ title }}
              <div class="path-input">
                <input v-model="form[key]" :aria-label="title" /><button
                  @click="pick(key, title)"
                  :disabled="busy"
                >
                  选择文件
                </button>
              </div></label
            >
          </fieldset>
          <footer>
            <button
              class="secondary"
              @click="showEditor = false"
              :disabled="busy"
            >
              返回</button
            ><button class="primary" @click="save" :disabled="busy">
              <span
                v-if="operation === 'save'"
                class="spinner"
                aria-hidden="true"
              ></span
              >{{ operation === "save" ? "正在保存…" : "保存设备" }}
            </button>
          </footer>
        </dialog>
      </template>
      <div v-show="page === 'diagnostics'">
        <DiagnosticPanel :devices="devices" :initial-device="diagnosticDevice" :selection-sequence="diagnosticSequence" />
      </div>
      <section v-if="page === 'logs'" class="panel logs">
        <div class="section-title">
          <h2>任务状态记录</h2>
          <span>本机持久保存 · 最近 200 条</span>
        </div>
        <p v-if="!history.length" class="muted">尚无已记录的任务状态变化。</p>
        <div v-for="(entry, i) in history" :key="'history' + i" class="log">
          <time>{{ new Date(entry.updated_at).toLocaleString("zh-CN") }}</time
          ><span :class="['badge', entry.state]">{{ word(entry.state) }}</span
          ><span
            ><small :title="entry.task_id">{{ taskName(entry.task_id) }}</small>
            <p v-if="entry.error" class="task-error">{{ entry.error }}</p></span
          >
          <button
            class="text-button log-jump"
            @click="jumpToTask(entry.task_id)"
            :disabled="!tasks.some((t) => t.id === entry.task_id)"
          >
            查看任务 <Icon name="arrow" />
          </button>
        </div>
        <details class="session-activity">
          <summary>
            本次操作 <span>当前会话 · {{ entries.length }} 条</span>
          </summary>
          <p v-if="!entries.length" class="muted">暂无活动记录。</p>
          <div v-for="(entry, i) in entries" :key="i" class="log">
            <time>{{ entry.time }}</time
            ><span>{{ entry.text }}</span>
          </div>
        </details>
      </section>
      <footer class="page-footer" :class="{ 'refresh-failed': refreshError }">
        {{ refreshError ? "任务刷新失败" : "任务状态最近刷新" }} ·
        {{ refreshedAt || "尚未完成" }} · 本机时间
      </footer>
    </main>
  </div>
</template>
