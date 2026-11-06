// geili: administrator-issued commercial invoices.
export default {
  invoice: {
    nav: '开票',
    title: '开票',
    description: '填写金额开具商业发票，打印或另存为 PDF',
    printTitle: '打印发票',
    issue: '开票',
    issuerSettings: '开票信息',
    issuerNotReady: '尚未填写开票信息。请先在「开票信息」中填写公司名称和各币种收款信息。',
    columns: {
      invoiceNo: '发票号',
      issueDate: '开票日期',
      billTo: '客户',
      amount: '金额',
      status: '状态',
      createdAt: '创建时间',
      actions: '操作'
    },
    status: {
      issued: '已开具',
      voided: '已作废'
    },
    print: '打印',
    void: '作废',
    voidConfirm: '作废发票 {no}？作废后发票号保留、不会再被使用，打印时显示 VOID 标记。',
    voided: '发票已作废',
    emptyTitle: '还没有发票',
    emptyDescription: '点击「开票」填写金额即可开具第一张发票',
    failedToLoad: '加载发票失败',
    form: {
      currency: '币种',
      amount: '金额',
      amountHint: '大于 0，最多两位小数',
      issueDate: '开票日期',
      description: '项目描述',
      defaultDescription: 'AI API Services',
      billToName: '客户名称（选填）',
      billToAddress: '客户地址（选填）',
      notes: '备注（选填）',
      paymentPreview: '将印在发票上的收款信息',
      paymentMissing: '该币种还没有收款信息，请先在「开票信息」中填写。',
      submit: '开具并打印',
      created: '已开具发票 {no}'
    },
    issuer: {
      companyName: '公司名称',
      companyAddress: '公司地址',
      companyContact: '联系方式',
      companyContactHint: '邮箱、电话或网址，可多行',
      numberPrefix: '发票号前缀',
      numberPrefixHint: '大写字母或数字，最多 12 位。发票号形如 {example}，每年从 0001 重新编号。',
      payment: '{currency} 收款信息',
      paymentHint: '原样印在发票上，例如 Wise 账户名、账号、SWIFT/BIC、银行名称和地址',
      footerNote: '页脚说明（选填）',
      seal: '公章',
      sealHint: '透明背景 PNG 最佳，上传时会缩放到 800 像素以内',
      sealUpload: '上传公章',
      sealRemove: '移除公章',
      sealNone: '未上传公章',
      sealTooLarge: '图片处理后仍超过 1 MB，请换一张更小的图片',
      sealInvalid: '无法读取该图片',
      saved: '开票信息已保存',
      failedToSave: '保存失败'
    },
    errors: {
      notReady: '该币种的收款信息或公司名称未填写，请先完善开票信息',
      invalid: '发票内容不合法，请检查金额、币种和日期',
      issuerInvalid: '开票信息不合法，请检查公司名称、前缀和公章图片'
    },
    sheet: {
      back: '返回',
      print: '打印 / 另存为 PDF',
      printHint: '打印对话框中选择「另存为 PDF」，并关闭「页眉和页脚」。'
    }
  }
}
