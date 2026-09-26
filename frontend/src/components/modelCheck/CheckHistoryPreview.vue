<template>
  <div @pointerover="pointerOver" @pointerout="pointerOut" @pointerdown="pointerDown" @focusin="focusIn" @focusout="hide" @click="hide">
    <slot />
    <Teleport to="body">
      <div v-if="visible" ref="panel" class="mc-history-preview" role="tooltip" :style="position" aria-live="polite">
        <p v-if="loading">{{ t('modelCheck.previewLoading') }}</p>
        <template v-else-if="preview">
          <img v-if="preview.thumbnail" :src="preview.thumbnail" :alt="t('modelCheck.preview')" @load="place" />
          <p v-else class="mc-history-preview-empty">{{ message || t('modelCheck.noPreview') }}</p>
          <div><strong>{{ t(`modelCheck.status.${preview.status}`) }}</strong><time>{{ date(preview.created_at) }}</time></div>
        </template>
        <p v-else>{{ message }}</p>
      </div>
    </Teleport>
  </div>
</template>
<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { checkErrorCode, modelCheckAPI, type CheckRun } from '@/api/modelCheck'
const { t, te, locale } = useI18n()
const auth = useAuthStore()
type Preview = Pick<CheckRun, 'status' | 'created_at' | 'thumbnail'> & { error?: string }
const cache = new Map<string, { value: Preview; at: number }>()
const visible = ref(false), loading = ref(false), preview = ref<Preview | null>(null), message = ref('')
const panel = ref<HTMLElement | null>(null), position = ref({ left: '8px', top: '8px' })
let anchor: HTMLElement | null = null, timer: ReturnType<typeof setTimeout> | undefined, sequence = 0, touchUntil = 0
const date = (value: number) => new Date(value).toLocaleString(locale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
const errorText = (code: string) => te(`modelCheck.errors.${code}`) ? t(`modelCheck.errors.${code}`) : t('modelCheck.previewUnavailable')
function hide() { sequence++; clearTimeout(timer); visible.value = false; anchor = null }
function target(event: Event) { return event.target instanceof Element ? event.target.closest<HTMLElement>('button[data-preview-run]') : null }
function place() {
  if (!anchor || !panel.value) return
  const rect = anchor.getBoundingClientRect(), box = panel.value.getBoundingClientRect()
  const width = window.innerWidth, height = window.innerHeight
  const left = Math.max(8, Math.min(rect.left + rect.width / 2 - box.width / 2, width - box.width - 8))
  const top = rect.top >= box.height + 16 ? rect.top - box.height - 8 : Math.max(8, Math.min(rect.bottom + 8, height - box.height - 8))
  position.value = { left: `${left}px`, top: `${top}px` }
}
async function show(element: HTMLElement) {
  if (anchor === element) return
  hide(); anchor = element
  const current = sequence, id = element.dataset.previewRun || '', key = id + ':' + (element.dataset.previewStatus || '')
  timer = setTimeout(async () => {
    visible.value = true; loading.value = true; preview.value = null; message.value = ''
    await nextTick(); place()
    try {
      let record = cache.get(key)
      if (record && Date.now() - record.at > 60_000) { cache.delete(key); record = undefined }
      let value = record?.value
      if (!value) {
        const run = await modelCheckAPI.run(id)
        if (current !== sequence) return
        value = { status: run.status, created_at: run.created_at, thumbnail: run.status !== 'failed' && typeof run.thumbnail === 'string' && run.thumbnail.length <= 500_000 ? run.thumbnail : null, error: run.error?.split(':')[0] }
        if (['normal', 'review', 'failed'].includes(run.status)) {
          if (cache.size >= 20) cache.delete(cache.keys().next().value!)
          cache.set(key, { value, at: Date.now() })
        }
      }
      if (current !== sequence) return
      preview.value = value
      message.value = value.error ? errorText(value.error) : ''
    } catch (error) { if (current === sequence) message.value = errorText(checkErrorCode(error)) }
    finally { if (current === sequence) { loading.value = false; await nextTick(); place() } }
  }, 300)
}
function pointerOver(event: PointerEvent) { if (event.pointerType === 'touch') return; const element = target(event); if (element) void show(element) }
function pointerOut(event: PointerEvent) { if (anchor && !(event.relatedTarget instanceof Node && anchor.contains(event.relatedTarget))) hide() }
function pointerDown(event: PointerEvent) { if (event.pointerType === 'touch') touchUntil = Date.now() + 1000; hide() }
function focusIn(event: FocusEvent) { if (Date.now() < touchUntil) return; const element = target(event); if (element) void show(element) }
function keyDown(event: KeyboardEvent) { if (event.key === 'Escape') hide() }
watch(() => [auth.user?.id, auth.token], () => { cache.clear(); hide() }, { flush: 'sync' })
onMounted(() => { window.addEventListener('keydown', keyDown); window.addEventListener('resize', hide); window.addEventListener('scroll', hide, true) })
onUnmounted(() => { hide(); cache.clear(); window.removeEventListener('keydown', keyDown); window.removeEventListener('resize', hide); window.removeEventListener('scroll', hide, true) })
</script>
<style>
.mc-history-preview{position:fixed;z-index:100;pointer-events:none;width:280px;max-width:calc(100vw - 16px);max-height:calc(100vh - 16px);overflow:hidden;border:1px solid #dbe5dd;border-radius:12px;background:#fff;color:#456150;box-shadow:0 12px 36px #183c3029;font-size:12px;line-height:1.6}
.mc-history-preview img{display:block;width:100%;aspect-ratio:1.6;object-fit:contain;background:#f3f7f1}
.mc-history-preview>div{display:flex;justify-content:space-between;gap:8px;flex-wrap:wrap;padding:10px 12px}.mc-history-preview strong{font-weight:550}.mc-history-preview time{color:#798d7e;font-size:11px}.mc-history-preview>p{padding:18px 14px}.mc-history-preview-empty{min-height:90px;display:flex;align-items:center}
.dark .mc-history-preview{background:#243328;color:#c4d6c9;border-color:#415745}.dark .mc-history-preview time{color:#a5baa9}
</style>
