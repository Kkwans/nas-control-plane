<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowRight, KeyRound, LoaderCircle, RotateCw, ShieldCheck } from '@lucide/vue'

import { NcpApiError } from '@/api/system'
import NcpLogo from '@/components/NcpLogo.vue'
import { useAuthStore } from '@/stores/auth'

const emit = defineEmits<{
  authenticated: []
}>()

const authStore = useAuthStore()
const username = ref('')
const password = ref('')
const confirmation = ref('')
const formError = ref<string | null>(null)
const isSubmitting = ref(false)

const isSetup = computed(() => authStore.state === 'setup')
const title = computed(() => (isSetup.value ? '初始化 Root 控制台' : '进入 NAS Control Plane'))
const description = computed(() =>
  isSetup.value
    ? '创建唯一的 Root 管理账号。账号创建后，控制面功能将通过该会话进入。'
    : '使用 Root 管理账号进入实时系统与服务控制台。',
)

async function submit() {
  formError.value = null
  if (isSetup.value && password.value !== confirmation.value) {
    formError.value = '两次输入的密码不一致。'
    return
  }
  isSubmitting.value = true
  try {
    const credentials = { username: username.value, password: password.value }
    if (isSetup.value) {
      await authStore.bootstrap(credentials)
    } else {
      await authStore.login(credentials)
    }
    password.value = ''
    confirmation.value = ''
    emit('authenticated')
  } catch (error) {
    formError.value = error instanceof NcpApiError ? error.message : '登录服务暂不可用，请稍后重试。'
  } finally {
    isSubmitting.value = false
  }
}
</script>

<template>
  <main class="access-page">
    <div class="access-page__ambient" aria-hidden="true"></div>
    <section class="access-panel" aria-labelledby="access-title">
      <div class="access-brand">
        <NcpLogo :size="38" />
        <span>
          <strong>NAS CONTROL PLANE</strong>
          <small>ROOT OPERATOR CONSOLE</small>
        </span>
      </div>

      <template v-if="authStore.state === 'unavailable'">
        <div class="access-state-icon access-state-icon--warning" aria-hidden="true"><RotateCw :size="24" /></div>
        <p class="access-kicker">CONTROL PLANE OFFLINE</p>
        <h1 id="access-title">正式控制面尚未连接</h1>
        <p class="access-description">
          当前页面没有使用演示数据。请启动 ncp-server 与 Root Agent 后重新连接。
        </p>
        <p v-if="authStore.errorCode" class="access-error-code">{{ authStore.errorCode }}</p>
        <button class="primary-action" type="button" @click="authStore.refresh">
          重新连接
          <RotateCw :size="16" aria-hidden="true" />
        </button>
      </template>

      <template v-else>
        <div class="access-state-icon" aria-hidden="true"><ShieldCheck :size="24" /></div>
        <p class="access-kicker">{{ isSetup ? 'FIRST-RUN SETUP' : 'ROOT SESSION' }}</p>
        <h1 id="access-title">{{ title }}</h1>
        <p class="access-description">{{ description }}</p>

        <form class="access-form" @submit.prevent="submit">
          <label>
            <span>账号</span>
            <input v-model.trim="username" autocomplete="username" maxlength="64" required placeholder="例如 root-admin" />
          </label>
          <label>
            <span>密码</span>
            <input
              v-model="password"
              type="password"
              autocomplete="current-password"
              minlength="8"
              maxlength="256"
              required
              placeholder="至少 8 个字符"
            />
          </label>
          <label v-if="isSetup">
            <span>确认密码</span>
            <input
              v-model="confirmation"
              type="password"
              autocomplete="new-password"
              minlength="8"
              maxlength="256"
              required
              placeholder="再次输入密码"
            />
          </label>

          <p v-if="formError" class="access-form__error" role="alert">{{ formError }}</p>
          <button class="primary-action" type="submit" :disabled="isSubmitting">
            <LoaderCircle v-if="isSubmitting" class="spin" :size="16" aria-hidden="true" />
            <KeyRound v-else :size="16" aria-hidden="true" />
            {{ isSetup ? '创建 Root 账号' : '进入控制台' }}
            <ArrowRight v-if="!isSubmitting" :size="16" aria-hidden="true" />
          </button>
        </form>
      </template>

      <footer class="access-footer">
        <span></span>
        <p>本地 NAS · Root 管理控制台</p>
      </footer>
    </section>
  </main>
</template>

<style scoped>
.access-page {
  position: relative;
  display: grid;
  min-height: 100dvh;
  padding: 24px;
  place-items: center;
  overflow: hidden;
}

.access-page__ambient {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(circle at 13% 12%, rgba(42, 107, 221, 0.13), transparent 25rem),
    radial-gradient(circle at 86% 84%, rgba(27, 162, 132, 0.11), transparent 29rem),
    linear-gradient(135deg, rgba(255, 255, 255, 0.9), rgba(244, 247, 252, 0.78));
}

