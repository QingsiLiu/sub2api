/**
 * Geili Tailwind 配置：包装上游 `frontend/tailwind.config.js`，不修改上游文件。
 *
 * - 由 `postcss.config.js` 的 geili hook 指定为 Tailwind 配置入口。
 * - 上游的 content / darkMode / 动画 / 阴影等全部保留；这里只把语义色板换成
 *   `rgb(var(--geili-*) / <alpha-value>)`，让配色由 `styles/tokens.css` 里的 CSS 变量
 *   统一驱动（支持运行时换肤、深色模式集中定义），Tailwind 的 `/50` 透明度修饰符照常可用。
 * - tokens.css 当前取值与上游色板一一对应，因此骨架阶段视觉零变化；
 *   视觉重塑只需改 tokens.css，不需要再碰这里。
 *
 * @type {import('tailwindcss').Config}
 */
import upstream from '../../tailwind.config.js'

const SCALE = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950]

/** 生成 `{ 50: 'rgb(var(--geili-<name>-50) / <alpha-value>)', ... }` */
function tokenPalette(name) {
  return Object.fromEntries(
    SCALE.map((step) => [step, `rgb(var(--geili-${name}-${step}) / <alpha-value>)`])
  )
}

const upstreamExtend = upstream.theme?.extend ?? {}

export default {
  ...upstream,
  theme: {
    ...upstream.theme,
    extend: {
      ...upstreamExtend,
      colors: {
        ...(upstreamExtend.colors ?? {}),
        primary: tokenPalette('primary'),
        accent: tokenPalette('accent'),
        dark: tokenPalette('dark')
      },
      fontFamily: {
        ...(upstreamExtend.fontFamily ?? {}),
        sans: ['var(--geili-font-sans)'],
        mono: ['var(--geili-font-mono)'],
        // 预留品牌展示字体位，供首页/营销区使用
        display: ['var(--geili-font-display)']
      }
    }
  },
  plugins: [...(upstream.plugins ?? [])]
}
