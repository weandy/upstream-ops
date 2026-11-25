#!/usr/bin/env bash
set -euo pipefail

# UpstreamOps 一键 Docker 部署 / 升级脚本
# 支持直接通过 curl | bash 运行，也可带参数：
#   curl -fsSL https://raw.githubusercontent.com/weandy/upstream-ops/main/scripts/install-docker.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/weandy/upstream-ops/main/scripts/install-docker.sh | bash -s -- --port 8080 --tag latest

REPO_RAW_BASE="${UPSTREAM_OPS_RAW_BASE:-https://raw.githubusercontent.com/weandy/upstream-ops/main}"
DEFAULT_DIR="/opt/upstream-ops"
if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  DEFAULT_DIR="${HOME}/upstream-ops"
fi

INSTALL_DIR="${UPSTREAM_OPS_DIR:-$DEFAULT_DIR}"
HTTP_PORT="${HTTP_PORT:-}"
IMAGE_TAG="${IMAGE_TAG:-}"
DB_MODE="${DB_MODE:-sqlite}"
SKIP_PULL="false"

usage() {
  cat <<'EOF'
UpstreamOps 一键 Docker 部署脚本

用法:
  install-docker.sh [选项]

选项:
  --dir <路径>       安装与数据目录（root 默认 /opt/upstream-ops，普通用户默认 ~/upstream-ops）
  --port <端口>      宿主机对外 HTTP 端口（新安装默认 8080）
  --tag <标签>       Docker 镜像标签（如 latest、v0.4.1、edge；新安装默认 latest）
  --db <类型>        数据库类型：sqlite（默认）| mysql | postgres
  --no-pull          跳过 docker compose pull，直接启动/重建容器
  -h, --help         显示帮助信息
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir)
      INSTALL_DIR="${2:?--dir 需要传入目录路径}"
      shift 2
      ;;
    --port)
      HTTP_PORT="${2:?--port 需要传入端口号}"
      shift 2
      ;;
    --tag)
      IMAGE_TAG="${2:?--tag 需要传入镜像标签}"
      shift 2
      ;;
    --db)
      DB_MODE="${2:?--db 需要传入 sqlite|mysql|postgres}"
      shift 2
      ;;
    --no-pull)
      SKIP_PULL="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

case "$DB_MODE" in
  sqlite|mysql|postgres) ;;
  *)
    echo "不支持的 --db 类型: $DB_MODE（仅支持 sqlite / mysql / postgres）" >&2
    exit 1
    ;;
esac

