CREATE OR REPLACE FUNCTION memstation_fact_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE captured_at TIMESTAMPTZ := clock_timestamp(); node memory_nodes%ROWTYPE; effective_from TIMESTAMPTZ;
BEGIN
 IF TG_OP = 'UPDATE' AND (to_jsonb(OLD) - 'updated_at') = (to_jsonb(NEW) - 'updated_at') THEN RETURN NEW; END IF;
 SELECT * INTO node FROM memory_nodes WHERE user_id=NEW.user_id AND id=NEW.node_id;
 SELECT valid_from INTO effective_from FROM memory_fact_history WHERE user_id=NEW.user_id AND node_id=NEW.node_id AND recorded_to IS NULL;
 effective_from := COALESCE(NEW.valid_from,effective_from,node.activity_at,NEW.created_at);
 UPDATE memory_fact_history SET recorded_to=captured_at WHERE user_id=NEW.user_id AND node_id=NEW.node_id AND recorded_to IS NULL;
 INSERT INTO memory_fact_history(user_id,generation_id,node_id,recorded_at,valid_from,valid_to,document)
 VALUES (NEW.user_id,NEW.generation_id,NEW.node_id,captured_at,effective_from,NEW.valid_to,
 jsonb_build_object('id',NEW.id,'node_id',NEW.node_id,'user_id',NEW.user_id,'generation_id',NEW.generation_id,'conversation_id',NEW.conversation_id,'type','fact','content',NEW.content,'importance',node.importance,'decay_factor',node.decay_factor,'activity_at',COALESCE(node.activity_at,NEW.created_at),'created_at',NEW.created_at,'updated_at',NEW.updated_at,'metadata',COALESCE(NEW.metadata, '{}'::jsonb) || (to_jsonb(NEW)-'content'-'metadata') || jsonb_build_object('valid_from',effective_from)));
 RETURN NEW;
END $$;