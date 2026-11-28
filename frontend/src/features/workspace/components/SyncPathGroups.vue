<script setup lang="ts">
import { computed } from "vue";
import AppTooltip from "@shared/ui/AppTooltip.vue";
import type { SkillLinkAssociation, SyncPathOption } from "../types";
import {
  canRemoveSkillLink,
  pendingSkillLinkCleanupCount,
  hasCurrentSourceSkillLinks,
  hasUnmanagedSkillLinks,
  selectableSyncPathIds,
  skillDirIsAlias,
  skillLinkSourceLabel,
  skillLinkSourcePath,
  skillLinkStateInfo,
} from "../syncAssociations";

type SyncPathGroupsProps = {
  busy: boolean;
  currentSourceId?: string;
  sourceLabels?: Record<string, string>;
  selectedPathIds: Set<string>;
  paths: SyncPathOption[];
};

const props = defineProps<SyncPathGroupsProps>();

defineEmits<{
  setPaths: [pathIds: string[], selected: boolean];
  togglePath: [name: string];
  removeLink: [pathName: string, destinationPath: string];
}>();

function isCurrentSourceLink(link: SkillLinkAssociation): boolean {
  return Boolean(props.currentSourceId && (link.matchedSourceIds ?? []).includes(props.currentSourceId));
}

const selectableIds = computed(() => selectableSyncPathIds(props.paths));
</script>

<template>
  <div class="manager-sync-target-groups">
    <div class="manager-sync-target-group">
      <div class="manager-sync-target-group__header">
        <span>技能路径</span>
        <span class="manager-sync-target-group__count">
          {{ selectableIds.filter((id) => selectedPathIds.has(id)).length }}/{{ selectableIds.length }}
        </span>
        <div class="manager-sync-target-group__actions">
          <button
            class="secondary-button manager-sync-target-group__action"
            :disabled="busy || selectableIds.length === 0 || selectableIds.every((id) => selectedPathIds.has(id))"
            type="button"
            @click="$emit('setPaths', selectableIds, true)"
          >
            全选
          </button>
          <button
            class="secondary-button manager-sync-target-group__action"
            :disabled="busy || selectableIds.every((id) => !selectedPathIds.has(id))"
            type="button"
            @click="$emit('setPaths', selectableIds, false)"
          >
            取消
          </button>
        </div>
      </div>
      <div
        v-for="path in paths"
        :key="path.name"
        class="manager-sync-target-entry"
      >
        <label
          :class="`manager-sync-target-row${busy || skillDirIsAlias(path) ? ' manager-sync-target-row--disabled' : ''}`"
        >
          <input
            type="checkbox"
            :checked="selectedPathIds.has(path.name)"
            :disabled="busy || skillDirIsAlias(path)"
            @change="$emit('togglePath', path.name)"
          />
          <span class="manager-sync-target-row__body">
            <span class="manager-sync-target-row__label-line">
              <span class="manager-sync-target-row__label">{{ path.name }}</span>
              <span class="manager-sync-target-row__tags">
                <span
                  v-if="hasUnmanagedSkillLinks(path.links ?? [])"
                  class="manager-sync-target-row__tag manager-sync-target-row__tag--unmanaged"
                >
                  非受管控
                </span>
                <span
                  v-if="hasCurrentSourceSkillLinks(path.links ?? [], currentSourceId)"
                  class="manager-sync-target-row__tag manager-sync-target-row__tag--linked"
                >
                  关联
                </span>
              </span>
            </span>
            <span class="manager-sync-target-row__path">{{ path.dir }}</span>
            <!-- 目录是软链接时，链接指向的原始目的地：这一行本身就说明它挂在
                 哪儿，不再另起一行写属主名字。 -->
            <span v-if="path.dirLinkTarget" class="manager-sync-target-row__alias">
              {{ path.dirLinkTarget }}
            </span>
          </span>
        </label>

        <details v-if="(path.links ?? []).length > 0" class="manager-sync-target-links">
          <summary>
            已关联 {{ (path.links ?? []).length }} 项
            <span v-if="pendingSkillLinkCleanupCount(path.links ?? []) > 0" class="manager-sync-target-links__cleanup">
              待清理 {{ pendingSkillLinkCleanupCount(path.links ?? []) }} 项
            </span>
          </summary>
          <div class="manager-sync-target-links__list">
            <div
              v-for="link in path.links"
              :key="link.destinationPath"
              :class="[
                'manager-sync-target-link',
                { 'manager-sync-target-link--current-source': isCurrentSourceLink(link) },
              ]"
            >
              <span class="manager-sync-target-link__main">
                <span class="manager-sync-target-link__name">{{ link.skillName }}</span>
                <span
                  v-if="skillLinkSourceLabel(link, currentSourceId, sourceLabels)"
                  class="manager-sync-target-link__source"
                >{{ skillLinkSourceLabel(link, currentSourceId, sourceLabels) }}</span>
                <span class="manager-sync-target-link__path" :title="link.sourcePath">
                  {{ skillLinkSourcePath(link, currentSourceId) }}
                </span>
                <AppTooltip
                  v-if="link.description"
                  class="manager-sync-target-link__note"
                  :tip="link.description"
                >
                  <span class="manager-skill-note">{{ link.description }}</span>
                </AppTooltip>
              </span>
              <span :class="`manager-sync-target-link__state manager-sync-target-link__state--${skillLinkStateInfo(link.state).tone}`">
                {{ skillLinkStateInfo(link.state).label }}
              </span>
              <button
                v-if="canRemoveSkillLink(link)"
                :aria-label="`移除 ${link.skillName} 的软链接`"
                :disabled="busy"
                class="manager-sync-target-link__remove"
                type="button"
                @click="$emit('removeLink', path.name, link.destinationPath)"
              >
                <svg aria-hidden="true" focusable="false" viewBox="0 0 24 24">
                  <path
                    d="M6.7 5.3 12 10.6l5.3-5.3 1.4 1.4-5.3 5.3 5.3 5.3-1.4 1.4-5.3-5.3-5.3 5.3-1.4-1.4 5.3-5.3-5.3-5.3z"
                  />
                </svg>
              </button>
            </div>
          </div>
        </details>
      </div>
    </div>
  </div>
</template>
