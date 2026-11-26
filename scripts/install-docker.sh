#!/usr/bin/env bash
set -euo pipefail

# UpstreamOps 一键 Docker 部署 / 升级脚本
# 支持直接通过 curl | bash 运行，也可带参数：
#   curl -fsSL https://raw.githubusercontent.com/weandy/upstream-ops/main/scripts/install-docker.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/weandy/upstream-ops/main/scripts/install-docker.sh | bash -s -- --port 8418 --tag latest

REPO_RAW_BASE="${UPSTREAM_OPS_RAW_BASE:-https://raw.githubusercontent.com/weandy/upstream-ops/main}"
DEFAULT_DIR="/opt/upstream-ops"
if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  DEFAULT_DIR="${HOME}/upstream-ops"
fi

INSTALL_DIR="${UPSTREAM_OPS_DIR:-$DEFAULT_DIR}"
DEPLOY_MODE="${DEPLOY_MODE:-}"
HTTP_BIND_IP="${HTTP_BIND_IP:-}"
HTTP_PORT="${HTTP_PORT:-}"
IMAGE_TAG="${IMAGE_TAG:-}"
DB_MODE="${DB_MODE:-}"
TRUSTED_PROXIES="${SERVER_TRUSTED_PROXIES:-}"
TRUSTED_PROXIES_SET="false"
if [[ -n "${SERVER_TRUSTED_PROXIES+x}" ]]; then
  TRUSTED_PROXIES_SET="true"
fi
SKIP_PULL="false"
TMP_STAGE=""

cleanup() {
  if [[ -n "${TMP_STAGE:-}" && -d "$TMP_STAGE" ]]; then
    rm -rf "$TMP_STAGE"
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
UpstreamOps 一键 Docker 部署脚本

用法:
  install-docker.sh [选项]

选项:
  --dir <路径>              安装与数据目录（root 默认 /opt/upstream-ops，普通用户默认 ~/upstream-ops）
  --mode <模式>             绑定模式：direct（新安装默认，绑定 0.0.0.0）| reverse-proxy（绑定 127.0.0.1）
  --bind <IP>               宿主机监听 IP（覆盖 --mode 默认值，支持 IPv4 / IPv6）
  --port <端口>             宿主机对外 HTTP 端口（新安装默认 8418）
  --tag <标签>              Docker 镜像标签（如 latest、v0.4.2、edge；新安装默认 latest）
  --db <类型>               数据库类型：sqlite（新安装默认）| mysql | postgres（升级时默认沿用现有配置）
  --trusted-proxies <列表>  逗号分隔的受信反代 IP/CIDR（写入 SERVER_TRUSTED_PROXIES）
  --no-pull                 跳过 docker compose pull，直接启动/重建容器
  -h, --help                显示帮助信息
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir)
      INSTALL_DIR="${2:?--dir 需要传入目录路径}"
      shift 2
      ;;
    --mode)
      DEPLOY_MODE="${2:?--mode 需要传入 direct 或 reverse-proxy}"
      shift 2
      ;;
    --bind)
      HTTP_BIND_IP="${2:?--bind 需要传入 IP 地址}"
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
    --trusted-proxies)
      TRUSTED_PROXIES="${2-}"
      TRUSTED_PROXIES_SET="true"
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

log() {
  printf '[upstream-ops] %s\n' "$*"
}

die() {
  echo "错误: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

validate_port() {
  local p="$1"
  [[ "$p" =~ ^[0-9]+$ ]] && (( p >= 1 && p <= 65535 ))
}

normalize_ip() {
  local raw="$1"
  raw="${raw#[}"
  raw="${raw%]}"
  printf '%s' "$raw"
}

validate_ip() {
  local ip
  ip="$(normalize_ip "$1")"
  [[ -n "$ip" ]] || return 1
  if [[ "$ip" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
    local o1 o2 o3 o4
    IFS='.' read -r o1 o2 o3 o4 <<<"$ip"
    for o in "$o1" "$o2" "$o3" "$o4"; do
      (( 10#$o >= 0 && 10#$o <= 255 )) || return 1
    done
    return 0
  fi
  if [[ "$ip" == *:* && "$ip" =~ ^[0-9a-fA-F:]+$ ]]; then
    return 0
  fi
  return 1
}

probe_host_for_bind() {
  local ip
  ip="$(normalize_ip "$1")"
  case "$ip" in
    ""|"0.0.0.0")
      printf '127.0.0.1'
      ;;
    "::")
      printf '[::1]'
      ;;
    *:*)
      printf '[%s]' "$ip"
      ;;
    *)
      printf '%s' "$ip"
      ;;
  esac
}

if [[ -n "$DEPLOY_MODE" ]]; then
  case "$DEPLOY_MODE" in
    direct|reverse-proxy) ;;
    *)
      die "不支持的 --mode 类型: $DEPLOY_MODE（仅支持 direct / reverse-proxy）"
      ;;
  esac
