<template>
  <section class="mc-health-panel" :aria-label="t('modelCheck.healthTitle')">
    <div class="mc-health-top">
      <div class="mc-health-source">
        <div class="mc-health-title"><h2>{{ t('modelCheck.healthTitle') }}</h2><span v-if="selected?.demo" class="mc-health-demo">{{ t('modelCheck.demo') }}</span></div>
        <label v-if="channels.length" class="mc-health-select">
          <span class="sr-only">{{ t('modelCheck.selectMonitor') }}</span>
          <select v-model="selectedId"><option v-for="channel in channels" :key="channel.id" :value="channel.id">{{ channel.name }}</option></select>
        </label>
        <p v-else class="mc-health-empty">{{ t('modelCheck.emptyChannels') }}</p>
        <div v-if="selected" class="mc-health-context">
          <CheckGroupBadge :group-name="selected.group_name" :group-id="selected.group_id" :key-source="selected.key_source" :demo="selected.demo" />
          <span>{{ selected.model }} · {{ t('modelCheck.topic.' + selected.topic) }}</span>
        </div>
      </div>
      <dl class="mc-health-metrics">
        <div :title="t('modelCheck.passRateHint')"><dd>{{ rate }}</dd><dt>{{ t('modelCheck.passRate') }}<Icon name="infoCircle" size="xs" /></dt></div>
        <div :title="t('modelCheck.latestDurationHint')"><dd>{{ duration(stats.latest_duration_ms) }}</dd><dt>{{ t('modelCheck.latestDuration') }}</dt></div>
        <div><dd>{{ stats.total }}<small>{{ t('modelCheck.runUnit') }}</small></dd><dt>{{ t('modelCheck.testCount') }}</dt></div>
      </dl>
    </div>
    <div class="mc-health-history-heading">
      <p>{{ t('modelCheck.monitorStatus') }}<span>{{ t('modelCheck.historyCapacity', { count: stats.limit }) }}</span></p>
      <div><span>{{ nextRun }}</span><button type="button" class="mc-refresh" :disabled="refreshing" :aria-label="t('modelCheck.refresh')" @click="$emit('refresh')"><Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" /></button></div>
    </div>
    <CheckHistoryPreview class="mc-health-track" :aria-label="t('modelCheck.historyCapacity', { count: stats.limit })">
      <span v-for="index in Math.max(0, stats.limit - stats.history.length)" :key="'empty-' + index" class="mc-health-slot" :title="t('modelCheck.noHistory')" />
      <button v-for="run in stats.history" :key="run.id" type="button" :class="['mc-health-slot', 'is-' + run.status]" :title="historyTitle(run)" :aria-label="historyTitle(run)" @click="$emit('select', run.id, selected?.baseline_id || null)" :data-preview-run="run.id" :data-preview-status="run.status" />
    </CheckHistoryPreview>
    <div class="mc-health-bottom">
      <time>{{ stats.history.length ? date(stats.history[0].created_at) : t('modelCheck.noRuns') }}</time>
      <div class="mc-health-legend">
        <span><i class="is-normal" />{{ t('modelCheck.healthPassed') }} {{ stats.normal }}</span>
        <span><i class="is-review" />{{ t('modelCheck.healthReview') }} {{ stats.review }}</span>
        <span><i class="is-failed" />{{ t('modelCheck.healthFailed') }} {{ stats.failed }}</span>
      </div>
      <span>{{ t('modelCheck.latestRecord') }}</span>
    </div>
    <div v-if="demo && (!selected || selected.demo)" class="mc-health-note">
      <Icon name="infoCircle" size="xs" /><p>{{ t('modelCheck.demoStatsHint') }}</p>
      <button type="button" :disabled="demoBusy" @click="$emit('demo', selected?.topic || 'pelican')">{{ t('modelCheck.demoTry') }}<Icon name="arrowRight" size="xs" /></button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import CheckGroupBadge from './CheckGroupBadge.vue'
