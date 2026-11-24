#!/bin/bash
# 临时复现脚本：直连真实网关，抓 write_file 工具调用的原始 SSE 分片格式
set -a; source <(tr '\0' '\n' < /proc/1548363/environ | grep '^AI_'); set +a

TOOLS='[{"type":"function","function":{"name":"write_file","description":"写文件","parameters":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}}}]'

curl -sN --max-time 60 "https://tokenlink.woa.com/openapi/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $AI_API_KEY" \
  -d "{
    \"model\": \"hy4\",
    \"stream\": true,
    \"messages\": [{\"role\":\"user\",\"content\":\"请调用工具把内容 hi 写入 /probe.txt\"}],
    \"tools\": $TOOLS,
    \"tool_choice\": \"auto\"
  }" | tee /tmp/hy4_sse.txt | head -60

echo "=== 统计 ==="
grep -c "^data:" /tmp/hy4_sse.txt
echo "--- tool_calls 相关分片 ---"
grep "tool_calls" /tmp/hy4_sse.txt | head -20
