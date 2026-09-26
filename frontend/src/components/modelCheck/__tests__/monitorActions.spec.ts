import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import ModelCheckSettingsView from '@/views/admin/ModelCheckSettingsView.vue'
import CheckRunDialog from '../CheckRunDialog.vue'
import type { CheckChannel, CheckRun } from '@/api/modelCheck'

const { api, auth } = vi.hoisted(() => ({
  api: { channels: vi.fn(), run: vi.fn(), deleteChannel: vi.fn(), review: vi.fn(), baseline: vi.fn(), keys: vi.fn() },
  auth: { isAdmin: true },
}))
vi.mock('@/api/modelCheck', () => ({
  modelCheckAPI: api,
  isCheckRunning: (status: string) => ['queued', 'generating', 'rendering'].includes(status),
  checkErrorCode: (error: { message?: string }) => error.message || 'internal_error',
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', async () => {
  const { ref } = await import('vue')
  const messages = (await import('@/i18n/locales/zh/modelCheck')).default
  return {
    useI18n: () => ({
      locale: ref('zh-CN'), te: () => true,
      t: (key: string, values: Record<string, unknown> = {}) => {
        const value = key.split('.').reduce<unknown>((item, part) => (item as Record<string, unknown>)?.[part], messages)
        return String(value ?? key).replace(/\{(\w+)\}/g, (_match, name: string) => String(values[name] ?? ''))
      },
    }),
  }
})

const DialogStub = defineComponent({
  props: ['show', 'title'], emits: ['close'],
  template: '<section v-if="show" role="dialog" :aria-label="title"><h2>{{ title }}</h2><slot /><footer><slot name="footer" /></footer></section>',
})
const global = { stubs: { BaseDialog: DialogStub, Icon: true, RouterLink: { template: '<a><slot /></a>' }, CheckConnectionForm: true, CheckGroupBadge: true } }
const completed = (): CheckRun => ({
  id: 'older-artwork', channel_id: 'monitor-1', source: 'manual', status: 'normal', model: 'fixture-model',
  topic: 'pelican', protocol: 'responses', reasoning: 'default', max_tokens: 8000,
  created_at: Date.UTC(2026, 8, 24, 1), prompt_hash: 'fixture-prompt-hash', prompt_version: 'v1',
  image: 'data:image/png;base64,fixture', thumbnail: 'data:image/png;base64,fixture-thumb', quality_review: null,
  assessment: { score: 100, verdict: 'normal', delta: null, reasons: [], dimensions: [], semantic_review: 'manual', method: 'rules' },
})
const monitor = (): CheckChannel => ({
  id: 'monitor-1', name: 'Fixture monitor', model: 'fixture-model', topic: 'pelican', protocol: 'responses', reasoning: 'default', max_tokens: 8000,
  enabled: true, public: true, interval_minutes: 30, next_run: Date.UTC(2026, 8, 24, 3), baseline_id: null, demo: false,
  latest: { ...completed(), id: 'newer-failure', status: 'failed', created_at: Date.UTC(2026, 8, 24, 2), image: null, thumbnail: null, assessment: undefined },
  preview: completed(), history: [],
})
function button(wrapper: VueWrapper, text: string) {
  const found = wrapper.findAll('button').find(node => node.text() === text)
  if (!found) throw new Error(`Button not found: ${text}`)
  return found
}

enableAutoUnmount(afterEach)
afterEach(() => vi.useRealTimers())
beforeEach(() => {
  vi.clearAllMocks()
  auth.isAdmin = true
  api.channels.mockResolvedValue([monitor()])
  api.run.mockResolvedValue(completed())
})

describe('monitor management actions', () => {
  it('opens the dated completed preview rather than the newer failure, and deletes only after explicit confirmation', async () => {
    const wrapper = mount(ModelCheckSettingsView, { global })
    await flushPromises()
    expect(wrapper.text()).toContain('请求 / 检测失败')
    const preview = wrapper.get('button[aria-label^="最近预览"]')
    expect(preview.get('time').attributes('datetime')).toBe(new Date(completed().created_at).toISOString())
    await preview.trigger('click')
    await flushPromises()
    expect(api.run).toHaveBeenCalledWith('older-artwork')
    expect(api.run).not.toHaveBeenCalledWith('newer-failure')
    wrapper.findComponent(CheckRunDialog).vm.$emit('close')
    await flushPromises()

    await button(wrapper, '删除任务').trigger('click')
    expect(api.deleteChannel).not.toHaveBeenCalled()
    const confirmation = wrapper.get('[role="dialog"][aria-label="删除任务"]')
    expect(confirmation.text()).toContain('永久删除该任务的历史记录和基准作品')
    expect(confirmation.text()).toContain('不会删除原模型 API Key')
    await button(wrapper, '取消').trigger('click')
    expect(api.deleteChannel).not.toHaveBeenCalled()
    await button(wrapper, '删除任务').trigger('click')
    api.deleteChannel.mockResolvedValue({ deleted: true })
    api.channels.mockResolvedValue([])
    await button(wrapper, '确认删除').trigger('click')
    await flushPromises()
    expect(api.deleteChannel).toHaveBeenCalledTimes(1)
    expect(api.deleteChannel).toHaveBeenCalledWith('monitor-1')
    expect(wrapper.text()).toContain('监测任务已删除')
    expect(wrapper.find('article').exists()).toBe(false)
  })

  it('discards a poll started before deletion and refreshes even while that old request is pending', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const wrapper = mount(ModelCheckSettingsView, { global })
    await flushPromises()
    let resolveStale!: (channels: CheckChannel[]) => void
    let resolveFresh!: (channels: CheckChannel[]) => void
    api.channels.mockImplementationOnce(() => new Promise<CheckChannel[]>(resolve => { resolveStale = resolve }))
    await vi.advanceTimersByTimeAsync(8000)
    expect(api.channels).toHaveBeenCalledTimes(2)

    api.channels.mockImplementationOnce(() => new Promise<CheckChannel[]>(resolve => { resolveFresh = resolve }))
    api.deleteChannel.mockResolvedValue({ deleted: true })
    await button(wrapper, '删除任务').trigger('click')
    await button(wrapper, '确认删除').trigger('click')
    await flushPromises()
    expect(api.channels).toHaveBeenCalledTimes(3)
    expect(wrapper.find('article').exists()).toBe(false)

    resolveStale([monitor()])
    await flushPromises()
    expect(wrapper.find('article').exists()).toBe(false)
    resolveFresh([])
    await flushPromises()
    expect(wrapper.text()).toContain('监测任务已删除')
    expect(wrapper.find('article').exists()).toBe(false)
  })

  it('cannot delete a monitor while its latest test is running', async () => {
    const channel = monitor()
    channel.latest = { ...completed(), status: 'generating' }
    api.channels.mockResolvedValue([channel])
    const wrapper = mount(ModelCheckSettingsView, { global })
    await flushPromises()
    expect(button(wrapper, '删除任务').attributes('disabled')).toBeDefined()
    await button(wrapper, '删除任务').trigger('click')
    expect(wrapper.find('[role="dialog"][aria-label="删除任务"]').exists()).toBe(false)
    expect(api.deleteChannel).not.toHaveBeenCalled()
  })
})