import CheckHistoryPreview from './CheckHistoryPreview.vue'
import type { CheckChannel, CheckStatistics, CheckTopic } from '@/api/modelCheck'

const props = defineProps<{ channels: CheckChannel[]; demo: boolean; refreshing: boolean; demoBusy: boolean }>()
defineEmits<{ refresh: []; select: [id: string, baseline: string | null]; demo: [topic: CheckTopic] }>()
const { t, locale } = useI18n()
const preferredId = ref('')
const selected = computed(() => props.channels.find(channel => channel.id === preferredId.value) || props.channels.find(channel => !channel.demo) || props.channels[0])
const selectedId = computed({ get: () => selected.value?.id || '', set: (id: string) => { preferredId.value = id } })
const empty: CheckStatistics = { limit: 30, total: 0, judged: 0, normal: 0, review: 0, failed: 0, pass_rate: null, success_rate: null, latest_duration_ms: null, history: [] }
const stats = computed(() => selected.value?.statistics || empty)
const rate = computed(() => stats.value.pass_rate === null ? '—' : (stats.value.pass_rate * 100).toLocaleString(locale.value, { maximumFractionDigits: 1 }) + '%')
const now = ref(Date.now())
const nextRun = computed(() => {
  const channel = selected.value
  if (!channel || channel.demo) return ''
  if (!channel.enabled) return t('modelCheck.paused')
  const seconds = Math.max(0, Math.ceil((channel.next_run - now.value) / 1000))
  return seconds === 0 ? t('modelCheck.checkSoon') : t('modelCheck.nextCountdown', { minutes: Math.floor(seconds / 60), seconds: seconds % 60 })
})
const date = (value: number) => new Date(value).toLocaleString(locale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
const duration = (value: number | null) => value === null ? '—' : value < 1000 ? t('modelCheck.milliseconds', { value: Math.round(value) }) : t('modelCheck.seconds', { value: (value / 1000).toFixed(1) })
const historyTitle = (run: CheckStatistics['history'][number]) => date(run.created_at) + ' · ' + t('modelCheck.status.' + run.status) + ' · ' + duration(run.duration_ms)
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => { clock = setInterval(() => { now.value = Date.now() }, 1000) })
onUnmounted(() => clearInterval(clock))
</script>