log() {
  printf '[upstream-ops] %s\n' "$*"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

if ! need_cmd docker; then
  echo "错误: 未检测到 docker，请先安装 Docker（例如: curl -fsSL https://get.docker.com | sh）。" >&2
  exit 1
fi

COMPOSE_CMD=()
if docker compose version >/dev/null 2>&1; then
  COMPOSE_CMD=(docker compose)
elif need_cmd docker-compose; then
  COMPOSE_CMD=(docker-compose)
else
  echo "错误: 未检测到 docker compose 插件或 docker-compose，请先安装 Docker Compose。" >&2
  exit 1
fi

if ! need_cmd curl && ! need_cmd wget; then
  echo "错误: 需要 curl 或 wget 来下载编排文件。" >&2
  exit 1
fi

download_file() {
  local rel="$1"
  local dst="$2"
  local url="${REPO_RAW_BASE}/${rel}"
  if need_cmd curl; then
    curl -fsSL "$url" -o "$dst"
  else
    wget -qO "$dst" "$url"
  fi
}

gen_secret() {
  if need_cmd openssl; then
    openssl rand -hex 32
  elif [[ -r /dev/urandom ]]; then
    od -An -tx1 -N32 /dev/urandom | tr -d ' \n'
  else
    date +%s%N | sha256sum | awk '{print $1}'
  fi
}

set_env_kv() {
  local file="$1"
  local key="$2"
  local val="$3"
  local tmp="${file}.tmp.$$"
  if grep -qE "^${key}=" "$file"; then
    awk -v k="$key" -v v="$val" '
      BEGIN { FS="="; OFS="=" }
      $1 == k { print k, v; next }
      { print }
    ' "$file" >"$tmp" && mv "$tmp" "$file"
  else
    printf '%s=%s\n' "$key" "$val" >>"$file"
  fi
}

get_env_val() {
  local file="$1"
  local key="$2"
  awk -v k="$key" -F= '$1 == k { sub(/^[^=]*=/, ""); print; exit }' "$file"
}

log "准备部署目录: $INSTALL_DIR"
mkdir -p "$INSTALL_DIR/data"
cd "$INSTALL_DIR"

log "同步 Docker Compose 编排文件..."
download_file "docker-compose.yml" "$INSTALL_DIR/docker-compose.yml"
download_file ".env.example" "$INSTALL_DIR/.env.example"
if [[ "$DB_MODE" == "mysql" ]]; then
  download_file "docker-compose.mysql.yml" "$INSTALL_DIR/docker-compose.mysql.yml"
elif [[ "$DB_MODE" == "postgres" ]]; then
  download_file "docker-compose.postgres.yml" "$INSTALL_DIR/docker-compose.postgres.yml"
fi

if [[ ! -f "$INSTALL_DIR/.env" ]]; then
  cp "$INSTALL_DIR/.env.example" "$INSTALL_DIR/.env"
  chmod 600 "$INSTALL_DIR/.env" || true
  SECRET="$(gen_secret)"
  set_env_kv "$INSTALL_DIR/.env" "APP_SECRET" "$SECRET"
  set_env_kv "$INSTALL_DIR/.env" "HTTP_PORT" "${HTTP_PORT:-8080}"
  set_env_kv "$INSTALL_DIR/.env" "IMAGE_TAG" "${IMAGE_TAG:-latest}"
  log "已生成 .env 并自动创建随机 APP_SECRET。"
else
  CUR_SECRET="$(get_env_val "$INSTALL_DIR/.env" "APP_SECRET" || true)"
  if [[ -z "$CUR_SECRET" || "$CUR_SECRET" == *"please-change-me"* ]]; then
    SECRET="$(gen_secret)"
    set_env_kv "$INSTALL_DIR/.env" "APP_SECRET" "$SECRET"
    log "检测到 .env 中 APP_SECRET 为占位值，已自动替换为随机密钥。"
  else
    log "检测到已有 .env，保留现有 APP_SECRET 与基础设施配置。"
  fi
  if [[ -n "$HTTP_PORT" ]]; then
    set_env_kv "$INSTALL_DIR/.env" "HTTP_PORT" "$HTTP_PORT"
  fi
  if [[ -n "$IMAGE_TAG" ]]; then
    set_env_kv "$INSTALL_DIR/.env" "IMAGE_TAG" "$IMAGE_TAG"
  fi
fi

if [[ "$DB_MODE" == "mysql" ]]; then
  CUR_PW="$(get_env_val "$INSTALL_DIR/.env" "MYSQL_PASSWORD" || true)"
  if [[ -z "$CUR_PW" || "$CUR_PW" == "change-me" ]]; then
    set_env_kv "$INSTALL_DIR/.env" "MYSQL_PASSWORD" "$(gen_secret | cut -c1-24)"
    set_env_kv "$INSTALL_DIR/.env" "MYSQL_ROOT_PASSWORD" "$(gen_secret | cut -c1-24)"
    log "已为 MySQL 生成随机密码并写入 .env。"
  fi
elif [[ "$DB_MODE" == "postgres" ]]; then
  CUR_PW="$(get_env_val "$INSTALL_DIR/.env" "POSTGRES_PASSWORD" || true)"
  if [[ -z "$CUR_PW" || "$CUR_PW" == "change-me" ]]; then
    set_env_kv "$INSTALL_DIR/.env" "POSTGRES_PASSWORD" "$(gen_secret | cut -c1-24)"
    log "已为 PostgreSQL 生成随机密码并写入 .env。"
  fi
fi

COMPOSE_FILES=(-f "$INSTALL_DIR/docker-compose.yml")
if [[ "$DB_MODE" == "mysql" ]]; then
  COMPOSE_FILES+=(-f "$INSTALL_DIR/docker-compose.mysql.yml")
elif [[ "$DB_MODE" == "postgres" ]]; then
  COMPOSE_FILES+=(-f "$INSTALL_DIR/docker-compose.postgres.yml")
fi

if [[ "$SKIP_PULL" != "true" ]]; then
  log "拉取镜像 ghcr.io/weandy/upstream-ops:$(get_env_val "$INSTALL_DIR/.env" "IMAGE_TAG" || echo latest)..."
  "${COMPOSE_CMD[@]}" "${COMPOSE_FILES[@]}" pull
fi

log "启动容器..."
"${COMPOSE_CMD[@]}" "${COMPOSE_FILES[@]}" up -d

FINAL_PORT="$(get_env_val "$INSTALL_DIR/.env" "HTTP_PORT" || echo 8080)"
FINAL_PORT="${FINAL_PORT:-8080}"

log "等待服务健康检查就绪 (http://127.0.0.1:${FINAL_PORT}/healthz)..."
READY="false"
for _ in $(seq 1 30); do
  if need_cmd curl; then
    if curl -fsS "http://127.0.0.1:${FINAL_PORT}/healthz" >/dev/null 2>&1; then
      READY="true"
      break
    fi
  elif need_cmd wget; then
    if wget -qO- "http://127.0.0.1:${FINAL_PORT}/healthz" >/dev/null 2>&1; then
      READY="true"
      break
    fi
  fi
  sleep 2
done

cat <<EOF

============================================================
UpstreamOps 部署完成
============================================================
访问地址: http://localhost:${FINAL_PORT}  (公网访问请替换为服务器 IP / 域名)
安装目录: ${INSTALL_DIR}
数据目录: ${INSTALL_DIR}/data
默认鉴权: 已开启
初始账号: admin
初始密码: admin_ops  (首次登录后强制要求重设账号和密码)
健康检查: $([[ "$READY" == "true" ]] && echo "已就绪 (/healthz ok)" || echo "仍在启动中，请稍候查看日志")

常用运维命令:
  cd ${INSTALL_DIR}
  ${COMPOSE_CMD[*]} logs -f app          # 查看日志
  ${COMPOSE_CMD[*]} pull && ${COMPOSE_CMD[*]} up -d   # 更新到最新镜像
  ${COMPOSE_CMD[*]} down                 # 停止并移除容器（保留 data 数据）
============================================================
EOF
