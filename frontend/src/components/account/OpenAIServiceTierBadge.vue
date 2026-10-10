<template>
  <div v-if="account.platform === 'openai'" class="flex flex-wrap items-center gap-1 text-[10px]" @click.stop>
    <span v-for="tier in tiers" :key="tier.id" :title="tier.title"
      class="rounded px-1.5 py-0.5" :class="tier.className">{{ tier.label }} · {{ tier.status }}</span>
    <button type="button" class="rounded px-1 text-primary-600 hover:bg-primary-50 disabled:opacity-50"
      :disabled="busy" :title="t('admin.accounts.tierProbe.description')" @click="probe">
      {{ busy ? t('admin.accounts.tierProbe.queued') : t('admin.accounts.tierProbe.recheck') }}
    </button>
    <span v-if="notice" role="status" class="w-full text-gray-500">{{ notice }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'
import { serviceTierBadgeState } from '@/utils/serviceTierCapability'

const props = defineProps<{ account: { id: number; platform: string; extra?: Record<string, unknown> | null } }>()
const { t } = useI18n()
const busy = ref(false)
const notice = ref('')
const tiers = computed(() => ['priority', 'ultrafast'].map(id => {
  const state = serviceTierBadgeState(props.account.extra, id)
  const label = id === 'priority' ? 'Fast' : 'Ultrafast'
  return {
    id, label, status: t(`admin.accounts.tierProbe.${state.status}`),
    title: [t('admin.accounts.tierProbe.description'), ...state.results.map(r =>
      `${r.model}: ${t(`admin.accounts.tierProbe.${r.status}`)} · ${r.observed_tier || '—'} · ${r.checked_at}`
    )].join('\n'),
    className: state.status === 'supported' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-400'
      : state.status === 'unsupported' ? 'bg-red-50 text-red-600 dark:bg-red-900/20'
      : 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-400'
  }
}))
async function probe() {
  busy.value = true
  notice.value = ''
  try {
    // Queue both official Ultrafast models. A model not enabled on this account
    // is rejected independently without preventing the other check.
    const results = await Promise.allSettled(['gpt-6-astra', 'gpt-6.1-sol'].map(model =>
      apiClient.post(`/admin/accounts/${props.account.id}/probe-service-tiers`, { model })
    ))
    notice.value = t(results.some(r => r.status === 'fulfilled' && r.value.data?.queued === true) ? 'admin.accounts.tierProbe.queued' : 'admin.accounts.tierProbe.error')
  } catch {
    notice.value = t('admin.accounts.tierProbe.error')
  } finally {
    busy.value = false
  }
}
</script>
