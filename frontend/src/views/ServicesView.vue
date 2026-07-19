<script setup lang="ts">
import { computed } from 'vue'
import {
  Activity,
  ArrowUpRight,
  Boxes,
  Cable,
  Check,
  Clock3,
  HardDrive,
  Network,
  ShieldCheck,
} from '@lucide/vue'

import StatusPill from '@/components/StatusPill.vue'

const services = [
  {
    code: 'P0-01',
    title: '环境能力探测器',
    description: '识别系统、架构、Docker、cgroup、设备与可用权限边界。',
    detail: '已完成 NAS 实机核验',
    tone: 'healthy' as const,
    icon: HardDrive,
  },
  {
    code: 'P0-02',
    title: 'Docker SDK 通道',
    description: '验证容器、日志、事件、统计、受控 exec 与镜像拉取路径。',
    detail: '已完成 NAS 实机核验',
    tone: 'healthy' as const,
    icon: Boxes,
  },
  {
    code: 'P0-03',
    title: 'Agent Socket 通道',
    description: '通过 Unix Socket 和 gRPC 分离非特权 Server 与特权 Agent。',
    detail: '代码与跨架构构建已完成',
    tone: 'healthy' as const,
    icon: Cable,
  },
  {
    code: 'P0-04',
    title: '终端通道验证',
    description: '验证 PTY、WebSocket、尺寸同步、会话退出和资源上限。',
    detail: '待开始',
    tone: 'pending' as const,
    icon: Activity,
  },
  {
    code: 'P0-05',
    title: '日志通道验证',
    description: '验证 journald 查询、Cursor 分页、Follow 与时间筛选。',
    detail: '待开始',
    tone: 'pending' as const,
    icon: Network,
  },
  {
    code: 'P0-06',
    title: '网关通道验证',
    description: '验证反向代理路由、持久化及重启后的配置恢复。',
    detail: '待开始',
    tone: 'pending' as const,
    icon: ShieldCheck,
  },
]

const completedCount = computed(() => services.filter((service) => service.tone === 'healthy').length)
</script>

<template>
  <div class="page services-page">
    <header class="page-header reveal">
      <div>
        <p class="eyebrow"><Boxes :size="14" aria-hidden="true" /> 服务中心</p>
        <h1>每项服务都有边界、证据和下一步。</h1>
        <p class="page-header__description">
          这里呈现的是项目实现进度，不是伪装成在线服务的静态仪表盘；真实运行态会在 Server API 接通后替换同一套组件。
        </p>
      </div>
      <div class="progress-chip">
        <strong>{{ completedCount }}<span>/{{ services.length }}</span></strong>
        <span>已完成技术验证</span>
      </div>
    </header>

    <section class="service-summary panel reveal" style="--reveal-index: 1" aria-labelledby="service-summary-title">
      <div>
        <span class="service-summary__eyebrow">BUILD WITH EVIDENCE</span>
        <h2 id="service-summary-title">先锁定真实能力，再开放管理动作。</h2>
        <p>
          当前 UI 不会凭空显示“运行中”。每一格状态都对应已验证的 PoC、明确的未完成项或可追溯的下一阶段。
        </p>
      </div>
      <div class="service-summary__meta">
        <span><Check :size="15" aria-hidden="true" /> 已核验</span>
        <span><Clock3 :size="15" aria-hidden="true" /> 待验证</span>
      </div>
    </section>

    <section class="service-board" aria-label="Phase 0 服务验证进度">
      <article
        v-for="(service, index) in services"
        :key="service.code"
        class="service-card reveal"
        :class="`service-card--${service.tone}`"
        :style="{ '--reveal-index': index + 2 }"
      >
        <div class="service-card__topline">
          <span>{{ service.code }}</span>
          <StatusPill :label="service.tone === 'healthy' ? '已核验' : '待验证'" :tone="service.tone" />
        </div>
        <span class="service-card__icon" aria-hidden="true">
          <component :is="service.icon" :size="21" :stroke-width="1.75" />
        </span>
        <h2>{{ service.title }}</h2>
        <p>{{ service.description }}</p>
        <footer>
          <span>{{ service.detail }}</span>
          <RouterLink v-if="service.code === 'P0-03'" to="/infrastructure" aria-label="查看 Agent Socket 架构边界">
            <ArrowUpRight :size="16" aria-hidden="true" />
          </RouterLink>
        </footer>
      </article>
    </section>
  </div>
