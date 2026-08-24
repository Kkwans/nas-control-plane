<script setup lang="ts">
import type { Component } from 'vue'
import { ElTooltip } from 'element-plus'

import type { SystemDetails } from '@/api/system'
import SectionHeader from '@/components/SectionHeader.vue'

type StorageMount = SystemDetails['storage']['mounts'][number]
type StorageDisk = SystemDetails['storage']['disks'][number]

defineProps<{
  details: SystemDetails
  volumeMounts: readonly StorageMount[]
  auxiliaryMounts: readonly StorageMount[]
  volumeTotalBytes: number
  volumeUsedBytes: number
  volumeUsedPercent: number
  physicalDisks: readonly StorageDisk[]
  auxiliaryDisks: readonly StorageDisk[]
  formatBytes: (value: number) => string
  blockDeviceDescription: (disk: StorageDisk) => string
  blockDeviceKindLabel: (disk: StorageDisk) => string
  blockDeviceTransport: (disk: StorageDisk) => string
  icons: {
    boxes: Component
    database: Component
    gauge: Component
    hardDrive: Component
  }
}>()
</script>

<template>
  <section class="storage-layout">
    <article class="storage-summary-grid storage-layout__full">
      <div class="storage-summary-card panel">
        <span class="storage-summary-card__icon"><component :is="icons.hardDrive" :size="18" /></span>
        <div>
          <small>可管理存储卷</small>
          <strong>{{ volumeMounts.length }}</strong>
          <p>{{ volumeMounts.map((item) => item.path).join('、') || '未发现 /volume 存储卷' }}</p>
        </div>
      </div>
      <div class="storage-summary-card panel">
        <span class="storage-summary-card__icon"><component :is="icons.gauge" :size="18" /></span>
        <div>
          <small>合计已用</small>
          <strong>{{ formatBytes(volumeUsedBytes) }}</strong>
          <p>总容量 {{ formatBytes(volumeTotalBytes) }}</p>
        </div>
      </div>
      <div class="storage-summary-card panel">
        <span class="storage-summary-card__icon"><component :is="icons.database" :size="18" /></span>
        <div>
          <small>使用率</small>
          <strong>{{ volumeTotalBytes ? `${volumeUsedPercent.toFixed(1)}%` : '—' }}</strong>
          <p>按 NAS 存储卷合计计算</p>
        </div>
      </div>
    </article>

    <article class="panel detail-card storage-layout__full">
      <SectionHeader class="detail-card__section-header" title="存储卷" description="仅展示系统根目录和 /volume 数据卷；系统镜像挂载点收进明细" :icon="icons.hardDrive" />
      <div class="volume-list">
        <div v-for="mount in volumeMounts" :key="mount.path" class="volume-row">
          <div class="volume-row__name">
            <strong>{{ mount.path === '/' ? '系统根目录' : mount.path }}</strong>
            <small>{{ mount.filesystem || '未知文件系统' }} · {{ mount.device || '未知设备' }}</small>
          </div>
          <div class="volume-row__usage">
            <div class="meter"><i :style="{ width: `${Math.min(100, mount.usedPercent)}%` }"></i></div>
            <strong>{{ mount.usedPercent.toFixed(1) }}%</strong>
            <small>{{ formatBytes(mount.usedBytes) }} / {{ formatBytes(mount.totalBytes) }}</small>
          </div>
        </div>
      </div>
      <details v-if="auxiliaryMounts.length" class="storage-details">
        <summary>查看系统挂载点 <span>{{ auxiliaryMounts.length }} 个系统 / 镜像挂载点</span></summary>
        <div class="storage-details__list">
          <div v-for="mount in auxiliaryMounts" :key="mount.path">
            <strong>{{ mount.path }}</strong>
            <span>{{ mount.filesystem || '未知' }}</span>
            <span>{{ mount.usedPercent.toFixed(1) }}%</span>
          </div>
        </div>
      </details>
      <div v-if="!volumeMounts.length" class="inline-empty">未发现可管理的存储卷。</div>
    </article>

    <article class="panel detail-card">
      <SectionHeader class="detail-card__section-header" title="物理磁盘" description="只展示可独立更换的数据盘；RAID、系统 eMMC 与内存设备不混入此处" :icon="icons.database" />
      <div v-if="physicalDisks.length" class="disk-list">
        <div v-for="disk in physicalDisks" :key="disk.name" class="disk-row">
          <span class="disk-row__icon"><component :is="icons.hardDrive" :size="16" /></span>
          <div>
            <strong>{{ disk.name }}</strong>
            <small>{{ disk.model || '型号未知' }} · {{ blockDeviceKindLabel(disk) }} · {{ blockDeviceTransport(disk) }}</small>
          </div>
          <span class="disk-row__size">{{ formatBytes(disk.sizeBytes) }}</span>
          <span :class="['disk-health', { unknown: !disk.health || disk.health === 'unknown' }]">{{ disk.health && disk.health !== 'unknown' ? disk.health : '健康状态未知' }}</span>
        </div>
      </div>
      <div v-else class="inline-empty">系统未暴露物理磁盘信息。</div>
      <details v-if="auxiliaryDisks.length" class="storage-details">
        <summary>查看系统与内存设备 <span>{{ auxiliaryDisks.length }} 个，均不是数据盘</span></summary>
        <div class="storage-details__list storage-details__list--devices">
          <div v-for="disk in auxiliaryDisks" :key="disk.name">
            <span class="auxiliary-device__identity">
              <strong>{{ disk.name }}</strong>
              <small>{{ blockDeviceKindLabel(disk) }} · {{ blockDeviceTransport(disk) }}</small>
            </span>
            <ElTooltip :content="blockDeviceDescription(disk)" placement="top" :show-after="350">
              <span class="auxiliary-device__description">{{ blockDeviceDescription(disk) }}</span>
            </ElTooltip>
            <span>{{ formatBytes(disk.sizeBytes) }}</span>
          </div>
        </div>
      </details>
    </article>

    <article class="panel detail-card">
      <SectionHeader class="detail-card__section-header" title="存储阵列" description="阵列级别、运行状态与成员设备；md1、md2 只在这里展示" :icon="icons.boxes" />
      <div v-if="details.storage.raid.length" class="compact-list">
        <div v-for="raid in details.storage.raid" :key="raid.name">
          <span><strong>{{ raid.name }}</strong><small>{{ raid.level || '级别未知' }}</small></span>
          <span :class="{ 'raid-state--active': raid.state === 'active' }">{{ raid.state || '状态未知' }}</span>
          <span>{{ raid.devices.join('、') || '成员未知' }}</span>
        </div>
      </div>
      <div v-else class="inline-empty">未发现可读取的软件 RAID 信息。</div>
    </article>
  </section>
