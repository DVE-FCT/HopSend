<script setup lang="ts">
import { ref, watchEffect } from "vue";
import { backend, type Device, type NetworkReport } from "../bridge";
import Icon from "./Icon.vue";
const props = defineProps<{ devices: Device[] }>();
const selected = ref(""),
  manifest = ref(""),
  busy = ref(false),
  error = ref(""),
  report = ref<NetworkReport>();
const labels: Record<string, string> = {
  tailscale: "Tailscale 设备与路径",
  address: "设备地址",
  agent_address: "HTTPS 地址",
  https_port: "HTTPS 控制端口",
  ssh_port: "SSH 文件端口",
  https_identity: "HTTPS 身份与 API 授权",
  ssh_identity: "SSH 主机信任与公钥认证",
  real_data: "真实文件读取与 SHA-256",
};
const states: Record<string, string> = {
  ok: "通过",
  failed: "失败",
  unknown: "待确认",
  skipped: "未执行",
};
watchEffect(() => {
  if (!selected.value) selected.value = props.devices[0]?.id || "";
});
async function run() {
  if (manifest.value && !/^[a-f0-9]{64}$/.test(manifest.value.trim())) {
    error.value = "清单 ID 需要 64 位小写十六进制字符。";
    return;
  }
  busy.value = true;
  error.value = "";
  report.value = undefined;
  try {
    report.value = await backend().DiagnoseDevice(
      selected.value,
      manifest.value.trim(),
    );
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}
async function stop() {
  try {
    await backend().StopDiagnostic();
  } catch (e) {
    error.value = String(e);
  }
}
</script>
<template>
  <section class="panel diagnostic-form">
    <div class="section-title">
      <h2>逐层检查设备连接</h2>
      <span>独立 HTTPS 与 SFTP</span>
    </div>
    <div v-if="error" class="message error" role="alert">{{ error }}</div>
    <div class="form-grid">
      <label
        >目标设备<select
          v-model="selected"
          :disabled="busy"
          aria-label="诊断设备"
        >
          <option disabled value="">请先添加设备</option>
          <option v-for="d in devices" :key="d.id" :value="d.id">
            {{ d.name }}
          </option>
        </select></label
      >
      <label
        >清单 ID（选填）<input
          v-model="manifest"
          :disabled="busy"
          aria-label="诊断清单 ID"
          placeholder="填写后，额外读取第 0 块并核验哈希"
          spellcheck="false"
      /></label>
    </div>
    <div class="create-footer">
      <span
        >设备在线、端口可达、身份可信、文件可读分别检查。<br />填写清单会产生实际流量；大块经中继读取可能耗时数分钟。</span
      ><button v-if="busy" class="secondary" @click="stop">停止诊断</button
      ><button v-else class="primary" :disabled="!selected" @click="run">
        <Icon name="network" />开始诊断
      </button>
    </div>
  </section>
  <section v-if="busy" class="panel empty" role="status">
    <span class="spinner"></span>
    <h3>正在检查连接与信任</h3>
    <p>结果来自本次实际探测，请保持设备在线。</p>
  </section>
  <template v-else-if="report">
    <section class="panel diagnostic-summary">
      <Icon name="network" />
      <div>
        <h2>
          {{
            report.path === "direct"
              ? "观察到直连路径"
              : report.path === "relay"
                ? "观察到 DERP 中继路径"
                : "当前路径尚未确认"
          }}
        </h2>
        <p>
          观测时间 {{ new Date(report.observed_at).toLocaleString("zh-CN") }} ·
          路径依据本机 Tailscale 活动连接快照<br />{{
            report.transfer_verified
              ? "第 0 块真实读取与 SHA-256 一致。"
              : "文件字节尚未验证，具体原因见下方。"
          }}
        </p>
      </div>
    </section>
    <section class="panel">
      <article
        v-for="(layer, i) in report.layers"
        :key="layer.name"
        class="diagnostic-layer"
      >
        <span class="layer-number">{{ String(i + 1).padStart(2, "0") }}</span>
        <div>
          <h3>{{ labels[layer.name] || layer.name }}</h3>
          <p>{{ layer.detail }}</p>
        </div>
        <div class="layer-status">
          <small>{{ layer.milliseconds }} ms</small
          ><span :class="['badge', layer.state]">{{
            states[layer.state] || layer.state
          }}</span>
        </div>
      </article>
    </section>
  </template>
  <section v-else class="panel empty">
    <Icon name="network" />
    <h3>让连接问题有据可查</h3>
    <p>选择一台已配对设备，开始一次实际诊断。</p>
  </section>
</template>
