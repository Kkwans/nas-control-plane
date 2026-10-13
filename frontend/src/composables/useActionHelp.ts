import { reactive, watch, type ShallowRef } from 'vue'
import type { TooltipInstance } from 'element-plus'

// Element Plus 分别处理 hover/focus 的关闭；任一交互仍在进行时保留帮助。
export function useActionHelp(tooltip: Readonly<ShallowRef<TooltipInstance | null>>, disabled: () => boolean | undefined) {
  const state = reactive({ visible: false })
  let focused = false, triggerHovered = false, contentHovered = false, dismissed = false
  const update = (visible: boolean) => {
    state.visible = !disabled() && !dismissed && (visible || focused || triggerHovered || contentHovered)
  }
  const triggerEvents = {
    focus: () => { focused = true; dismissed = false },
    blur: () => { focused = false },
    mouseenter: () => { triggerHovered = true; dismissed = false },
    mouseleave: () => { triggerHovered = false },
  }
  // 使用公开的 popper DOM ref，覆盖其内边距和箭头，而非仅监听文字 span。
  watch(() => tooltip.value?.popperRef?.contentRef, (element, _previous, cleanup) => {
    contentHovered = false
    if (!element) return
    const enter = () => { contentHovered = true }
    const leave = () => { contentHovered = false }
    element.addEventListener('mouseenter', enter)
    element.addEventListener('mouseleave', leave)
    cleanup(() => {
      element.removeEventListener('mouseenter', enter)
      element.removeEventListener('mouseleave', leave)
    })
  }, { flush: 'post' })
  const dismiss = (event?: KeyboardEvent) => {
    dismissed = true
    state.visible = false
    // 关闭同时取消待显示计时器；不移动焦点，也不吞掉其他控件的 Escape。
    tooltip.value?.onClose(event, 0)
  }
  watch(disabled, () => {
    // 禁用原生按钮不保证浏览器派发 blur；恢复后不得复活旧交互。
    focused = triggerHovered = contentHovered = false
    dismiss()
  })
  return { state, triggerEvents, update, dismiss }
}

