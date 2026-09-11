#!/usr/bin/env bash
# 本组件自己的种子数据：5 个示例产品，覆盖三种 TrackingType（NONE/
# BATCH/SERIAL）+ ACTIVE/DISABLED 两种状态（不止 happy path，总纲
# SOP-W-7"完整度要求"）。
#
# ⚠️ 只给本地开发/演示用，不出现在任何部署/CI 流程里。数据都是假的，
# 灌进的是真实运行中的本组件数据库（用户已明确同意，见根仓库
# feedback_direct_db_seeding_ok 记录）。全程走真实 gRPC 调用，不是直接
# 写库——claim-first 幂等（固定 idempotency_key），重复跑不会重复建。
#
# 零强依赖——本组件是叶子，`make -C components/mdm/product seed` 只需要
# 本组件自己的容器在跑（同 mdm-customer 的既有样板）。
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(cd "$DIR/../../.." && pwd)"

C_GRN=$'\033[32m'; C_RED=$'\033[31m'; C_OFF=$'\033[0m'
ok()  { echo "${C_GRN}✓${C_OFF} $*"; }
die() { echo "${C_RED}✗${C_OFF} $*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1"; }
need docker; need python3

NET="${BRICKKIT_NET:-brickkit-$(basename "$ROOT")-net}"
docker network inspect "$NET" >/dev/null 2>&1 || die "docker 网络 $NET 不存在——先把本组件 brickkit up 起来（整套或只装这一个）"

CNAME="$(docker ps --filter "name=${NET%-net}-mdm-product-" --format '{{.Names}}' | head -1)"
[ -n "$CNAME" ] || die "mdm-product 容器没在跑——先 brickkit up"

GRPC_PORT="$(awk -F'\t' '$2=="mdm/product"{print $4}' "$ROOT/registry/ports.tsv")"
[ -n "$GRPC_PORT" ] || die "registry/ports.tsv 里找不到 mdm/product 的 grpc 端口"

GRPCURL="docker run --rm --network $NET -v $DIR/contracts:/contracts:ro fullstorydev/grpcurl:latest"
TARGET="$CNAME:$GRPC_PORT"

# base_uom_id=1 是迁移播种数据里 "EA"（个）那一行——固定顺序插入的第一条，
# 同 erp-sales WH-EAST 那个既有假设一样的判据（迁移只跑一次，序列号
# 确定）。本组件没有 UoM 管理接口（设计计划 §9），没有别的办法查它的 id。
BASE_UOM_ID="1"

mkproduct() {
  local key="$1" name="$2" cost="$3" tracking="$4"
  $GRPCURL -plaintext -import-path /contracts -proto mdm/product/v1/product.proto \
    -d "{\"idempotency_key\":\"$key\",\"name\":\"$name\",\"base_uom_id\":\"$BASE_UOM_ID\",\"tracking_type\":\"$tracking\",\"standard_cost\":\"$cost\"}" \
    "$TARGET" mdm.product.v1.ProductService/Create \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["product"]["id"])'
}

setstatus() {
  local key="$1" id="$2" status="$3"
  local version
  version="$($GRPCURL -plaintext -import-path /contracts -proto mdm/product/v1/product.proto \
    -d "{\"id\":\"$id\"}" "$TARGET" mdm.product.v1.ProductService/Get \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])')"
  $GRPCURL -plaintext -import-path /contracts -proto mdm/product/v1/product.proto \
    -d "{\"idempotency_key\":\"$key\",\"id\":\"$id\",\"version\":$version,\"status\":\"$status\"}" \
    "$TARGET" mdm.product.v1.ProductService/SetStatus >/dev/null
}

echo "── mdm-product：灌 5 个示例产品 ──"
P1="$(mkproduct seed-product-1 "「本地测试」标准螺栓 M8" 0.50 TRACKING_TYPE_NONE)"
P2="$(mkproduct seed-product-2 "「本地测试」工业润滑油 20L" 120.00 TRACKING_TYPE_BATCH)"
P3="$(mkproduct seed-product-3 "「本地测试」不锈钢管件 DN50" 35.00 TRACKING_TYPE_NONE)"
P4="$(mkproduct seed-product-4 "「本地测试」工业设备主控制器" 880.00 TRACKING_TYPE_SERIAL)"
P5="$(mkproduct seed-product-5 "「本地测试」包装纸箱 60x40x40（已停用样例）" 3.20 TRACKING_TYPE_NONE)"
setstatus seed-product-5-disable "$P5" PRODUCT_STATUS_DISABLED

ok "产品：$P1(NONE) $P2(BATCH) $P3(NONE) $P4(SERIAL) $P5(DISABLED)"
