<script setup lang="ts">
import {
  Activity,
  Cable,
  Check,
  Clock3,
  HardDrive,
  Network,
  Server,
  ShieldCheck,
} from '@lucide/vue'

const verifiedCapabilities = [
  {
    phase: 'P0-01',
    title: '环境能力探测',
    detail: '系统、架构、Docker、cgroup、设备与权限探测已在 NAS 上完成。',
    icon: HardDrive,
  },
  {
    phase: 'P0-02',
    title: '受控 Docker SDK',
    detail: '容器运行态与受控验证路径来自 Docker Engine，而不是本地猜测。',
    icon: Network,
  },
  {
    phase: 'P0-03',
    title: 'Unix Socket · gRPC',
    detail: 'Server 与 Agent 通过固定 Socket 进行最小化、受控的通信。',
    icon: Cable,
  },
]

const pendingCapabilities = [
  { phase: 'P0-04', title: 'PTY 终端通道', icon: Activity },
  { phase: 'P0-05', title: 'journald 日志通道', icon: Server },
  { phase: 'P0-06', title: 'Caddy 网关通道', icon: ShieldCheck },
]
</script>

<template>
  <div class="page infrastructure-page">
    <header class="page-header reveal">
      <div>
        <p class="eyebrow"><ShieldCheck :size="14" aria-hidden="true" /> 能力地图</p>
        <h1>把“能做什么”和“还不能做什么”放在同一张图上。</h1>
        <p class="page-header__description">
          NCP 的管理能力必须源自已验证的受控通道。这里记录架构冻结前的真实边界，后续所有页面都会复用这份事实来源。
        </p>
      </div>
    </header>

    <section class="architecture-frame panel reveal" style="--reveal-index: 1" aria-labelledby="architecture-title">
      <div class="architecture-frame__heading">
        <span>TRUSTED PATH</span>
        <h2 id="architecture-title">浏览器 → 非特权 Server → 特权 Agent → NAS 能力</h2>
        <p>浏览器和 Server 不直接拥有 Docker Socket 或宿主机 root 权限。</p>
      </div>
      <ol class="architecture-path">
        <li><span>01</span><strong>浏览器</strong><small>界面与用户意图</small></li>
        <li><span>02</span><strong>Go Server</strong><small>API、权限与编排</small></li>
        <li><span>03</span><strong>Go Agent</strong><small>白名单 RPC</small></li>
        <li><span>04</span><strong>NAS 能力</strong><small>系统与 Docker</small></li>
      </ol>
    </section>

    <section class="capability-layout">
      <article class="capability-column panel reveal" style="--reveal-index: 2">
        <header class="panel-header">
          <div>
            <h2>已核验通道</h2>
            <p>可作为后续业务实现的可信基础</p>
          </div>
          <span class="capability-count"><Check :size="14" aria-hidden="true" /> {{ verifiedCapabilities.length }}</span>
        </header>
        <ul class="capability-list">
          <li v-for="item in verifiedCapabilities" :key="item.phase">
            <span class="capability-list__icon" aria-hidden="true"><component :is="item.icon" :size="18" :stroke-width="1.75" /></span>
            <div>
              <span>{{ item.phase }}</span>
              <strong>{{ item.title }}</strong>
              <p>{{ item.detail }}</p>
            </div>
            <Check :size="17" :stroke-width="2" aria-label="已核验" />
          </li>
        </ul>
      </article>

      <article class="capability-column panel reveal" style="--reveal-index: 3">
        <header class="panel-header">
          <div>
            <h2>待核验通道</h2>
            <p>在完成实机验证前不向管理界面承诺能力</p>
          </div>
          <span class="capability-count capability-count--pending"><Clock3 :size="14" aria-hidden="true" /> {{ pendingCapabilities.length }}</span>
        </header>
        <ul class="capability-list capability-list--pending">
          <li v-for="item in pendingCapabilities" :key="item.phase">
            <span class="capability-list__icon" aria-hidden="true"><component :is="item.icon" :size="18" :stroke-width="1.75" /></span>
            <div>
              <span>{{ item.phase }}</span>
              <strong>{{ item.title }}</strong>
              <p>等待独立 PoC、失败路径与 NAS 实机证据。</p>
            </div>
            <Clock3 :size="17" :stroke-width="1.8" aria-label="待核验" />
          </li>
        </ul>
      </article>
    </section>

    <section class="guardrails panel reveal" style="--reveal-index: 4" aria-labelledby="guardrails-title">
      <header class="panel-header">
        <div>
          <h2 id="guardrails-title">不可跨越的架构边界</h2>
          <p>这些约束决定了 UI 中哪些动作可以出现，哪些必须等到受控能力成熟后再开放。</p>
        </div>
      </header>
      <ul>
        <li><span>01</span><strong>Web Server 不直接访问 Docker Socket</strong></li>
        <li><span>02</span><strong>Server 不以 root 身份运行</strong></li>
        <li><span>03</span><strong>Agent 不暴露任意命令执行 RPC</strong></li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.architecture-frame {
  padding: clamp(24px, 4vw, 40px);
  background:
    radial-gradient(circle at 88% 20%, rgba(140, 226, 190, 0.12), transparent 16rem),
    linear-gradient(135deg, rgba(26, 43, 41, 0.93), rgba(13, 22, 23, 0.97));
}

