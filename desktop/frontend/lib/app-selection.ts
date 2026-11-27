/**
 * 客户端里的文字选择: 界面默认不可选, 内容区可选 (规则在 globals.css, 挂在 html[data-app-shell] 下)。
 *
 * 只在壳里生效: Wails 在 <head> 最前面注入的 runtime.js 会设 window.runtime / window.WailsInvoke,
 * 移动端壳由 nativeBootScript 装上 window.go; 浏览器里直接开 next dev 时保持网页默认。
 * 必须排在 nativeBootScript 之后执行。
 */
export const appShellBootScript = `if (window.runtime || window.WailsInvoke || window.go) document.documentElement.setAttribute("data-app-shell", "");`;

/** 可以整块全选的内容 (Ctrl/⌘+A 只选这一块): 代码块、纯文本预览、笔记阅读视图、AI 总结。 */
const SELECT_ALL_BLOCK = "[data-select-all], pre";

const NON_TEXT_INPUTS = new Set(["checkbox", "radio", "button", "submit", "reset", "range", "color", "file", "image"]);

function isEditable(target: EventTarget | null): boolean {
  if (target instanceof HTMLTextAreaElement) return true;
  if (target instanceof HTMLInputElement) return !NON_TEXT_INPUTS.has(target.type);
  return target instanceof HTMLElement && target.isContentEditable;
}

/**
 * 接管 Ctrl/⌘+A: 焦点在输入框或可编辑区时照常全选; 否则不全选整页,
 * 最近一次点击落在可整块全选的内容里时只选中这一块。返回卸载函数。
 */
export function installSelectionGuard(): () => void {
  if (!document.documentElement.hasAttribute("data-app-shell")) return () => {};
  let lastPointer: Element | null = null;
  const onPointer = (e: PointerEvent) => {
    lastPointer = e.target instanceof Element ? e.target : null;
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.defaultPrevented || e.altKey || e.shiftKey || !(e.ctrlKey || e.metaKey) || e.key.toLowerCase() !== "a") return;
    if (isEditable(e.target)) return;
    e.preventDefault();
    const selection = window.getSelection();
    if (!selection) return;
    selection.removeAllRanges();
    const block = lastPointer?.isConnected ? lastPointer.closest(SELECT_ALL_BLOCK) : null;
    if (!block) return;
    const range = document.createRange();
    range.selectNodeContents(block);
    selection.addRange(range);
  };
  document.addEventListener("pointerdown", onPointer, true);
  // 冒泡阶段处理: 组件自己接管了 Ctrl+A 的 (preventDefault) 不再插手。
  document.addEventListener("keydown", onKey);
  return () => {
    document.removeEventListener("pointerdown", onPointer, true);
    document.removeEventListener("keydown", onKey);
  };
}
