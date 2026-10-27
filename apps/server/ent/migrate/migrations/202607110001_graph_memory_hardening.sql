ALTER TABLE memory_nodes
  ADD COLUMN IF NOT EXISTS embedding_revision BIGINT NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_nodes_user_id
  ON memory_nodes (user_id, id);

CREATE INDEX IF NOT EXISTS idx_memory_relations_user_generation_src
  ON memory_relations (user_id, generation_id, src_node_id);

CREATE INDEX IF NOT EXISTS idx_memory_relations_user_generation_dst
  ON memory_relations (user_id, generation_id, dst_node_id);

CREATE INDEX IF NOT EXISTS idx_memory_embeddings_hnsw_cosine_3
  ON memory_embeddings USING hnsw ((vector::vector(3)) vector_cosine_ops)
  WHERE vector_dims(vector) = 3;

CREATE INDEX IF NOT EXISTS idx_memory_embeddings_hnsw_cosine_768
  ON memory_embeddings USING hnsw ((vector::vector(768)) vector_cosine_ops)
  WHERE vector_dims(vector) = 768;

CREATE INDEX IF NOT EXISTS idx_memory_embeddings_hnsw_cosine_1024
  ON memory_embeddings USING hnsw ((vector::vector(1024)) vector_cosine_ops)
  WHERE vector_dims(vector) = 1024;

CREATE INDEX IF NOT EXISTS idx_memory_embeddings_hnsw_cosine_1536
  ON memory_embeddings USING hnsw ((vector::vector(1536)) vector_cosine_ops)
  WHERE vector_dims(vector) = 1536;

CREATE INDEX IF NOT EXISTS idx_memory_embeddings_hnsw_cosine_3072
  ON memory_embeddings USING hnsw ((vector::halfvec(3072)) halfvec_cosine_ops)
  WHERE vector_dims(vector) = 3072;

ALTER TABLE memory_relations
  ADD CONSTRAINT memory_relations_distinct_nodes_check CHECK (src_node_id <> dst_node_id),
  ADD CONSTRAINT memory_relations_type_check CHECK (BTRIM(relation_type) <> ''),
  ADD CONSTRAINT memory_relations_weight_check CHECK (weight >= 0 AND weight <= 1),
  ADD CONSTRAINT memory_relations_src_node_fk FOREIGN KEY (user_id, src_node_id)
    REFERENCES memory_nodes (user_id, id) ON DELETE CASCADE,
  ADD CONSTRAINT memory_relations_dst_node_fk FOREIGN KEY (user_id, dst_node_id)
    REFERENCES memory_nodes (user_id, id) ON DELETE CASCADE;

ALTER TABLE memory_embeddings
  ADD CONSTRAINT memory_embeddings_node_fk FOREIGN KEY (user_id, node_id)
    REFERENCES memory_nodes (user_id, id) ON DELETE CASCADE;

ALTER TABLE memory_evidences
  ADD CONSTRAINT memory_evidences_node_fk FOREIGN KEY (user_id, node_id)
    REFERENCES memory_nodes (user_id, id) ON DELETE CASCADE;

ALTER TABLE memory_summaries
  ADD CONSTRAINT memory_summaries_node_fk FOREIGN KEY (user_id, node_id)
    REFERENCES memory_nodes (user_id, id) ON DELETE CASCADE;

ALTER TABLE impressions
  ADD CONSTRAINT impressions_node_fk FOREIGN KEY (user_id, node_id)
    REFERENCES memory_nodes (user_id, id) ON DELETE CASCADE;

ALTER TABLE facts
  ADD CONSTRAINT facts_node_fk FOREIGN KEY (user_id, node_id)
    REFERENCES memory_nodes (user_id, id) ON DELETE CASCADE;

ALTER TABLE key_memories
  ADD CONSTRAINT key_memories_node_fk FOREIGN KEY (user_id, node_id)
    REFERENCES memory_nodes (user_id, id) ON DELETE CASCADE;

ALTER TABLE memory_items
  ADD CONSTRAINT memory_items_node_fk FOREIGN KEY (user_id, node_id)
    REFERENCES memory_nodes (user_id, id) ON DELETE CASCADE;