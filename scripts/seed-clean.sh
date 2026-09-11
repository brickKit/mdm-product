#!/usr/bin/env bash
# 撤销 seed.sh 灌的数据。反查 command_idempotency 表拿真实行 id 再精确
# 删除，不靠名字模糊匹配（同 mdm-customer 的既有样板）。
set -euo pipefail

C_GRN=$'\033[32m'; C_OFF=$'\033[0m'
ok() { echo "${C_GRN}✓${C_OFF} $*"; }

psqlx() { docker exec -i be-postgres psql -U postgres -d brickkit_db -v ON_ERROR_STOP=1 "$@"; }

psqlx -q <<'SQL'
SET search_path TO mdm_product;
DO $$
DECLARE
  pid BIGINT;
  k TEXT;
BEGIN
  FOR k IN SELECT unnest(ARRAY[
    'seed-product-1','seed-product-2','seed-product-3','seed-product-4','seed-product-5'
  ])
  LOOP
    SELECT result_id::BIGINT INTO pid FROM command_idempotency WHERE idempotency_key = k;
    IF pid IS NOT NULL THEN
      DELETE FROM products WHERE id = pid;
    END IF;
  END LOOP;

  DELETE FROM command_idempotency WHERE idempotency_key LIKE 'seed-product-%';
END $$;
SQL

ok "mdm-product 种子数据已清空"
