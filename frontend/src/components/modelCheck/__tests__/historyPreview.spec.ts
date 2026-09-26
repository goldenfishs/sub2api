import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import CheckHistoryPreview from '../CheckHistoryPreview.vue'
const { run } = vi.hoisted(() => ({ run: vi.fn() }))
vi.mock('@/api/modelCheck', () => ({ modelCheckAPI: { run }, checkErrorCode: () => 'not_found' }))
vi.mock('@/stores/auth', async () => { const { reactive } = await import('vue'); const state = reactive({ user: { id: 1 }, token: 'fixture-one' }); return { useAuthStore: () => state } })
vi.mock('vue-i18n', async () => { const { ref } = await import('vue'); return { useI18n: () => ({ t: (key: string) => key, te: () => true, locale: ref('en') }) } })
import { useAuthStore } from '@/stores/auth'
const result = (id: string, status = 'normal') => ({ id, status, created_at: 1, thumbnail: `data:image/png;base64,${id}`, html: 'must not retain', image: 'must not retain', error: status === 'failed' ? 'upstream_timeout' : undefined })
const Host = defineComponent({ components: { CheckHistoryPreview }, emits: ['open'], template: '<CheckHistoryPreview><button data-preview-run="a" data-preview-status="normal" @click="$emit(\'open\', \'a\')">A</button><button data-preview-run="b" data-preview-status="normal">B</button></CheckHistoryPreview>' })
const tooltip = () => document.querySelector('[role="tooltip"]')
async function wait() { await vi.advanceTimersByTimeAsync(300); await flushPromises() }
enableAutoUnmount(afterEach)
afterEach(() => { vi.useRealTimers(); document.body.innerHTML = '' })
beforeEach(() => { vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); run.mockReset(); run.mockImplementation(async id => result(id)) })

it('debounces brief visits, supports focus and Escape, and preserves the original click', async () => {
  const wrapper = mount(Host, { attachTo: document.body })
  const a = wrapper.get('button')
  await a.trigger('pointerover'); await vi.advanceTimersByTimeAsync(100); await a.trigger('pointerout'); await wait()
  expect(run).not.toHaveBeenCalled(); expect(tooltip()).toBeNull()
  await a.trigger('focusin'); await wait()
  expect(run).toHaveBeenCalledWith('a'); expect(tooltip()?.querySelector('img')?.getAttribute('src')).toContain(',a')
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })); await flushPromises(); expect(tooltip()).toBeNull()
  await a.trigger('click'); expect(wrapper.emitted('open')).toEqual([['a']]); expect(tooltip()).toBeNull()
})

it('never displays a late response from the previously hovered record', async () => {
  const resolvers = new Map<string, (value: unknown) => void>()
  run.mockImplementation(id => new Promise(resolve => resolvers.set(id, resolve)))
  const wrapper = mount(Host, { attachTo: document.body }); const [a, b] = wrapper.findAll('button')
  await a.trigger('pointerover'); await wait(); await a.trigger('pointerout'); await b.trigger('pointerover'); await wait()
  resolvers.get('b')!(result('b')); await flushPromises(); expect(tooltip()?.querySelector('img')?.getAttribute('src')).toContain(',b')
  resolvers.get('a')!(result('a')); await flushPromises(); expect(tooltip()?.querySelector('img')?.getAttribute('src')).toContain(',b')
  await b.trigger('pointerout'); expect(tooltip()).toBeNull()
})

it('shows failures without an image and handles inaccessible records without caching them', async () => {
  run.mockResolvedValueOnce(result('a', 'failed')).mockRejectedValueOnce(new Error('private'))
  const wrapper = mount(Host, { attachTo: document.body }); const [a, b] = wrapper.findAll('button')
  await a.trigger('pointerover'); await wait(); expect(tooltip()?.querySelector('img')).toBeNull(); expect(tooltip()?.textContent).toContain('upstream_timeout')
  await a.trigger('pointerout'); await b.trigger('pointerover'); await wait(); expect(tooltip()?.textContent).toContain('not_found'); expect(tooltip()?.querySelector('img')).toBeNull()
})

it('clears private previews and cached results when the authenticated account changes', async () => {
  const wrapper = mount(Host, { attachTo: document.body }); const a = wrapper.get('button')
  await a.trigger('pointerover'); await wait(); await a.trigger('pointerout'); await a.trigger('pointerover'); await wait(); expect(run).toHaveBeenCalledTimes(1)
  useAuthStore().user!.id = 2; await flushPromises(); expect(tooltip()).toBeNull()
  await a.trigger('pointerover'); await wait(); expect(run).toHaveBeenCalledTimes(2)
})

it('does not create a hover request for touch interactions', async () => {
  const wrapper = mount(Host, { attachTo: document.body }); const a = wrapper.get('button')
  for (const type of ['pointerover', 'pointerdown']) { const event = new Event(type, { bubbles: true }); Object.defineProperty(event, 'pointerType', { value: 'touch' }); a.element.dispatchEvent(event) }
  await a.trigger('focusin'); await a.trigger('click'); await wait()
  expect(run).not.toHaveBeenCalled(); expect(wrapper.emitted('open')).toEqual([['a']]); expect(tooltip()).toBeNull()
})

it('shows the no-preview fallback for a completed record without a thumbnail', async () => {
  run.mockResolvedValueOnce({ ...result('a'), thumbnail: null })
  const wrapper = mount(Host, { attachTo: document.body })
  await wrapper.get('button').trigger('pointerover'); await wait()
  expect(tooltip()?.querySelector('img')).toBeNull()
  expect(tooltip()?.textContent).toContain('modelCheck.noPreview')
})
