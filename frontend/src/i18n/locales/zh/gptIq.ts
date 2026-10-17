// geili: GPT group IQ monitor page.
export default {
  gptIq: {
    nav: 'GPT智商',
    title: '分组智商监控',
    description: '每 2 分钟开一场新会话做同一道推理题，实时看各 GPT 分组有没有降智',
    eyebrow: 'GPT 分组',
    subtitle: '最近 60 分钟 · 每 {interval} 分钟一次全新会话 · 答案为 {answer} 即正常',
    updatedAt: '数据更新于 {time}',
    waiting: '等待首轮数据',
    nextRefresh: '{s} 秒后自动刷新',
    refreshing: '刷新中',
    refresh: '刷新',
    config: {
      model: '模型',
      reasoning: '推理强度',
      interval: '检测间隔',
      intervalValue: '{n} 分钟',
      answer: '判定答案'
    },
    status: {
      normal: '正常',
      degraded: '弱智',
      pending: '检测中',
      error: '超时 / 异常',
      skipped: '暂无结果'
    },
    legend: {
      skipped: '暂无 / 空槽'
    },
    hint: '偶尔出现一两格红色是正常的：每次检测都是全新会话，单次答错不代表整组不可用。连续多格变红，或你自己的会话也明显变笨，才说明这一组当前不稳定。蓝色是超时或网关波动，不是智商结果。',
    group: {
      normalRate: '近 60 分钟正确率',
      noJudged: '暂无判定',
      streak: '最近连续 {n} 次{status}',
      counts: '正常 {normal} · 弱智 {degraded} · 异常 {error} · 暂无 {skipped}',
      axisPast: '60 分钟前',
      axisNow: '现在',
      cellLabel: '{time} {status}',
      lightsLabel: '{name} 最近 60 分钟检测结果'
    },
    errors: {
      authFailed: '监控服务暂时无法确认你的登录状态。请稍后刷新；如果一直出现，请退出后重新登录。',
      unavailable: '分组状态读取失败，请稍后重试。',
      stale: '刷新失败，当前显示的是上次结果。',
      adminOnly: '只有管理员可以删除画面。',
      deleteFailed: '画面删除失败，请稍后重试。'
    },
    idle: '近 60 分钟还没有探测结果。若刚刚启用探测，请等这一轮跑完再刷新。',
    retry: '重试',
    question: {
      kicker: '推理题',
      title: '乳糖酶遗传学身高题',
      lede: '每 {interval} 分钟用这道题开一场新会话，只看最终回复里的数值。',
      expand: '展开全文',
      collapse: '收起',
      answerLabel: '判定答案',
      answerUnit: '英寸',
      rule: '只从最终回复正文抽数值：优先取最后一行单独的数值，否则取正文最后一个数值，等于 {answer} 为正常。不看全文有没有出现过 {answer}，也不看推理过程里的数字。'
    },
    drawings: {
      kicker: '作图测试',
      lede: '每 {interval} 分钟检测一次 · 每组保留最近 {keep} 张成功画面',
      empty: '这一组还没有画出来。启用作图探测后，请等这一轮跑完再刷新。',
      noSvg: '这一次没有画出可用的 SVG',
      alt: '{group} 在 {time} 画的海绵宝宝骑自行车',
      enlarge: '查看大图',
      delete: '删除',
      deleting: '删除中',
      confirmTitle: '删除这张画面？',
      confirmMessage: '删除后所有用户都看不到这张画面。',
      ok: '已画出',
      error: '失败',
      skipped: '跳过',
      pendingAccount: '待归因',
      elapsed: '耗时 {time}'
    },
    admin: {
      kicker: '仅管理员可见',
      title: '最近 20 次检测',
      lede: '普通用户看不到上游账号。',
      reasoningTab: '推理题',
      drawingTab: '作图',
      emptyReasoning: '还没有推理题记录。新的一轮跑完后会出现账号名。',
      emptyDrawing: '还没有作图记录。新的一轮跑完后会出现账号名。',
      time: '时间',
      group: '分组',
      result: '结果',
      answer: '抽出',
      elapsed: '耗时',
      reason: '原因',
      account: '账号',
      attempts: '共 {n} 次尝试',
      stream: '流式',
      nonStream: '非流式',
      budget: '剩余时间不足，未继续重试；上方保留最后一次实际结果。'
    },
    detail: {
      extracted: '抽出的最终数值',
      expected: '期望',
      noAnswer: '未能抽出数值',
      account: '上游账号',
      reply: '最终回复',
      close: '关闭'
    },
    reasons: {
      no_sample: '这一格没有记录（上一轮还没结束，定时器没有启动）',
      probe_busy: '上一轮还在跑，这一格被跳过',
      missing_key: '没有探测 Key，未发请求',
      ok: '抽出 {answer}',
      wrong_answer: '抽出的数值不是 {answer}',
      empty_response: '有回复，但没抽出数值',
      incomplete: '上游回复未完成',
      no_svg: '回复里没有画出 SVG',
      svg_rejected: '画出的 SVG 不能安全展示',
      draw_busy: '上一轮作图还在跑，这一次没记入画廊',
      probe_timeout: '探测超时',
      timeout: '探测超时',
      non_stream_not_allowed: '该号只接受流式对话，探测会改用流式',
      empty_stream: '流式回复是空的',
      upstream_transport: '上游连接异常',
      output_limit: '输出达到长度上限',
      response_failed: '上游生成失败',
      http: '上游返回 HTTP {code}'
    },
    reply: {
      no_sample: '（这一格没有记录。上一轮各组还没跑完时，定时器不会启动，这一格就会空着。）',
      probe_busy: '（上一轮还在跑，这一格被跳过。）',
      missing_key: '（这一分组还没有探测 Key，未发请求。）',
      none: '（这一次没有保存回复正文）'
    },
    duration: {
      underSecond: '不到 1 秒',
      seconds: '{s} 秒',
      minutes: '{m} 分 {s} 秒'
    }
  }
}
