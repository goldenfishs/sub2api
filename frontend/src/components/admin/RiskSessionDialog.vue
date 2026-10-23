<template>
  <BaseDialog :show="show" :title="t('admin.riskControl.sessions.title')" width="extra-wide" @close="close">
    <div class="space-y-5">
      <p class="text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.riskControl.sessions.scopeHint') }}</p>

      <form class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-900/40" @submit.prevent="saveConfig">
        <div v-if="configLoading" class="py-4 text-sm text-gray-500" role="status">{{ t('common.loading') }}</div>
        <div v-else-if="configError" class="flex items-center justify-between gap-3" role="alert">
          <p class="text-sm text-red-600 dark:text-red-400">{{ configError }}</p>
          <button type="button" class="btn btn-secondary btn-sm" @click="loadConfig">{{ t('admin.riskControl.sessions.retry') }}</button>
        </div>
        <template v-else>
          <div class="flex items-start justify-between gap-4">
            <div>
              <label id="risk-session-enabled-label" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.riskControl.sessions.enabled') }}</label>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.riskControl.sessions.enabledHint') }}</p>
            </div>
            <Toggle v-model="config.enabled" aria-labelledby="risk-session-enabled-label" :disabled="saving" data-test="session-enabled" />
          </div>
          <div class="mt-4 flex flex-col gap-3 border-t border-gray-200 pt-4 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <label for="risk-session-retention" class="block text-xs font-medium text-gray-700 dark:text-gray-300">{{ t('admin.riskControl.sessions.retention') }}</label>
              <div class="mt-1 flex items-center gap-2">
                <input id="risk-session-retention" v-model.number="config.snapshot_retention_days" type="number" min="1" max="3650" step="1" required :disabled="saving" class="input w-28" data-test="session-retention" />
                <span class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.riskControl.sessions.days') }}</span>
              </div>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.riskControl.sessions.retentionHint') }}</p>
            </div>
            <button type="submit" class="btn btn-primary shrink-0" :disabled="saving || !validRetention" data-test="save-session-config">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
          </div>
        </template>
      </form>

      <section class="overflow-hidden rounded-xl border border-gray-200 dark:border-dark-700" :aria-label="t('admin.riskControl.sessions.records')">
        <div class="flex items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
          <div class="flex items-center gap-2">
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.riskControl.sessions.records') }}</h4>
            <span class="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400">{{ total }}</span>
          </div>
          <button type="button" class="btn btn-secondary btn-sm inline-flex items-center gap-1.5" :disabled="listLoading" @click="loadList(page)">
            <Icon name="refresh" size="sm" :class="listLoading ? 'animate-spin' : ''" />
            {{ t('admin.riskControl.refresh') }}
          </button>
        </div>
        <div v-if="listError" class="px-4 py-3 text-sm text-red-600 dark:text-red-400" role="alert">{{ listError }}</div>
        <div class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-900/40 dark:text-gray-400">
              <tr>
                <th class="px-4 py-3 font-medium">{{ t('admin.riskControl.sessions.timeUser') }}</th>
                <th class="px-4 py-3 font-medium">{{ t('admin.riskControl.sessions.trigger') }}</th>
                <th class="px-4 py-3 font-medium">{{ t('admin.riskControl.sessions.sessionStatus') }}</th>
                <th class="px-4 py-3 font-medium">{{ t('admin.riskControl.sessions.snapshot') }}</th>
                <th class="px-4 py-3 text-right font-medium">{{ t('common.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="listLoading"><td colspan="5" class="px-4 py-12 text-center text-gray-500" role="status">{{ t('common.loading') }}</td></tr>
              <tr v-else-if="!items.length"><td colspan="5" class="px-4 py-12 text-center text-gray-500 dark:text-gray-400">{{ t('admin.riskControl.sessions.empty') }}</td></tr>
              <tr v-for="row in listLoading ? [] : items" :key="row.id">
                <td class="px-4 py-3">
                  <p class="max-w-[240px] truncate font-medium text-gray-900 dark:text-white" :title="row.user_email">{{ row.user_email || userLabel(row.user_id) }}</p>
                  <p class="mt-1 max-w-[240px] truncate text-xs text-gray-500 dark:text-gray-400">{{ row.api_key_name || '—' }} · {{ date(row.created_at) }}</p>
                </td>
                <td class="px-4 py-3">
                  <p class="text-gray-800 dark:text-gray-200">{{ sourceLabel(row.source) }}</p>
                  <p class="mt-1 max-w-[200px] truncate font-mono text-xs text-gray-500 dark:text-gray-400" :title="row.model">{{ row.model || '—' }}</p>
                </td>
                <td class="whitespace-nowrap px-4 py-3">
                  <span class="inline-flex rounded-full px-2 py-1 text-xs font-medium" :class="row.blocked ? 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'">{{ blockLabel(row) }}</span>
                </td>
                <td class="px-4 py-3"><span class="text-xs" :class="row.snapshot_status === 'stored' ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-500 dark:text-gray-400'">{{ snapshotLabel(row.snapshot_status) }}</span></td>
                <td class="whitespace-nowrap px-4 py-3 text-right"><button type="button" class="btn btn-secondary btn-sm" :data-test="`session-detail-${row.id}`" @click="openDetail(row.id)">{{ t('admin.riskControl.sessions.view') }}</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" :show-page-size-selector="false" @update:page="loadList" />
      </section>
    </div>
    <template #footer><button type="button" class="btn btn-secondary" @click="close">{{ t('common.close') }}</button></template>
  </BaseDialog>

  <BaseDialog :show="show && detailOpen" :title="t('admin.riskControl.sessions.detailTitle', { id: detailID })" width="extra-wide" :z-index="60" @close="closeDetail">
    <p v-if="detailLoading" class="py-12 text-center text-sm text-gray-500" role="status">{{ t('common.loading') }}</p>
    <div v-else-if="detailError" class="space-y-3" role="alert">
      <p class="text-sm text-red-600 dark:text-red-400">{{ detailError }}</p>
      <button type="button" class="btn btn-secondary" @click="openDetail(detailID)">{{ t('admin.riskControl.sessions.retry') }}</button>
    </div>
    <div v-else-if="detail" class="space-y-5">
      <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900/50">
        <div class="flex flex-wrap items-center gap-2 text-sm">
          <span class="font-semibold text-gray-900 dark:text-white">{{ detail.user_email || userLabel(detail.user_id) }}</span>
          <span class="text-gray-400">·</span><span class="text-gray-600 dark:text-gray-300">{{ sourceLabel(detail.source) }}</span>
          <span class="rounded-full px-2 py-0.5 text-xs font-medium" :class="detail.blocked ? 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300' : 'bg-gray-200 text-gray-600 dark:bg-dark-700 dark:text-gray-300'">{{ blockLabel(detail) }}</span>
        </div>
        <p class="mt-2 break-all text-xs text-gray-500 dark:text-gray-400">{{ date(detail.created_at) }} · {{ detail.api_key_name || '—' }} · {{ detail.model || '—' }} · {{ detail.endpoint }}</p>
        <p class="mt-3 whitespace-pre-wrap break-words text-sm text-gray-700 dark:text-gray-300">{{ detail.reason || '—' }}</p>
        <p v-if="detail.released_at" class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.riskControl.sessions.releasedAt', { time: date(detail.released_at) }) }}<span v-if="detail.release_reason"> · {{ detail.release_reason }}</span></p>
        <p v-if="!detail.blocked && !detail.released_at" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.riskControl.sessions.noIdentity') }}</p>
      </div>
      <div class="rounded-lg border border-sky-100 bg-sky-50 px-4 py-3 text-xs leading-5 text-sky-800 dark:border-sky-800/40 dark:bg-sky-900/20 dark:text-sky-200">
        <p class="font-medium" data-test="snapshot-scope">{{ scopeLabel(detail.snapshot_scope) }}</p>
        <p>{{ t('admin.riskControl.sessions.scopeHint') }}</p>
        <p>{{ t('admin.riskControl.sessions.retentionHint') }}</p>
      </div>
      <div class="flex flex-wrap gap-x-5 gap-y-2 text-xs text-gray-500 dark:text-gray-400">
        <span>{{ snapshotLabel(detail.snapshot_status) }}</span>
        <span>{{ t('admin.riskControl.sessions.byteCounts', { request: formatBytes(detail.request_bytes), response: formatBytes(detail.response_bytes) }) }}</span>
        <span v-if="detail.snapshot_expires_at">{{ t('admin.riskControl.sessions.expiresAt', { time: date(detail.snapshot_expires_at) }) }}</span>
      </div>
      <p v-if="detail.snapshot_status !== 'stored'" class="rounded-lg bg-gray-50 p-4 text-sm text-gray-500 dark:bg-dark-900/50 dark:text-gray-400" data-test="snapshot-unavailable">{{ snapshotLabel(detail.snapshot_status) }}</p>
      <template v-else>
        <section v-if="messages.length" class="space-y-3">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.riskControl.sessions.messages') }}</h4>
          <div class="max-h-80 space-y-2 overflow-auto rounded-lg border border-gray-200 p-3 dark:border-dark-700">
            <div v-for="(message, index) in messages" :key="index" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-900/50">
              <p class="mb-1 font-mono text-xs font-semibold text-primary-600 dark:text-primary-400">{{ message.role }}</p>
              <pre class="whitespace-pre-wrap break-words text-sm leading-6 text-gray-800 dark:text-gray-200">{{ message.content }}</pre>
            </div>
          </div>
        </section>
        <details :open="!messages.length" class="rounded-lg border border-gray-200 dark:border-dark-700">
          <summary class="cursor-pointer px-4 py-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.riskControl.sessions.requestBody') }}</summary>
          <pre class="max-h-[460px] overflow-auto whitespace-pre-wrap break-words border-t border-gray-200 bg-gray-950 p-4 text-xs leading-5 text-gray-100 dark:border-dark-700" data-test="request-body">{{ prettyJSON(detail.request_body) }}</pre>
        </details>
        <details v-if="detail.response_body" class="rounded-lg border border-gray-200 dark:border-dark-700">
          <summary class="cursor-pointer px-4 py-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.riskControl.sessions.responseBody') }}</summary>
          <p class="border-t border-gray-200 bg-gray-50 px-4 py-3 text-xs leading-5 text-gray-500 dark:border-dark-700 dark:bg-dark-900/50 dark:text-gray-400" data-test="response-scope">{{ responseScopeLabel(detail.response_scope) }}</p>
          <pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words border-t border-gray-200 bg-gray-950 p-4 text-xs leading-5 text-gray-100 dark:border-dark-700" data-test="response-body">{{ prettyJSON(detail.response_body) }}</pre>
        </details>
      </template>
    </div>
    <template #footer>
      <div class="flex w-full items-center justify-between gap-3">
        <button v-if="detail?.blocked" type="button" class="btn btn-secondary" data-test="release-session" @click="releaseReason = ''; releaseOpen = true">{{ t('admin.riskControl.sessions.release') }}</button>
        <span v-else></span>
        <button type="button" class="btn btn-secondary" @click="closeDetail">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>

  <BaseDialog :show="show && releaseOpen" :title="t('admin.riskControl.sessions.releaseTitle')" width="narrow" :z-index="70" :close-on-escape="!releasing" :show-close-button="!releasing" @close="releaseOpen = false">
    <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">{{ t('admin.riskControl.sessions.releaseHint') }}</p>
    <label for="risk-session-release-reason" class="mt-4 block text-sm text-gray-700 dark:text-gray-300">{{ t('admin.riskControl.sessions.releaseReason') }}</label>
    <textarea id="risk-session-release-reason" v-model="releaseReason" rows="3" maxlength="1000" :disabled="releasing" class="input mt-2 w-full" data-test="release-reason"></textarea>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="releasing" @click="releaseOpen = false">{{ t('common.cancel') }}</button>
      <button type="button" class="btn btn-primary" :disabled="releasing" data-test="confirm-release" @click="releaseSession">{{ t(releasing ? 'common.loading' : 'admin.riskControl.sessions.confirmRelease') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { riskSessionsAPI, type RiskSessionConfig, type RiskSessionDetail, type RiskSessionRecord } from '@/api/admin/riskSessions'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ (event: 'close'): void }>()
const { t } = useI18n()
const appStore = useAppStore()
const config = ref<RiskSessionConfig>({ enabled: false, snapshot_retention_days: 180 })
const configLoading = ref(false)
const configError = ref('')
const saving = ref(false)
const items = ref<RiskSessionRecord[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const listLoading = ref(false)
const listError = ref('')
const detailOpen = ref(false)
const detailID = ref(0)
const detail = ref<RiskSessionDetail | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
const releaseOpen = ref(false)
const releaseReason = ref('')
const releasing = ref(false)
let listSequence = 0
let detailSequence = 0
let configSequence = 0

const validRetention = computed(() => Number.isInteger(config.value.snapshot_retention_days) && config.value.snapshot_retention_days >= 1 && config.value.snapshot_retention_days <= 3650)
const messages = computed(() => {
  if (detail.value?.snapshot_status !== 'stored' || !detail.value.request_body) return []
  try {
    const body = JSON.parse(detail.value.request_body)
    const input: unknown = body.messages ?? body.input ?? body.contents
    if (typeof input === 'string') return [{ role: 'user', content: input }]
    if (!Array.isArray(input)) return []
    return input.filter((message) => message && typeof message === 'object').map((message) => ({
      role: typeof message.role === 'string' ? message.role : String(message.type ?? 'message'),
      content: typeof message.content === 'string' ? message.content : JSON.stringify(message.content ?? message.parts ?? message, null, 2),
    }))
  } catch { return [] }
})

watch(() => props.show, (show) => {
  if (show) {
    void loadConfig()
    void loadList(1)
  } else {
    configSequence++
    listSequence++
    closeDetail()
  }
}, { immediate: true })

function close() { emit('close') }
function closeDetail() {
  detailSequence++
  detailOpen.value = false
  releaseOpen.value = false
  detail.value = null
}
function date(value: string) { return formatDateTime(value) || '—' }
function userLabel(id: number | null) { return id ? `#${id}` : '—' }
function sourceLabel(source: RiskSessionRecord['source']) {
  return t(source === 'local_audit' ? 'admin.riskControl.sessions.localAudit' : 'admin.riskControl.sessions.upstreamCyber')
}
function blockLabel(row: RiskSessionRecord) {
  return t(row.blocked ? 'admin.riskControl.sessions.blocked' : row.released_at ? 'admin.riskControl.sessions.released' : 'admin.riskControl.sessions.notBlocked')
}
function scopeLabel(scope: RiskSessionRecord['snapshot_scope']) {
  if (scope === 'websocket_turn_only') return t('admin.riskControl.sessions.scopeWebsocket')
  if (scope === 'audit_payload_only') return t('admin.riskControl.sessions.scopeAudit')
  return t('admin.riskControl.sessions.scopeRequest')
}
function snapshotLabel(status: RiskSessionRecord['snapshot_status']) {
  const labels: Record<RiskSessionRecord['snapshot_status'], string> = {
    stored: 'admin.riskControl.sessions.stored',
    expired: 'admin.riskControl.sessions.expired',
    unavailable_encryption: 'admin.riskControl.sessions.encryptionUnavailable',
    unavailable_oversize: 'admin.riskControl.sessions.oversize',
    unavailable_empty: 'admin.riskControl.sessions.emptyBody',
    unavailable_storage: 'admin.riskControl.sessions.storageUnavailable',
    unreadable: 'admin.riskControl.sessions.unreadable',
  }
  return t(labels[status] ?? 'admin.riskControl.sessions.unavailable')
}
function responseScopeLabel(scope: RiskSessionRecord['response_scope']) {
  const labels: Record<NonNullable<RiskSessionRecord['response_scope']>, string> = {
    local_rejection_summary: 'admin.riskControl.sessions.responseScopeLocal',
    upstream_error_body_bounded: 'admin.riskControl.sessions.responseScopeBounded',
    upstream_error_event: 'admin.riskControl.sessions.responseScopeEvent',
    reconstructed_upstream_error_event: 'admin.riskControl.sessions.responseScopeReconstructed',
    upstream_error_summary: 'admin.riskControl.sessions.responseScopeSummary',
    upstream_error_summary_oversize: 'admin.riskControl.sessions.responseScopeOversize',
  }
  return t(scope && labels[scope] ? labels[scope] : 'admin.riskControl.sessions.responseScopeUnknown')
}
function prettyJSON(value: string) {
  try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value || '—' }
}
function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1048576).toFixed(1)} MB`
}
async function loadConfig() {
  const sequence = ++configSequence
  configLoading.value = true
  configError.value = ''
  try {
    const result = await riskSessionsAPI.getConfig()
    if (sequence === configSequence) config.value = result
  } catch (error) {
    if (sequence === configSequence) configError.value = extractApiErrorMessage(error, t('admin.riskControl.sessions.loadConfigFailed'))
  } finally {
    if (sequence === configSequence) configLoading.value = false
  }
}
async function saveConfig() {
  if (saving.value || !validRetention.value || configError.value) return
  saving.value = true
  const sequence = configSequence
  try {
    const result = await riskSessionsAPI.updateConfig({ ...config.value })
    if (sequence === configSequence) config.value = result
    appStore.showSuccess(t('admin.riskControl.sessions.saved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.riskControl.sessions.saveFailed')))
  } finally { saving.value = false }
}
async function loadList(nextPage: number) {
  const sequence = ++listSequence
  listLoading.value = true
  listError.value = ''
  try {
    const result = await riskSessionsAPI.list({ page: nextPage, page_size: pageSize })
    if (sequence !== listSequence) return
    items.value = result.items
    total.value = result.total
    page.value = result.page
  } catch (error) {
    if (sequence === listSequence) listError.value = extractApiErrorMessage(error, t('admin.riskControl.sessions.listFailed'))
  } finally {
    if (sequence === listSequence) listLoading.value = false
  }
}
async function openDetail(id: number) {
  const sequence = ++detailSequence
  detailID.value = id
  detailOpen.value = true
  detailLoading.value = true
  detail.value = null
  detailError.value = ''
  try {
    const result = await riskSessionsAPI.get(id)
    if (sequence === detailSequence) detail.value = result
  } catch (error) {
    if (sequence === detailSequence) detailError.value = extractApiErrorMessage(error, t('admin.riskControl.sessions.detailFailed'))
  } finally {
    if (sequence === detailSequence) detailLoading.value = false
  }
}
async function releaseSession() {
  if (!detail.value?.blocked || releasing.value) return
  const id = detail.value.id
  releasing.value = true
  try {
    const result = await riskSessionsAPI.release(id, releaseReason.value.trim())
    if (detail.value?.id === id) detail.value = { ...detail.value, ...result }
    items.value = items.value.map((item) => item.id === id ? { ...item, ...result } : item)
    releaseOpen.value = false
    appStore.showSuccess(t('admin.riskControl.sessions.releaseSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.riskControl.sessions.releaseFailed')))
  } finally { releasing.value = false }
}
</script>
