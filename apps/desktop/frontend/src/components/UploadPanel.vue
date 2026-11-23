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
  preparing = ref(false);
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
  <section class="panel create">
    <div class="section-title">
      <h2>新建上传</h2>
      <span>接收端确认分块与最终发布</span>
    </div>
    <div v-if="error" class="message error" role="alert">{{ error }}</div>
    <div class="create-grid">
      <label
        >接收设备<select v-model="device" aria-label="接收设备">
          <option disabled value="">请先添加设备</option>
          <option v-for="d in devices" :key="d.id" :value="d.id">
            {{ d.name }}
          </option>
        </select></label
      ><label
        >接收共享 ID<input
          v-model="share"
          aria-label="接收共享 ID"
          placeholder="需具备上传权限，例如 inbox"
      /></label>
    </div>
    <label
      >本机文件或文件夹
      <div class="path-input">
        <input
          v-model="source"
          aria-label="本机源文件"
          placeholder="选择要发送的文件或文件夹"
        /><button @click="choose" :disabled="busy">选择文件</button
        ><button @click="chooseFolder" :disabled="busy">选择文件夹</button>
      </div></label
    >
    <label style="margin-top: 14px"
      >接收文件名<input
        v-model="target"
        aria-label="接收文件名"
        placeholder="留空沿用源文件名；已有文件不会覆盖"
    /></label>
    <div class="create-footer">
      <span>{{
        preparing
          ? "正在生成不可变上传版本，请稍候……"
          : "写入结束后等待接收端校验，接收端发布后才完成。"
      }}</span
      ><button
        class="primary"
        @click="start"
        :disabled="busy || !device || !source || !share"
      >
        开始上传 <Icon name="arrow" />
      </button>
    </div>
  </section>
</template>
