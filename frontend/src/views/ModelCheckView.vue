<template>
  <component :is="auth.isAuthenticated ? AppLayout : 'div'" :class="{ 'mc-public-shell': !auth.isAuthenticated }">
    <header v-if="!auth.isAuthenticated" class="mc-public-nav"><RouterLink to="/home" class="mc-brand"><img v-if="app.cachedPublicSettings?.site_logo" :src="app.cachedPublicSettings.site_logo" alt="" /><span>{{ app.cachedPublicSettings?.site_name || 'Lumivia' }}</span><span class="mc-nav-divider" /><span class="mc-nav-label">{{ t('modelCheck.nav') }}</span></RouterLink><RouterLink to="/login?redirect=/model-check" class="mc-btn mc-btn-outline">{{ t('home.login') }}<Icon name="arrowRight" size="sm" /></RouterLink></header>
    <div class="model-check-page">
      <section class="mc-hero">
        <div class="mc-hero-copy"><p class="mc-eyebrow"><span />{{ t('modelCheck.eyebrow') }}</p><h1>{{ t('modelCheck.headline') }}</h1><p class="mc-intro">{{ t('modelCheck.intro') }}</p><div class="mc-hero-actions"><button class="mc-btn mc-btn-primary" @click="openTest"><Icon name="sparkles" size="sm" />{{ t('modelCheck.newTest') }}<Icon name="arrowRight" size="sm" /></button><button class="mc-btn mc-btn-quiet" @click="methodOpen = true">{{ t('modelCheck.methodology') }}<Icon name="infoCircle" size="sm" /></button></div></div>
        <div class="mc-hero-art" aria-hidden="true"><div class="mc-art-orbit orbit-one" /><div class="mc-art-orbit orbit-two" /><div class="mc-mini-window"><div class="mc-mini-top"><i /><i /><i /><span>creative-study.html</span></div><svg viewBox="0 0 240 150" fill="none"><ellipse cx="120" cy="123" rx="64" ry="10" fill="#dceee5"/><path d="M64 55 119 28 176 57 120 86Z" fill="#d0e8dc" stroke="#79a993"/><path d="M64 55v44l56 29V86Z" fill="#e4f2eb" stroke="#79a993"/><path d="m120 86 56-29v43l-56 28Z" fill="#b6d9c5" stroke="#79a993"/><path d="m88 43 56 29M92 70v44m55-42v42" stroke="#79a993"/><circle cx="191" cy="34" r="15" fill="#f9e8b7"/><path d="M35 87v14m-7-7h14M191 110v10m-5-5h10" stroke="#93b5a3" stroke-width="2" stroke-linecap="round"/></svg><span class="mc-mini-caption"><span />SVG · MOTION · DETAIL</span></div><span class="mc-art-spark">✦</span></div>
      </section>

      <div class="mc-page-tabs"><div role="tablist" :aria-label="t('modelCheck.nav')"><button role="tab" :aria-selected="tab === 'public'" :class="{ selected: tab === 'public' }" @click="tab = 'public'"><Icon name="globe" size="sm" />{{ t('modelCheck.publicTab') }}</button><button role="tab" :aria-selected="tab === 'mine'" :class="{ selected: tab === 'mine' }" @click="showMine"><Icon name="beaker" size="sm" />{{ t('modelCheck.mineTab') }}<span v-if="mine.length" class="mc-count">{{ mine.length }}</span></button></div><RouterLink v-if="auth.isAdmin" to="/admin/model-check" class="mc-settings-link"><Icon name="cog" size="sm" />{{ t('modelCheck.settings') }}</RouterLink></div>
      <div v-if="overview?.demo && tab === 'mine'" class="mc-demo-notice"><Icon name="infoCircle" size="sm" /><span>{{ t('modelCheck.demoBanner') }}</span><button @click="tryDemo()" :disabled="demoBusy">{{ t('modelCheck.demoTry') }}<Icon name="arrowRight" size="xs" /></button></div>
      <div v-if="error" class="mc-error" role="alert"><Icon name="exclamationCircle" size="sm" />{{ error }}<button class="mc-btn mc-btn-quiet" @click="refresh">{{ t('modelCheck.retry') }}</button></div>
      <div v-if="loading" class="mc-loading"><span class="mc-spinner" />{{ t('modelCheck.status.queued') }}</div>
      <template v-else-if="overview">
        <CheckMonitorSummary v-if="tab === 'public'" :channels="overview.channels" :demo="overview.demo" :refreshing="refreshing" :demo-busy="demoBusy" @select="selectRun" @refresh="refresh" @demo="tryDemo" />
        <section class="mc-gallery-section">
          <div class="mc-section-heading"><div><h2>{{ tab === 'mine' ? t('modelCheck.mineTab') : t('modelCheck.recent') }}<span class="mc-count">{{ works.length }}</span></h2><p v-if="tab === 'mine'"><Icon name="lock" size="xs" />{{ t('modelCheck.privateHint') }}</p></div><button class="mc-refresh" :disabled="refreshing" :aria-label="t('modelCheck.refresh')" @click="refresh"><Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" /></button></div>
          <div v-if="tab === 'mine' && !auth.isAuthenticated" class="mc-empty"><Icon name="lock" size="xl" /><h3>{{ t('modelCheck.login') }}</h3><p>{{ t('modelCheck.loginHint') }}</p><RouterLink to="/login?redirect=/model-check" class="mc-btn mc-btn-primary">{{ t('home.login') }}<Icon name="arrowRight" size="sm" /></RouterLink></div>
          <div v-else-if="!works.length" class="mc-empty"><Icon name="beaker" size="xl" /><h3>{{ tab === 'mine' ? t('modelCheck.emptyMine') : t('modelCheck.emptyWorks') }}</h3><p>{{ t('modelCheck.emptyWorksHint') }}</p><button class="mc-btn mc-btn-primary" @click="openTest">{{ t('modelCheck.newTest') }}</button></div>
          <div v-else class="mc-work-grid">
            <article v-for="work in works" :key="work.id" class="mc-work-card"><button class="mc-work-cover" :aria-label="`${t('modelCheck.viewWork')} ${workTitle(work)}`" @click="selectWork(work)"><img v-if="work.thumbnail" :src="work.thumbnail" :alt="t(`modelCheck.topic.${work.topic}`)" loading="lazy" /><div v-else class="mc-cover-empty"><Icon :name="isCheckRunning(work.status) ? 'sparkles' : 'exclamationCircle'" size="xl" /><span>{{ t(`modelCheck.status.${work.status}`) }}</span></div><span v-if="work.source === 'demo'" class="mc-cover-label">{{ t('modelCheck.demo') }}</span><span class="mc-cover-open"><Icon name="externalLink" size="sm" />{{ t('modelCheck.viewWork') }}</span></button><div class="mc-work-body"><div class="mc-work-title"><h3>{{ workTitle(work) }}</h3><CheckQualityBadge v-if="work.assessment" :review="work.quality_review" /><span v-else :class="['mc-status', `is-${work.status}`]">{{ t(`modelCheck.status.${work.status}`) }}</span></div><CheckGroupBadge :group-name="work.group_name" :group-id="work.group_id" :key-source="work.key_source" :demo="work.source === 'demo'" /><p>{{ work.model }}<span v-if="work.channel_id && work.source !== 'demo'"> · {{ t(`modelCheck.topic.${work.topic}`) }}</span></p><div class="mc-work-footer"><time>{{ dateLabel(work.created_at) }}</time><span v-if="work.assessment">{{ t('modelCheck.visualScore') }} <strong>{{ work.assessment.score }}</strong></span><span v-else>—</span></div></div></article>
          </div>
        </section>
        <footer class="mc-page-footer"><span><Icon name="shield" size="xs" />{{ t('modelCheck.scoreNote') }}</span><small>{{ t('modelCheck.refreshed', { time: dateLabel(overview.updated_at) }) }}</small></footer>
      </template>
    </div>
  </component>

  <BaseDialog :show="formOpen" :title="t('modelCheck.newTest')" width="wide" :close-on-escape="!submitting" @close="closeForm">
    <form id="mc-self-form" class="mc-self-form" @submit.prevent="submitTest"><CheckConnectionForm v-model="form" :keys="keys" /><p class="mc-cost-note"><Icon name="infoCircle" size="sm" />{{ t('modelCheck.costHint') }}</p><div v-if="formError" class="mc-error" role="alert">{{ formError }}</div></form>
    <template #footer><button class="mc-btn mc-btn-outline" :disabled="submitting" @click="closeForm">{{ t('modelCheck.cancel') }}</button><button type="submit" form="mc-self-form" class="mc-btn mc-btn-primary" :disabled="submitting"><Icon name="play" size="sm" />{{ submitting ? t('modelCheck.starting') : t('modelCheck.start') }}</button></template>
  </BaseDialog>
  <BaseDialog :show="methodOpen" :title="t('modelCheck.methodTitle')" width="wide" @close="methodOpen = false"><div class="mc-method"><p>{{ t('modelCheck.methodIntro') }}</p><div v-for="(step, index) in ['One', 'Two', 'Three']" :key="step" class="mc-method-step"><span>0{{ index + 1 }}</span><div><h3>{{ t(`modelCheck.method${step}`) }}</h3><p>{{ t(`modelCheck.method${step}Text`) }}</p></div></div><p class="mc-method-limit">{{ t('modelCheck.methodLimit') }}</p><small>{{ t('modelCheck.retention') }}</small></div></BaseDialog>
  <CheckRunDialog :run-id="selectedRun" :baseline-id="selectedBaseline" @close="selectedRun = null; refresh()" />
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import CheckConnectionForm from '@/components/modelCheck/CheckConnectionForm.vue'
import CheckRunDialog from '@/components/modelCheck/CheckRunDialog.vue'
import CheckGroupBadge from '@/components/modelCheck/CheckGroupBadge.vue'
import CheckQualityBadge from '@/components/modelCheck/CheckQualityBadge.vue'
import CheckMonitorSummary from '@/components/modelCheck/CheckMonitorSummary.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { modelCheckAPI, checkErrorCode, isCheckRunning, type CheckOverview, type CheckRun, type CheckForm, type CheckKey, type CheckTopic } from '@/api/modelCheck'
import '@/components/modelCheck/modelCheck.css'
const { t, te, locale } = useI18n(), auth = useAuthStore(), app = useAppStore(), router = useRouter()
const overview = ref<CheckOverview | null>(null), mine = ref<CheckRun[]>([]), loading = ref(true), refreshing = ref(false), error = ref('')
const tab = ref<'public' | 'mine'>('public'), methodOpen = ref(false)
const selectedRun = ref<string | null>(null), selectedBaseline = ref<string | null>(null)
const formOpen = ref(false), submitting = ref(false), formError = ref(''), keys = ref<CheckKey[]>([]), demoBusy = ref(false)
const form = ref<CheckForm>({ model: 'gpt-6-astra', topic: 'pelican', protocol: 'responses', reasoning: 'default', max_tokens: 8000, key_source: 'existing', key_id: null, base_url: '', key: '' })
const works = computed(() => tab.value === 'mine' ? mine.value : overview.value?.works || [])
const workTitle = (work: CheckRun) => work.channel_id && work.source !== 'demo' && work.label ? work.label : t(`modelCheck.topic.${work.topic}`)
let timer: ReturnType<typeof setInterval> | undefined
let disposed = false
const dateLabel = (date: number) => new Date(date).toLocaleString(locale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
const explainError = (e: unknown) => { const code = checkErrorCode(e); return te(`modelCheck.errors.${code}`) ? t(`modelCheck.errors.${code}`) : t('modelCheck.loadFailed') }
async function refresh() {
  if (refreshing.value || disposed) return
  refreshing.value = true
  try {
    const result = await modelCheckAPI.overview()
    if (disposed) return
    overview.value = result; error.value = ''
    if (auth.isAuthenticated) mine.value = await modelCheckAPI.mine()
  } catch (e) { if (!disposed) error.value = explainError(e) }
  finally { if (!disposed) { loading.value = false; refreshing.value = false } }
}
function selectRun(id: string, baseline: string | null = null) { selectedRun.value = id; selectedBaseline.value = baseline }
function selectWork(work: CheckRun) { selectRun(work.id, overview.value?.channels.find(c => c.id === work.channel_id)?.baseline_id || null) }
function showMine() { tab.value = 'mine'; void refresh() }
async function openTest() {
  if (!auth.isAuthenticated) { await router.push('/login?redirect=/model-check'); return }
  formError.value = ''; formOpen.value = true
  try { keys.value = await modelCheckAPI.keys(); if (!form.value.key_id) form.value.key_id = keys.value[0]?.id || null } catch (e) { formError.value = explainError(e) }
}
function closeForm() { if (submitting.value) return; formOpen.value = false; form.value.key = '' }
async function submitTest() {
  if (submitting.value) return
  submitting.value = true; formError.value = ''
  try {
    const run = await modelCheckAPI.test({ ...form.value }); form.value.key = ''; formOpen.value = false
    tab.value = 'mine'; mine.value.unshift(run); selectRun(run.id)
  } catch (e) { formError.value = explainError(e) }
  finally { submitting.value = false }
}
async function tryDemo(topic: CheckTopic = form.value.topic) {
  if (demoBusy.value) return
  demoBusy.value = true
  try { const run = await modelCheckAPI.demo(topic); selectRun(run.id); void refresh() } catch (e) { error.value = explainError(e) } finally { demoBusy.value = false }
}
onMounted(() => { void app.fetchPublicSettings(); void refresh(); timer = setInterval(() => { if (document.visibilityState === 'visible') void refresh() }, 8000) })
onUnmounted(() => { disposed = true; clearInterval(timer); form.value.key = '' })
</script>
