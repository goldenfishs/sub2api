import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import RiskSessionDialog from '../RiskSessionDialog.vue'
import type { RiskSessionDetail, RiskSessionRecord } from '@/api/admin/riskSessions'

const { getConfig, updateConfig, list, get, release, showSuccess, showError } = vi.hoisted(() => ({
  getConfig: vi.fn(), updateConfig: vi.fn(), list: vi.fn(), get: vi.fn(), release: vi.fn(), showSuccess: vi.fn(), showError: vi.fn(),
}))
vi.mock('@/api/admin/riskSessions', () => ({ riskSessionsAPI: { getConfig, updateConfig, list, get, release } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('@/utils/apiError', () => ({ extractApiErrorMessage: (_error: unknown, fallback: string) => fallback }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const { default: messages } = await import('@/i18n/locales/zh')
  return { ...actual, useI18n: () => ({ t: (key: string, values: Record<string, unknown> = {}) => {
    let value: unknown = messages
    for (const segment of key.split('.')) value = (value as Record<string, unknown>)?.[segment]
    return String(value ?? key).replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? `{${name}}`))
  } }) }
})

const BaseDialogStub = defineComponent({
  props: { show: Boolean, title: String },
  emits: ['close'],
  template: '<section v-if="show" role="dialog" :aria-label="title"><slot /><footer><slot name="footer" /></footer></section>',
})
const PaginationStub = defineComponent({
  props: ['page', 'total'], emits: ['update:page'],
  template: '<button data-test="next-page" @click="$emit(\'update:page\', page + 1)">next</button>',
})
const record = (overrides: Partial<RiskSessionRecord> = {}): RiskSessionRecord => ({
  id: 42, created_at: '2026-10-24T10:00:00Z', user_id: 7, user_email: 'user@example.test',
  api_key_id: 12, api_key_name: 'CLI', source: 'local_audit', reason: 'Test audit hit',
  model: 'test-model', endpoint: '/v1/responses', blocked: true, snapshot_status: 'stored',
  request_bytes: 128, response_bytes: 0, snapshot_scope: 'received_request_only',
  snapshot_expires_at: '2027-04-22T10:00:00Z', ...overrides,
})
const detailRecord = (overrides: Partial<RiskSessionDetail> = {}): RiskSessionDetail => ({
  ...record(), request_body: JSON.stringify({ input: [{ role: 'user', content: '<img src=x onerror=alert(1)>' }] }),
  response_body: '', ...overrides,
})
function mountDialog(show = true) {
  return mount(RiskSessionDialog, {
    props: { show },
    global: {
      stubs: { BaseDialog: BaseDialogStub, Pagination: PaginationStub, Icon: true },
    },
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  getConfig.mockResolvedValue({ enabled: true, snapshot_retention_days: 180 })
  updateConfig.mockImplementation(async (config) => config)
  list.mockResolvedValue({ items: [record()], total: 21, page: 1, page_size: 20 })
  get.mockResolvedValue(detailRecord())
  release.mockResolvedValue(record({ blocked: false, released_at: '2026-10-24T11:00:00Z', release_reason: 'Reviewed' }))
})

describe('RiskSessionDialog', () => {
  it('loads only when opened and validates retention before saving config', async () => {
    const wrapper = mountDialog(false)
    expect(getConfig).not.toHaveBeenCalled()
    expect(list).not.toHaveBeenCalled()
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(list).toHaveBeenCalledWith({ page: 1, page_size: 20 })
    expect(wrapper.get('[data-test="session-enabled"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('[data-test="session-retention"]').setValue(0)
    await wrapper.get('form').trigger('submit')
    expect(updateConfig).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="save-session-config"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="session-retention"]').setValue(3651)
    await wrapper.get('form').trigger('submit')
    expect(updateConfig).not.toHaveBeenCalled()
    await wrapper.get('[data-test="session-retention"]').setValue(90)
    await wrapper.get('[data-test="session-enabled"]').trigger('click')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateConfig).toHaveBeenCalledWith({ enabled: false, snapshot_retention_days: 90 })
    expect(showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not offer default settings for saving when config could not load', async () => {
    getConfig.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.find('[data-test="save-session-config"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('加载会话封锁设置失败')
    expect(wrapper.text()).toContain('user@example.test')
    wrapper.unmount()
  })

  it('renders raw request content as text and identifies the actual saved scope', async () => {
    get.mockResolvedValueOnce(detailRecord({ snapshot_scope: 'websocket_turn_only', response_scope: 'local_rejection_summary', response_body: '<script>alert(1)</script>' }))
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-test="session-detail-42"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="request-body"]').text()).toContain('<img src=x onerror=alert(1)>')
    expect(wrapper.get('[data-test="response-body"]').text()).toContain('<script>alert(1)</script>')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.get('[data-test="snapshot-scope"]').text()).toContain('WebSocket 当轮消息')
    expect(wrapper.get('[data-test="response-scope"]').text()).toContain('本地审计拒绝摘要，不是上游响应')
    expect(wrapper.text()).toContain('不包含客户端未上传、已压缩或删除的历史')
    wrapper.unmount()
  })

  it('requires confirmation before release, keeps archived bodies, and does not imply an account unban', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-test="session-detail-42"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="release-session"]').trigger('click')
    expect(release).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('不会解封用户账号')
    await wrapper.get('[data-test="release-reason"]').setValue('  Reviewed  ')
    await wrapper.get('[data-test="confirm-release"]').trigger('click')
    await flushPromises()
    expect(release).toHaveBeenCalledWith(42, 'Reviewed')
    expect(wrapper.find('[data-test="release-session"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="request-body"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('已解除')
    expect(showSuccess).toHaveBeenCalledWith('已解除会话封锁；用户账号状态未改变')
    wrapper.unmount()
  })

  it('keeps an expired snapshot separate from the permanent block', async () => {
    get.mockResolvedValueOnce(detailRecord({ snapshot_status: 'expired' }))
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-test="session-detail-42"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="snapshot-unavailable"]').text()).toContain('正文已到期清理')
    expect(wrapper.find('[data-test="request-body"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="release-session"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('正文到期清理不解除封锁')
    expect(wrapper.text()).toContain('调整保留时间仅影响新快照')
    wrapper.unmount()
  })

  it.each([
    ['upstream_error_body_bounded', '受长度上限限制'],
    ['upstream_error_event', '上游拒绝事件'],
    ['reconstructed_upstream_error_event', '根据上游错误重建的事件'],
    ['upstream_error_summary', '上游错误摘要'],
    ['upstream_error_summary_oversize', '超出上限，仅保存摘要'],
  ] as const)('labels %s response evidence without implying a full stream', async (scope, label) => {
    get.mockResolvedValueOnce(detailRecord({ response_scope: scope, response_body: '{"code":"cyber_policy"}' }))
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-test="session-detail-42"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="response-scope"]').text()).toContain(label)
    expect(wrapper.get('[data-test="response-body"]').text()).toContain('cyber_policy')
    wrapper.unmount()
  })

  it('keeps a session blocked when the snapshot could not be stored', async () => {
    get.mockResolvedValueOnce(detailRecord({ snapshot_status: 'unavailable_storage' }))
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-test="session-detail-42"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="snapshot-unavailable"]').text()).toBe('正文存储失败')
    expect(wrapper.find('[data-test="release-session"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="request-body"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('loads subsequent pages and ignores a detail response from a previously closed dialog', async () => {
    let resolveOld: (detail: RiskSessionDetail) => void = () => undefined
    get.mockImplementationOnce(() => new Promise<RiskSessionDetail>((resolve) => { resolveOld = resolve }))
    const wrapper = mountDialog()
    await flushPromises()
    list.mockResolvedValueOnce({ items: [record({ id: 43 })], total: 21, page: 2, page_size: 20 })
    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith({ page: 2, page_size: 20 })
    await wrapper.get('[data-test="session-detail-43"]').trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    get.mockResolvedValueOnce(detailRecord({ reason: 'New active request' }))
    await wrapper.get('[data-test="session-detail-42"]').trigger('click')
    await flushPromises()
    resolveOld(detailRecord({ id: 43, reason: 'Stale request must not display' }))
    await flushPromises()
    expect(wrapper.text()).toContain('New active request')
    expect(wrapper.text()).not.toContain('Stale request must not display')
    wrapper.unmount()
  })
})
