#!/usr/bin/env bash
# 端到端验收脚本：真实 zlib 搜索/下载 + TTS 转有声书 + 账号隔离 + Bearer Token。
#
# 前置：
#   - Echo Service 已启动（默认 http://127.0.0.1:8090）
#   - 本机存在 zlib 会话文件（默认 ~/.config/zlib/session.json，可用 ZLIB_SESSION 指定；
#     先用 zlib CLI `zlib login` 登录，或从浏览器 Cookie 导入）
#   - TTS 服务可达（默认 http://127.0.0.1:8123，见 https://github.com/imlinhanchao/tts-service）
#   - 需要 curl、jq；ffprobe 可选（有则校验时长）
#
# 用法：
#   BASE=http://127.0.0.1:8090 ./scripts/e2e.sh
#   BASE=... ZLIB_SESSION=~/zlib-session.json ./scripts/e2e.sh
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8090}"
ZLIB_SESSION="${ZLIB_SESSION:-$HOME/.config/zlib/session.json}"
WORK_BASE="${WORK_BASE:-${TMPDIR:-/tmp}}"
mkdir -p "$WORK_BASE"
WORK="$(mktemp -d "$WORK_BASE/echo-e2e.XXXXXX")"
COOKIE_A="$WORK/a.cookies"
COOKIE_B="$WORK/b.cookies"
trap 'rm -rf "$WORK"' EXIT

say() { printf '\n\033[1;36m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[1;31mFAIL: %s\033[0m\n' "$*" >&2; exit 1; }

poll_job() { # $1 cookie file, $2 job id, $3 timeout seconds
  local cookie="$1" id="$2" timeout="${3:-300}" start now status progress
  start=$(date +%s)
  while :; do
    local body
    body=$(curl -sS -b "$cookie" "$BASE/api/jobs/$id")
    status=$(echo "$body" | jq -r '.job.status')
    progress=$(echo "$body" | jq -r '.job.progress')
    printf '  job %s: %-10s %s%%\n' "$id" "$status" "$progress"
    case "$status" in
      done) return 0 ;;
      failed|cancelled) echo "$body" | jq '.job.error' >&2; fail "任务 $id 状态 $status" ;;
    esac
    now=$(date +%s)
    if (( now - start > timeout )); then fail "任务 $id 超时（${timeout}s）"; fi
    sleep 2
  done
}

say "0. 服务健康检查"
curl -sS "$BASE/health" | jq .

say "1. 注册账号 A"
EMAIL_A="e2e_a_$(date +%s)@example.com"
curl -sS -c "$COOKIE_A" -X POST "$BASE/api/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_A\",\"password\":\"password123\"}" | jq .

say "2. 用本机 zlib 会话配置账号 A"
[[ -f "$ZLIB_SESSION" ]] || fail "找不到 zlib 会话文件 $ZLIB_SESSION（先运行 zlib login，或用 ZLIB_SESSION 指定）"
DOMAIN=$(jq -r '.domain' "$ZLIB_SESSION")
ZUID=$(jq -r '.cookies.remix_userid' "$ZLIB_SESSION")
ZKEY=$(jq -r '.cookies.remix_userkey' "$ZLIB_SESSION")
[[ "$ZUID" != "null" && "$ZKEY" != "null" ]] || fail "会话文件缺少 remix_userid/remix_userkey"
PAYLOAD=$(jq -n --arg d "$DOMAIN" --arg u "$ZUID" --arg k "$ZKEY" \
  '{mode:"cookies", domain:$d, remix_userid:$u, remix_userkey:$k}')
curl -sS -b "$COOKIE_A" -X PUT "$BASE/api/providers/zlib/account" \
  -H 'Content-Type: application/json' -d "$PAYLOAD" | jq .

say "3. 搜索《呐喊》"
SEARCH=$(curl -sS -b "$COOKIE_A" \
  "$BASE/api/search?provider=zlib&q=%E5%91%90%E5%96%8A&ext=epub&limit=5")
echo "$SEARCH" | jq -r '.books[] | "  \(.ref)\t\(.title)\t\(.format)\t\(.size)"'
REF=$(echo "$SEARCH" | jq -r '.books[0].ref')
[[ -n "$REF" && "$REF" != "null" ]] || fail "搜索没有结果"
echo "选中：$REF"

say "4. 下载到书库"
JOB1=$(curl -sS -b "$COOKIE_A" -X POST "$BASE/api/library/download" \
  -H 'Content-Type: application/json' \
  -d "{\"provider\":\"zlib\",\"ref\":\"$REF\"}" | jq -r '.job.id')
poll_job "$COOKIE_A" "$JOB1" 300

say "5. 书库与章节"
LIB=$(curl -sS -b "$COOKIE_A" "$BASE/api/library")
echo "$LIB" | jq -r '.books[] | "  [\(.id)] \(.title) (\(.format), \(.size_bytes) bytes)"'
BOOK_ID=$(echo "$LIB" | jq -r '.books[0].id')
CHAPTERS=$(curl -sS -b "$COOKIE_A" "$BASE/api/library/$BOOK_ID/chapters")
echo "$CHAPTERS" | jq -r '.chapters[] | "  [\(.index)] \(.title) \(.chars) 字"'
# 选一个 200~1500 字的章节，控制转码时长
CH=$(echo "$CHAPTERS" | jq -r '[.chapters[] | select(.chars >= 200 and .chars <= 1500)][0].index // .chapters[0].index')
[[ -n "$CH" && "$CH" != "null" ]] || fail "没有可用章节"
echo "转换章节：$CH"

