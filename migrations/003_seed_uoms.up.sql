-- 最小可用的计量单位参考集。UoM 目前没有独立的管理接口——它是准静态
-- 参考数据（客户很少需要新增计量单位类别），用迁移播种比为它单开一整套
-- CRUD 更简单。实现时才发现契约（Task 5）没有留 UoM 管理接口，记进
-- 设计计划 §9 待决问题；标准客户改动量小的默认值走 Fork（决策 25），
-- 真有频繁自定义单位的需求再回来评估要不要开接口。

INSERT INTO uoms (code, name, category, is_base, rounding) VALUES
    ('EA',  '个',   'count',  true,  1),
    ('BOX', '箱',   'count',  false, 1),
    ('KG',  '千克', 'weight', true,  0.001),
    ('G',   '克',   'weight', false, 1);

-- 1 箱 = 12 个（默认值，客户 Fork 后可按自己的包装规格改）
INSERT INTO uom_conversions (uom_id, factor)
SELECT id, 12 FROM uoms WHERE code = 'BOX';
-- 1 克 = 0.001 千克
INSERT INTO uom_conversions (uom_id, factor)
SELECT id, 0.001 FROM uoms WHERE code = 'G';
