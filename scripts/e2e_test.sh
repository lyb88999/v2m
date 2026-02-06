#!/bin/sh
set -eu

API="${API_BASE:-http://localhost:8080}"
URL="${1:-${VIDEO_URL:-}}"
TOKEN="${API_TOKEN:-}"
OUT_DIR="${OUT_DIR:-downloads}"
INTERVAL="${POLL_INTERVAL:-2}"

if [ -z "$URL" ]; then
  echo "用法: scripts/e2e_test.sh <视频URL>"
  echo "可选环境变量: API_BASE / API_TOKEN / OUT_DIR / POLL_INTERVAL"
  exit 1
fi

AUTH_HEADER=""
if [ -n "$TOKEN" ]; then
  AUTH_HEADER="Authorization: Bearer $TOKEN"
fi

mkdir -p "$OUT_DIR"

request() {
  method="$1"
  endpoint="$2"
  body="${3:-}"

  tmp_headers="$(mktemp)"
  tmp_body="$(mktemp)"
  if [ -n "$body" ]; then
    curl -s -D "$tmp_headers" -o "$tmp_body" -X "$method" \
      -H "Content-Type: application/json" \
      ${AUTH_HEADER:+-H "$AUTH_HEADER"} \
      --data "$body" \
      "$API$endpoint" || true
  else
    curl -s -D "$tmp_headers" -o "$tmp_body" -X "$method" \
      ${AUTH_HEADER:+-H "$AUTH_HEADER"} \
      "$API$endpoint" || true
  fi

  code="$(awk 'NR==1 {print $2}' "$tmp_headers")"
  if [ -z "$code" ]; then
    echo "请求失败：未获取到响应状态码（可能服务未启动或网络问题）" >&2
    rm -f "$tmp_headers" "$tmp_body"
    exit 1
  fi
  if [ "$code" = "429" ]; then
    retry="$(awk 'BEGIN{IGNORECASE=1} /^Retry-After:/ {print $2}' "$tmp_headers" | tr -d '\r')"
    if [ -z "$retry" ]; then
      retry=3
    fi
    echo "触发限流，等待 ${retry}s 再试..." >&2
    sleep "$retry"
    rm -f "$tmp_headers" "$tmp_body"
    request "$method" "$endpoint" "$body"
    return
  fi

  if [ "$code" -lt 200 ] || [ "$code" -ge 300 ]; then
    echo "请求失败：HTTP $code" >&2
    if [ -s "$tmp_body" ]; then
      echo "响应内容：" >&2
      cat "$tmp_body" >&2
    fi
    rm -f "$tmp_headers" "$tmp_body"
    exit 1
  fi

  cat "$tmp_body"
  rm -f "$tmp_headers" "$tmp_body"
}

echo "创建任务..."
payload="$(printf '{"url":"%s"}' "$URL")"
resp="$(request POST /jobs "$payload")"
if [ -z "$resp" ]; then
  echo "创建任务失败：响应为空" >&2
  exit 1
fi
if ! job_id="$(printf "%s" "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin)["job_id"])')"; then
  echo "创建任务失败：响应不是 JSON 或无 job_id" >&2
  echo "响应内容：" >&2
  printf "%s\n" "$resp" >&2
  exit 1
fi

echo "job_id=$job_id"
echo "开始轮询状态..."

while true; do
  data="$(request GET "/jobs/$job_id")"
  if ! status="$(printf "%s" "$data" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')"; then
    echo "解析任务状态失败：" >&2
    printf "%s\n" "$data" >&2
    exit 1
  fi
  echo "status=$status"
  if [ "$status" = "ready" ]; then
    break
  fi
  if [ "$status" = "failed" ] || [ "$status" = "expired" ]; then
    echo "任务失败/过期:"
    echo "$data"
    exit 1
  fi
  sleep "$INTERVAL"
done

token_q=""
if [ -n "$TOKEN" ]; then
  token_q="$(python3 -c 'import os,urllib.parse; print("?token=" + urllib.parse.quote(os.environ.get("API_TOKEN","")))')"
fi

outfile="$OUT_DIR/${job_id}.mp3"
echo "下载到: $outfile"
curl -L -o "$outfile" "$API/jobs/$job_id/download$token_q"
ls -lh "$outfile"
