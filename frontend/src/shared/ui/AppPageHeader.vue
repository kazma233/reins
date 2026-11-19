<script setup lang="ts">
import "./app-page-header.css";

// 顶层页统一的页头:左侧标题与一句数据口径描述,右侧页面控件(默认插槽)。
// 定位外壳(吸顶、页面留白、底部分隔线)由各页自己负责,这里只管一行内的结构。
type AppPageHeaderProps = {
  title: string;
  meta?: string;
};

withDefaults(defineProps<AppPageHeaderProps>(), {
  meta: undefined
});
</script>

<template>
  <div class="app-page-header">
    <div class="app-page-header__main">
      <h2 class="app-page-header__title">{{ title }}</h2>
      <p v-if="meta" class="app-page-header__meta">{{ meta }}</p>
    </div>
    <div class="app-page-header__actions">
      <slot />
    </div>
  </div>
</template>
