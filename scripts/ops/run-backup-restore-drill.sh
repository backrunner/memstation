#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
MODE=drill
OUT_DIR="$ROOT_DIR/.artifacts/drills/backup-restore"
BACKUP_DIR=""
DRY_RUN=0
KEEP_STACKS=0
FAIL_REASON=""
STATUS="running"
GENERATED_AT=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
SOURCE_PROJECT=${MEMSTATION_BACKUP_SOURCE_PROJECT:-memstation-backup-source}
RESTORE_PROJECT=${MEMSTATION_BACKUP_RESTORE_PROJECT:-memstation-backup-restore}
DRILL_SUFFIX=${MEMSTATION_BACKUP_DRILL_SUFFIX:-$(date -u +"%Y%m%dT%H%M%SZ")}
DRILL_USER_ID="drill-user-${DRILL_SUFFIX}"
DRILL_CONVERSATION_ID="drill-conversation-${DRILL_SUFFIX}"
DRILL_NODE_ID="drill-node-${DRILL_SUFFIX}"
REDIS_MARKER_KEY="memstation:drill:marker"
REDIS_META_HASH="memstation:drill:meta"
MARKER_VALUE="backup-restore-${DRILL_SUFFIX}"
TEMP_ROOT=""
SOURCE_COMPOSE_FILE=""
RESTORE_COMPOSE_FILE=""
SOURCE_REDIS_DATA_DIR=""
RESTORE_REDIS_DATA_DIR=""
SOURCE_TABLE_COUNTS='{}'
RESTORE_TABLE_COUNTS='{}'
REDIS_MARKER_MATCH=false
REDIS_META_MATCH=false
POSTGRES_DUMP_BYTES=0
REDIS_RDB_BYTES=0
BACKUP_DURATION_MS=0
RESTORE_DURATION_MS=0
TOTAL_DURATION_MS=0
START_TS=$(date +%s)

usage() {
  cat <<USAGE
Usage: $(basename "$0") [--mode drill|backup|restore] [--out-dir PATH] [--backup-dir PATH] [--dry-run] [--keep-stacks]
USAGE
}

