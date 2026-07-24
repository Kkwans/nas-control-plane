<script setup lang="ts">
withDefaults(defineProps<{ size?: number }>(), { size: 40 })
</script>

<template>
  <svg
    class="ncp-logo"
    :width="size"
    :height="size"
    viewBox="0 0 64 64"
    role="img"
    aria-label="NAS Control Plane"
  >
    <defs>
      <linearGradient id="ncp-logo-surface" x1="8" y1="7" x2="56" y2="59" gradientUnits="userSpaceOnUse">
        <stop stop-color="#3A82F0" />
        <stop offset="1" stop-color="#1755BD" />
      </linearGradient>
      <filter id="ncp-logo-shadow" x="-20%" y="-20%" width="140%" height="150%">
        <feDropShadow dx="0" dy="5" stdDeviation="5" flood-color="#164CA8" flood-opacity=".28" />
      </filter>
    </defs>
    <rect x="4" y="4" width="56" height="56" rx="17" fill="url(#ncp-logo-surface)" filter="url(#ncp-logo-shadow)" />
    <path d="M19 45V19L45 45V19" fill="none" stroke="white" stroke-width="5" stroke-linecap="round" stroke-linejoin="round" />
    <circle cx="19" cy="19" r="4" fill="#A9D5FF" stroke="white" stroke-width="2" />
    <circle cx="45" cy="45" r="4" fill="#75F0D0" stroke="white" stroke-width="2" />
    <path d="M42 18h6M16 46h6" stroke="white" stroke-width="2" stroke-linecap="round" opacity=".7" />
  </svg>
</template>

<style scoped>
.ncp-logo {
  display: block;
  flex: 0 0 auto;
}
</style>
