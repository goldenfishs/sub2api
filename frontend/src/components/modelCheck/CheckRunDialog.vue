<template>
  <BaseDialog :show="!!runId" :title="t('modelCheck.viewDetails')" width="extra-wide" @close="$emit('close')">
    <div class="mc-detail">
      <div v-if="error" class="mc-error" role="alert">{{ error }}<button class="mc-btn mc-btn-quiet" @click="load">{{ t('modelCheck.retry') }}</button></div>
      <div v-else-if="!run" class="mc-loading"><span class="mc-spinner" />{{ t('modelCheck.status.queued') }}</div>
      <template v-else>
        <div class="mc-detail-top"><div><span :class="['mc-status', `is-${run.status}`]">{{ t(`modelCheck.status.${run.status}`) }}</span><CheckQualityBadge v-if="run.assessment" :review="run.quality_review" /><strong>{{ run.model }}</strong><CheckGroupBadge :group-name="run.group_name" :group-id="run.group_id" :key-source="run.key_source" :demo="run.source === 'demo'" /><span class="mc-dim">{{ t(`modelCheck.topic.${run.topic}`) }}</span></div><span v-if="run.source === 'demo'" class="mc-demo-tag">{{ t('modelCheck.demo') }}</span></div>
        <div v-if="isCheckRunning(run.status)" class="mc-processing" aria-live="polite"><div class="mc-processing-orbit"><Icon name="sparkles" size="xl" /></div><h3>{{ t('modelCheck.processing') }}</h3><p>{{ run.retry_at ? t('modelCheck.retryScheduled', { attempt: (run.attempt_count || 0) + 1, max: run.max_attempts, time: new Date(run.retry_at).toLocaleTimeString(locale) }) : t(`modelCheck.status.${run.status}`) }}</p><small>{{ t('modelCheck.processingHint') }}</small></div>
        <div v-else-if="run.status === 'failed'" class="mc-failed-preview"><Icon name="exclamationCircle" size="xl" /><h3>{{ t('modelCheck.status.failed') }}</h3><p>{{ translatedError(run.error || 'test_failed') }}</p></div>
        <div v-else class="mc-detail-grid">
          <div>
            <div v-if="baseline && compare" class="mc-compare-grid"><figure><img :src="baseline.image || baseline.thumbnail || ''" :alt="t('modelCheck.baseline')" /><figcaption>{{ t('modelCheck.baseline') }} · {{ baseline.assessment?.score }}</figcaption></figure><figure><img :src="run.image || run.thumbnail || ''" :alt="t('modelCheck.current')" /><figcaption>{{ t('modelCheck.current') }} · {{ run.assessment?.score }}</figcaption></figure></div>
            <div v-else class="mc-artwork-stage"><iframe v-if="playing && run.html" :srcdoc="run.html" sandbox="" referrerpolicy="no-referrer" :title="`${run.model} · ${t('modelCheck.preview')}`" /><img v-else :src="run.image || run.thumbnail || ''" :alt="`${run.model} · ${t(`modelCheck.topic.${run.topic}`)}`" /></div>
            <p v-if="baseline && compare && !comparable" class="mc-field-note">{{ t('modelCheck.differentBrief') }}</p>
            <div class="mc-stage-toolbar"><button class="mc-btn mc-btn-quiet" @click="playing = !playing; compare = false"><Icon :name="playing ? 'eye' : 'play'" size="sm" />{{ playing ? t('modelCheck.screenshot') : t('modelCheck.animation') }}</button><button v-if="baseline" class="mc-btn mc-btn-quiet" @click="compare = !compare">{{ t('modelCheck.compare') }}</button><button class="mc-btn mc-btn-quiet" @click="download"><Icon name="download" size="sm" />{{ t('modelCheck.download') }}</button></div>
          </div>
          <aside v-if="run.assessment" class="mc-score-panel"><span class="mc-overline">{{ t('modelCheck.visualScore') }}</span><div class="mc-score-number">{{ run.assessment.score }}<small>/ 100</small></div><span v-if="run.assessment.delta !== null" class="mc-delta">{{ t('modelCheck.delta', { delta: run.assessment.delta, value: `${run.assessment.delta > 0 ? '+' : ''}${run.assessment.delta}` }) }}</span>
            <div class="mc-dimension" v-for="dimension in run.assessment.dimensions" :key="dimension.id"><div><span>{{ t(`modelCheck.dimensions.${dimension.id}`) }}</span><span>{{ dimension.score }}/{{ dimension.max }}</span></div><div class="mc-dimension-track"><span :style="{ width: `${dimension.score / dimension.max * 100}%` }" /></div></div>
          </aside>
        </div>
        <section v-if="run.attempt_count && (run.attempt_count > 1 || run.retry_at || run.status === 'failed')" class="mc-attempts" aria-live="polite">
          <strong>{{ t('modelCheck.attemptProgress', { attempt: run.attempt_count, max: run.max_attempts }) }}</strong>
          <p v-if="run.retry_at && run.retry_max_tokens && run.retry_max_tokens > (run.effective_max_tokens ?? run.max_tokens)">{{ t('modelCheck.tokenLimitRetry', { from: formatTokens(run.effective_max_tokens ?? run.max_tokens), to: formatTokens(run.retry_max_tokens) }) }}</p>
          <p v-else-if="run.effective_max_tokens && run.effective_max_tokens > run.max_tokens">{{ t('modelCheck.tokenLimitExpanded', { requested: formatTokens(run.max_tokens), effective: formatTokens(run.effective_max_tokens) }) }}</p>
          <p v-if="run.retry_at">{{ t('modelCheck.retryScheduled', { attempt: run.attempt_count + 1, max: run.max_attempts, time: new Date(run.retry_at).toLocaleTimeString(locale) }) }}</p>
          <ul><li v-for="attempt in run.attempts || []" :key="attempt.attempt">{{ t('modelCheck.attemptLabel', { attempt: attempt.attempt }) }} · {{ t('modelCheck.attemptTokenLimit', { value: formatTokens(attempt.max_tokens ?? run.max_tokens) }) }} · {{ t('modelCheck.seconds', { value: (attempt.duration_ms / 1000).toFixed(1) }) }} · {{ attempt.error ? translatedError(attempt.error) : t('modelCheck.attemptSucceeded') }}</li></ul>
          <small>{{ t('modelCheck.retryUsageHint') }}</small>
        </section>
        <template v-if="!isCheckRunning(run.status)">
          <div v-if="run.assessment" class="mc-assessment mc-quality-review">
            <strong>{{ t('modelCheck.qualityReview') }}</strong>
            <p>{{ t('modelCheck.qualityReviewHint') }}</p>
            <CheckQualityBadge :review="run.quality_review" />
            <small v-if="run.quality_review">{{ new Date(run.quality_review.reviewed_at).toLocaleString(locale) }}</small>
            <div v-if="auth.isAdmin && run.channel_id" class="mc-stage-toolbar">
              <button class="mc-btn mc-btn-outline" :disabled="reviewing || run.quality_review?.verdict === 'normal'" @click="reviewQuality('normal')">{{ t('modelCheck.confirmQuality') }}</button>
              <button class="mc-btn mc-btn-outline" :disabled="reviewing || run.quality_review?.verdict === 'degraded'" @click="reviewQuality('degraded')">{{ t('modelCheck.markDegraded') }}</button>
              <button v-if="run.quality_review" class="mc-btn mc-btn-quiet" :disabled="reviewing" @click="reviewQuality('clear')">{{ t('modelCheck.clearQuality') }}</button>
              <button v-if="run.status === 'normal' && run.quality_review?.verdict !== 'degraded' && baselineId !== run.id" class="mc-btn mc-btn-quiet" :disabled="reviewing" @click="setBaseline">{{ t('modelCheck.setBaseline') }}</button>
            </div>
            <p v-if="reviewError" class="mc-error" role="alert">{{ reviewError }}</p>
            <p v-if="reviewNotice" role="status">{{ reviewNotice }}</p>
          </div>
          <div v-if="run.assessment" class="mc-assessment"><strong>{{ t('modelCheck.assessment') }}</strong><ul v-if="run.assessment.reasons.length"><li v-for="reason in run.assessment.reasons" :key="reason">{{ t(`modelCheck.reasons.${reason}`) }}</li></ul><p v-else>{{ t('modelCheck.normalNote') }}</p><small>{{ t('modelCheck.scoreNote') }}</small></div>
          <div class="mc-facts">
            <div><span>{{ t('modelCheck.generationTime') }}</span><strong>{{ run.generation_ms == null ? '—' : t('modelCheck.seconds', { value: (run.generation_ms / 1000).toFixed(1) }) }}</strong></div>
            <div :title="t('modelCheck.tpsHint')"><span class="mc-fact-label">{{ t('modelCheck.tps') }}<Icon name="infoCircle" size="xs" /></span><strong>{{ tps }}</strong></div>
            <div><span>{{ t('modelCheck.inputTokens') }}</span><strong>{{ run.usage?.input_tokens?.toLocaleString() ?? '—' }}</strong></div>
            <div><span>{{ t('modelCheck.outputTokens') }}</span><strong>{{ run.usage?.output_tokens?.toLocaleString() ?? '—' }}</strong></div>
            <div><span>{{ t('modelCheck.reasoning') }}</span><strong>{{ run.reasoning === 'default' ? t('modelCheck.defaultReasoning') : t(`modelCheck.${run.reasoning}`) }}</strong></div>
          </div>
          <p v-if="run.source === 'demo'" class="mc-field-note">{{ t('modelCheck.sampleNoTokens') }}</p>
          <details class="mc-prompt"><summary>{{ t('modelCheck.prompt') }}<Icon name="chevronDown" size="xs" /></summary><pre>{{ run.prompt }}</pre><small>{{ run.prompt_version }} · {{ run.prompt_hash.slice(0, 12) }}</small></details>
        </template>
      </template>
    </div>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import CheckGroupBadge from './CheckGroupBadge.vue'
