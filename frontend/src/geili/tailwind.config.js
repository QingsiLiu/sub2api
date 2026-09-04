/**
 * Geili Tailwind 配置：包装上游 `frontend/tailwind.config.js`，不修改上游文件。
 *
 * - 由 `postcss.config.js` 的 geili hook 指定为 Tailwind 配置入口。
 * - 上游的 content / darkMode / 动画等全部保留。
 * - 视觉重塑（方向 F「素白 Atelier Blanc」）靠三件事完成，全部不触碰上游代码：
 *     1. 色板重定向：primary / accent / dark 以及 Tailwind 默认的 gray 系与状态色系
 *        统统指向 `styles/tokens.css` 的 CSS 变量，深色模式在 `.dark` 作用域整体切换；
 *     2. 圆角收敛：上游遍布的 rounded-lg / xl / 2xl 一律压到 4px；
 *     3. 阴影归零：F 的硬性规则是零阴影，这里把所有 shadow-* 置为 none。
 *   上游 2500+ 处工具类与语义类因此自动跟随。
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

const primary = tokenPalette('primary')
const neutral = tokenPalette('neutral')
const dark = tokenPalette('dark')
const success = tokenPalette('success')
const warning = tokenPalette('warning')
const danger = tokenPalette('danger')
const info = tokenPalette('info')

const upstreamExtend = upstream.theme?.extend ?? {}

export default {
  ...upstream,
  // 设计系统预览页是纯 HTML，不在上游的 content 范围里，得单独加进来
  content: [...(upstream.content ?? []), './src/geili/design/preview.html'],
  theme: {
    ...upstream.theme,
    extend: {
      ...upstreamExtend,
      colors: {
        ...(upstreamExtend.colors ?? {}),
        primary,
        accent: neutral,
        dark,
        // Tailwind 默认中性色系：上游大量直接使用，一并收进暖中性阶
        gray: neutral,
        slate: neutral,
        neutral,
        zinc: neutral,
        stone: neutral,
        // 状态色：上游用这些名字表达成功 / 警告 / 危险 / 信息
        emerald: success,
        green: success,
        teal: success,
        amber: warning,
        yellow: warning,
        orange: warning,
        red: danger,
        rose: danger,
        blue: info,
        sky: info,
        cyan: info,
        indigo: info,
        violet: neutral,
        purple: neutral,
        fuchsia: neutral,
        pink: neutral,
        lime: success
      },
      fontFamily: {
        ...(upstreamExtend.fontFamily ?? {}),
        sans: ['var(--geili-font-sans)'],
        mono: ['var(--geili-font-mono)'],
        display: ['var(--geili-font-display)']
      },
      // 全站圆角收敛到 4px（药丸形与整圆保留，用于头像与开关）
      borderRadius: {
        ...(upstreamExtend.borderRadius ?? {}),
        none: '0px',
        sm: '2px',
        DEFAULT: 'var(--geili-radius)',
        md: 'var(--geili-radius)',
        lg: 'var(--geili-radius)',
        xl: 'var(--geili-radius)',
        '2xl': 'var(--geili-radius)',
        '3xl': 'var(--geili-radius)',
        '4xl': 'var(--geili-radius)'
      },
      // 零阴影：F 的硬性规则，弹窗的层次改由 hairline + 遮罩表达
      boxShadow: {
        ...(upstreamExtend.boxShadow ?? {}),
        none: 'none',
        sm: 'none',
        DEFAULT: 'none',
        md: 'none',
        lg: 'none',
        xl: 'none',
        '2xl': 'none',
        inner: 'none',
        glass: 'none',
        'glass-sm': 'none',
        glow: 'none',
        'glow-lg': 'none',
        card: 'none',
        'card-hover': 'none',
        'inner-glow': 'none'
      },
      // 上游的 teal 渐变与网格光晕在本方向里一律取消
      backgroundImage: {
        ...(upstreamExtend.backgroundImage ?? {}),
        'gradient-primary': 'none',
        'gradient-dark': 'none',
        'gradient-glass': 'none',
        'mesh-gradient': 'none'
      },
      // 焦点环落在页面底色上，深色模式自动跟随
      ringOffsetColor: {
        ...(upstreamExtend.ringOffsetColor ?? {}),
        DEFAULT: 'rgb(var(--geili-surface))'
      }
    }
  },
  plugins: [...(upstream.plugins ?? [])]
}
