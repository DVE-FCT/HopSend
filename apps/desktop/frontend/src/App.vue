<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { backend, type Device, type Task, type TaskEvent } from "./bridge";
import UploadPanel from "./components/UploadPanel.vue";
import DiagnosticPanel from "./components/DiagnosticPanel.vue";
import Icon from "./components/Icon.vue";
const transferMode = ref("download");
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
});
const form = reactive(empty());
const entries = ref<{ time: string; text: string }[]>([]);
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
      (taskFilter.value === "attention" &&
        [
          "failed",
          "paused",
          "recoverable",
          "interrupted",
          "waiting_network",
        ].includes(t.state)),
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
  if (value === "logs")
    void backend()
      .Activity()
      .then((v) => (history.value = v))
      .catch((e) => (error.value = String(e)));
});
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
async function perform(action: () => Promise<void>) {
  error.value = "";
  busy.value = true;
  try {
    await action();
  } catch (e) {
    error.value = String(e);
    log(error.value);
  } finally {
    busy.value = false;
  }
}
async function refresh() {
  tasks.value = await backend().Tasks();
}
async function load() {
  devices.value = await backend().Devices();
  if (!selected.value) selected.value = devices.value[0]?.id || "";
  await refresh();
}
function edit(d?: Device) {
  Object.assign(form, d || empty());
  showEditor.value = true;
  page.value = "devices";
}
async function importConfig() {
  await perform(async () => {
    const d = await backend().ImportDevice();
    if (d.name) {
      Object.assign(form, d);
      showEditor.value = true;
      page.value = "devices";
      notice.value = "配置已读入，请核对后保存。";
    }
  });
}
async function save() {
  await perform(async () => {
    const d = await backend().SaveDevice({ ...form });
    selected.value = d.id;
    showEditor.value = false;
    await load();
    notice.value = "设备已保存。";
    log(`保存设备：${d.name}`);
  });
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
  await perform(async () => {
    const r = await backend().CheckDevice(id);
    notice.value = r.message;
    log(`${r.message} · ${r.observed_at}`);
  });
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
  });
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
onMounted(() => {
  void perform(load);
  let ticks = 0;
  timer = setInterval(() => {
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
  if (timer) clearInterval(timer);
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
        <p>可信设备之间的可靠传输</p>
        <small>跃传 HopSend · 可信设备传输</small>
      </div>
    </aside>
    <main>
      <header class="page-header">
        <div>
          <div class="eyebrow">HOPSEND / 工作空间</div>
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
          ><button class="secondary" @click="importConfig" :disabled="busy">
            <Icon name="plus" />导入配对配置
          </button>
        </div>
      </header>
      <div v-if="error" class="message error" role="alert">{{ error }}</div>
      <div v-else-if="notice" class="message" role="status">{{ notice }}</div>
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
          <div class="reliable">
            <Icon name="shield" />
            <section>
              每一块，完整抵达<small>完整性校验 · 中断可继续</small>
            </section>
          </div>
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
              >源设备<select v-model="selected" aria-label="源设备">
                <option disabled value="">请先添加设备</option>
                <option v-for="d in devices" :value="d.id" :key="d.id">
                  {{ d.name }}
                </option>
              </select></label
            ><label class="manifest"
              >清单 ID<input
                v-model="manifest"
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
              开始下载 <Icon name="arrow" />
            </button>
          </div>
        </section>
        <div class="section-title list-title">
          <h2>
            全部任务 <span>{{ tasks.length }}</span>
          </h2>
          <button class="text-button" @click="perform(refresh)">
            刷新状态
          </button>
        </div>
        <div v-if="!tasks.length" class="empty panel">
          <div>⇩</div>
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
        <article v-for="t in visibleTasks" :key="t.id" class="task panel">
          <div class="file-icon">
            <Icon :name="t.direction === 'upload' ? 'upload' : 'download'" />
          </div>
          <div class="task-body">
            <div class="task-heading">
              <h3>{{ t.name }}</h3>
              <span class="muted" style="font-size: 10px">{{
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
        <div class="section-title">
          <h2>可信源设备</h2>
          <button class="primary" @click="edit()">＋ 添加设备</button>
        </div>
        <div class="device-grid">
          <article v-for="d in devices" :key="d.id" class="panel device">
            <div class="device-icon"><Icon name="device" /></div>
            <h3>{{ d.name }}</h3>
            <p>{{ d.agent_url }}</p>
            <p>SSH {{ d.ssh_address }} · {{ d.ssh_user }}</p>
            <small>信任记录与 API 凭据分别验证</small>
            <footer>
              <button class="secondary" @click="check(d.id)" :disabled="busy">
                检查连接</button
              ><button class="text-button" @click="page = 'diagnostics'">
                分层诊断</button
              ><button class="text-button" @click="edit(d)">编辑</button>
            </footer>
          </article>
        </div>
        <div v-if="!devices.length && !showEditor" class="empty panel">
          <h3>尚未保存设备</h3>
          <p>添加已完成配对的源设备，或导入仅含文件引用的配置。</p>
        </div>
        <section v-if="showEditor" class="panel editor">
          <div class="section-title">
            <h2>{{ form.id ? "编辑设备" : "添加设备" }}</h2>
            <button class="text-button" @click="showEditor = false">
              收起
            </button>
          </div>
          <div class="form-grid">
            <label
              >设备名称<input
                v-model="form.name"
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
          <footer>
            <button class="secondary" @click="showEditor = false">返回</button
            ><button class="primary" @click="save" :disabled="busy">
              保存设备
            </button>
          </footer>
        </section>
      </template>
      <div v-show="page === 'diagnostics'">
        <DiagnosticPanel :devices="devices" />
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
            ><small>任务 {{ entry.task_id.slice(0, 12) }}</small>
            <p v-if="entry.error" class="task-error">{{ entry.error }}</p></span
          >
        </div>
        <div class="section-title list-title">
          <h2>本次操作</h2>
          <span>当前桌面会话</span>
        </div>
        <p v-if="!entries.length" class="muted">暂无活动记录。</p>
        <div v-for="(entry, i) in entries" :key="i" class="log">
          <time>{{ entry.time }}</time
          ><span>{{ entry.text }}</span>
        </div>
      </section>
      <footer class="page-footer">版本可信 · 分块续传 · 接收端确认</footer>
    </main>
  </div>
</template>
