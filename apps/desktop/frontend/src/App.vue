<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from "vue";
import { backend, type Device, type Task } from "./bridge";
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
      "rechecking",
      "verifying",
      "waiting_network",
    ].includes(t.state),
  ),
);
const completed = computed(
  () => tasks.value.filter((t) => t.state === "completed").length,
);
const words: Record<string, string> = {
  connecting: "正在连接",
  pending: "准备中",
  downloading: "正在下载",
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
  const i = Math.min(4, Math.floor(Math.log(n) / Math.log(1024)));
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
  timer = setInterval(() => {
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
        <span>⇩</span>传输任务 <b>{{ tasks.length }}</b>
      </button>
      <button :class="{ chosen: page === 'devices' }" @click="page = 'devices'">
        <span>▣</span>设备与信任 <b>{{ devices.length }}</b>
      </button>
      <button :class="{ chosen: page === 'logs' }" @click="page = 'logs'">
        <span>≡</span>活动记录
      </button>
      <div class="side-bottom">
        <span class="dot"></span>本地任务 · SQLite 持久化
        <p>可信设备之间的可靠传输</p>
        <small>控制信息 HTTPS · 文件数据 SFTP</small>
      </div>
    </aside>
    <main>
      <header>
        <div>
          <div class="eyebrow">
            HOPSEND /
            {{
              page === "tasks"
                ? "TRANSFER"
                : page === "devices"
                  ? "DEVICES"
                  : "ACTIVITY"
            }}
          </div>
          <h1>
            {{
              page === "tasks"
                ? "传输任务"
                : page === "devices"
                  ? "设备与信任"
                  : "活动记录"
            }}
          </h1>
          <p>
            {{
              page === "tasks"
                ? "从源设备获取可信版本，逐块验证，安全落盘。"
                : page === "devices"
                  ? "连接信息与信任文件分别保存，始终验证远端身份。"
                  : "当前桌面会话中的实际操作与结果。"
            }}
          </p>
        </div>
        <button class="secondary" @click="importConfig" :disabled="busy">
          导入配对配置
        </button>
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
            <label>已保存设备</label
            ><strong>{{ devices.length }} <span>台</span></strong>
          </div>
          <div class="reliable">
            <span>✓</span>
            <section>
              每一块都有校验<small>中断后重核验，继续原版本</small>
            </section>
          </div>
        </div>
        <section class="panel create">
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
              开始下载 <span>↗</span>
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
        <article v-for="t in tasks" :key="t.id" class="task panel">
          <div class="file-icon">▤</div>
          <div class="task-body">
            <div class="task-heading">
              <h3>{{ t.name }}</h3>
              <span :class="['badge', t.state]">{{ word(t.state) }}</span
              ><strong>{{ ratio(t).toFixed(1) }}%</strong>
            </div>
            <p class="destination" :title="t.destination">
              {{ t.destination }}
            </p>
            <div class="bar"><i :style="{ width: ratio(t) + '%' }"></i></div>
            <div class="task-meta">
              <span
                >{{ size(t.verified_bytes) }} / {{ size(t.total_bytes) }} ·
                {{ t.completed_chunks }} 块已确认</span
              ><span>版本 {{ t.manifest_id.slice(0, 12) }}</span>
            </div>
            <p v-if="t.error" class="task-error">{{ t.error }}</p>
          </div>
          <div class="task-actions">
            <button
              v-if="
                [
                  'connecting',
                  'downloading',
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
            <div class="device-icon">▣</div>
            <h3>{{ d.name }}</h3>
            <p>{{ d.agent_url }}</p>
            <p>SSH {{ d.ssh_address }} · {{ d.ssh_user }}</p>
            <small>信任记录与 API 凭据分别验证</small>
            <footer>
              <button class="secondary" @click="check(d.id)" :disabled="busy">
                检查连接</button
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
      <section v-if="page === 'logs'" class="panel logs">
        <div class="section-title">
          <h2>本次会话</h2>
          <span>仅展示实际操作</span>
        </div>
        <p v-if="!entries.length" class="muted">暂无活动记录。</p>
        <div v-for="(entry, i) in entries" :key="i" class="log">
          <time>{{ entry.time }}</time
          ><span>{{ entry.text }}</span>
        </div>
      </section>
      <footer class="page-footer">
        文件经 SFTP 传输 · HTTPS 提供清单与控制信息 · Tailscale 提供跨网络连接
      </footer>
    </main>
  </div>
</template>