fi

if [[ -n "$HTTP_BIND_IP" ]]; then
  validate_ip "$HTTP_BIND_IP" || die "无效的 --bind IP 地址: $HTTP_BIND_IP"
  HTTP_BIND_IP="$(normalize_ip "$HTTP_BIND_IP")"
fi

if [[ -n "$HTTP_PORT" ]]; then
  validate_port "$HTTP_PORT" || die "无效的 --port 端口号: $HTTP_PORT（须为 1-65535 整数）"
fi

if [[ -n "$DB_MODE" ]]; then
  case "$DB_MODE" in
    sqlite|mysql|postgres) ;;
    *)
      die "不支持的 --db 类型: $DB_MODE（仅支持 sqlite / mysql / postgres）"
      ;;
  esac
fi

need_cmd docker || die "未检测到 docker，请先安装 Docker（例如: curl -fsSL https://get.docker.com | sh）。"

COMPOSE_CMD=()
if docker compose version >/dev/null 2>&1; then
  COMPOSE_CMD=(docker compose)
elif need_cmd docker-compose; then
  COMPOSE_CMD=(docker-compose)
else
  die "未检测到 docker compose 插件或 docker-compose，请先安装 Docker Compose。"
fi

if ! need_cmd curl && ! need_cmd wget; then
  die "需要 curl 或 wget 来下载编排文件并检查服务状态。"
fi

download_file() {
  local rel="$1"
  local dst="$2"
  local url="${REPO_RAW_BASE}/${rel}"
  if need_cmd curl; then
    curl -fsSL --max-time 30 "$url" -o "$dst"
  else
    wget -q -T 30 -O "$dst" "$url"
  fi
  [[ -s "$dst" ]] || die "下载文件为空或失败: $url"
}

gen_hex_bytes() {
  local nbytes="$1"
  if need_cmd openssl; then
    openssl rand -hex "$nbytes"
  elif [[ -r /dev/urandom ]] && need_cmd od; then
    LC_ALL=C od -An -tx1 -N"$nbytes" /dev/urandom | tr -d ' \r\n'
  else
    die "无法安全生成随机密钥（需要 openssl 或 /dev/urandom + od）。"
  fi
}

gen_secret() {
  gen_hex_bytes 32
}

gen_password() {
  gen_hex_bytes 12
}

is_placeholder_secret() {
  local s="${1:-}"
  local lower
  lower="$(printf '%s' "$s" | tr '[:upper:]' '[:lower:]')"
  [[ -z "$s" || "${#s}" -lt 32 || "$lower" == *"please-change-me"* || "$lower" == *"replace-with-"* || "$lower" == *"change-me"* ]]
}

set_env_kv() {
  local file="$1"
  local key="$2"
  local val="$3"
  local tmp="${file}.tmp.$$"
  if grep -qE "^${key}=" "$file"; then
    UPSTREAM_OPS_VAL="$val" awk -v k="$key" '
      BEGIN { FS="="; OFS="=" }
      $1 == k { print k, ENVIRON["UPSTREAM_OPS_VAL"]; next }
      { sub(/\r$/, ""); print }
    ' "$file" >"$tmp" && mv "$tmp" "$file"
  else
    printf '%s=%s\n' "$key" "$val" >>"$file"
  fi
}

get_env_val() {
  local file="$1"
  local key="$2"
  awk -v k="$key" -F= '$1 == k { sub(/^[^=]*=/, ""); sub(/\r$/, ""); print; exit }' "$file"
}