import CheckQualityBadge from './CheckQualityBadge.vue'
import { useAuthStore } from '@/stores/auth'
import { modelCheckAPI, isCheckRunning, checkErrorCode, type CheckRun } from '@/api/modelCheck'
const props = defineProps<{ runId: string | null; baselineId?: string | null }>()
defineEmits<{ close: [] }>()
const { t, te, locale } = useI18n()
const auth = useAuthStore()
const reviewing = ref(false), reviewError = ref(''), reviewNotice = ref('')
const comparable = computed(() => !!run.value && !!baseline.value && ['prompt_hash', 'model', 'reasoning', 'protocol', 'group_id', 'key_source'].every(key => run.value?.[key as keyof CheckRun] === baseline.value?.[key as keyof CheckRun]) && (run.value.effective_max_tokens ?? run.value.max_tokens) === (baseline.value.effective_max_tokens ?? baseline.value.max_tokens))
const run = ref<CheckRun | null>(null), baseline = ref<CheckRun | null>(null), error = ref(''), playing = ref(false), compare = ref(false)
const tps = computed(() => {
  const value = run.value?.tps
  if (run.value?.source === 'demo' || typeof value !== 'number' || !Number.isFinite(value) || value < 0) return t('modelCheck.notAvailable')
  return t('modelCheck.tokensPerSecond', { value: value.toLocaleString(locale.value, { minimumFractionDigits: 1, maximumFractionDigits: 1 }) })
})
let timer: ReturnType<typeof setTimeout> | undefined
let version = 0
const translatedError = (code: string) => te(`modelCheck.errors.${code.split(':')[0]}`) ? t(`modelCheck.errors.${code.split(':')[0]}`) : t('modelCheck.errors.test_failed')
const formatTokens = (value: number) => value.toLocaleString(locale.value)
async function load() {
  if (!props.runId) return
  const current = version
  try {
    const result = await modelCheckAPI.run(props.runId)
    if (current !== version) return
    run.value = result; error.value = ''
    if (isCheckRunning(result.status)) timer = setTimeout(load, 1800)
  } catch (e) { if (current === version) error.value = translatedError(checkErrorCode(e)) }
}
watch(() => [props.runId, props.baselineId], async () => {
  version++; clearTimeout(timer); reviewError.value = ''; reviewNotice.value = ''; run.value = null; baseline.value = null; error.value = ''; playing.value = false; compare.value = false
  const current = version
  void load()
  if (props.baselineId && props.baselineId !== props.runId) {
    try { const data = await modelCheckAPI.run(props.baselineId); if (current === version) baseline.value = data } catch { /* A removed/private baseline must not prevent viewing the selected run. */ }
  }
}, { immediate: true })
onUnmounted(() => { version++; clearTimeout(timer) })
async function reviewQuality(verdict: 'normal' | 'degraded' | 'clear') {
  if (!run.value || reviewing.value) return
  const current = version
  reviewing.value = true; reviewError.value = ''; reviewNotice.value = ''
  try {
    const result = await modelCheckAPI.review(run.value.id, verdict)
    if (current === version) { run.value = result; reviewNotice.value = t('modelCheck.qualitySaved') }
  } catch (e) { if (current === version) reviewError.value = translatedError(checkErrorCode(e)) }
  finally { reviewing.value = false }
}
async function setBaseline() {
  if (!run.value?.channel_id || reviewing.value) return
  const current = version
  reviewing.value = true; reviewError.value = ''; reviewNotice.value = ''
  try {
    await modelCheckAPI.baseline(run.value.channel_id, run.value.id)
    if (current === version) reviewNotice.value = t('modelCheck.baselineSet')
  } catch (e) { if (current === version) reviewError.value = translatedError(checkErrorCode(e)) }
  finally { reviewing.value = false }
}
function download() {
  if (!run.value?.html) return
  const url = URL.createObjectURL(new Blob([run.value.html], { type: 'text/html;charset=utf-8' }))
  const a = document.createElement('a'); a.href = url; a.download = `lumivia-study-${run.value.id.slice(0, 8)}.html`; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
}
</script>

<style scoped>
.mc-attempts { margin: 16px 0; padding: 14px 16px; border: 1px solid #dce7df; border-radius: 10px; font-size: 12px; line-height: 1.8; overflow-wrap: anywhere; }
.mc-attempts ul { list-style: none; margin: 8px 0; padding: 0; }
.mc-attempts small { color: #71867a; }
.mc-facts { grid-template-columns: repeat(5, minmax(0, 1fr)); }
.mc-facts > div { min-width: 0; }
.mc-facts .mc-fact-label { display: flex; align-items: center; gap: 4px; }
.mc-fact-label svg { flex-shrink: 0; }
.mc-facts strong { font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
@media (max-width: 700px) {
  .mc-attempts { margin: 16px 0; padding: 14px 16px; border: 1px solid #dce7df; border-radius: 10px; font-size: 12px; line-height: 1.8; overflow-wrap: anywhere; }
.mc-attempts ul { list-style: none; margin: 8px 0; padding: 0; }
.mc-attempts small { color: #71867a; }
.mc-facts { grid-template-columns: repeat(2, minmax(0, 1fr)); row-gap: 18px; }
  .mc-facts > div:last-child { grid-column: 1 / -1; }
}
</style>
