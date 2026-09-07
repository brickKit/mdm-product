DELETE FROM uom_conversions WHERE uom_id IN (SELECT id FROM uoms WHERE code IN ('BOX', 'G'));
DELETE FROM uoms WHERE code IN ('EA', 'BOX', 'KG', 'G');
