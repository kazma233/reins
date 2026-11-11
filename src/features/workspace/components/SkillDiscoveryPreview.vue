<script setup lang="ts">
import AppTooltip from "@shared/ui/AppTooltip.vue";
import type { SkillDiscoveryResult } from "../types";

type SkillDiscoveryPreviewProps = {
  discovery: SkillDiscoveryResult | null;
  emptyText: string;
  loading: boolean;
  // 预览读取失败：错误优先于空态展示，下一次成功读取时由调用方清空。
  error?: string | null;
};

const props = withDefaults(defineProps<SkillDiscoveryPreviewProps>(), {
  error: null,
});
</script>

<template>
  <div v-if="error" class="empty-state error-text">{{ error }}</div>
  <div v-else-if="loading || !discovery" class="empty-state">{{ emptyText }}</div>
  <div v-else class="manager-discovery-columns">
    <section class="manager-discovery-column" aria-label="将导入的列表">
      <div class="manager-discovery-column__header">
        <span class="manager-discovery-column__title">将导入</span>
        <span class="manager-discovery-column__count">{{ discovery.skills.length }} 项</span>
      </div>
      <div class="manager-import-skill-list manager-import-skill-list--compact">
        <template v-if="discovery.skills.length > 0">
          <div
            v-for="skill in discovery.skills"
            :key="`${discovery.discoveryId}-${skill.relativePath}-${skill.name}`"
            class="manager-import-skill-row manager-import-skill-row--compact"
          >
            <span class="manager-skill-name">{{ skill.name }}</span>
            <span class="manager-skill-path-label">{{ skill.relativePath }}</span>
            <AppTooltip v-if="skill.description" :tip="skill.description">
              <span class="manager-skill-note">{{ skill.description }}</span>
            </AppTooltip>
          </div>
        </template>
        <div v-else class="empty-state">没有匹配 include 条件的 skills。</div>
      </div>
    </section>

    <section class="manager-discovery-column" aria-label="被排除的列表">
      <div class="manager-discovery-column__header">
        <span class="manager-discovery-column__title">被排除的列表</span>
        <span class="manager-discovery-column__count">
          {{ discovery.excludedSkills?.length ?? 0 }} 项
        </span>
      </div>
      <template v-if="(discovery.excludedSkills?.length ?? 0) > 0">
        <div class="manager-import-skill-list manager-import-skill-list--compact">
          <div
            v-for="skill in discovery.excludedSkills"
            :key="`${discovery.discoveryId}-excluded-${skill.relativePath}-${skill.name}`"
            class="manager-import-skill-row manager-import-skill-row--compact"
          >
            <span class="manager-skill-name">{{ skill.name }}</span>
            <span class="manager-skill-path-label">{{ skill.relativePath }}</span>
            <AppTooltip v-if="skill.description" :tip="skill.description">
              <span class="manager-skill-note">{{ skill.description }}</span>
            </AppTooltip>
          </div>
        </div>
      </template>
      <div v-else class="empty-state">暂无被排除的 skills。</div>
    </section>
  </div>
</template>
