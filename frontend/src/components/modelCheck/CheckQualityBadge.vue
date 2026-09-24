<template>
  <span :class="['mc-status', review?.verdict === 'degraded' ? 'is-review' : review?.verdict === 'normal' ? 'is-normal' : 'is-paused']">
    {{ t(review?.verdict === 'degraded' ? 'modelCheck.qualityDegraded' : review?.verdict === 'normal' ? 'modelCheck.qualityNormal' : 'modelCheck.qualityPending') }}
  </span>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CheckRun } from '@/api/modelCheck'
defineProps<{ review?: CheckRun['quality_review'] }>()
const { t } = useI18n()
</script>