say "6. Edge TTS 转有声书（章节 $CH）"
JOB2=$(curl -sS -b "$COOKIE_A" -X POST "$BASE/api/library/$BOOK_ID/audiobooks" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"edge-tts\",\"voice\":\"zh-CN-YunxiNeural\",\"speed\":1.0,\"chapters\":[$CH]}" \
  | jq -r '.job.id')
poll_job "$COOKIE_A" "$JOB2" 600

say "7. 下载音频与字幕并校验"
AUDIOBOOKS=$(curl -sS -b "$COOKIE_A" "$BASE/api/audiobooks")
echo "$AUDIOBOOKS" | jq -r '.audiobooks[] | "  [\(.id)] \(.book_title) \(.format) \(.duration_s)s \(.segments) 段"'
AB_ID=$(echo "$AUDIOBOOKS" | jq -r '.audiobooks[0].id')
MANIFEST=$(curl -sS -b "$COOKIE_A" "$BASE/api/audiobooks/$AB_ID/chapters")
echo "$MANIFEST" | jq -r '.chunks[] | "  章节块 [\(.n)] \(.title) \(.duration_s)s"' | head -5
CHUNK=$(echo "$MANIFEST" | jq -r '.chunks[0].n')
[[ -n "$CHUNK" && "$CHUNK" != "null" ]] || fail "没有章节分块"
curl -sS -b "$COOKIE_A" "$BASE/api/files/audiobooks/$AB_ID?variant=chapter&n=$CHUNK" -o "$WORK/audio.mp3"
curl -sS -b "$COOKIE_A" "$BASE/api/files/audiobooks/$AB_ID?variant=chapter_srt&n=$CHUNK" -o "$WORK/subtitles.srt"
file "$WORK/audio.mp3"
grep -c ' --> ' "$WORK/subtitles.srt" | sed 's/^/  本章字幕条数：/'
if FFPROBE=$(command -v ffprobe); then
  "$FFPROBE" -v error -show_entries format=duration -of default=nw=1:nk=1 "$WORK/audio.mp3" | sed 's/^/  本章时长：/'
else
  echo "  （未安装 ffprobe，跳过时长校验）"
fi

say "8. 账号隔离：注册 B 并尝试访问 A 的资源"
EMAIL_B="e2e_b_$(date +%s)@example.com"
curl -sS -c "$COOKIE_B" -X POST "$BASE/api/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_B\",\"password\":\"password123\"}" | jq .
B_LIB=$(curl -sS -b "$COOKIE_B" "$BASE/api/library")
[[ "$(echo "$B_LIB" | jq '.books | length')" == "0" ]] || fail "B 不应看到 A 的书"
B_CODE=$(curl -sS -o /dev/null -w '%{http_code}' -b "$COOKIE_B" "$BASE/api/library/$BOOK_ID")
[[ "$B_CODE" == "404" ]] || fail "B 访问 A 的书应返回 404，实际 $B_CODE"
B_AB_CODE=$(curl -sS -o /dev/null -w '%{http_code}' -b "$COOKIE_B" "$BASE/api/files/audiobooks/$AB_ID?variant=audio")
[[ "$B_AB_CODE" == "404" ]] || fail "B 访问 A 的音频应返回 404，实际 $B_AB_CODE"
echo "  B 访问 A 的书/音频均返回 404 ✅"

say "9. Bearer Token 认证（Echo App 登录）"
TOKEN_RESP=$(curl -sS -X POST "$BASE/api/auth/token" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_A\",\"password\":\"password123\",\"name\":\"e2e-phone\"}")
TOKEN=$(echo "$TOKEN_RESP" | jq -r .token)
[[ "$TOKEN" == echo_pat_* ]] || fail "未拿到 Bearer Token"
curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/api/auth/me" | jq -e '.user.email' >/dev/null || fail "Bearer 访问 /api/auth/me 失败"
curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/api/library" >/dev/null || fail "Bearer 访问书库失败"
[[ "$(curl -sS -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer echo_pat_invalid' "$BASE/api/library")" == "401" ]] || fail "无效 Token 应返回 401"
NEW_TOKEN=$(curl -sS -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/auth/token/refresh" | jq -r .token)
[[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$BASE/api/library")" == "401" ]] || fail "刷新后旧 Token 应失效"
[[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $NEW_TOKEN" "$BASE/api/library")" == "200" ]] || fail "新 Token 应可用"
curl -sS -X POST -H "Authorization: Bearer $NEW_TOKEN" "$BASE/api/auth/token/revoke" >/dev/null
[[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $NEW_TOKEN" "$BASE/api/library")" == "401" ]] || fail "撤销后 Token 应失效"
echo "  Bearer Token 登录 / 刷新 / 撤销 ✅"

say "E2E 全部通过"
