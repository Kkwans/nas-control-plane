<script setup lang="ts">
import { ElDrawer } from 'element-plus'

defineOptions({ inheritAttrs: false })
</script>

<template>
  <Teleport to="body">
    <!-- 原生 section 将抽屉 header/footer 限定为局部内容，保留真实标题和对话框语义。 -->
    <section>
      <ElDrawer v-bind="$attrs" append-to="body" :append-to-body="false">
        <template v-for="(_, name) in $slots" #[name]="scope">
          <slot :name="name" v-bind="scope || {}" />
        </template>
      </ElDrawer>
    </section>
  </Teleport>
</template>
