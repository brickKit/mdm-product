-- Products with their own unit conversions and availability by legal entity, product categories, and the unit
-- catalog. Every product and category has a version (protocol 02, S1) and is published as a full-state event in the
-- transaction that changed it; conversions and availability are part of the product's state.

-- The unit catalog comes with the component (protocol 14: standard codes are base data, in the migration). factor is
-- the unit in its category's base unit; package units (box, carton, pack) have none: only a product says how many of
-- its base unit one holds. decimals is the rounding of quantities in the unit.
CREATE TABLE unit (
    code     text PRIMARY KEY,
    category text NOT NULL CHECK (category IN ('count', 'mass', 'length', 'area', 'volume', 'time', 'package')),
    factor   numeric(24,12) CHECK (factor > 0),
    decimals smallint NOT NULL CHECK (decimals BETWEEN 0 AND 6),
    name     text NOT NULL,
    names    jsonb NOT NULL DEFAULT '{}'::jsonb,
    CHECK ((category = 'package') = (factor IS NULL))
);
INSERT INTO unit (code, category, factor, decimals, name, names) VALUES
    ('C62', 'count', 1, 0, 'piece', jsonb_build_object('zh-CN', '件')),
    ('DZN', 'count', 12, 0, 'dozen', jsonb_build_object('zh-CN', '打')),
    ('PR', 'count', 2, 0, 'pair', jsonb_build_object('zh-CN', '双')),
    ('KGM', 'mass', 1, 3, 'kilogram', jsonb_build_object('zh-CN', '千克')),
    ('GRM', 'mass', 0.001, 1, 'gram', jsonb_build_object('zh-CN', '克')),
    ('TNE', 'mass', 1000, 3, 'tonne', jsonb_build_object('zh-CN', '吨')),
    ('LBR', 'mass', 0.45359237, 3, 'pound', jsonb_build_object('zh-CN', '磅')),
    ('MTR', 'length', 1, 3, 'metre', jsonb_build_object('zh-CN', '米')),
    ('CMT', 'length', 0.01, 1, 'centimetre', jsonb_build_object('zh-CN', '厘米')),
    ('MMT', 'length', 0.001, 0, 'millimetre', jsonb_build_object('zh-CN', '毫米')),
    ('KMT', 'length', 1000, 3, 'kilometre', jsonb_build_object('zh-CN', '千米')),
    ('MTK', 'area', 1, 3, 'square metre', jsonb_build_object('zh-CN', '平方米')),
    ('LTR', 'volume', 1, 3, 'litre', jsonb_build_object('zh-CN', '升')),
    ('MLT', 'volume', 0.001, 0, 'millilitre', jsonb_build_object('zh-CN', '毫升')),
    ('MTQ', 'volume', 1000, 3, 'cubic metre', jsonb_build_object('zh-CN', '立方米')),
    ('MIN', 'time', 1, 0, 'minute', jsonb_build_object('zh-CN', '分钟')),
    ('HUR', 'time', 60, 2, 'hour', jsonb_build_object('zh-CN', '小时')),
    ('DAY', 'time', 1440, 2, 'day', jsonb_build_object('zh-CN', '天')),
    ('BX', 'package', NULL, 0, 'box', jsonb_build_object('zh-CN', '箱')),
    ('CT', 'package', NULL, 0, 'carton', jsonb_build_object('zh-CN', '纸箱')),
    ('PK', 'package', NULL, 0, 'pack', jsonb_build_object('zh-CN', '包'));

CREATE TABLE category (
    id         uuid PRIMARY KEY,
    code       text NOT NULL UNIQUE CHECK (code ~ '^[A-Z0-9][A-Z0-9_.-]{0,47}$'),
    name       text NOT NULL CHECK (name <> ''),
    names      jsonb NOT NULL DEFAULT '{}'::jsonb,
    parent_id  uuid REFERENCES category (id),
    active     boolean NOT NULL DEFAULT true,
    version    bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE product (
    id             uuid PRIMARY KEY,
    code           text NOT NULL UNIQUE CHECK (code ~ '^[A-Z0-9][A-Z0-9_.-]{0,47}$'),
    kind           text NOT NULL CHECK (kind IN ('stock', 'service', 'non_stock')),
    name           text NOT NULL CHECK (name <> ''),
    names          jsonb NOT NULL DEFAULT '{}'::jsonb,
    category_id    uuid REFERENCES category (id),
    base_unit      text NOT NULL REFERENCES unit (code),
    tracking       text NOT NULL DEFAULT 'none' CHECK (tracking IN ('none', 'batch', 'serial')),
    has_movements  boolean NOT NULL DEFAULT false,
    state          text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'suspended', 'retired')),
    search         text NOT NULL,
    search_version integer NOT NULL,
    version        bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    -- a service or non-stock product never has inventory movements, so it cannot be tracked by batch or serial
    CHECK (kind = 'stock' OR tracking = 'none')
);

-- one of unit holds factor of the product's base unit; it comes before the catalog's conversion
CREATE TABLE product_conversion (
    product_id uuid NOT NULL REFERENCES product (id),
    unit       text NOT NULL REFERENCES unit (code),
    factor     numeric(24,12) NOT NULL CHECK (factor > 0),
    PRIMARY KEY (product_id, unit)
);

-- the legal entity is mdm/org's (an opaque reference, protocol 14)
CREATE TABLE product_availability (
    product_id      uuid NOT NULL REFERENCES product (id),
    legal_entity_id uuid NOT NULL,
    sellable        boolean NOT NULL,
    purchasable     boolean NOT NULL,
    PRIMARY KEY (product_id, legal_entity_id)
);
