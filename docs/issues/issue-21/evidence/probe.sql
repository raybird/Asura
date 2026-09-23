-- 2026-09-23：Issue #21 的隔離 SQLite 規劃探測；只在 :memory: 執行。
CREATE TABLE memories (
  id TEXT PRIMARY KEY,
  workspace TEXT NOT NULL,
  content TEXT NOT NULL,
  source TEXT NOT NULL,
  created_at TEXT NOT NULL
);

WITH RECURSIVE n(x) AS (
  SELECT 0
  UNION ALL SELECT x + 1 FROM n WHERE x < 9999
)
INSERT INTO memories
SELECT printf('m%05d', x), '/a',
  CASE WHEN x = 0 THEN replace(hex(zeroblob(524288)), '00', 'x') ELSE 'normal' END,
  'manual',
  printf('2026-09-23T00:%02d:%02dZ', (x / 60) % 60, x % 60)
FROM n;
INSERT INTO memories VALUES ('foreign', '/b', 'SECRET', 'manual', '2026-09-23T00:00:00Z');
CREATE INDEX idx_memories_workspace_created_id ON memories(workspace, created_at, id);

SELECT count(*), max(length(content)) FROM memories WHERE workspace = '/a';
EXPLAIN QUERY PLAN
SELECT id, substr(CAST(content AS BLOB), 1, 513)
FROM memories
WHERE workspace = '/a' AND (created_at, id) > ('2026-09-23T00:00:00Z', '')
ORDER BY created_at, id LIMIT 51;
SELECT count(*), max(length(summary)), sum(length(summary))
FROM (
  SELECT substr(CAST(content AS BLOB), 1, 513) AS summary
  FROM memories WHERE workspace = '/a' ORDER BY created_at, id LIMIT 51
);

UPDATE memories SET created_at = '2026-09-23T00:00:00Z' WHERE workspace = '/a';
SELECT count(*), min(id), max(id)
FROM (SELECT id FROM memories WHERE workspace = '/a' ORDER BY created_at, id LIMIT 50);
SELECT count(*), min(id), max(id)
FROM (
  SELECT id FROM memories
  WHERE workspace = '/a' AND (created_at, id) > ('2026-09-23T00:00:00Z', 'm00049')
  ORDER BY created_at, id LIMIT 50
);
SELECT count(*) FROM memories WHERE workspace = '/a' AND content = 'SECRET';
SELECT length(content), length(substr(CAST(content AS BLOB), 1, 513))
FROM memories WHERE id = 'm00000';
SELECT length(content), length(substr(CAST(content AS BLOB), 1, 65537))
FROM memories WHERE workspace = '/a' AND id = 'm00000';