infer_db_mode() {
  local dir="$1"
  local env_file="$dir/.env"
  if [[ -f "$env_file" ]]; then
    local saved_mode
    saved_mode="$(get_env_val "$env_file" "DB_MODE" || true)"
    case "$saved_mode" in
      sqlite|mysql|postgres)
        printf '%s' "$saved_mode"
        return 0
        ;;
    esac
    local saved_driver
    saved_driver="$(get_env_val "$env_file" "DATABASE_DRIVER" || true)"
    case "$saved_driver" in
      mysql|postgres)
        printf '%s' "$saved_driver"
        return 0
        ;;
    esac
  fi

  local has_mysql="false"
  local has_postgres="false"
  [[ -f "$dir/docker-compose.mysql.yml" ]] && has_mysql="true"
  [[ -f "$dir/docker-compose.postgres.yml" ]] && has_postgres="true"

  if [[ "$has_mysql" == "true" && "$has_postgres" == "true" ]]; then
    die "同时检测到 docker-compose.mysql.yml 与 docker-compose.postgres.yml，请显式传入 --db sqlite|mysql|postgres。"
  elif [[ "$has_mysql" == "true" ]]; then
    printf 'mysql'
    return 0
  elif [[ "$has_postgres" == "true" ]]; then
    printf 'postgres'
    return 0
  fi

  printf 'sqlite'
}

log "准备部署目录: $INSTALL_DIR"
mkdir -p "$INSTALL_DIR/data"
cd "$INSTALL_DIR"

if [[ -z "$DB_MODE" ]]; then
  DB_MODE="$(infer_db_mode "$INSTALL_DIR")"
fi
log "数据库模式: $DB_MODE"

TMP_STAGE="$(mktemp -d)"
log "下载并校验 Docker Compose 编排文件..."
download_file "docker-compose.yml" "$TMP_STAGE/docker-compose.yml"
download_file ".env.example" "$TMP_STAGE/.env.example"
STAGED_COMPOSE_FILES=(-f "$TMP_STAGE/docker-compose.yml")
if [[ "$DB_MODE" == "mysql" ]]; then
  download_file "docker-compose.mysql.yml" "$TMP_STAGE/docker-compose.mysql.yml"
  STAGED_COMPOSE_FILES+=(-f "$TMP_STAGE/docker-compose.mysql.yml")
elif [[ "$DB_MODE" == "postgres" ]]; then
  download_file "docker-compose.postgres.yml" "$TMP_STAGE/docker-compose.postgres.yml"
  STAGED_COMPOSE_FILES+=(-f "$TMP_STAGE/docker-compose.postgres.yml")
fi

HAS_EXISTING_DATA="false"
if [[ -f "$INSTALL_DIR/data/upstream-ops.db" || -f "$INSTALL_DIR/data/config.yaml" ]]; then
  HAS_EXISTING_DATA="true"
fi

CREATED_NEW_ENV="false"
GENERATED_ADMIN_PASSWORD="false"
umask 077
if [[ ! -f "$INSTALL_DIR/.env" ]]; then
  if [[ "$HAS_EXISTING_DATA" == "true" ]]; then
    die "检测到已有 $INSTALL_DIR/data 数据但缺少 .env。请先恢复原有的 .env（特别是 APP_SECRET），再重新运行升级。"
  fi
  cp "$TMP_STAGE/.env.example" "$TMP_STAGE/.env"
  chmod 600 "$TMP_STAGE/.env"
  set_env_kv "$TMP_STAGE/.env" "APP_SECRET" "$(gen_secret)"
  set_env_kv "$TMP_STAGE/.env" "ADMIN_PASSWORD" "$(gen_password)"
  set_env_kv "$TMP_STAGE/.env" "DB_MODE" "$DB_MODE"
  set_env_kv "$TMP_STAGE/.env" "SERVER_REQUIRE_SECURE_AUTH" "true"
  if [[ -z "$HTTP_BIND_IP" ]]; then
    if [[ "$DEPLOY_MODE" == "reverse-proxy" ]]; then
      HTTP_BIND_IP="127.0.0.1"
    else
      HTTP_BIND_IP="0.0.0.0"
    fi
  fi
  set_env_kv "$TMP_STAGE/.env" "HTTP_BIND_IP" "$HTTP_BIND_IP"
  set_env_kv "$TMP_STAGE/.env" "HTTP_PORT" "${HTTP_PORT:-8418}"
  set_env_kv "$TMP_STAGE/.env" "IMAGE_TAG" "${IMAGE_TAG:-latest}"
  if [[ "$TRUSTED_PROXIES_SET" == "true" ]]; then
    set_env_kv "$TMP_STAGE/.env" "SERVER_TRUSTED_PROXIES" "$TRUSTED_PROXIES"
  fi
  CREATED_NEW_ENV="true"
  GENERATED_ADMIN_PASSWORD="true"
  log "已生成 .env（权限 0600），并创建随机 APP_SECRET 与随机管理员初始密码。"
