#!/usr/bin/env bash
# 把 deploy/echo-service.service 安装为 systemd 用户服务（Linux）。
# 需要先执行 make build 生成 bin/echo-service。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNIT_SRC="$ROOT/deploy/echo-service.service"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
UNIT_DST="$UNIT_DIR/echo-service.service"

[[ -x "$ROOT/bin/echo-service" ]] || {
  echo "缺少 $ROOT/bin/echo-service，请先执行 make build" >&2
  exit 1
}
mkdir -p "$UNIT_DIR" "$ROOT/.tmp"

# systemd 的 WorkingDirectory 不接受含空格但不转义的路径；这里用 sed 替换占位符。
ROOT_ESC=${ROOT//&/\\&}
sed "s|__APP_DIR__|$ROOT_ESC|g" "$UNIT_SRC" > "$UNIT_DST"

systemctl --user daemon-reload
systemctl --user enable --now echo-service.service
echo "已安装：$UNIT_DST"
echo "查看状态：systemctl --user status echo-service"
echo "日志：tail -f $ROOT/.tmp/echo-service.log"
