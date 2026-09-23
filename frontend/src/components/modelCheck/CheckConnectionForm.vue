<template>
  <div class="mc-form">
    <fieldset class="mc-fieldset">
      <legend class="mc-field-label">{{ t('modelCheck.chooseTopic') }}</legend>
      <div class="mc-topic-picker">
        <button v-for="(topic, index) in topics" :key="topic" type="button" :class="['mc-topic-option', { selected: form.topic === topic }]" :aria-pressed="form.topic === topic" @click="form.topic = topic">
          <span class="mc-topic-number">0{{ index + 1 }}</span><strong>{{ t(`modelCheck.topic.${topic}`) }}</strong><small>{{ t(`modelCheck.topicHint.${topic}`) }}</small>
        </button>
      </div>
    </fieldset>
    <fieldset class="mc-fieldset">
      <legend class="mc-field-label">{{ t('modelCheck.source') }}</legend>
      <div class="mc-segment">
        <button type="button" :class="{ selected: form.key_source === 'existing' }" @click="form.key_source = 'existing'">{{ t('modelCheck.existing') }}</button>
        <button type="button" :class="{ selected: form.key_source === 'external' }" @click="form.key_source = 'external'">{{ t('modelCheck.external') }}</button>
      </div>
      <label v-if="form.key_source === 'existing'" class="mc-field">
        <span>{{ t('modelCheck.selectKey') }}</span>
        <select v-model="form.key_id" :required="!savedKey"><option :value="null" disabled>{{ savedKey ? t('modelCheck.savedKey') : t('modelCheck.selectKey') }}</option><option v-for="key in keys" :key="key.id" :value="key.id">{{ key.name }}{{ key.group_name ? ` · ${key.group_name}` : '' }}</option></select>
        <small v-if="!keys.length && !savedKey">{{ t('modelCheck.noKeys') }}</small>
      </label>
      <template v-else>
        <label class="mc-field"><span>{{ t('modelCheck.externalGroupLabel') }}</span><input v-model.trim="form.group_name" type="text" :placeholder="t('modelCheck.externalGroupPlaceholder')" maxlength="120" /><small>{{ t('modelCheck.externalGroupHint') }}</small></label>
        <label class="mc-field"><span>{{ t('modelCheck.baseURL') }}</span><input v-model.trim="form.base_url" type="url" placeholder="https://api.example.com/v1" autocomplete="off" required /><small>{{ t('modelCheck.baseHint') }}</small></label>
        <label class="mc-field"><span>{{ t('modelCheck.key') }}</span><input v-model="form.key" type="password" :placeholder="savedKey ? t('modelCheck.savedKey') : 'sk-…'" autocomplete="new-password" spellcheck="false" :required="!savedKey" /></label>
      </template>
      <p class="mc-field-note"><Icon name="lock" size="xs" />{{ admin ? t('modelCheck.adminKeyHint') : t('modelCheck.keyHint') }}</p>
    </fieldset>
    <div class="mc-form-row">
      <label class="mc-field"><span>{{ t('modelCheck.model') }}</span><input v-model.trim="form.model" placeholder="gpt-6-astra" list="mc-model-options" required maxlength="160" /><datalist id="mc-model-options"><option value="gpt-6-astra" /><option value="gpt-5.6-sol" /><option value="gpt-5.6-terra" /></datalist></label>
      <label class="mc-field"><span>{{ t('modelCheck.reasoning') }}</span><select v-model="form.reasoning"><option value="default">{{ t('modelCheck.defaultReasoning') }}</option><option v-for="level in ['low', 'medium', 'high']" :key="level" :value="level">{{ t(`modelCheck.${level}`) }}</option></select></label>
    </div>
    <details class="mc-advanced">
      <summary>{{ t('modelCheck.advanced') }}<Icon name="chevronDown" size="xs" /></summary>
      <div class="mc-form-row">
        <label class="mc-field"><span>{{ t('modelCheck.protocol') }}</span><select v-model="form.protocol"><option value="responses">Responses</option><option value="chat">Chat Completions</option></select></label>
        <label class="mc-field"><span>{{ t('modelCheck.maxTokens') }}</span><input v-model.number="form.max_tokens" type="number" min="1024" max="16000" step="1" required /></label>
      </div>
    </details>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { CheckForm, CheckKey, CheckTopic } from '@/api/modelCheck'
const form = defineModel<CheckForm>({ required: true })
defineProps<{ keys: CheckKey[]; savedKey?: boolean; admin?: boolean }>()
const { t } = useI18n()
const topics: CheckTopic[] = ['pelican', 'creative']
</script>
