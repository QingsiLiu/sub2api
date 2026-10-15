// geili: supplier console and supplier-owner administration.
export default {
  supplier: {
    roleName: '供应商',
    navSection: '供应商后台',
    nav: {
      accounts: '账号管理',
      usage: '使用记录'
    },
    accounts: {
      title: '账号管理',
      description: '管理你上传的上游账号',
      add: '上传账号',
      edit: '编辑账号',
      searchPlaceholder: '搜索账号名称',
      allPlatforms: '全部平台',
      allStates: '全部状态',
      reviewNotice: '新上传的账号需要管理员审核并分配分组后才会启用。修改 API Key 或 Base URL 后，账号会暂停调度，等待管理员重新确认。',
      columns: {
        name: '名称',
        platform: '平台',
        state: '状态',
        concurrency: '并发',
        groups: '分组',
        rateMultiplier: '结算倍率',
        lastUsed: '最近使用',
        createdAt: '上传时间',
        actions: '操作'
      },
      states: {
        pending: '待审核',
        running: '运行中',
        paused: '已暂停',
        disabled: '已停用',
        error: '异常'
      },
      noGroups: '未分组',
      form: {
        platform: '平台',
        name: '名称',
        notes: '备注',
        apiKey: 'API Key',
        apiKeyEditHint: '当前 Key：{hint}。留空则不修改',
        baseUrl: 'Base URL',
        baseUrlHint: '留空使用平台官方地址；只允许公网地址',
        concurrency: '并发数',
        concurrencyHint: '1～1000'
      },
      pause: '暂停调度',
      pauseConfirm: '暂停后需要管理员才能恢复调度，确定暂停「{name}」吗？',
      delete: '删除',
      deleteConfirm: '确定删除账号「{name}」吗？删除后无法恢复，历史使用记录仍会保留。',
      test: '测试',
      testTitle: '测试账号：{name}',
      testModel: '测试模型',
      testModelPlaceholder: '留空使用默认模型',
      startTest: '开始测试',
      testing: '测试中…',
      testSuccess: '测试成功',
      testFailed: '测试失败',
      created: '账号已上传，等待管理员审核',
      updated: '账号已更新',
      pausedDone: '账号已暂停调度',
      deleted: '账号已删除',
      failedToLoad: '加载账号失败',
      failedToSave: '保存失败',
      emptyTitle: '还没有账号',
      emptyDescription: '上传你的第一个 API Key 账号'
    },
    usage: {
      title: '使用记录',
      description: '你上传的账号产生的调用记录',
      allAccounts: '全部账号',
      modelPlaceholder: '按模型筛选',
      costHint: '账号成本 = 标准费用 × 账号结算倍率，仅供参考；不包含客户信息。',
      stats: {
        requests: '请求数',
        tokens: 'Token 总量',
        accountCost: '账号成本',
        avgDuration: '平均耗时'
      },
      columns: {
        time: '时间',
        account: '账号',
        model: '模型',
        input: '输入',
        output: '输出',
        cache: '缓存读/写',
        accountCost: '账号成本',
        duration: '耗时'
      },
      stream: '流式',
      failedToLoad: '加载使用记录失败'
    },
    admin: {
      owner: '供应商',
      viewAccounts: '供应商账号',
      setOwner: '设置供应商',
      setOwnerTitle: '设置账号「{name}」的供应商',
      ownerUserId: '供应商用户 ID',
      ownerUserIdHint: '填写角色为「供应商」的用户 ID；填 0 取消归属',
      ownerSaved: '供应商已更新',
      ownerFilter: '仅显示供应商 #{id} 的账号',
      clearOwnerFilter: '清除'
    }
  }
}