</template>

<style scoped>
.progress-chip {
  display: grid;
  min-width: 166px;
  padding: 13px 15px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-md);
  background: rgba(255, 255, 255, 0.025);
}

.progress-chip strong {
  color: var(--ncp-primary);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 1.35rem;
  font-weight: 650;
  letter-spacing: -0.08em;
  line-height: 1;
}

.progress-chip strong span {
  color: var(--ncp-text-subtle);
  font-size: 0.78rem;
  letter-spacing: -0.04em;
}

.progress-chip > span {
  margin-top: 7px;
  color: var(--ncp-text-subtle);
  font-size: 0.67rem;
  font-weight: 650;
}

.service-summary {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 24px;
  padding: clamp(24px, 4vw, 38px);
  background:
    radial-gradient(circle at 90% 10%, rgba(145, 199, 243, 0.12), transparent 17rem),
    linear-gradient(135deg, rgba(24, 38, 39, 0.94), rgba(12, 20, 21, 0.95));
}

.service-summary__eyebrow {
  color: var(--ncp-info);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.68rem;
  font-weight: 700;
  letter-spacing: 0.12em;
}

.service-summary h2 {
  max-width: 700px;
  margin: 10px 0 9px;
  font-size: clamp(1.55rem, 3vw, 2.5rem);
  font-weight: 650;
  letter-spacing: -0.052em;
  line-height: 1.12;
}

.service-summary p {
  max-width: 700px;
  margin: 0;
  color: var(--ncp-text-muted);
  font-size: 0.88rem;
  line-height: 1.7;
}

.service-summary__meta {
  display: flex;
  flex: 0 0 auto;
  gap: 14px;
  color: var(--ncp-text-muted);
  font-size: 0.74rem;
}

.service-summary__meta span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.service-summary__meta span:first-child svg {
  color: var(--ncp-primary);
}

.service-summary__meta span:last-child svg {
  color: var(--ncp-warning);
}

.service-board {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
  margin-top: 14px;
}

.service-card {
  display: flex;
  flex-direction: column;
  min-height: 255px;
  padding: 20px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-md);
  background: rgba(17, 27, 29, 0.78);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.12);
  transition:
    border-color var(--ncp-duration-base) var(--ncp-ease-out),
    background-color var(--ncp-duration-base) var(--ncp-ease-out),
    transform var(--ncp-duration-base) var(--ncp-ease-out);
}

.service-card:hover {
  border-color: var(--ncp-line-strong);
  background: var(--ncp-surface-hover);
  transform: translateY(-3px);
}

.service-card__topline {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.service-card__topline > span:first-child {
  color: var(--ncp-text-subtle);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.67rem;
  font-weight: 700;
}

.service-card__icon {
  display: grid;
  width: 42px;
  height: 42px;
  place-items: center;
  margin-top: 21px;
  border: 1px solid currentColor;
  border-radius: 11px;
}

.service-card--healthy .service-card__icon {
  color: var(--ncp-primary);
}

.service-card--pending .service-card__icon {
  color: var(--ncp-warning);
}

.service-card h2 {
  margin: 18px 0 6px;
  font-size: 0.95rem;
  letter-spacing: -0.03em;
}

.service-card p {
  margin: 0;
  color: var(--ncp-text-muted);
  font-size: 0.76rem;
  line-height: 1.65;
}

.service-card footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-top: auto;
  padding-top: 16px;
  border-top: 1px solid var(--ncp-line);
}

.service-card footer > span {
  color: var(--ncp-text-subtle);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.64rem;
}

.service-card footer a {
  display: grid;
  width: 32px;
  height: 32px;
  place-items: center;
  border-radius: 8px;
  color: var(--ncp-primary);
  transition: background-color var(--ncp-duration-fast) var(--ncp-ease-out);
}

.service-card footer a:hover {
  background: var(--ncp-primary-soft);
}

@media (max-width: 1080px) {
  .service-board {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 700px) {
  .progress-chip {
    display: inline-grid;
    margin-top: 20px;
  }

  .service-summary {
    display: block;
    padding: 22px;
  }

  .service-summary__meta {
    margin-top: 20px;
  }

  .service-board {
    grid-template-columns: 1fr;
  }
}
</style>
