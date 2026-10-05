export default {
  "responseAudit": {
    "title": "响应审计",
    "result": "响应结果",
    "range": "观察范围",
    "details": "证据详情",
    "receipt": "关联凭证",
    "identities": "用户 / Key / 账号",
    "existingSettlement": "现有结算",
    "unobserved": "未审计",
    "unobservedHint": "没有审计证据或关联不唯一；不能据此判断请求成功或失败。",
    "observationHint": "观察版：只标记和统计，扣费规则保持现状。统计仅包含已落库的审计记录，历史记录不补判。",
    "writeHint": "写出成功表示网关完成写入，不代表客户端应用已收到或处理。思考内容计入有效输出。",
    "summary": "已记录 {total} 次；缺少终止写出 {missing} 次；下游写失败 {failed} 次；空结果或输出前失败关联的收费凭证 {charged} 份（按凭证去重，仅作核查线索）。",
    "coverage": "现存最早观察时间 {start}；本进程启动 {process} 后丢弃 {dropped} 次、落库失败 {failed} 次。",
    "linkedLogs": "查看关联排障日志",
    "noLinkedLogs": "没有找到关联日志，可能尚未写入、已清理或监控未启用。",
    "logsUnavailable": "部分排障日志不可用，可能监控未启用；审计证据仍可查看。",
    "unavailable": "响应审计暂不可用",
    "invalidId": "ID 必须是正整数",
    "status": {
      "success": "成功",
      "partial_failure": "部分输出后失败",
      "empty": "空结果",
      "failed": "输出前失败",
      "unknown": "结果未知"
    },
    "fields": {
      "user_id": "用户 ID",
      "api_key_id": "Key ID",
      "account_id": "账号 ID",
      "model": "模型",
      "request_id": "网关请求 ID",
      "client_request_id": "客户端请求 ID",
      "usage_request_id": "结算关联 ID",
      "upstream_request_id": "上游请求 ID",
      "started_at": "开始时间",
      "finished_at": "结束时间",
      "http_status": "HTTP 状态",
      "upstream_content_seen": "已观察到上游内容",
      "text_written": "文本已写出",
      "reasoning_written": "思考已写出",
      "complete_tool_written": "完整工具调用已写出",
      "upstream_terminal": "上游终止事件",
      "terminal": "下游终止事件",
      "terminal_written": "终止事件已写出",
      "write_failed": "下游写失败",
      "client_disconnected": "完成前连接断开",
      "usage_present": "观察到上游用量",
      "first_output_ms": "首次有效输出（毫秒）"
    },
    "reasons": {
      "output_before_failure": "已写出有效输出，随后失败",
      "completed_with_output": "正常结束并写出有效输出",
      "unsupported_or_oversized_output": "输出类型无法识别或超过审计解析限制",
      "failed_before_output": "有效输出前失败",
      "completed_without_output": "正常结束但没有有效输出",
      "downstream_write_failed": "下游写入失败",
      "client_disconnected": "正常结束前连接断开",
      "missing_terminal": "未观察到完整的正常结束事件"
    },
    "settlement": {
      "settled": "已有结算凭证",
      "pending": "结算待完成",
      "not_found": "未找到结算凭证",
      "unlinked": "关联未知"
    }
  }
}