else
  cp "$INSTALL_DIR/.env" "$TMP_STAGE/.env"
  chmod 600 "$TMP_STAGE/.env"

  CUR_SECRET="$(get_env_val "$TMP_STAGE/.env" "APP_SECRET" || true)"
  if is_placeholder_secret "$CUR_SECRET"; then
    if [[ "$HAS_EXISTING_DATA" == "true" ]]; then
      die "检测到已有数据但 .env 中的 APP_SECRET 为空或占位值。为防止既有加密数据无法解密，已中止自动覆盖；请核实原 APP_SECRET 后手动修复 .env。"
    fi
    set_env_kv "$TMP_STAGE/.env" "APP_SECRET" "$(gen_secret)"
    log "检测到 .env 中 APP_SECRET 为空或占位值（且无存量数据），已自动替换为随机密钥。"
  else
    log "检测到已有 .env，保留现有 APP_SECRET 与核心配置。"
  fi

  CUR_ADMIN_PW="$(get_env_val "$TMP_STAGE/.env" "ADMIN_PASSWORD" || true)"
  if [[ ! -f "$INSTALL_DIR/data/config.yaml" ]]; then
    if [[ -z "$CUR_ADMIN_PW" || "$CUR_ADMIN_PW" == "admin_ops" ]]; then
      set_env_kv "$TMP_STAGE/.env" "ADMIN_PASSWORD" "$(gen_password)"
      GENERATED_ADMIN_PASSWORD="true"
      log "尚未生成 data/config.yaml，已为首次启动写入随机管理员强密码。"
    fi
  fi

  set_env_kv "$TMP_STAGE/.env" "DB_MODE" "$DB_MODE"
  if [[ -n "$HTTP_BIND_IP" ]]; then
    set_env_kv "$TMP_STAGE/.env" "HTTP_BIND_IP" "$HTTP_BIND_IP"
  elif [[ "$DEPLOY_MODE" == "reverse-proxy" ]]; then
    set_env_kv "$TMP_STAGE/.env" "HTTP_BIND_IP" "127.0.0.1"
  elif [[ "$DEPLOY_MODE" == "direct" ]]; then
    set_env_kv "$TMP_STAGE/.env" "HTTP_BIND_IP" "0.0.0.0"
  elif [[ -z "$(get_env_val "$TMP_STAGE/.env" "HTTP_BIND_IP" || true)" ]]; then
    set_env_kv "$TMP_STAGE/.env" "HTTP_BIND_IP" "0.0.0.0"
  fi
  if [[ -n "$HTTP_PORT" ]]; then
    set_env_kv "$TMP_STAGE/.env" "HTTP_PORT" "$HTTP_PORT"
  fi
  if [[ -n "$IMAGE_TAG" ]]; then
    set_env_kv "$TMP_STAGE/.env" "IMAGE_TAG" "$IMAGE_TAG"
  fi
  if [[ "$TRUSTED_PROXIES_SET" == "true" ]]; then
    set_env_kv "$TMP_STAGE/.env" "SERVER_TRUSTED_PROXIES" "$TRUSTED_PROXIES"
  fi
  if [[ -z "$(get_env_val "$TMP_STAGE/.env" "SERVER_REQUIRE_SECURE_AUTH" || true)" ]]; then
    set_env_kv "$TMP_STAGE/.env" "SERVER_REQUIRE_SECURE_AUTH" "true"
  fi