resolve_path() {
  local path=$1
  if [[ "$path" = /* ]]; then
    printf '%s\n' "$path"
  else
    printf '%s/%s\n' "$ROOT_DIR" "$path"
  fi
}

compose_cmd() {
  local project=$1
  local file=$2
  shift 2
  docker compose -p "$project" -f "$file" "$@"
}

require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "docker is required for backup/restore drill" >&2
    exit 1
  fi
  docker compose version >/dev/null 2>&1
}

write_report() {
  local exit_code=$1
  local report_json="$OUT_DIR/report.json"
  local report_md="$OUT_DIR/report.md"
  mkdir -p "$OUT_DIR"
  cat >"$report_json" <<JSON
{
  "generated_at": "$GENERATED_AT",
  "mode": "$MODE",
  "status": "$STATUS",
  "dry_run": $([[ $DRY_RUN -eq 1 ]] && echo true || echo false),
  "fail_reason": "${FAIL_REASON}",
  "backup_dir": "${BACKUP_DIR}",
  "postgres_dump_bytes": ${POSTGRES_DUMP_BYTES},
  "redis_rdb_bytes": ${REDIS_RDB_BYTES},
  "backup_duration_ms": ${BACKUP_DURATION_MS},
  "restore_duration_ms": ${RESTORE_DURATION_MS},
  "total_duration_ms": ${TOTAL_DURATION_MS},
  "redis_marker_match": ${REDIS_MARKER_MATCH},
  "redis_meta_match": ${REDIS_META_MATCH},
  "source_table_counts": ${SOURCE_TABLE_COUNTS},
  "restore_table_counts": ${RESTORE_TABLE_COUNTS},
  "exit_code": ${exit_code}
}
JSON
  cat >"$report_md" <<MD
# MemStation Backup Restore Drill

- Mode: $MODE
- Status: $STATUS
- Dry run: $([[ $DRY_RUN -eq 1 ]] && echo true || echo false)
- Backup dir: $BACKUP_DIR
- Postgres dump bytes: $POSTGRES_DUMP_BYTES
- Redis RDB bytes: $REDIS_RDB_BYTES
- Backup duration: ${BACKUP_DURATION_MS}ms
- Restore duration: ${RESTORE_DURATION_MS}ms
- Total duration: ${TOTAL_DURATION_MS}ms
- Redis marker match: $REDIS_MARKER_MATCH
- Redis metadata match: $REDIS_META_MATCH
- Exit code: $exit_code

## Counts

- Source table counts: $SOURCE_TABLE_COUNTS
- Restore table counts: $RESTORE_TABLE_COUNTS

## Failure

- ${FAIL_REASON:-none}
MD
}

cleanup() {
  if [[ $KEEP_STACKS -eq 0 ]]; then
    if [[ -n "$SOURCE_COMPOSE_FILE" && -f "$SOURCE_COMPOSE_FILE" ]]; then
      compose_cmd "$SOURCE_PROJECT" "$SOURCE_COMPOSE_FILE" down -v >/dev/null 2>&1 || true
    fi
    if [[ -n "$RESTORE_COMPOSE_FILE" && -f "$RESTORE_COMPOSE_FILE" ]]; then
      compose_cmd "$RESTORE_PROJECT" "$RESTORE_COMPOSE_FILE" down -v >/dev/null 2>&1 || true
    fi
  fi
  if [[ -n "$TEMP_ROOT" && -d "$TEMP_ROOT" ]]; then
    if ! rm -rf "$TEMP_ROOT" 2>/dev/null; then
      if command -v sudo >/dev/null 2>&1; then
        sudo rm -rf "$TEMP_ROOT" || true
      else
        chmod -R u+rwX "$TEMP_ROOT" 2>/dev/null || true
        rm -rf "$TEMP_ROOT" || true
      fi
    fi
  fi
}

finalize() {
  local exit_code=$1
  TOTAL_DURATION_MS=$(( ($(date +%s) - START_TS) * 1000 ))
  if [[ $exit_code -eq 0 && "$STATUS" = "running" ]]; then
    STATUS=success
  fi
  if [[ $exit_code -ne 0 && -z "$FAIL_REASON" ]]; then
    FAIL_REASON="backup/restore drill failed"
    STATUS=failed
  fi
  write_report "$exit_code"
  cleanup
}

trap 'FAIL_REASON="command failed near line ${LINENO}"; STATUS=failed' ERR
trap 'finalize $?' EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      MODE=$2
      shift 2
      ;;
    --out-dir)
      OUT_DIR=$(resolve_path "$2")
      shift 2
      ;;
    --backup-dir)
      BACKUP_DIR=$(resolve_path "$2")
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --keep-stacks)
      KEEP_STACKS=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$BACKUP_DIR" ]]; then
  BACKUP_DIR="$OUT_DIR/backup"
fi
mkdir -p "$OUT_DIR" "$BACKUP_DIR"

if [[ $DRY_RUN -eq 1 ]]; then
  STATUS=dry-run
  exit 0
fi

require_docker
TEMP_ROOT=$(mktemp -d)
SOURCE_COMPOSE_FILE="$TEMP_ROOT/source-compose.yml"
RESTORE_COMPOSE_FILE="$TEMP_ROOT/restore-compose.yml"
SOURCE_REDIS_DATA_DIR="$TEMP_ROOT/source-redis"
RESTORE_REDIS_DATA_DIR="$TEMP_ROOT/restore-redis"
mkdir -p "$SOURCE_REDIS_DATA_DIR" "$RESTORE_REDIS_DATA_DIR"

create_compose_file() {
  local file=$1
  local redis_data_dir=$2
  cat >"$file" <<YAML
services:
  postgres:
    image: pgvector/pgvector:pg16
    environment:
      POSTGRES_USER: memstation
      POSTGRES_PASSWORD: memstation_dev
      POSTGRES_DB: memstation
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U memstation -d memstation"]
      interval: 5s
      timeout: 5s
      retries: 20
    volumes:
      - postgres-data:/var/lib/postgresql/data
      - $ROOT_DIR:/workspace/memstation:ro
      - $BACKUP_DIR:/backups
  redis:
    image: redis:7
    command: ["redis-server", "--save", "60", "1", "--appendonly", "no"]
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 5s
      retries: 20
    volumes:
      - $redis_data_dir:/data
      - $BACKUP_DIR:/backups
volumes:
  postgres-data:
YAML
}

wait_for_postgres() {
  local project=$1
  local file=$2
  local attempts=0
  until compose_cmd "$project" "$file" exec -T postgres pg_isready -U memstation -d memstation >/dev/null 2>&1; do
    attempts=$((attempts + 1))
    if [[ $attempts -ge 60 ]]; then
      echo "postgres did not become ready" >&2
      exit 1
    fi
    sleep 1
  done
}

wait_for_redis() {
  local project=$1
  local file=$2
  local attempts=0
  until [[ "$(compose_cmd "$project" "$file" exec -T redis redis-cli --raw ping 2>/dev/null | tr -d '\r')" = "PONG" ]]; do
    attempts=$((attempts + 1))
    if [[ $attempts -ge 60 ]]; then
      echo "redis did not become ready" >&2
      exit 1
    fi
    sleep 1
  done
}

apply_migrations() {
  local project=$1
  local file=$2
  local migration_path
  for migration_path in "$ROOT_DIR"/apps/server/ent/migrate/migrations/*.sql; do
    compose_cmd "$project" "$file" exec -T postgres psql -U memstation -d memstation -v ON_ERROR_STOP=1 -f "/workspace/memstation/apps/server/ent/migrate/migrations/$(basename "$migration_path")" >/dev/null
  done
}

seed_source_fixture() {
  local project=$1
  local file=$2
  compose_cmd "$project" "$file" exec -T postgres psql -U memstation -d memstation -v ON_ERROR_STOP=1 >/dev/null <<SQL
INSERT INTO users (id, name, email, is_admin) VALUES ('$DRILL_USER_ID', 'Backup Drill User', 'drill@example.com', TRUE);
INSERT INTO workspaces (id,user_id,name,capabilities) VALUES ('personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'$DRILL_USER_ID','Drill personal','["gateway.invoke","memory.read"]');
INSERT INTO actors (id,user_id,kind,display_name) VALUES ('actor-$DRILL_SUFFIX','$DRILL_USER_ID','agent','Drill agent');
INSERT INTO agents (id,user_id,actor_id,name,default_workspace_id) VALUES ('agent-$DRILL_SUFFIX','$DRILL_USER_ID','actor-$DRILL_SUFFIX','Drill agent','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'));
INSERT INTO workspace_grants (id,user_id,workspace_id,agent_id,capabilities) VALUES ('grant-$DRILL_SUFFIX','$DRILL_USER_ID','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'agent-$DRILL_SUFFIX','["gateway.invoke","memory.read"]');
INSERT INTO api_keys (id,user_id,agent_id,default_workspace_id,name,prefix,hash,capability_ceiling) VALUES ('api-$DRILL_SUFFIX','$DRILL_USER_ID','agent-$DRILL_SUFFIX','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'drill-key','msk_drill','hash-drill','["memory.read"]');
INSERT INTO user_settings (id, user_id, chat_model, summary_model, analysis_model, embedding_model, tool_write_enabled, max_injection_tokens)
VALUES ('settings-$DRILL_SUFFIX', '$DRILL_USER_ID', 'chat-model', 'summary-model', 'analysis-model', 'embedding-model', FALSE, 2048);
INSERT INTO provider_credentials (scope, provider, encrypted_api_key, base_url) VALUES ('gateway', 'openai_compatible', 'ciphertext-drill', 'https://api.example.com');
INSERT INTO conversations (id, user_id, workspace_id, agent_id, request_format, upstream_provider, mode, finalize_status, summary_status, analysis_status, metadata)
VALUES ('$DRILL_CONVERSATION_ID', '$DRILL_USER_ID', 'personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'), 'agent-$DRILL_SUFFIX', 'openai_chat', 'openai_compatible', 'inject', 'ended', 'completed', 'completed', '{"drill":true}'::jsonb);
INSERT INTO messages (id, user_id, workspace_id, conversation_id, role, content_blocks, text_content, provider_payload, sequence_num)
VALUES
  ('msg-user-$DRILL_SUFFIX', '$DRILL_USER_ID', 'personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'), '$DRILL_CONVERSATION_ID', 'user', '[{"type":"text","text":"remember my deployment window"}]'::jsonb, 'remember my deployment window', '{}'::jsonb, 1),
  ('msg-assistant-$DRILL_SUFFIX', '$DRILL_USER_ID', 'personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'), '$DRILL_CONVERSATION_ID', 'assistant', '[{"type":"text","text":"captured deployment window"}]'::jsonb, 'captured deployment window', '{}'::jsonb, 2);
INSERT INTO token_usages (id, user_id, workspace_id, conversation_id, provider, model, input_tokens, output_tokens, total_tokens, cost)
VALUES ('usage-$DRILL_SUFFIX', '$DRILL_USER_ID', 'personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'), '$DRILL_CONVERSATION_ID', 'openai_compatible', 'chat-model', 12, 18, 30, 0.03);
INSERT INTO blob_objects (blob_id,user_id,workspace_id,source_kind,storage_backend,storage_key)
VALUES ('blob-$DRILL_SUFFIX','$DRILL_USER_ID','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'upload','local','v3/personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex') || '/partition-' || encode(sha256(convert_to('','UTF8')),'hex') || '/blob-' || encode(sha256(convert_to('blob-$DRILL_SUFFIX','UTF8')),'hex') || '/content');
INSERT INTO message_parts (part_id,user_id,workspace_id,conversation_id,role,part_type,blob_id,text_content)
VALUES ('part-$DRILL_SUFFIX','$DRILL_USER_ID','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'$DRILL_CONVERSATION_ID','user','file','blob-$DRILL_SUFFIX','Retained source fixture');
INSERT INTO message_part_artifacts (artifact_id,part_id,user_id,workspace_id,conversation_id,artifact_type,status,text_content)
VALUES ('artifact-$DRILL_SUFFIX','part-$DRILL_SUFFIX','$DRILL_USER_ID','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'$DRILL_CONVERSATION_ID','text','completed','Retained source fixture');
INSERT INTO memory_generations (generation_id,user_id,workspace_id,status,is_active)
VALUES ('generation-$DRILL_SUFFIX','$DRILL_USER_ID','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'activated',true);
INSERT INTO memory_source_snapshots (snapshot_id,user_id,workspace_id,conversation_id,snapshot_version,source_hash,request_format,message_count,message_parts)
VALUES ('snapshot-$DRILL_SUFFIX','$DRILL_USER_ID','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'$DRILL_CONVERSATION_ID',1,'source-hash','openai_chat',1,jsonb_build_array(jsonb_build_object('part_id','part-$DRILL_SUFFIX','blob_id','blob-$DRILL_SUFFIX','user_id','$DRILL_USER_ID','workspace_id','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'conversation_id','$DRILL_CONVERSATION_ID','role','user','part_type','file','sequence_num',0,'text_content','Retained source fixture')));
INSERT INTO memory_source_messages (id,snapshot_id,user_id,workspace_id,conversation_id,snapshot_version,sequence_num,role,text_content)
VALUES ('source-message-$DRILL_SUFFIX','snapshot-$DRILL_SUFFIX','$DRILL_USER_ID','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'$DRILL_CONVERSATION_ID',1,1,'user','Retained source fixture');
INSERT INTO memory_generation_sources (id,generation_id,user_id,workspace_id,snapshot_id,conversation_id,source_hash,status)
VALUES ('source-$DRILL_SUFFIX','generation-$DRILL_SUFFIX','$DRILL_USER_ID','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'),'snapshot-$DRILL_SUFFIX','$DRILL_CONVERSATION_ID','source-hash','completed');
INSERT INTO audit_events (id, actor_type, actor_id, action, target_type, target_id, payload)
VALUES ('audit-$DRILL_SUFFIX', 'system', '$DRILL_USER_ID', 'backup_restore_seed', 'conversation', '$DRILL_CONVERSATION_ID', '{"marker":"$MARKER_VALUE"}'::jsonb);
INSERT INTO outbox_events (id, user_id, event_type, aggregate_type, aggregate_id, payload, status, available_at)
VALUES ('outbox-$DRILL_SUFFIX', '$DRILL_USER_ID', 'conversation.finalize', 'conversation', '$DRILL_CONVERSATION_ID', '{"marker":"$MARKER_VALUE"}'::jsonb, 'processed', NOW());
INSERT INTO memory_nodes (id, workspace_id, user_id, node_type, entity_id, importance, decay_factor, embedding_status)
VALUES
  ('$DRILL_NODE_ID', 'personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'), '$DRILL_USER_ID', 'summary', 'summary-$DRILL_SUFFIX', 1.1, 0.95, 'ready'),
  ('impression-node-$DRILL_SUFFIX', 'personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'), '$DRILL_USER_ID', 'impression', 'impression-$DRILL_SUFFIX', 1.0, 1.0, 'pending'),
  ('fact-node-$DRILL_SUFFIX', 'personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'), '$DRILL_USER_ID', 'fact', 'fact-$DRILL_SUFFIX', 1.0, 1.0, 'pending'),
  ('key-node-$DRILL_SUFFIX', 'personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'), '$DRILL_USER_ID', 'key_memory', 'key-$DRILL_SUFFIX', 1.0, 1.0, 'pending');
INSERT INTO memory_summaries (id, user_id, conversation_id, node_id, summary, topic_label, summary_version)
VALUES ('summary-$DRILL_SUFFIX', '$DRILL_USER_ID', '$DRILL_CONVERSATION_ID', '$DRILL_NODE_ID', 'Deployment window captured for backup restore drill.', 'deployment', 1);
INSERT INTO memory_keywords (id, user_id, summary_id, keyword, weight)
VALUES ('keyword-$DRILL_SUFFIX', '$DRILL_USER_ID', 'summary-$DRILL_SUFFIX', 'deployment-window', 1.0);
INSERT INTO impressions (id, user_id, conversation_id, node_id, subject, subject_type, evaluation, sentiment, familiarity, description)
VALUES ('impression-$DRILL_SUFFIX', '$DRILL_USER_ID', '$DRILL_CONVERSATION_ID', 'impression-node-$DRILL_SUFFIX', 'Backup Drill User', 'person', 'reliable', 'positive', 'known', 'Reliable deployment operator.');
INSERT INTO facts (id, user_id, conversation_id, node_id, category, subject, content, confidence)
VALUES ('fact-$DRILL_SUFFIX', '$DRILL_USER_ID', '$DRILL_CONVERSATION_ID', 'fact-node-$DRILL_SUFFIX', 'preference', 'deployment window', 'Deployment window is every Tuesday 09:00 CST.', 0.98);
INSERT INTO key_memories (id, user_id, node_id, key, value, category, source, review_status,workspace_id)
VALUES ('key-$DRILL_SUFFIX', '$DRILL_USER_ID', 'key-node-$DRILL_SUFFIX', 'recent_conversation', '$DRILL_CONVERSATION_ID', 'system', 'drill', 'approved','personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'));
INSERT INTO memory_relations (id, user_id, src_node_id, dst_node_id, relation_type, weight, source, metadata)
VALUES ('relation-$DRILL_SUFFIX', '$DRILL_USER_ID', '$DRILL_NODE_ID', 'fact-node-$DRILL_SUFFIX', 'supports', 1.0, 'drill', '{"marker":"$MARKER_VALUE"}'::jsonb);
INSERT INTO memory_embeddings (embedding_id, workspace_id, user_id, node_id, node_type, embedding_version, embedding_model, content_hash, vector)
VALUES ('embedding-$DRILL_SUFFIX', 'personal-' || encode(sha256(convert_to('$DRILL_USER_ID','UTF8')),'hex'), '$DRILL_USER_ID', '$DRILL_NODE_ID', 'summary', 1, 'embedding-model', 'hash-$DRILL_SUFFIX', '[0.11,0.22,0.33]');
INSERT INTO system_settings (id, default_provider, vector_store_backend, conversation_timeout_seconds, provider_timeout_seconds, message_threshold, token_threshold, max_context_tokens, max_injection_tokens, rate_limit_rps, rate_limit_burst, cache_ttl_seconds, cors_allowed_origins, session_cookie_cross_site)
VALUES ('system-$DRILL_SUFFIX', 'openai_compatible', 'pgvector', 1800, 60, 40, 4000, 8192, 2048, 10, 20, 60, '["http://localhost:4173"]'::jsonb, TRUE);
SQL
  compose_cmd "$project" "$file" exec -T redis redis-cli set "$REDIS_MARKER_KEY" "$MARKER_VALUE" >/dev/null
  compose_cmd "$project" "$file" exec -T redis redis-cli hset "$REDIS_META_HASH" user_id "$DRILL_USER_ID" conversation_id "$DRILL_CONVERSATION_ID" >/dev/null
}

postgres_counts_json() {
  local project=$1
  local file=$2
  compose_cmd "$project" "$file" exec -T postgres psql -U memstation -d memstation -At -c "SELECT json_build_object(
    'users', (SELECT count(*) FROM users),
    'api_keys', (SELECT count(*) FROM api_keys),
    'workspaces', (SELECT count(*) FROM workspaces),
    'actors', (SELECT count(*) FROM actors),
    'agents', (SELECT count(*) FROM agents),
    'workspace_grants', (SELECT count(*) FROM workspace_grants),
    'user_settings', (SELECT count(*) FROM user_settings),
    'provider_credentials', (SELECT count(*) FROM provider_credentials),
    'conversations', (SELECT count(*) FROM conversations),
    'messages', (SELECT count(*) FROM messages),
    'token_usages', (SELECT count(*) FROM token_usages),
    'blob_objects', (SELECT count(*) FROM blob_objects),
    'message_parts', (SELECT count(*) FROM message_parts),
    'message_part_artifacts', (SELECT count(*) FROM message_part_artifacts),
    'memory_generations', (SELECT count(*) FROM memory_generations),
    'memory_source_snapshots', (SELECT count(*) FROM memory_source_snapshots),
    'memory_source_messages', (SELECT count(*) FROM memory_source_messages),
    'memory_generation_sources', (SELECT count(*) FROM memory_generation_sources),
    'audit_events', (SELECT count(*) FROM audit_events),
    'outbox_events', (SELECT count(*) FROM outbox_events),
    'memory_nodes', (SELECT count(*) FROM memory_nodes),
    'memory_relations', (SELECT count(*) FROM memory_relations),
    'memory_fact_history', (SELECT count(*) FROM memory_fact_history),
    'entity_aliases', (SELECT count(*) FROM entity_aliases),
    'memory_summaries', (SELECT count(*) FROM memory_summaries),
    'memory_keywords', (SELECT count(*) FROM memory_keywords),
    'impressions', (SELECT count(*) FROM impressions),
    'facts', (SELECT count(*) FROM facts),
    'key_memories', (SELECT count(*) FROM key_memories),
    'memory_embeddings', (SELECT count(*) FROM memory_embeddings),
    'system_settings', (SELECT count(*) FROM system_settings)
  )::text;" | tr -d '\r'
}

write_backup_metadata() {
  local counts_file="$BACKUP_DIR/table-counts.json"
  printf '%s\n' "$SOURCE_TABLE_COUNTS" >"$counts_file"
  cat >"$BACKUP_DIR/metadata.env" <<ENV
REDIS_MARKER_KEY=$REDIS_MARKER_KEY
REDIS_META_HASH=$REDIS_META_HASH
MARKER_VALUE=$MARKER_VALUE
DRILL_USER_ID=$DRILL_USER_ID
DRILL_CONVERSATION_ID=$DRILL_CONVERSATION_ID
COUNTS_FILE=table-counts.json
POSTGRES_DUMP=postgres.dump
REDIS_DUMP=redis.rdb
ENV
  cat >"$BACKUP_DIR/metadata.json" <<JSON
{
  "generated_at": "$GENERATED_AT",
  "marker_value": "$MARKER_VALUE",
  "redis_marker_key": "$REDIS_MARKER_KEY",
  "redis_meta_hash": "$REDIS_META_HASH",
  "drill_user_id": "$DRILL_USER_ID",
  "drill_conversation_id": "$DRILL_CONVERSATION_ID",
  "table_counts": $SOURCE_TABLE_COUNTS,
  "backup_files": {
    "postgres_dump": "postgres.dump",
    "redis_rdb": "redis.rdb"
  }
}
JSON
}

run_backup_export() {
  local backup_started
  backup_started=$(date +%s)
  create_compose_file "$SOURCE_COMPOSE_FILE" "$SOURCE_REDIS_DATA_DIR"
  compose_cmd "$SOURCE_PROJECT" "$SOURCE_COMPOSE_FILE" up -d postgres redis >/dev/null
  wait_for_postgres "$SOURCE_PROJECT" "$SOURCE_COMPOSE_FILE"
  wait_for_redis "$SOURCE_PROJECT" "$SOURCE_COMPOSE_FILE"
  apply_migrations "$SOURCE_PROJECT" "$SOURCE_COMPOSE_FILE"
  seed_source_fixture "$SOURCE_PROJECT" "$SOURCE_COMPOSE_FILE"
  SOURCE_TABLE_COUNTS=$(postgres_counts_json "$SOURCE_PROJECT" "$SOURCE_COMPOSE_FILE")
  compose_cmd "$SOURCE_PROJECT" "$SOURCE_COMPOSE_FILE" exec -T postgres pg_dump -U memstation -d memstation -Fc -f /backups/postgres.dump >/dev/null
  rm -f "$BACKUP_DIR/redis.rdb"
  compose_cmd "$SOURCE_PROJECT" "$SOURCE_COMPOSE_FILE" exec -T redis redis-cli --rdb /backups/redis.rdb >/dev/null
  POSTGRES_DUMP_BYTES=$(wc -c <"$BACKUP_DIR/postgres.dump")
  REDIS_RDB_BYTES=$(wc -c <"$BACKUP_DIR/redis.rdb")
  write_backup_metadata
  BACKUP_DURATION_MS=$(( ($(date +%s) - backup_started) * 1000 ))
}

run_restore_only() {
  local restore_started
  restore_started=$(date +%s)
  if [[ ! -f "$BACKUP_DIR/metadata.env" ]]; then
    echo "backup metadata is required at $BACKUP_DIR/metadata.env" >&2
    exit 1
  fi
  # shellcheck disable=SC1090
  source "$BACKUP_DIR/metadata.env"
  if [[ ! -f "$BACKUP_DIR/$POSTGRES_DUMP" || ! -f "$BACKUP_DIR/$REDIS_DUMP" || ! -f "$BACKUP_DIR/$COUNTS_FILE" ]]; then
    echo "backup payload is incomplete in $BACKUP_DIR" >&2
    exit 1
  fi
  SOURCE_TABLE_COUNTS=$(tr -d '\n' <"$BACKUP_DIR/$COUNTS_FILE")
  cp "$BACKUP_DIR/$REDIS_DUMP" "$RESTORE_REDIS_DATA_DIR/dump.rdb"
  create_compose_file "$RESTORE_COMPOSE_FILE" "$RESTORE_REDIS_DATA_DIR"
  compose_cmd "$RESTORE_PROJECT" "$RESTORE_COMPOSE_FILE" up -d postgres redis >/dev/null
  wait_for_postgres "$RESTORE_PROJECT" "$RESTORE_COMPOSE_FILE"
  wait_for_redis "$RESTORE_PROJECT" "$RESTORE_COMPOSE_FILE"
  compose_cmd "$RESTORE_PROJECT" "$RESTORE_COMPOSE_FILE" exec -T postgres pg_restore -U memstation -d memstation --clean --if-exists --no-owner --no-privileges /backups/$POSTGRES_DUMP >/dev/null
  RESTORE_TABLE_COUNTS=$(postgres_counts_json "$RESTORE_PROJECT" "$RESTORE_COMPOSE_FILE")
  local restored_marker
  local restored_meta
  restored_marker=$(compose_cmd "$RESTORE_PROJECT" "$RESTORE_COMPOSE_FILE" exec -T redis redis-cli --raw get "$REDIS_MARKER_KEY" | tr -d '\r')
  restored_meta=$(compose_cmd "$RESTORE_PROJECT" "$RESTORE_COMPOSE_FILE" exec -T redis redis-cli --raw hget "$REDIS_META_HASH" conversation_id | tr -d '\r')
  if [[ "$SOURCE_TABLE_COUNTS" != "$RESTORE_TABLE_COUNTS" ]]; then
    echo "restored table counts do not match backup metadata" >&2
    echo "source:  $SOURCE_TABLE_COUNTS" >&2
    echo "restore: $RESTORE_TABLE_COUNTS" >&2
    exit 1
  fi
  if [[ "$restored_marker" = "$MARKER_VALUE" ]]; then
    REDIS_MARKER_MATCH=true
  else
    echo "restored redis marker mismatch" >&2
    exit 1
  fi
  if [[ "$restored_meta" = "$DRILL_CONVERSATION_ID" ]]; then
    REDIS_META_MATCH=true
  else
    echo "restored redis hash mismatch" >&2
    exit 1
  fi
  RESTORE_DURATION_MS=$(( ($(date +%s) - restore_started) * 1000 ))
}

case "$MODE" in
  drill)
    run_backup_export
    run_restore_only
    ;;
  backup)
    run_backup_export
    ;;
  restore)
    run_restore_only
    ;;
  *)
    echo "unsupported mode: $MODE" >&2
    exit 1
    ;;
esac

STATUS=success
