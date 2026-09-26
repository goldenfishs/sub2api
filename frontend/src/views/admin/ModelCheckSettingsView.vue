<template>
  <AppLayout>
    <div class="model-check-page">
      <section class="mc-admin-hero"><div><p class="mc-eyebrow"><span />{{ t('modelCheck.adminNav') }}</p><h1>{{ t('modelCheck.adminTitle') }}</h1><p class="mc-intro">{{ t('modelCheck.adminIntro') }}</p></div><RouterLink to="/model-check" class="mc-btn mc-btn-outline"><Icon name="externalLink" size="sm" />{{ t('modelCheck.backPublic') }}</RouterLink></section>
      <div class="mc-admin-note"><Icon name="clock" size="sm" />{{ t('modelCheck.adminCost') }}</div>
      <div v-if="error" class="mc-error" role="alert">{{ error }}<button class="mc-btn mc-btn-quiet" @click="load()">{{ t('modelCheck.retry') }}</button></div>
      <div v-if="notice" class="mc-success" role="status"><Icon name="checkCircle" size="sm" />{{ notice }}</div>
      <div class="mc-section-heading mc-admin-heading"><h2>{{ t('modelCheck.monitoring') }}<span class="mc-count">{{ channels.length }}</span></h2><button class="mc-btn mc-btn-primary" @click="editChannel()"><Icon name="plus" size="sm" />{{ t('modelCheck.createChannel') }}</button></div>
      <div v-if="loading" class="mc-loading"><span class="mc-spinner" /></div>
      <div v-else-if="!channels.length" class="mc-empty mc-admin-empty"><span class="mc-empty-icon"><Icon name="beaker" size="xl" /></span><h3>{{ t('modelCheck.adminEmpty') }}</h3><p>{{ t('modelCheck.adminEmptyHint') }}</p><button class="mc-btn mc-btn-primary" @click="editChannel()"><Icon name="plus" size="sm" />{{ t('modelCheck.createChannel') }}</button><div class="mc-setup-steps"><span>01 · {{ t('modelCheck.source') }}</span><Icon name="chevronRight" size="xs" /><span>02 · {{ t('modelCheck.chooseTopic') }}</span><Icon name="chevronRight" size="xs" /><span>03 · {{ t('modelCheck.enabledLabel') }}</span></div></div>
      <div v-else class="mc-admin-list">
        <article v-for="channel in channels" :key="channel.id" class="mc-admin-channel">
          <div class="mc-admin-channel-main"><span :class="['mc-channel-icon', `tone-${channel.topic}`]"><Icon name="beaker" size="md" /></span><div><h3>{{ channel.name }}<span :class="['mc-status', channel.enabled ? 'is-normal' : 'is-paused']">{{ channel.enabled ? t('modelCheck.live') : t('modelCheck.paused') }}</span></h3><p>{{ channel.model }} · {{ t(`modelCheck.topic.${channel.topic}`) }} · {{ t('modelCheck.interval', { minutes: channel.interval_minutes }) }}</p></div><span class="mc-visibility"><Icon :name="channel.public ? 'globe' : 'lock'" size="sm" />{{ channel.public ? t('modelCheck.publicTab') : t('modelCheck.privateHint') }}</span></div>
          <div class="mc-admin-channel-body">
            <div class="mc-admin-channel-details"><CheckHistoryPreview class="mc-history"><span v-for="empty in Math.max(0, 24 - channel.history.length)" :key="`empty-${empty}`" class="mc-history-empty" /><button v-for="run in channel.history" :key="run.id" :class="`is-${run.status}`" :title="`${date(run.created_at)} · ${t(`modelCheck.status.${run.status}`)}`" :aria-label="`${t('modelCheck.viewDetails')} ${date(run.created_at)}`" @click="selectedRun = run.id; selectedBaseline = channel.baseline_id" :data-preview-run="run.id" :data-preview-status="run.status" /></CheckHistoryPreview><div class="mc-admin-channel-meta"><span v-if="channel.enabled">{{ t('modelCheck.scheduledAt', { time: date(channel.next_run) }) }}</span><span v-else>{{ t('modelCheck.paused') }}</span><span><Icon name="shield" size="xs" />{{ channel.baseline_id ? t('modelCheck.baselineConfigured') : t('modelCheck.noBaseline') }}</span></div></div>
            <button v-if="channel.preview?.thumbnail || channel.preview?.image" class="mc-admin-preview" :aria-label="`${t('modelCheck.latestPreview')} · ${date(channel.preview.created_at)} · ${t('modelCheck.viewDetails')}`" @click="selectedRun = channel.preview.id; selectedBaseline = channel.baseline_id">
              <img :src="channel.preview.thumbnail || channel.preview.image || ''" :alt="t('modelCheck.preview')" loading="lazy" />
              <span class="mc-admin-preview-caption"><strong>{{ t('modelCheck.latestPreview') }}</strong><time :datetime="new Date(channel.preview.created_at).toISOString()">{{ date(channel.preview.created_at) }}</time><span>{{ t('modelCheck.viewDetails') }} ↗</span></span>
            </button>
            <div v-else class="mc-admin-preview-empty"><Icon name="beaker" size="lg" /><span>{{ t('modelCheck.noPreview') }}</span></div>
          </div>
          <div class="mc-admin-channel-actions"><span v-if="channel.latest" :class="['mc-plain-status', `is-${channel.latest.status}`]">{{ t(`modelCheck.status.${channel.latest.status}`) }}</span><span v-else class="mc-dim">{{ t('modelCheck.noRuns') }}</span><CheckQualityBadge v-if="channel.latest?.assessment" :review="channel.latest.quality_review" /><div><button class="mc-btn mc-btn-delete" :disabled="isChannelBusy(channel)" @click="requestDelete(channel)"><Icon name="trash" size="sm" />{{ t('modelCheck.deleteChannel') }}</button><button v-if="channel.latest?.status === 'normal' && channel.latest.quality_review?.verdict !== 'degraded' && channel.baseline_id !== channel.latest.id" class="mc-btn mc-btn-quiet" @click="setBaseline(channel)">{{ t('modelCheck.setBaseline') }}</button><button class="mc-btn mc-btn-outline" :disabled="isChannelBusy(channel)" @click="editChannel(channel)"><Icon name="edit" size="sm" />{{ t('modelCheck.edit') }}</button><button class="mc-btn mc-btn-primary" :disabled="isChannelBusy(channel)" @click="runNow(channel)"><Icon name="play" size="sm" />{{ t('modelCheck.runNow') }}</button></div></div>
        </article>
      </div>
      <footer class="mc-page-footer"><span>{{ t('modelCheck.retention') }}</span></footer>
    </div>
  </AppLayout>
  <BaseDialog :show="formOpen" :title="editing ? t('modelCheck.editChannel') : t('modelCheck.createChannel')" width="wide" :close-on-escape="!saving" @close="closeForm">
    <form id="mc-channel-form" class="mc-admin-form" @submit.prevent="save"><label class="mc-field"><span>{{ t('modelCheck.channelName') }}</span><input v-model.trim="form.name" required maxlength="60" placeholder="Codex Pro · 主线路" /></label><CheckConnectionForm v-model="connection" :keys="keys" :saved-key="editing?.has_key" admin />
      <label class="mc-field"><span>{{ t('modelCheck.intervalLabel') }}</span><input v-model.number="form.interval_minutes" type="number" min="5" max="1440" required /><small>{{ t('modelCheck.intervalHint') }}</small></label>
      <div class="mc-toggle-row"><div><strong>{{ t('modelCheck.enabledLabel') }}</strong><small>{{ t('modelCheck.adminCost') }}</small></div><button type="button" role="switch" :aria-checked="form.enabled" :aria-label="t('modelCheck.enabledLabel')" :class="['mc-switch', { enabled: form.enabled }]" @click="form.enabled = !form.enabled"><span /></button></div>
      <div class="mc-toggle-row"><div><strong>{{ t('modelCheck.publicLabel') }}</strong><small>{{ t('modelCheck.publicHint') }}</small></div><button type="button" role="switch" :aria-checked="form.public" :aria-label="t('modelCheck.publicLabel')" :class="['mc-switch', { enabled: form.public }]" @click="form.public = !form.public"><span /></button></div>
      <div v-if="formError" class="mc-error" role="alert">{{ formError }}</div>
    </form>
    <template #footer><button class="mc-btn mc-btn-outline" :disabled="saving" @click="closeForm">{{ t('modelCheck.cancel') }}</button><button form="mc-channel-form" type="submit" class="mc-btn mc-btn-primary" :disabled="saving">{{ saving ? t('modelCheck.starting') : t('modelCheck.save') }}</button></template>
  </BaseDialog>
  <BaseDialog :show="!!deletingChannel" :title="t('modelCheck.deleteChannel')" width="narrow" :close-on-escape="!deleting" :show-close-button="!deleting" @close="closeDelete">
    <div class="mc-delete-confirm"><p>{{ t('modelCheck.deleteConfirm', { name: deletingChannel?.name }) }}</p><p>{{ t('modelCheck.deleteConsequence') }}</p><p>{{ t('modelCheck.deleteKeyHint') }}</p><div v-if="deleteError" class="mc-error" role="alert">{{ deleteError }}</div><p v-if="deleteBlocked" role="status">{{ t('modelCheck.errors.already_running') }}</p></div>
    <template #footer><button class="mc-btn mc-btn-outline" :disabled="deleting" @click="closeDelete">{{ t('modelCheck.cancel') }}</button><button class="mc-btn mc-btn-danger" :disabled="deleting || deleteBlocked" @click="deleteChannel">{{ deleting ? t('modelCheck.deleting') : t('modelCheck.confirmDelete') }}</button></template>
  </BaseDialog>
  <CheckRunDialog :run-id="selectedRun" :baseline-id="selectedBaseline" @close="selectedRun = null; load()" />
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import CheckConnectionForm from '@/components/modelCheck/CheckConnectionForm.vue'
import CheckRunDialog from '@/components/modelCheck/CheckRunDialog.vue'
import CheckQualityBadge from '@/components/modelCheck/CheckQualityBadge.vue'
import CheckHistoryPreview from '@/components/modelCheck/CheckHistoryPreview.vue'
import { modelCheckAPI, checkErrorCode, isCheckRunning, type CheckChannel, type CheckChannelForm, type CheckForm, type CheckKey } from '@/api/modelCheck'
import '@/components/modelCheck/modelCheck.css'
const { t, te, locale } = useI18n()
const channels = ref<CheckChannel[]>([]), keys = ref<CheckKey[]>([]), loading = ref(true), error = ref(''), notice = ref(''), pending = ref('')
const formOpen = ref(false), saving = ref(false), formError = ref(''), editing = ref<CheckChannel | null>(null)
const selectedRun = ref<string | null>(null), selectedBaseline = ref<string | null>(null)
const deletingChannel = ref<CheckChannel | null>(null), deleting = ref(false), deleteError = ref('')
const isChannelBusy = (channel: CheckChannel) => pending.value === channel.id || (deleting.value && deletingChannel.value?.id === channel.id) || (!!channel.latest && isCheckRunning(channel.latest.status))
const deleteBlocked = computed(() => {
  const channel = channels.value.find(item => item.id === deletingChannel.value?.id)
  return !!channel && (pending.value === channel.id || (!!channel.latest && isCheckRunning(channel.latest.status)))
})
const newForm = (): CheckChannelForm => ({ name: '', model: 'gpt-6-astra', topic: 'pelican', protocol: 'responses', reasoning: 'default', max_tokens: 8000, key_source: 'existing', group_name: '', key_id: null, base_url: '', key: '', interval_minutes: 60, enabled: false, public: false })
const form = ref<CheckChannelForm>(newForm())
const connection = computed<CheckForm>({ get: () => form.value, set: value => { form.value = { ...form.value, ...value } } })
let timer: ReturnType<typeof setInterval> | undefined
let disposed = false, refreshing = false
let loadVersion = 0
const date = (value: number) => new Date(value).toLocaleString(locale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
const explainError = (e: unknown) => { const code = checkErrorCode(e); return te(`modelCheck.errors.${code}`) ? t(`modelCheck.errors.${code}`) : t('modelCheck.errors.internal_error') }
async function load(force = false) {
  if (disposed || (refreshing && !force)) return
  const current = ++loadVersion
  refreshing = true
  try {
    const result = await modelCheckAPI.channels()
    if (!disposed && current === loadVersion) { channels.value = result; error.value = '' }
  } catch (e) {
    if (!disposed && current === loadVersion) error.value = explainError(e)
  } finally {
    if (current === loadVersion) { refreshing = false; if (!disposed) loading.value = false }
  }
}
async function editChannel(channel?: CheckChannel) {
  editing.value = channel || null; formError.value = ''; notice.value = ''
  form.value = channel ? { name: channel.name, model: channel.model, topic: channel.topic, protocol: channel.protocol, reasoning: channel.reasoning, max_tokens: channel.max_tokens, key_source: channel.key_source || 'external', group_name: channel.group_name || '', key_id: channel.key_id || null, base_url: channel.base_url || '', key: '', interval_minutes: channel.interval_minutes, enabled: channel.enabled, public: channel.public } : newForm()
  formOpen.value = true
  try { keys.value = await modelCheckAPI.keys(); if (!editing.value) form.value.key_id = keys.value[0]?.id || null } catch (e) { formError.value = explainError(e) }
}
function closeForm() { if (saving.value) return; form.value.key = ''; formOpen.value = false }
async function save() {
  if (saving.value) return
  saving.value = true; formError.value = ''
  try { await modelCheckAPI.save({ ...form.value }, editing.value?.id); form.value.key = ''; formOpen.value = false; notice.value = t('modelCheck.saved'); await load(true) } catch (e) { formError.value = explainError(e) } finally { saving.value = false }
}
async function runNow(channel: CheckChannel) {
  if (pending.value) return
  pending.value = channel.id; notice.value = ''
  try { const run = await modelCheckAPI.runChannel(channel.id); selectedRun.value = run.id; selectedBaseline.value = channel.baseline_id; await load(true) } catch (e) { error.value = explainError(e) } finally { pending.value = '' }
}
function requestDelete(channel: CheckChannel) {
  if (isChannelBusy(channel)) return
  deletingChannel.value = channel; deleteError.value = ''; notice.value = ''
}
function closeDelete() { if (!deleting.value) deletingChannel.value = null }
async function deleteChannel() {
  if (!deletingChannel.value || deleting.value || deleteBlocked.value) return
  const id = deletingChannel.value.id
  deleting.value = true; deleteError.value = ''
  try {
    await modelCheckAPI.deleteChannel(id)
    channels.value = channels.value.filter(channel => channel.id !== id)
    deletingChannel.value = null; notice.value = t('modelCheck.deleted')
    await load(true)
  } catch (e) { deleteError.value = explainError(e) } finally { deleting.value = false }
}
async function setBaseline(channel: CheckChannel) {
  if (!channel.latest) return
  try { await modelCheckAPI.baseline(channel.id, channel.latest.id); notice.value = t('modelCheck.baselineSet'); await load(true) } catch (e) { error.value = explainError(e) }
}
onMounted(() => { void load(); timer = setInterval(() => { if (document.visibilityState === 'visible') void load() }, 8000) })
onUnmounted(() => { disposed = true; clearInterval(timer); form.value.key = '' })
</script>
