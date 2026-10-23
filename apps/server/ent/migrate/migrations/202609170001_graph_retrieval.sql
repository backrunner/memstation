-- Immutable fact snapshots distinguish valid time from the time the service
-- recorded a statement/correction. Historical knowledge starts at migration.
CREATE TABLE memory_fact_history (
 id BIGSERIAL PRIMARY KEY,
 user_id TEXT NOT NULL,
 generation_id TEXT NOT NULL,
 node_id TEXT NOT NULL,
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT transaction_timestamp(),
 recorded_to TIMESTAMPTZ,
 valid_from TIMESTAMPTZ NOT NULL,
 valid_to TIMESTAMPTZ,
 document JSONB NOT NULL,
 FOREIGN KEY (user_id,node_id) REFERENCES memory_nodes(user_id,id) ON DELETE CASCADE
);
CREATE INDEX idx_fact_history_scope_time ON memory_fact_history(user_id,generation_id,recorded_at,recorded_to);
CREATE UNIQUE INDEX idx_fact_history_current ON memory_fact_history(user_id,node_id) WHERE recorded_to IS NULL;

CREATE FUNCTION memstation_fact_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE captured_at TIMESTAMPTZ := clock_timestamp(); node memory_nodes%ROWTYPE; effective_from TIMESTAMPTZ;
BEGIN
 IF TG_OP = 'UPDATE' AND (to_jsonb(OLD) - 'updated_at') = (to_jsonb(NEW) - 'updated_at') THEN RETURN NEW; END IF;
 SELECT * INTO node FROM memory_nodes WHERE user_id=NEW.user_id AND id=NEW.node_id;
 SELECT valid_from INTO effective_from FROM memory_fact_history WHERE user_id=NEW.user_id AND node_id=NEW.node_id AND recorded_to IS NULL;
 effective_from := COALESCE(NEW.valid_from,effective_from,node.activity_at,NEW.created_at);
 UPDATE memory_fact_history SET recorded_to=captured_at WHERE user_id=NEW.user_id AND node_id=NEW.node_id AND recorded_to IS NULL;
 INSERT INTO memory_fact_history(user_id,generation_id,node_id,recorded_at,valid_from,valid_to,document)
 VALUES (NEW.user_id,NEW.generation_id,NEW.node_id,captured_at,effective_from,NEW.valid_to,
 jsonb_build_object('id',NEW.id,'node_id',NEW.node_id,'user_id',NEW.user_id,'generation_id',NEW.generation_id,'conversation_id',NEW.conversation_id,'type','fact','content',NEW.content,'importance',node.importance,'decay_factor',node.decay_factor,'activity_at',COALESCE(node.activity_at,NEW.created_at),'created_at',NEW.created_at,'updated_at',NEW.updated_at,'metadata',(to_jsonb(NEW)-'content') || jsonb_build_object('valid_from',effective_from)));
 RETURN NEW;
END $$;
CREATE TRIGGER fact_snapshot_after_write AFTER INSERT OR UPDATE ON facts FOR EACH ROW EXECUTE FUNCTION memstation_fact_snapshot();
INSERT INTO memory_fact_history(user_id,generation_id,node_id,valid_from,valid_to,document)
SELECT f.user_id,f.generation_id,f.node_id,COALESCE(f.valid_from,n.activity_at,f.created_at),f.valid_to,
 jsonb_build_object('id',f.id,'node_id',f.node_id,'user_id',f.user_id,'generation_id',f.generation_id,'conversation_id',f.conversation_id,'type','fact','content',f.content,'importance',n.importance,'decay_factor',n.decay_factor,'activity_at',COALESCE(n.activity_at,f.created_at),'created_at',f.created_at,'updated_at',f.updated_at,'metadata',(to_jsonb(f)-'content') || jsonb_build_object('valid_from',COALESCE(f.valid_from,n.activity_at,f.created_at)))
FROM facts f JOIN memory_nodes n ON n.user_id=f.user_id AND n.id=f.node_id;

CREATE TABLE entity_aliases (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL,
 generation_id TEXT NOT NULL,
 scope TEXT NOT NULL DEFAULT '',
 alias TEXT NOT NULL,
 source_node_id TEXT,
 target_node_id TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('candidate','confirmed','revoked')),
 reason TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (source_node_id IS NULL OR source_node_id <> target_node_id),
 FOREIGN KEY (user_id,source_node_id) REFERENCES memory_nodes(user_id,id) ON DELETE CASCADE,
 FOREIGN KEY (user_id,target_node_id) REFERENCES memory_nodes(user_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX entity_aliases_identity ON entity_aliases(user_id,generation_id,scope,alias,target_node_id);
CREATE UNIQUE INDEX entity_aliases_confirmed_alias ON entity_aliases(user_id,generation_id,scope,alias) WHERE status='confirmed';
CREATE UNIQUE INDEX entity_aliases_confirmed_source ON entity_aliases(user_id,generation_id,source_node_id) WHERE status='confirmed' AND source_node_id IS NOT NULL;
CREATE INDEX entity_aliases_lookup ON entity_aliases(user_id,generation_id,scope,alias);

-- Online traversal can fetch the strongest edges without sorting a whole hub.
CREATE INDEX idx_relations_ranked_src ON memory_relations(user_id,generation_id,src_node_id,weight DESC,id);
CREATE INDEX idx_relations_ranked_dst ON memory_relations(user_id,generation_id,dst_node_id,weight DESC,id);
