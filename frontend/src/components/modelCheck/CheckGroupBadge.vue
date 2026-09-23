<template>
  <span class="mc-group-badge" :title="label">
    <span class="mc-group-prefix">{{ t(external && !groupName ? 'modelCheck.groupSource' : 'modelCheck.groupLabel') }}</span>
    <span class="mc-group-name">{{ label }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  groupName?: string | null
  groupId?: number | null
  keySource?: 'existing' | 'external' | null
  demo?: boolean
}>()
const { t } = useI18n()
const external = computed(() => !props.demo && props.keySource === 'external')
const label = computed(() => {
  if (props.demo) return t('modelCheck.groupDemo')
  if (props.groupName) return props.groupName
  if (external.value) return t('modelCheck.groupExternal')
  if (props.groupId) return '#' + props.groupId
  return t(props.keySource === 'existing' ? 'modelCheck.groupUnassigned' : 'modelCheck.groupUnknown')
})
</script>

<style scoped>
.mc-group-badge { display: inline-flex; align-items: center; max-width: 100%; min-width: 0; gap: 6px; padding: 4px 8px; border: 1px solid #deebe3; border-radius: 6px; background: #f3f8f4; color: #466b54; font-size: 11px; line-height: 1.4; vertical-align: middle; }
.mc-group-prefix { color: #7d9083; flex-shrink: 0; padding-right: 6px; border-right: 1px solid #d7e3da; font-size: 10px; }
.mc-group-name { min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
:global(.mc-work-body) > .mc-group-badge { margin-top: 10px; }
:global(.dark) .mc-group-badge { background: #25382b; border-color: #3b5140; color: #b1ccba; }
:global(.dark) .mc-group-prefix { color: #8ba694; border-color: #425a48; }
</style>
