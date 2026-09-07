-- mdm-product 主表。schema 由迁移工具的 search_path 指定，SQL 里不写限定名
-- ⚠️ 迁移状态表必须落在本组件 schema 里（§11.2.3）：
--    golang-migrate 的 x-migrations-table + search_path，见 backend/cmd/migrate/main.go
--
-- ⚠️ 不分区：这四张表是主数据（产品/单位），不是交易流水。§11.2.5 需要
-- 分区的大表清单里没有 mdm-*，量级是"SKU 数量级"（几千到几十万行），
-- 不会像库存流水那样无限增长（设计计划 §7）。

CREATE TABLE uoms (
    id         BIGSERIAL PRIMARY KEY,
    code       TEXT           NOT NULL,   -- 'EA'/'BOX'/'KG'...
    name       TEXT           NOT NULL,
    category   TEXT           NOT NULL,   -- 单位类别：'count'/'weight'/'length'...
    is_base    BOOLEAN        NOT NULL DEFAULT false,
    rounding   NUMERIC(18,6)  NOT NULL DEFAULT 1,
    -- §11.2.1 强制字段
    created_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    version    BIGINT         NOT NULL DEFAULT 1,
    status     TEXT           NOT NULL DEFAULT 'ACTIVE'
);
CREATE UNIQUE INDEX uoms_code_uniq ON uoms (code);
-- 每个单位类别内有且只有一个基准单位（设计计划 §2）
CREATE UNIQUE INDEX uoms_base_per_category ON uoms (category) WHERE is_base;

-- 换算因子：1 个本单位 = factor 个该 category 的基准单位。只存一个方向，
-- 不存 factor_inv——Odoo 那样两个方向都存是缓存优化，代价是浮点下两者
-- 迟早不一致；我们现算，一次除法不值得为它引入一个可能说谎的字段
-- （设计计划 §2.1）。NUMERIC(18,6) 不用浮点：换算因子和金额是同一类
-- 跨语言精度问题。
CREATE TABLE uom_conversions (
    id         BIGSERIAL PRIMARY KEY,
    uom_id     BIGINT         NOT NULL REFERENCES uoms (id),
    factor     NUMERIC(18,6)  NOT NULL,
    created_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    version    BIGINT         NOT NULL DEFAULT 1,
    status     TEXT           NOT NULL DEFAULT 'ACTIVE'
);
CREATE UNIQUE INDEX uom_conversions_uom_uniq ON uom_conversions (uom_id);

-- 分类树用物化路径（path 列），不用邻接表——前缀匹配一次查完子树，
-- 不需要递归 CTE（同 §14.2.3 的 dept_path 判据，设计计划 §2）。
CREATE TABLE product_categories (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT           NOT NULL,
    path       TEXT           NOT NULL,   -- 如 '/electronics/phones'
    created_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    version    BIGINT         NOT NULL DEFAULT 1,
    status     TEXT           NOT NULL DEFAULT 'ACTIVE'
);
CREATE INDEX product_categories_path ON product_categories (path text_pattern_ops);

CREATE TABLE products (
    id            BIGSERIAL PRIMARY KEY,
    sku           TEXT           NOT NULL,
    name          TEXT           NOT NULL,
    category_id   BIGINT         REFERENCES product_categories (id),
    base_uom_id   BIGINT         NOT NULL REFERENCES uoms (id),
    -- 挂在产品上而不是挂在批次上——追踪策略是产品的属性（借鉴 Odoo
    -- product.tracking 这个位置，设计计划 §8）
    tracking_type TEXT           NOT NULL DEFAULT 'NONE',   -- NONE/BATCH/SERIAL
    standard_cost NUMERIC(18,2)  NOT NULL DEFAULT 0,
    -- §11.2.1 强制字段
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ    NOT NULL DEFAULT now(),
    version       BIGINT         NOT NULL DEFAULT 1,
    status        TEXT           NOT NULL DEFAULT 'ACTIVE'
);
CREATE UNIQUE INDEX products_sku_uniq ON products (sku);
CREATE INDEX products_status ON products (status);
