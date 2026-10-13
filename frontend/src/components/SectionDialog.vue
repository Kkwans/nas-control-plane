<script setup lang="ts">
import { ElDialog } from 'element-plus'

defineOptions({ inheritAttrs: false })
</script>

<template>
  <Teleport to="body">
    <!-- 原生 section 将 Element Plus 的 header/footer 限定为弹窗内容，避免重复页面地标。 -->
    <section>
      <ElDialog v-bind="$attrs" append-to="body" :append-to-body="false">
        <template v-for="(_, name) in $slots" #[name]="scope">
          <slot :name="name" v-bind="scope || {}" />
        </template>
      </ElDialog>
    </section>
  </Teleport>
</template>
