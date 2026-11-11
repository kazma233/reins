<script setup lang="ts">
import SourceRow from "./SourceRow.vue";
import type { SkillSourceConfigView } from "../types";

type SkillSourceGroupProps = {
  heading: string;
  headingId: string;
  sources: SkillSourceConfigView[];
  loading: boolean;
  // 导出入口挂在组头部，一次导出整组（批量导入 JSON 是来源数组）；
  // 只有远端组启用——本地来源不在批量导入的能力范围内。
  exportable?: boolean;
};

withDefaults(defineProps<SkillSourceGroupProps>(), {
  exportable: false,
});

defineEmits<{
  edit: [source: SkillSourceConfigView];
  delete: [source: SkillSourceConfigView];
  sync: [source: SkillSourceConfigView];
  export: [sources: SkillSourceConfigView[]];
}>();
</script>

<template>
  <section v-if="sources.length" class="manager-skill-source-group" :aria-labelledby="headingId">
    <header class="manager-skill-source-group__header">
      <h2 :id="headingId">{{ heading }}</h2>
      <div class="manager-skill-source-group__meta">
        <span>{{ sources.length }} 项</span>
        <button
          v-if="exportable"
          class="secondary-button manager-skill-source-group__export"
          :disabled="loading"
          type="button"
          @click="$emit('export', sources)"
        >
          导出 JSON
        </button>
      </div>
    </header>
    <div class="manager-skill-source-group__list">
      <SourceRow
        v-for="source in sources"
        :key="source.id"
        :loading="loading"
        :source="source"
        @edit="$emit('edit', $event)"
        @delete="$emit('delete', $event)"
        @sync="$emit('sync', $event)"
      />
    </div>
  </section>
</template>