.architecture-frame__heading > span {
  color: var(--ncp-primary);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.68rem;
  font-weight: 700;
  letter-spacing: 0.12em;
}

.architecture-frame__heading h2 {
  max-width: 800px;
  margin: 12px 0 8px;
  font-size: clamp(1.5rem, 3vw, 2.5rem);
  font-weight: 650;
  letter-spacing: -0.055em;
  line-height: 1.15;
}

.architecture-frame__heading p {
  margin: 0;
  color: var(--ncp-text-muted);
  font-size: 0.86rem;
}

.architecture-path {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  padding: 0;
  margin: 32px 0 0;
  list-style: none;
}

.architecture-path li {
  position: relative;
  display: grid;
  gap: 6px;
  min-height: 122px;
  padding: 17px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-sm);
  background: rgba(8, 15, 16, 0.36);
}

.architecture-path li:not(:last-child)::after {
  position: absolute;
  top: 50%;
  right: -7px;
  z-index: 1;
  width: 13px;
  height: 1px;
  background: var(--ncp-primary);
  content: '';
}

.architecture-path span {
  color: var(--ncp-primary);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.65rem;
  font-weight: 700;
}

.architecture-path strong {
  margin-top: auto;
  font-size: 0.88rem;
}

.architecture-path small {
  color: var(--ncp-text-subtle);
  font-size: 0.67rem;
}

.capability-layout {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  margin-top: 14px;
}

.capability-column {
  padding: 24px;
}

.capability-count {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--ncp-primary);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.7rem;
  font-weight: 700;
}

.capability-count--pending {
  color: var(--ncp-warning);
}

.capability-list {
  display: grid;
  padding: 0;
  margin: 22px 0 0;
  list-style: none;
}

.capability-list li {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  gap: 12px;
  align-items: start;
  padding: 16px 0;
  border-top: 1px solid var(--ncp-line);
}

.capability-list__icon {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 9px;
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary);
}

.capability-list li > div {
  display: grid;
  gap: 3px;
}

.capability-list li > div > span {
  color: var(--ncp-text-subtle);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.62rem;
  font-weight: 700;
}

.capability-list strong {
  font-size: 0.81rem;
}

.capability-list p {
  margin: 2px 0 0;
  color: var(--ncp-text-muted);
  font-size: 0.72rem;
  line-height: 1.6;
}

.capability-list > li > svg {
  margin-top: 9px;
  color: var(--ncp-primary);
}

.capability-list--pending .capability-list__icon {
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning);
}

.capability-list--pending > li > svg {
  color: var(--ncp-warning);
}

.guardrails {
  margin-top: 14px;
  padding: 24px;
}

.guardrails ul {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  padding: 0;
  margin: 23px 0 0;
  list-style: none;
}

.guardrails li {
  display: grid;
  gap: 17px;
  min-height: 104px;
  padding: 16px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-sm);
  background: rgba(8, 15, 16, 0.28);
}

.guardrails li span {
  color: var(--ncp-danger);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.66rem;
  font-weight: 700;
}

.guardrails li strong {
  font-size: 0.78rem;
  line-height: 1.5;
}

@media (max-width: 920px) {
  .architecture-path,
  .guardrails ul {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .architecture-path li:nth-child(2)::after {
    display: none;
  }
}

@media (max-width: 680px) {
  .architecture-frame,
  .capability-column,
  .guardrails {
    padding: 20px;
  }

  .architecture-path,
  .capability-layout,
  .guardrails ul {
    grid-template-columns: 1fr;
  }

  .architecture-path li:not(:last-child)::after {
    display: none;
  }
}
</style>
