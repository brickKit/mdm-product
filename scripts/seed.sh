#!/usr/bin/env bash
# 本组件自己的种子数据：12 个示例产品，覆盖三种 TrackingType（NONE/
# BATCH/SERIAL，且每种不止一条样本）+ ACTIVE/DISABLED 两种状态 + 单价
# 从几毛到近千元的完整区间（总纲 SOP-W-7"数据种类越多越好"）。
#
# ⚠️ seed-product-1..5 是已经被 erp-inventory/crm-opportunity 反查引用
# 过的固定 idempotency_key（总纲 SOP-W-7"种子数据是轻量契约"）——只增
# 不改，新增产品一律往后接 seed-product-6 起。
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

echo "── mdm-product：灌 12 个示例产品（1-5 是已被下游引用的固定契约，只增不改）──"
P1="$(mkproduct seed-product-1 "「本地测试」标准螺栓 M8" 0.50 TRACKING_TYPE_NONE)"
P2="$(mkproduct seed-product-2 "「本地测试」工业润滑油 20L" 120.00 TRACKING_TYPE_BATCH)"
P3="$(mkproduct seed-product-3 "「本地测试」不锈钢管件 DN50" 35.00 TRACKING_TYPE_NONE)"
P4="$(mkproduct seed-product-4 "「本地测试」工业设备主控制器" 880.00 TRACKING_TYPE_SERIAL)"
P5="$(mkproduct seed-product-5 "「本地测试」包装纸箱 60x40x40（已停用样例）" 3.20 TRACKING_TYPE_NONE)"
setstatus seed-product-5-disable "$P5" PRODUCT_STATUS_DISABLED

# 6-12 是本轮新增：每种 TrackingType 再多几条样本，单价铺开到几毛到
# 近千元的区间。
P6="$(mkproduct seed-product-6 "「本地测试」六角螺母 M8" 0.20 TRACKING_TYPE_NONE)"
P7="$(mkproduct seed-product-7 "「本地测试」防锈涂料 5L" 68.00 TRACKING_TYPE_BATCH)"
P8="$(mkproduct seed-product-8 "「本地测试」工业密封胶 300ml" 15.50 TRACKING_TYPE_BATCH)"
P9="$(mkproduct seed-product-9 "「本地测试」精密减速机" 1580.00 TRACKING_TYPE_SERIAL)"
P10="$(mkproduct seed-product-10 "「本地测试」变频器控制模块" 2200.00 TRACKING_TYPE_SERIAL)"
P11="$(mkproduct seed-product-11 "「本地测试」PVC 波纹管 DN100" 12.80 TRACKING_TYPE_NONE)"
P12="$(mkproduct seed-product-12 "「本地测试」通用垫片套装（已停用样例）" 4.50 TRACKING_TYPE_NONE)"
setstatus seed-product-12-disable "$P12" PRODUCT_STATUS_DISABLED

ok "产品：$P1(NONE) $P2(BATCH) $P3(NONE) $P4(SERIAL) $P5(DISABLED) $P6(NONE) $P7(BATCH) $P8(BATCH) $P9(SERIAL) $P10(SERIAL) $P11(NONE) $P12(DISABLED)"

# ── 时间跨度回填（同 mdm-customer 的既有判据，products 表也不分区，
# 直接 UPDATE 安全）：只回填新增的 6-12。
psqlx() { docker exec -i be-postgres psql -U postgres -d brickkit_db -v ON_ERROR_STOP=1 -q "$@"; }
psqlx <<SQL
SET search_path TO mdm_product;
UPDATE products SET created_at = now() - interval '4 months', updated_at = now() - interval '4 months' WHERE id = '$P6';
UPDATE products SET created_at = now() - interval '3 months', updated_at = now() - interval '3 months' WHERE id = '$P8';
UPDATE products SET created_at = now() - interval '2 months', updated_at = now() - interval '2 months' WHERE id = '$P9';
UPDATE products SET created_at = now() - interval '1 months', updated_at = now() - interval '1 months' WHERE id = '$P11';
SQL
ok "已给 4 个产品回填历史创建时间（1-4 个月前）"
