<script setup lang="ts">
import { ref, watchEffect } from "vue";
import { backend, type Device } from "../bridge";
import Icon from "./Icon.vue";
const props = defineProps<{ devices: Device[] }>();
const emit = defineEmits<{ started: [id: string] }>();
const device = ref(""),
  share = ref("inbox"),
  source = ref(""),
  target = ref(""),
  busy = ref(false),
  error = ref(""),
  dropNotice = ref(""),
  preparing = ref(false);
function selectDropped(paths: string[]) {
  if (busy.value) { dropNotice.value = "正在处理上传，请完成后再选择其他内容。"; return; }
  if (paths.length !== 1) { error.value = "每次添加一个文件或文件夹；多个文件可先放入一个文件夹。"; return; }
  source.value = paths[0] || "";
  error.value = "";
  dropNotice.value = "已选择拖入的内容，核对接收设备后点击开始上传。";
}
defineExpose({selectDropped});
watchEffect(() => {
  if (!device.value) device.value = props.devices[0]?.id || "";
});
async function choose() {
  try {
    const path = await backend().PickFile("选择要上传的本机文件");
    if (path) source.value = path;
  } catch (e) {
    error.value = String(e);
  }
}
async function start() {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  preparing.value = true;
  try {
    const id = await backend().StartUpload(
      device.value,
      share.value,
      source.value,
      target.value,
    );
    emit("started", id);
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
    preparing.value = false;
  }
}
async function chooseFolder() {
  try {
    const path = await backend().PickFolder();
    if (path) source.value = path;
  } catch (e) {
    error.value = String(e);
  }
}
</script>
<template>
  <section class="panel create" :aria-busy="busy">
    <div class="section-title">
      <h2>新建上传</h2>
      <span>接收端确认分块与最终发布</span>
    </div>
    <div v-if="error" class="message error" role="alert">{{ error }}</div>
    <div v-if="dropNotice" class="message" role="status">{{ dropNotice }}</div>
    <div
      class="drop-zone"
      :class="{ 'drop-disabled': busy }"
      aria-label="拖拽上传区域"
      @dragover.prevent
      @drop.prevent
    >
      <Icon name="upload" />
      <div>
        <strong>拖入文件或文件夹</strong>
        <p>每次添加一项，确认后再开始上传；也可以使用下方选择按钮。</p>
      </div>
    </div>
    <div class="create-grid">
      <label
        >接收设备<select
          v-model="device"
          :disabled="busy"
          aria-label="接收设备"
        >
          <option disabled value="">请先添加设备</option>
          <option v-for="d in devices" :key="d.id" :value="d.id">
            {{ d.name }}
          </option>
        </select></label
      ><label
        >接收共享 ID<input
          v-model="share"
          :disabled="busy"
          aria-label="接收共享 ID"
          placeholder="需具备上传权限，例如 inbox"
      /></label>
    </div>
    <label
      >本机文件或文件夹
      <div class="path-input">
        <input
          v-model="source"
          :disabled="busy"
          aria-label="本机源文件"
          placeholder="选择要发送的文件或文件夹"
        /><button @click="choose" :disabled="busy">选择文件</button
        ><button @click="chooseFolder" :disabled="busy">选择文件夹</button>
      </div></label
    >
    <label class="target-field"
      >接收文件名<input
        v-model="target"
        :disabled="busy"
        aria-label="接收文件名"
        placeholder="留空沿用源文件名；已有文件不会覆盖"
    /></label>
    <div class="create-footer">
      <span>{{
        preparing
          ? "正在准备设备、测试连接并生成上传版本，请稍候……"
          : "写入结束后等待接收端校验，接收端发布后才完成。"
      }}</span
      ><button
        class="primary"
        @click="start"
        :disabled="busy || !device || !source || !share"
      >
        <span v-if="busy" class="spinner" aria-hidden="true"></span
        >{{ busy ? "正在准备上传…" : "开始上传" }}
        <Icon v-if="!busy" name="arrow" />
      </button>
    </div>
  </section>
</template>