describe('artwork quality review', () => {
  it('lets an administrator mark monitor quality and updates the displayed verdict from the saved result', async () => {
    const wrapper = mount(CheckRunDialog, { props: { runId: 'older-artwork' }, global })
    await flushPromises()
    expect(wrapper.text()).toContain('质量待复核')
    expect(api.review).not.toHaveBeenCalled()
    api.review.mockResolvedValue({ ...completed(), quality_review: { verdict: 'degraded', reviewed_at: Date.now() } })
    await button(wrapper, '标记明显退步').trigger('click')
    await flushPromises()
    expect(api.review).toHaveBeenCalledTimes(1)
    expect(api.review).toHaveBeenCalledWith('older-artwork', 'degraded')
    expect(wrapper.text()).toContain('人工标记退步')
    expect(wrapper.text()).toContain('复核结果已保存')
    expect(wrapper.text()).toContain('基础检查通过')
    expect(button(wrapper, '标记明显退步').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').some(node => node.text() === '设为基准')).toBe(false)
  })

  it.each([
    { name: 'a non-administrator viewing a monitor', isAdmin: false, channelId: 'monitor-1' },
    { name: 'an administrator viewing a personal self-test', isAdmin: true, channelId: null },
  ])('hides quality mutation controls for $name', async ({ isAdmin, channelId }) => {
    auth.isAdmin = isAdmin
    api.run.mockResolvedValue({ ...completed(), channel_id: channelId, source: channelId ? 'manual' : 'self' })
    const wrapper = mount(CheckRunDialog, { props: { runId: 'older-artwork' }, global })
    await flushPromises()
    expect(wrapper.text()).toContain('质量待复核')
    expect(wrapper.findAll('button').some(node => ['确认正常', '标记明显退步', '撤销复核'].includes(node.text()))).toBe(false)
    expect(api.review).not.toHaveBeenCalled()
  })
})

it('shows a scheduled retry and its failed attempt without claiming a completed result', async () => {
  api.run.mockResolvedValue({
    ...completed(), status: 'generating', assessment: undefined, image: null, thumbnail: null,
    attempt_count: 1, max_attempts: 3, retry_at: Date.now() + 5000,
    attempts: [{ attempt: 1, started_at: 1000, finished_at: 2100, duration_ms: 1100, error: 'upstream_http:502', usage: null }],
  })
  const wrapper = mount(CheckRunDialog, { props: { runId: 'retry-run' }, global })
  await flushPromises()
  expect(wrapper.text()).toContain('已尝试 1 / 3 次')
  expect(wrapper.text()).toContain('自动进行第 2 / 3 次尝试')
  expect(wrapper.text()).toContain('第 1 次 · 输出上限 8,000 · 1.1 秒')
  expect(wrapper.text()).toContain('失败请求也可能产生费用')
  expect(wrapper.text()).not.toContain('作品质量复核')
})

describe('output token limit retry details', () => {
  const failed = () => ({ ...completed(), status: 'failed' as const, assessment: undefined, image: null, thumbnail: null, error: 'truncated_output' })

  it('does not claim an old single-attempt token-limit failure was automatically upgraded', async () => {
    api.run.mockResolvedValue({ ...failed(), attempt_count: 1, max_attempts: 3, attempts: [{ attempt: 1, started_at: 1000, finished_at: 2100, duration_ms: 1100, error: 'truncated_output' }] })
    const wrapper = mount(CheckRunDialog, { props: { runId: 'old-failed-run' }, global })
    await flushPromises()
    expect(wrapper.text()).toContain('Token')
    expect(wrapper.text()).not.toMatch(/已.{0,12}(?:提升|提高|升至|增加)/)
    expect(wrapper.text()).not.toContain('自动进行第 2')
    expect(wrapper.text()).not.toMatch(/16,?000/)
    expect(wrapper.text()).toContain('第 1 次 · 输出上限 8,000')
  })

  it('explains the next higher limit while keeping the failed attempt at its original cap', async () => {
    api.run.mockResolvedValue({
      ...failed(), status: 'generating', error: undefined,
      max_tokens: 8000, effective_max_tokens: 8000, retry_max_tokens: 16000,
      attempt_count: 1, max_attempts: 3, retry_at: Date.now() + 5000, last_attempt_error: 'truncated_output',
      attempts: [{ attempt: 1, max_tokens: 8000, started_at: 1000, finished_at: 2100, duration_ms: 1100, error: 'truncated_output', usage: null }],
    })
    const wrapper = mount(CheckRunDialog, { props: { runId: 'awaiting-higher-cap' }, global })
    await flushPromises()
    expect(wrapper.text()).toContain('自动进行第 2 / 3 次尝试')
    expect(wrapper.text()).toMatch(/16,?000/)
    expect(wrapper.text()).toMatch(/(?:提升|提高|升至|增加)/)
    const firstAttempt = wrapper.findAll('li').find(item => item.text().includes('第 1 次'))
    expect(firstAttempt?.text()).toMatch(/8,?000/)
    expect(firstAttempt?.text()).not.toMatch(/16,?000/)
    expect(wrapper.text()).not.toContain('作品质量复核')
  })

  it('shows the actual cap for each completed attempt after a successful upgrade', async () => {
    api.run.mockResolvedValue({
      ...completed(), max_tokens: 8000, effective_max_tokens: 16000, retry_max_tokens: null,
      attempt_count: 2, max_attempts: 3, retry_at: null,
      attempts: [
        { attempt: 1, max_tokens: 8000, started_at: 1000, finished_at: 2100, duration_ms: 1100, error: 'truncated_output', usage: null },
        { attempt: 2, max_tokens: 16000, started_at: 5000, finished_at: 10000, duration_ms: 5000, error: null, usage: { input_tokens: 10, output_tokens: 9000 } },
      ],
    })
    const wrapper = mount(CheckRunDialog, { props: { runId: 'completed-upgrade' }, global })
    await flushPromises()
    const rows = wrapper.findAll('li')
    expect(rows.find(item => item.text().includes('第 1 次'))?.text()).toMatch(/8,?000/)
    expect(rows.find(item => item.text().includes('第 2 次'))?.text()).toMatch(/16,?000/)
    expect(rows.find(item => item.text().includes('第 2 次'))?.text()).toContain('生成完成')
    expect(wrapper.text()).toContain('基础检查通过')
    expect(wrapper.text()).not.toContain('自动进行第 3')
  })

  it('shows comparison as reference only when effective output limits differ despite matching original caps', async () => {
    api.run.mockImplementation(async (id: string) => ({ ...completed(), id, max_tokens: 8000, ...(id === 'upgraded-run' ? { effective_max_tokens: 16000 } : {}) }))
    const wrapper = mount(CheckRunDialog, { props: { runId: 'upgraded-run', baselineId: 'legacy-baseline' }, global })
    await flushPromises()
    await button(wrapper, '对比基准').trigger('click')
    expect(wrapper.text()).toContain('不计算基准分差')
  })
})