<style scoped>
.mc-health-panel { margin-top: 24px; padding: 25px 25px 18px; border: 1px solid #e0e9e3; border-radius: 14px; background: #ffffffdc; color: #3a5748; }
.mc-health-top { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: start; gap: 26px; }
.mc-health-title { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; }
.mc-health-title h2 { margin: 0; font-size: 16px; font-weight: 600; color: #294c3d; }
.mc-health-demo { font-size: 10px; color: #819083; padding: 3px 6px; border-radius: 5px; background: #f1f5ef; }
.mc-health-select { display: block; max-width: 310px; }
.mc-health-select select { width: 100%; border: 1px solid #dce8df; background: #f9fbf9; color: #41614e; border-radius: 7px; padding: 8px 30px 8px 10px; font-size: 12px; cursor: pointer; text-overflow: ellipsis; }
.mc-health-select select:focus-visible, .mc-health-slot:focus-visible { outline: 2px solid #338b69; outline-offset: 3px; }
.mc-health-context { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-top: 10px; font-size: 11px; color: #7c8a81; line-height: 1.7; }
.mc-health-metrics { display: flex; gap: 25px; margin: 17px 0 0; }
.mc-health-metrics > div { min-width: 72px; }
.mc-health-metrics dd { font-size: 27px; font-weight: 600; color: #304f40; letter-spacing: -.6px; line-height: 1.35; margin: 0 0 6px; white-space: nowrap; }
.mc-health-metrics dd small { font-weight: 400; font-size: 11px; color: #7e9284; margin-left: 5px; letter-spacing: 0; }
.mc-health-metrics dt { display: flex; gap: 4px; align-items: center; font-size: 11px; color: #87958d; white-space: nowrap; }
.mc-health-metrics dt svg { width: 12px; height: 12px; }
.mc-health-history-heading { margin-top: 25px; display: flex; justify-content: space-between; align-items: center; gap: 10px; font-size: 11px; }
.mc-health-history-heading p { margin: 0; font-weight: 550; }
.mc-health-history-heading p span { font-weight: 400; color: #8a9990; margin-left: 10px; }
.mc-health-history-heading > div { display: flex; gap: 10px; align-items: center; color: #82968a; }
.mc-health-history-heading .mc-refresh { width: 25px; height: 25px; border: 0; background: transparent; }
.mc-health-track { display: grid; grid-template-columns: repeat(30, minmax(0, 1fr)); gap: 5px; margin-top: 13px; }
.mc-health-slot { display: block; height: 34px; border: 0; padding: 0; border-radius: 4px; background: #ecf1ed; min-width: 0; }
button.mc-health-slot { cursor: pointer; transition: transform .15s, filter .15s; }
button.mc-health-slot:hover { transform: translateY(-2px); filter: brightness(.95); }
.mc-health-slot.is-normal, .mc-health-legend .is-normal { background: #71a68d; }
.mc-health-slot.is-review, .mc-health-legend .is-review { background: #d79387; }
.mc-health-slot.is-failed, .mc-health-legend .is-failed { background: #e3bd64; }
.mc-health-bottom { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-top: 10px; color: #8c9a91; font-size: 10px; }
.mc-health-legend { display: flex; flex-wrap: wrap; justify-content: center; gap: 14px; }
.mc-health-legend span { display: inline-flex; align-items: center; gap: 5px; white-space: nowrap; }
.mc-health-legend i { display: inline-block; width: 5px; height: 9px; border-radius: 2px; }
.mc-health-note { display: flex; align-items: center; gap: 7px; font-size: 10px; color: #879886; margin-top: 17px; padding-top: 13px; border-top: 1px solid #edf2ed; }
.mc-health-note > svg { flex-shrink: 0; }
.mc-health-note p { margin: 0; }
.mc-health-note button { display: inline-flex; align-items: center; gap: 5px; margin-left: auto; color: #65896b; white-space: nowrap; }
.mc-health-note button:disabled { opacity: .5; cursor: wait; }
.mc-health-empty { margin: 0; font-size: 12px; color: #7a8c7e; }
@media (max-width: 700px) {
  .mc-health-panel { padding: 19px 17px 15px; }
  .mc-health-top { grid-template-columns: 1fr; gap: 18px; }
  .mc-health-title { margin-bottom: 10px; }
  .mc-health-select { max-width: none; }
  .mc-health-metrics { margin: 0; justify-content: space-between; gap: 12px; }
  .mc-health-metrics dd { font-size: 26px; }
  .mc-health-history-heading { margin-top: 21px; flex-wrap: wrap; }
  .mc-health-track { gap: 3px; }
  .mc-health-slot { height: 30px; border-radius: 3px; }
  .mc-health-bottom { flex-wrap: wrap; }
  .mc-health-legend { order: 3; width: 100%; margin-top: 5px; }
  .mc-health-note { flex-wrap: wrap; line-height: 1.8; }
  .mc-health-note button { margin-left: 20px; }
}
:global(.dark) .mc-health-panel { background: #1d2c23; border-color: #334b3c; color: #b5cbba; }
:global(.dark) .mc-health-title h2, :global(.dark) .mc-health-metrics dd { color: #c8dfce; }
:global(.dark) .mc-health-select select { background: #263c2b; border-color: #405c48; color: #b4d0bc; }
:global(.dark) .mc-health-demo { background: #2e402f; color: #9fb9a0; }
:global(.dark) .mc-health-slot:not(button) { background: #304237; }
:global(.dark) .mc-health-note { border-color: #344b3b; color: #9aae9a; }
</style>
