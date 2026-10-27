-- Semantic episodes reuse memory_items and the existing node/source graph.
-- Widen the existing type check; no stored records are rewritten.
ALTER TABLE memory_items
  DROP CONSTRAINT memory_items_item_type_check,
  ADD CONSTRAINT memory_items_item_type_check CHECK (
    item_type IN ('episode', 'event', 'task', 'procedure', 'artifact', 'entity', 'agent_action')
  ) NOT VALID;

ALTER TABLE memory_items VALIDATE CONSTRAINT memory_items_item_type_check;