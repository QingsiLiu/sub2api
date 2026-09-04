/**
 * Geili 中文词条。全部挂在 `geili` 命名空间下，与上游词条永不冲突。
 * 上游词条继续用 `t('dashboard.title')`；Geili 自有组件用 `t('geili.xxx')`。
 */
export default {
  geili: {
    brand: {
      name: '给力 API',
      tagline: '稳定、透明、可追溯的 AI 模型网关'
    },
    system: {
      selfUpdateDisabled:
        '本站由 Geili 发布流程统一升级，后台在线更新已停用；有新版本时会在此提示，由运维合并上游后发布。'
    }
  }
}
