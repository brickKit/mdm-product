#!/usr/bin/env bash
# 本组件的种子数据：14 个示例产品，覆盖三种 TrackingType（NONE / BATCH /
# SERIAL，每种不止一条）、ACTIVE / DISABLED 两种状态、标准成本从几毛到上千元，
# 以及两条给关键字搜索 q 演示用的样例：13、14 的 sku 以 BOLT 开头——q=bolt 只
# 命中这两条（sku 前缀匹配，大小写不敏感）；q=螺栓 命中 1、13、14，三条都只是
# 名称包含（名称都以"「本地测试」"开头），同一档按建档时间倒序。
#
# seed-product-1..5 是下游组件（erp/inventory、crm/opportunity 等）的种子脚本
# 按幂等键反查引用的固定产品：只增不改，新产品一律往后接。
#
# 只给本地开发 / 演示用，不出现在任何部署或 CI 流程里。全程走真实 gRPC 调用，
# 幂等键固定，重复跑不会重复建。唯一的例外是末尾的"时间跨度回填"：直接
# UPDATE created_at，让列表的默认时间窗口与排序有新旧之分可看（products 不分区，
# 改 created_at 没有分区放错的风险）。
#
# 没有依赖：只需要本组件自己在跑（make -C components/mdm/product seed）。
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(cd "$DIR/../../.." && pwd)"
source "$ROOT/infra/scripts/lib/seed-net.sh"

need docker; need python3

seed_net_check

GRPC_PORT="$(awk -F'\t' '$2=="mdm/product"{print $4}' "$ROOT/registry/ports.tsv")"
[ -n "$GRPC_PORT" ] || die "registry/ports.tsv 里找不到 mdm/product 的 grpc 端口"

# gRPC 额外端口不映射到宿主机：grpcurl 用容器镜像加入项目网络直连服务名。
GRPCURL="docker run --rm --network $NET -v $DIR/contracts:/contracts:ro fullstorydev/grpcurl:latest"
TARGET="$(service_name mdm/product):$GRPC_PORT"

# base_uom_id=1 是迁移播种的"个"（EA）：003_seed_uoms 第一条插入的行，迁移
# 只跑一次，序列号确定。本组件没有单位的管理接口，gRPC 上查不到它的 id。
BASE_UOM_ID="1"

# mkproduct <幂等键> <名称> <标准成本> <追踪类型> [sku]：sku 不给就由组件自动编号。
mkproduct() {
  local key="$1" name="$2" cost="$3" tracking="$4" sku="${5:-}"
  $GRPCURL -plaintext -import-path /contracts -proto mdm/product/v1/product.proto \
    -d "{\"idempotency_key\":\"$key\",\"sku\":\"$sku\",\"name\":\"$name\",\"base_uom_id\":\"$BASE_UOM_ID\",\"tracking_type\":\"$tracking\",\"standard_cost\":\"$cost\"}" \
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

echo "── mdm-product：灌 14 个示例产品（1-5 被下游种子脚本引用，只增不改）──"
P1="$(mkproduct seed-product-1 "「本地测试」标准螺栓 M8" 0.50 TRACKING_TYPE_NONE)"
P2="$(mkproduct seed-product-2 "「本地测试」工业润滑油 20L" 120.00 TRACKING_TYPE_BATCH)"
P3="$(mkproduct seed-product-3 "「本地测试」不锈钢管件 DN50" 35.00 TRACKING_TYPE_NONE)"
P4="$(mkproduct seed-product-4 "「本地测试」工业设备主控制器" 880.00 TRACKING_TYPE_SERIAL)"
P5="$(mkproduct seed-product-5 "「本地测试」包装纸箱 60x40x40（已停用样例）" 3.20 TRACKING_TYPE_NONE)"
setstatus seed-product-5-disable "$P5" PRODUCT_STATUS_DISABLED

# 6-12：每种 TrackingType 再多几条样本，标准成本铺开到几毛到上千元。
P6="$(mkproduct seed-product-6 "「本地测试」六角螺母 M8" 0.20 TRACKING_TYPE_NONE)"
P7="$(mkproduct seed-product-7 "「本地测试」防锈涂料 5L" 68.00 TRACKING_TYPE_BATCH)"
P8="$(mkproduct seed-product-8 "「本地测试」工业密封胶 300ml" 15.50 TRACKING_TYPE_BATCH)"
P9="$(mkproduct seed-product-9 "「本地测试」精密减速机" 1580.00 TRACKING_TYPE_SERIAL)"
P10="$(mkproduct seed-product-10 "「本地测试」变频器控制模块" 2200.00 TRACKING_TYPE_SERIAL)"
P11="$(mkproduct seed-product-11 "「本地测试」PVC 波纹管 DN100" 12.80 TRACKING_TYPE_NONE)"
P12="$(mkproduct seed-product-12 "「本地测试」通用垫片套装（已停用样例）" 4.50 TRACKING_TYPE_NONE)"
setstatus seed-product-12-disable "$P12" PRODUCT_STATUS_DISABLED

# 13-14：显式 sku，给关键字搜索 q 的前缀匹配做样例。
P13="$(mkproduct seed-product-13 "「本地测试」高强度螺栓 M10" 0.80 TRACKING_TYPE_NONE BOLT-M10)"
P14="$(mkproduct seed-product-14 "「本地测试」高强度螺栓 M12" 1.10 TRACKING_TYPE_BATCH BOLT-M12)"

ok "产品：$P1(NONE) $P2(BATCH) $P3(NONE) $P4(SERIAL) $P5(DISABLED) $P6(NONE) $P7(BATCH) $P8(BATCH) $P9(SERIAL) $P10(SERIAL) $P11(NONE) $P12(DISABLED) $P13(BOLT-M10) $P14(BOLT-M12)"

# ── 时间跨度回填：给 6、8、9、11 改成 1-4 个月前建档（其余保持"刚建"）。
psqlx -q <<SQL
SET search_path TO mdm_product;
UPDATE products SET created_at = now() - interval '4 months', updated_at = now() - interval '4 months' WHERE id = '$P6';
UPDATE products SET created_at = now() - interval '3 months', updated_at = now() - interval '3 months' WHERE id = '$P8';
UPDATE products SET created_at = now() - interval '2 months', updated_at = now() - interval '2 months' WHERE id = '$P9';
UPDATE products SET created_at = now() - interval '1 months', updated_at = now() - interval '1 months' WHERE id = '$P11';
SQL
ok "已给 4 个产品回填历史创建时间（1-4 个月前）"