</template>

<style scoped>
.storage-layout {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.storage-layout__full {
  grid-column: 1 / -1;
}

.detail-card {
  overflow: hidden;
}

.detail-card__section-header {
  padding: 17px 18px;
  border-bottom: 1px solid var(--ncp-line);
  background: linear-gradient(120deg, var(--ncp-surface), var(--ncp-surface-quiet));
}

.detail-card__section-header :deep(.section-header__icon) {
  border-color: color-mix(in srgb, var(--ncp-object-storage) 20%, var(--ncp-line));
  background: var(--ncp-object-storage-soft);
  color: var(--ncp-object-storage);
}

.storage-summary-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.storage-summary-card {
  display: grid;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: start;
  gap: 10px;
  padding: 15px;
}

.storage-summary-card__icon {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 10px;
  background: var(--ncp-object-storage-soft);
  color: var(--ncp-object-storage);
}

.storage-summary-card > div {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.storage-summary-card small {
  color: var(--ncp-text-subtle);
  font-size: .7rem;
}

.storage-summary-card strong {
  overflow: hidden;
  color: var(--ncp-text);
  font-family: var(--ncp-font-latin);
  font-size: .94rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.storage-summary-card p {
  overflow: hidden;
  margin: 0;
  color: var(--ncp-text-muted);
  font-size: .72rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.volume-list,
.disk-list {
  display: grid;
  padding: 7px 18px;
}

.volume-row {
  display: grid;
  grid-template-columns: minmax(180px, .8fr) minmax(280px, 1.2fr);
  align-items: center;
  gap: 24px;
  min-height: 76px;
  border-bottom: 1px solid var(--ncp-line);
}

.volume-row:last-child,
.disk-row:last-child {
  border-bottom: 0;
}

.volume-row__name,
.disk-row > div {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.volume-row__name strong,
.disk-row strong {
  font-family: var(--ncp-font-mono);
  font-size: .85rem;
}

.volume-row__name small,
.volume-row__usage small,
.disk-row small {
  overflow: hidden;
  color: var(--ncp-text-subtle);
  font-size: .7rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.volume-row__usage {
  display: grid;
  grid-template-columns: minmax(90px, 1fr) auto;
  align-items: center;
  gap: 7px;
}

.volume-row__usage .meter {
  grid-column: 1 / -1;
}

.volume-row__usage strong {
  font-family: var(--ncp-font-latin);
  font-size: .78rem;
}

.volume-row__usage small {
  text-align: right;
}

.meter {
  overflow: hidden;
  height: 7px;
  border-radius: 999px;
  background: var(--ncp-surface-sunken);
}

.meter i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--ncp-object-storage);
}

.storage-details {
  border-top: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
}

.storage-details summary {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 18px;
  cursor: pointer;
  color: var(--ncp-primary-strong);
  font-size: .78rem;
  font-weight: 750;
  list-style: none;
}

.storage-details summary::-webkit-details-marker {
  display: none;
}

.storage-details summary::before {
  display: grid;
  width: 22px;
  height: 22px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--ncp-primary-border);
  border-radius: 6px;
  background: var(--ncp-primary-soft);
  content: '+';
  font-size: 1rem;
  line-height: 1;
}

.storage-details[open] summary::before {
  content: '−';
}

.storage-details summary span {
  margin-left: auto;
  color: var(--ncp-text-subtle);
  font-size: .7rem;
  font-weight: 600;
}

.storage-details__list {
  display: grid;
  border-top: 1px solid var(--ncp-line);
}

.storage-details__list > div {
  display: grid;
  grid-template-columns: 1.3fr 1fr 90px;
  align-items: center;
  gap: 12px;
  min-height: 46px;
  padding: 7px 18px;
  border-bottom: 1px solid var(--ncp-line);
  font-size: .75rem;
}

.storage-details__list > div:last-child {
  border-bottom: 0;
}

.storage-details__list span {
  color: var(--ncp-text-subtle);
}

.disk-row {
  display: grid;
  grid-template-columns: 30px minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 10px;
  min-height: 62px;
  border-bottom: 1px solid var(--ncp-line);
}

.disk-row__icon {
  display: grid;
  width: 28px;
  height: 28px;
  place-items: center;
  border-radius: 8px;
  background: var(--ncp-object-storage-soft);
  color: var(--ncp-object-storage);
}

.disk-row strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.disk-row__size {
  color: var(--ncp-text-muted);
  font-family: var(--ncp-font-latin);
  font-size: .75rem;
}

.disk-health {
  color: var(--ncp-success-strong);
  font-size: .72rem;
  font-weight: 700;
}

.disk-health.unknown {
  color: var(--ncp-text-subtle);
  font-weight: 600;
}

.auxiliary-device__identity {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.auxiliary-device__identity strong,
.auxiliary-device__identity small,
.auxiliary-device__description {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.auxiliary-device__identity small {
  color: var(--ncp-text-subtle);
  font-size: .67rem;
}

.compact-list {
  display: grid;
  padding: 6px 18px 14px;
}

.compact-list > div {
  display: grid;
  grid-template-columns: 1.2fr .8fr 1fr;
  align-items: center;
  gap: 12px;
  min-height: 58px;
  border-bottom: 1px solid var(--ncp-line);
}

.compact-list > div:last-child {
  border-bottom: 0;
}

.compact-list > div > span:first-child {
  display: grid;
  gap: 2px;
}

.compact-list small {
  color: var(--ncp-text-subtle);
}

.raid-state--active {
  color: var(--ncp-success-strong);
  font-weight: 700;
}

.inline-empty {
  padding: 28px 18px;
  color: var(--ncp-text-subtle);
  text-align: center;
}

@media (max-width: 1050px) {
  .storage-layout {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 760px) {
  .storage-summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .storage-details__list--devices > div {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .auxiliary-device__description {
    grid-column: 1 / -1;
  }
}

@media (max-width: 520px) {
  .storage-summary-grid {
    grid-template-columns: 1fr;
  }

  .volume-row {
    grid-template-columns: 1fr;
    gap: 10px;
    padding-block: 13px;
  }

  .disk-row {
    grid-template-columns: 30px minmax(0, 1fr);
    padding-block: 10px;
  }

  .disk-row__size,
  .disk-health {
    grid-column: 2;
  }

  .storage-details__list > div,
  .compact-list > div {
    grid-template-columns: 1fr;
    gap: 4px;
    padding-block: 10px;
  }
}
</style>
