#!/usr/bin/env bash
# 直接调用 Google Stitch 官方 MCP（https://stitch.googleapis.com/mcp）的薄封装，
# 供设计流程（frontend/src/geili/design/BRIEF.md）使用，不依赖编辑器的 MCP 客户端。
#
# 用法：
#   .github/geili/stitch.sh tools                          # 列出工具
#   .github/geili/stitch.sh call <tool> '<json-arguments>'  # 调用工具，输出结果 JSON
#   .github/geili/stitch.sh screen <projectId> <screenId>   # 取屏详情（含 HTML / 截图 URL）
#
# 鉴权：环境变量 STITCH_API_KEY；缺省时读 ~/.cursor/mcp.json 里 stitch 服务的 X-Goog-Api-Key。
# 密钥不得写进仓库。
set -euo pipefail

MCP_URL="${STITCH_MCP_URL:-https://stitch.googleapis.com/mcp}"

api_key() {
  if [ -n "${STITCH_API_KEY:-}" ]; then echo "${STITCH_API_KEY}"; return; fi
  local f="${HOME}/.cursor/mcp.json"
  [ -f "$f" ] && jq -r '.mcpServers.stitch.headers["X-Goog-Api-Key"] // empty' "$f"
}

rpc() { # <method> <params-json>
  local key; key="$(api_key)"
  [ -n "$key" ] || { echo "error: 未找到 Stitch API key（STITCH_API_KEY 或 ~/.cursor/mcp.json）" >&2; exit 1; }
  curl -sS -m "${STITCH_TIMEOUT:-600}" -X POST "${MCP_URL}" \
    -H "X-Goog-Api-Key: ${key}" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -d "$(jq -cn --arg m "$1" --argjson p "$2" '{jsonrpc:"2.0",id:1,method:$m,params:$p}')"
}

# MCP 工具结果放在 result.content[].text，通常是 JSON 字符串；能解析就解析后输出
unwrap() {
  jq -r '
    if .error then ("RPC error: " + (.error|tojson)) | halt_error(1) else . end
    | .result
    | if .isError then ("tool error: " + ([.content[]?.text] | join("\n"))) | halt_error(1) else . end
    | [.content[]? | select(.type=="text") | .text] | join("\n")
    | (try fromjson catch .)'
}

case "${1:-}" in
  tools)  rpc tools/list '{}' | jq -r '.result.tools[] | "\(.name)\t\(.description | split("\n")[0])"' ;;
  call)   [ $# -ge 2 ] || { echo "用法: stitch.sh call <tool> '<json>'" >&2; exit 1; }
          rpc tools/call "$(jq -cn --arg n "$2" --argjson a "${3:-{\}}" '{name:$n,arguments:$a}')" | unwrap ;;
  screen) [ $# -eq 3 ] || { echo "用法: stitch.sh screen <projectId> <screenId>" >&2; exit 1; }
          rpc tools/call "$(jq -cn --arg n "projects/$2/screens/$3" --arg p "$2" --arg s "$3" '{name:"get_screen",arguments:{name:$n,projectId:$p,screenId:$s}}')" | unwrap ;;
  -h|--help|"") sed -n '2,11p' "$0" ;;
  *) echo "未知子命令 $1" >&2; exit 1 ;;
esac
