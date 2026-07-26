import { computed, onMounted, ref } from 'vue'

import { useSystemStore } from '@/stores/system'

export function useListPreference(listKey: string) {
  const systemStore = useSystemStore()
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')
  const preference = computed(() => systemStore.listPreference(listKey))
  const pageSize = computed(() => preference.value.pageSize)

  async function load() {
    loading.value = true
    error.value = ''
    try {
      await systemStore.ensureListPreference(listKey)
    } catch {
      error.value = '列表偏好加载失败，已使用默认值。'
    } finally {
      loading.value = false
    }
  }

  async function setPageSize(value: number) {
    const normalized = Math.min(200, Math.max(1, Math.round(value)))
    saving.value = true
    error.value = ''
    try {
      await systemStore.setListPreference(listKey, { pageSize: normalized })
    } catch {
      error.value = '每页数量保存失败。'
      throw new Error(error.value)
    } finally {
      saving.value = false
    }
  }

  async function setSort(sortKey: string, sortDirection: 'asc' | 'desc') {
    saving.value = true
    error.value = ''
    try {
      await systemStore.setListPreference(listKey, { sortKey, sortDirection })
    } catch {
      error.value = '排序偏好保存失败。'
      throw new Error(error.value)
    } finally {
      saving.value = false
    }
  }

  onMounted(() => void load())

  return { preference, pageSize, loading, saving, error, load, setPageSize, setSort }
}
