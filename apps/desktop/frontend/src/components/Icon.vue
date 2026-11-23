<script setup lang="ts">
defineProps<{ name: string }>();
const paths: Record<string, string> = {
  transfer: "M5 7h14m-4-4 4 4-4 4M19 17H5m4-4-4 4 4 4",
  device: "M4 4h16v12H4zM8 20h8m-4-4v4",
  activity: "M4 5h16M4 12h10M4 19h14",
  network: "M8 4h8v5H8zM3 16h6v5H3zM15 16h6v5h-6zM12 9v4M6 16v-3h12v3",
  shield: "M12 3 4 6v6c0 4 5 8 8 9 3-1 8-5 8-9V6zM8.5 11.5l2.5 2.5 4.5-5",
  file: "M5 3h9l5 5v13H5zM14 3v6h5M8 13h8M8 17h6",
  upload: "M12 16V3m-5 5 5-5 5 5M4 15v6h16v-6",
  download: "M12 3v13m-5-5 5 5 5-5M4 17v4h16v-4",
  sun: "M12 3V1m0 22v-2M3 12H1m22 0h-2M5.6 5.6 4.2 4.2m15.6 15.6-1.4-1.4M5.6 18.4l-1.4 1.4M19.8 4.2l-1.4 1.4M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0",
  moon: "M20.8 13A9 9 0 0 1 11 3.2 9 9 0 1 0 20.8 13",
  refresh: "M20 8a8 8 0 0 0-14-3L3 8m0-5v5h5M4 16a8 8 0 0 0 14 3l3-3m0 5v-5h-5",
  plus: "M12 4v16M4 12h16",
  arrow: "M4 12h16m-6-6 6 6-6 6",
  close: "M6 6l12 12M18 6 6 18",
  folder: "M3 6h7l2 2h9v12H3zM3 6V4h7l2 2",
};
</script>
<template>
  <svg class="icon" viewBox="0 0 24 24" aria-hidden="true">
    <path :d="paths[name] || paths.file" />
  </svg>
</template>
