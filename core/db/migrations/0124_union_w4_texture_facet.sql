-- UNION-W4 (B's E1, supervisor ruling): texture is now a REAL queryable facet (DIB-61 filter +
-- count badges), which is exactly the condition 0021 (L-8) named for re-adding the index it
-- dropped as dead. Re-added TOGETHER with flipping internal/product's A-18 assertion from
-- "this index must NOT exist" (n != 0) to "this index MUST exist" (n != 1) — the pair travels
-- in one commit so the gate never lies in either direction.
CREATE INDEX IF NOT EXISTS idx_products_texture ON products(texture) WHERE texture <> '';
