-- Unify the two current extraction engines on the explicit entity name field.
UPDATE memory_items
SET metadata=metadata || jsonb_build_object('canonical_name',metadata->>'title')
WHERE item_type='entity' AND NOT(metadata ? 'canonical_name')
  AND jsonb_typeof(metadata->'title')='string' AND BTRIM(metadata->>'title')<>'';

CREATE INDEX memory_items_entity_name_lookup
ON memory_items(user_id,generation_id,LOWER(BTRIM(metadata->>'canonical_name')))
WHERE item_type='entity' AND deleted_at IS NULL;