fi

if [[ "$DB_MODE" == "mysql" ]]; then
  CUR_PW="$(get_env_val "$TMP_STAGE/.env" "MYSQL_PASSWORD" || true)"
  CUR_ROOT_PW="$(get_env_val "$TMP_STAGE/.env" "MYSQL_ROOT_PASSWORD" || true)"
  if [[ -z "$CUR_PW" || "$CUR_PW" == "change-me" || -z "$CUR_ROOT_PW" || "$CUR_ROOT_PW" == "change-me-root" ]]; then
    if [[ "$HAS_EXISTING_DATA" == "true" ]] && docker volume ls -q 2>/dev/null | grep -q 'mysql-data'; then
      die "检测到已有 MySQL 数据卷且 .env 中仍为空或占位密码（change-me），请先在 MySQL 内修改密码并手动写入 .env 后再升级。"
    fi
    [[ -z "$CUR_PW" || "$CUR_PW" == "change-me" ]] && set_env_kv "$TMP_STAGE/.env" "MYSQL_PASSWORD" "$(gen_password)"
    [[ -z "$CUR_ROOT_PW" || "$CUR_ROOT_PW" == "change-me-root" ]] && set_env_kv "$TMP_STAGE/.env" "MYSQL_ROOT_PASSWORD" "$(gen_password)"
    log "已为 MySQL 生成随机密码并写入 .env。"
  fi
elif [[ "$DB_MODE" == "postgres" ]]; then
  CUR_PW="$(get_env_val "$TMP_STAGE/.env" "POSTGRES_PASSWORD" || true)"
  if [[ -z "$CUR_PW" || "$CUR_PW" == "change-me" ]]; then
    if [[ "$HAS_EXISTING_DATA" == "true" ]] && docker volume ls -q 2>/dev/null | grep -q 'postgres-data'; then
      die "检测到已有 PostgreSQL 数据卷且 .env 中仍为空或占位密码（change-me），请先在 PostgreSQL 内修改密码并手动写入 .env 后再升级。"
    fi
    set_env_kv "$TMP_STAGE/.env" "POSTGRES_PASSWORD" "$(gen_password)"
    log "已为 PostgreSQL 生成随机密码并写入 .env。"
  fi
fi

log "校验 Compose 配置..."
(cd "$TMP_STAGE" && "${COMPOSE_CMD[@]}" "${STAGED_COMPOSE_FILES[@]}" config -q)

mv "$TMP_STAGE/docker-compose.yml" "$INSTALL_DIR/docker-compose.yml"
mv "$TMP_STAGE/.env.example" "$INSTALL_DIR/.env.example"
if [[ "$DB_MODE" == "mysql" ]]; then
  mv "$TMP_STAGE/docker-compose.mysql.yml" "$INSTALL_DIR/docker-compose.mysql.yml"
elif [[ "$DB_MODE" == "postgres" ]]; then
  mv "$TMP_STAGE/docker-compose.postgres.yml" "$INSTALL_DIR/docker-compose.postgres.yml"
fi
mv "$TMP_STAGE/.env" "$INSTALL_DIR/.env"
chmod 600 "$INSTALL_DIR/.env"

COMPOSE_FILES=(-f "$INSTALL_DIR/docker-compose.yml")
if [[ "$DB_MODE" == "mysql" ]]; then
  COMPOSE_FILES+=(-f "$INSTALL_DIR/docker-compose.mysql.yml")
elif [[ "$DB_MODE" == "postgres" ]]; then
  COMPOSE_FILES+=(-f "$INSTALL_DIR/docker-compose.postgres.yml")
fi

FINAL_TAG="$(get_env_val "$INSTALL_DIR/.env" "IMAGE_TAG" || echo latest)"
FINAL_TAG="${FINAL_TAG:-latest}"

if [[ "$SKIP_PULL" != "true" ]]; then
  log "拉取镜像 ghcr.io/weandy/upstream-ops:${FINAL_TAG}..."
  "${COMPOSE_CMD[@]}" "${COMPOSE_FILES[@]}" pull
fi

