import { onScopeDispose, readonly, ref } from 'vue'

export function useReducedMotion() {
  const media = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-reduced-motion: reduce)')
    : undefined
  const reducedMotion = ref(media?.matches ?? false)
  if (media) {
    const update = (event: MediaQueryListEvent) => { reducedMotion.value = event.matches }
    media.addEventListener('change', update)
    onScopeDispose(() => media.removeEventListener('change', update))
  }
  return readonly(reducedMotion)
}
