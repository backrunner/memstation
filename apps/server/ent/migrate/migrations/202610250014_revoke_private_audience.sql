ALTER TABLE memory_partitions DROP CONSTRAINT memory_partitions_audience_actor_ids_check;
ALTER TABLE memory_partitions ADD CONSTRAINT memory_partitions_audience_actor_ids_check CHECK(jsonb_typeof(audience_actor_ids)='array' AND jsonb_array_length(audience_actor_ids)<=64);