log "启动容器..."
if "${COMPOSE_CMD[@]}" up --help 2>&1 | grep -q -- '--wait'; then
  "${COMPOSE_CMD[@]}" "${COMPOSE_FILES[@]}" up -d --wait --wait-timeout 90
else
  "${COMPOSE_CMD[@]}" "${COMPOSE_FILES[@]}" up -d
fi

FINAL_BIND_IP="$(get_env_val "$INSTALL_DIR/.env" "HTTP_BIND_IP" || echo 0.0.0.0)"
FINAL_BIND_IP="${FINAL_BIND_IP:-0.0.0.0}"
FINAL_PORT="$(get_env_val "$INSTALL_DIR/.env" "HTTP_PORT" || echo 8418)"
FINAL_PORT="${FINAL_PORT:-8418}"
PROBE_HOST="$(probe_host_for_bind "$FINAL_BIND_IP")"
PROBE_URL="http://${PROBE_HOST}:${FINAL_PORT}/healthz"

log "等待宿主机端口健康检查就绪 (${PROBE_URL})..."
READY="false"
for _ in $(seq 1 30); do
  if need_cmd curl; then
    if curl -fsS --max-time 5 "$PROBE_URL" >/dev/null 2>&1; then
      READY="true"
      break
    fi
  elif need_cmd wget; then
    if wget -qO- -T 5 "$PROBE_URL" >/dev/null 2>&1; then
      READY="true"
      break
    fi
  fi
  sleep 2
done

if [[ "$READY" != "true" ]]; then
  echo "错误: 服务在预期时间内未通过健康检查 (${PROBE_URL})。" >&2
  echo "请运行以下排查命令查看容器状态与日志:" >&2
  echo "  cd ${INSTALL_DIR} && ${COMPOSE_CMD[*]} ${COMPOSE_FILES[*]} ps" >&2
  echo "  cd ${INSTALL_DIR} && ${COMPOSE_CMD[*]} ${COMPOSE_FILES[*]} logs --tail=100 app" >&2
  exit 1
fi

ACCESS_HINT="http://${PROBE_HOST}:${FINAL_PORT}"
if [[ "$FINAL_BIND_IP" == "0.0.0.0" || "$FINAL_BIND_IP" == "::" ]]; then
  ACCESS_HINT="http://<服务器公网IP>:${FINAL_PORT}  (本机测试: http://${PROBE_HOST}:${FINAL_PORT})"
fi

cat <<EOF

============================================================
UpstreamOps 部署完成
============================================================
访问地址: ${ACCESS_HINT}
监听绑定: ${FINAL_BIND_IP}:${FINAL_PORT}
安装目录: ${INSTALL_DIR}
数据目录: ${INSTALL_DIR}/data
默认鉴权: 已强制开启（admin + 独立强密码）
EOF

if [[ "$GENERATED_ADMIN_PASSWORD" == "true" ]]; then
  cat <<EOF
初始密码: 已安全写入 ${INSTALL_DIR}/.env（未在终端明文回显）
查看密码: grep '^ADMIN_PASSWORD=' ${INSTALL_DIR}/.env
EOF
elif [[ "$CREATED_NEW_ENV" == "false" ]]; then
  echo "后台账号: 沿用现有配置（${INSTALL_DIR}/data/config.yaml）"
fi

if [[ "$FINAL_BIND_IP" == "0.0.0.0" || "$FINAL_BIND_IP" == "::" ]]; then
  echo "安全提示: 当前以 HTTP 直连绑定在 ${FINAL_BIND_IP}:${FINAL_PORT}，建议配合防火墙放行或后续套 HTTPS 反代。"
else
  echo "反代提示: 当前仅绑定本机回环 ${FINAL_BIND_IP}:${FINAL_PORT}，由反代转发时请核对 SERVER_TRUSTED_PROXIES。"
fi

cat <<EOF
健康检查: 已就绪 (/healthz ok)

常用运维命令:
  cd ${INSTALL_DIR}
  ${COMPOSE_CMD[*]} logs -f app          # 查看日志
  ${COMPOSE_CMD[*]} pull && ${COMPOSE_CMD[*]} up -d   # 更新到最新镜像
  ${COMPOSE_CMD[*]} down                 # 停止并移除容器（保留 data 数据）
============================================================
EOF
