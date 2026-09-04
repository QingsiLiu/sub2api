export default {
  plugins: {
    // geili hook: Tailwind 主题由 src/geili/tailwind.config.js 包装上游配置后提供
    tailwindcss: { config: './src/geili/tailwind.config.js' },
    autoprefixer: {}
  }
}
