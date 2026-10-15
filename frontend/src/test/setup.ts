// vitest 浏览器环境注入 setup
import '@testing-library/jest-dom/vitest'

// antd 响应式组件（如 Table）在 jsdom 下依赖 window.matchMedia；补一个最小实现。
// jsdom 未实现 matchMedia，缺失会让 antd 的 responsiveObserver 抛错。
if (typeof window !== 'undefined' && typeof window.matchMedia !== 'function') {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
}

// jsdom 未实现 getComputedStyle 的伪元素参数；antd Table 量滚动条宽度时传入 pseudoElt，
// 会触发 jsdom 的 "Not implemented" 报错（大量 stderr 明显拖慢用例）。丢弃该参数即可。
if (typeof window !== 'undefined') {
  const original = window.getComputedStyle.bind(window)
  window.getComputedStyle = ((elt: Element) => original(elt)) as typeof window.getComputedStyle
}
