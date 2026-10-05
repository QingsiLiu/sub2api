export default {
  "responseAudit": {
    "title": "Response audit",
    "result": "Response result",
    "range": "Observation window",
    "details": "Evidence",
    "receipt": "Linked receipt",
    "identities": "User / Key / Account",
    "existingSettlement": "Existing settlement",
    "unobserved": "Not audited",
    "unobservedHint": "Evidence is absent or association is ambiguous; this does not establish success or failure.",
    "observationHint": "Observation only: billing stays unchanged. Statistics cover persisted audits only; historical outcomes are not inferred.",
    "writeHint": "A successful write confirms gateway transmission, not receipt or processing by the client application. Returned reasoning counts as output.",
    "summary": "{total} recorded; {missing} without a written terminal; {failed} downstream write failures; {charged} distinct charged receipts associated with empty or failed-before-output requests (investigation leads only).",
    "coverage": "Earliest retained observation {start}; process started {process}; {dropped} dropped and {failed} persistence failures since process start.",
    "linkedLogs": "View linked troubleshooting logs",
    "noLinkedLogs": "No matching logs; they may be pending, expired or monitoring may be disabled.",
    "logsUnavailable": "Some troubleshooting logs are unavailable; monitoring may be disabled. Audit evidence remains available.",
    "unavailable": "Response audit unavailable",
    "invalidId": "IDs must be positive integers",
    "status": {
      "success": "Success",
      "partial_failure": "Failure after output",
      "empty": "Empty result",
      "failed": "Failure before output",
      "unknown": "Unknown result"
    },
    "fields": {
      "user_id": "User id",
      "api_key_id": "Api key id",
      "account_id": "Account id",
      "model": "Model",
      "request_id": "Request id",
      "client_request_id": "Client request id",
      "usage_request_id": "Usage request id",
      "upstream_request_id": "Upstream request id",
      "started_at": "Started at",
      "finished_at": "Finished at",
      "http_status": "Http status",
      "upstream_content_seen": "Upstream content seen",
      "text_written": "Text written",
      "reasoning_written": "Reasoning written",
      "complete_tool_written": "Complete tool written",
      "upstream_terminal": "Upstream terminal",
      "terminal": "Terminal",
      "terminal_written": "Terminal written",
      "write_failed": "Write failed",
      "client_disconnected": "Client disconnected",
      "usage_present": "Usage present",
      "first_output_ms": "First output ms"
    },
    "reasons": {
      "output_before_failure": "Output was written before failure",
      "completed_with_output": "Completed with written output",
      "unsupported_or_oversized_output": "Unsupported or oversized output",
      "failed_before_output": "Failed before usable output",
      "completed_without_output": "Completed without usable output",
      "downstream_write_failed": "Downstream write failed",
      "client_disconnected": "Client disconnected before completion",
      "missing_terminal": "Normal terminal event missing"
    },
    "settlement": {
      "settled": "Existing settled receipt",
      "pending": "Settlement pending",
      "not_found": "Receipt not found",
      "unlinked": "Unknown association"
    }
  }
}