.access-page__ambient::after {
  position: absolute;
  inset: 0;
  background-image: linear-gradient(rgba(43, 64, 100, 0.035) 1px, transparent 1px), linear-gradient(90deg, rgba(43, 64, 100, 0.035) 1px, transparent 1px);
  background-size: 28px 28px;
  content: '';
  mask-image: linear-gradient(to bottom, black, transparent 70%);
}

.access-panel {
  position: relative;
  z-index: 1;
  width: min(100%, 476px);
  padding: clamp(30px, 6vw, 52px);
  border: 1px solid rgba(43, 64, 100, 0.12);
  border-radius: 24px;
  background: rgba(255, 255, 255, 0.86);
  box-shadow: 0 28px 75px rgba(35, 55, 91, 0.14), 0 2px 7px rgba(35, 55, 91, 0.04);
  backdrop-filter: blur(18px);
}

.access-brand {
  display: flex;
  align-items: center;
  gap: 11px;
}

.access-brand strong,
.access-brand small {
  display: block;
}

.access-brand strong {
  color: var(--ncp-text);
  font-size: 0.74rem;
  letter-spacing: 0.08em;
}

.access-brand small,
.access-kicker,
.access-error-code,
.access-footer p {
  color: var(--ncp-text-subtle);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.62rem;
  font-weight: 650;
  letter-spacing: 0.08em;
}

.access-brand small {
  margin-top: 3px;
  font-size: 0.53rem;
}

.access-state-icon {
  display: grid;
  width: 48px;
  height: 48px;
  margin-top: clamp(42px, 8vw, 62px);
  place-items: center;
  border: 1px solid rgba(35, 135, 108, 0.18);
  border-radius: 15px;
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.access-state-icon--warning {
  border-color: rgba(199, 129, 24, 0.2);
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning-strong);
}

.access-kicker {
  margin: 23px 0 0;
  color: var(--ncp-primary-strong);
}

.access-state-icon--warning + .access-kicker {
  color: var(--ncp-warning-strong);
}

.access-panel h1 {
  max-width: 370px;
  margin: 12px 0 0;
  color: var(--ncp-text);
  font-size: clamp(2rem, 6vw, 2.65rem);
  font-weight: 720;
  letter-spacing: -0.065em;
  line-height: 1.05;
}

.access-description {
  max-width: 358px;
  margin: 14px 0 0;
  color: var(--ncp-text-muted);
  font-size: 0.89rem;
  line-height: 1.75;
}

.access-error-code {
  margin: 18px 0 0;
  color: var(--ncp-warning-strong);
}

.access-form {
  display: grid;
  gap: 16px;
  margin-top: 30px;
}

.access-form label {
  display: grid;
  gap: 7px;
  color: var(--ncp-text-muted);
  font-size: 0.73rem;
  font-weight: 750;
}

.access-form input {
  width: 100%;
  min-height: 48px;
  padding: 0 14px;
  border: 1px solid var(--ncp-line);
  border-radius: 12px;
  outline: 0;
  background: #fff;
  color: var(--ncp-text);
  font-size: 0.88rem;
  transition: border-color var(--ncp-duration-fast) var(--ncp-ease-out), box-shadow var(--ncp-duration-fast) var(--ncp-ease-out);
}

.access-form input::placeholder {
  color: #a3acbb;
}

.access-form input:focus {
  border-color: var(--ncp-primary);
  box-shadow: 0 0 0 4px rgba(38, 102, 220, 0.11);
}

.access-form__error {
  margin: -2px 0 0;
  color: var(--ncp-danger-strong);
  font-size: 0.76rem;
}

.primary-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 9px;
  min-height: 48px;
  margin-top: 8px;
  padding: 0 18px;
  border-radius: 12px;
  background: linear-gradient(135deg, #2869dc, #1d56bd);
  box-shadow: 0 11px 20px rgba(31, 92, 197, 0.2);
  color: #fff;
  font-size: 0.82rem;
  font-weight: 750;
  transition: transform var(--ncp-duration-fast) var(--ncp-ease-out), box-shadow var(--ncp-duration-fast) var(--ncp-ease-out), filter var(--ncp-duration-fast) var(--ncp-ease-out);
}

.primary-action:hover:not(:disabled) {
  box-shadow: 0 14px 26px rgba(31, 92, 197, 0.27);
  filter: brightness(1.04);
  transform: translateY(-2px);
}

.primary-action:disabled {
  cursor: wait;
  opacity: 0.72;
}

.spin {
  animation: spin 0.8s linear infinite;
}

.access-footer {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  gap: 12px;
  align-items: center;
  margin-top: clamp(42px, 8vw, 68px);
}

.access-footer::before,
.access-footer span {
  height: 1px;
  background: var(--ncp-line);
  content: '';
}

.access-footer p {
  margin: 0;
  font-size: 0.51rem;
  letter-spacing: 0.06em;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

@media (max-width: 540px) {
  .access-page { padding: 16px; }
  .access-panel { padding: 28px 24px; border-radius: 20px; }
  .access-footer { grid-template-columns: 1fr; justify-items: center; }
  .access-footer::before,
  .access-footer span { display: none; }
}
</style>
