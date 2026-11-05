ALTER TABLE memory_ai_calls
 ADD COLUMN stage text NOT NULL DEFAULT '',
 ADD COLUMN strategy text NOT NULL DEFAULT '',
 ADD COLUMN duration_ms bigint NOT NULL DEFAULT 0 CHECK(duration_ms >= 0),
 ADD COLUMN usage_known boolean NOT NULL DEFAULT false;
CREATE INDEX memory_ai_calls_usage ON memory_ai_calls(user_id, workspace_id, created_at, stage, strategy);

-- Preserve known historical provider usage without inventing strategy or prices.
UPDATE memory_ai_calls SET usage_known=true WHERE input_tokens>0 OR output_tokens>0;
