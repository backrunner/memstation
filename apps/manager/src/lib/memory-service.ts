export interface SourceRef { workspace_id:string;message_id:string;revision:number;part_index:number;span?:{start:number;end:number} }
export interface MemoryCandidate { id:string;kind:string;content:string;status:string;revision:number;source_refs:SourceRef[];subject_actor_ids:string[];target_node_id?:string;target_revision?:number;result_node_id?:string }
export interface MemoryOperation {id:string;kind:string;status:string;read_blocked:boolean;online_cleaned:boolean;journal_persisted:boolean;backup_status:string;failure_code?:string;result?:unknown;expires_at?:string}
export interface MemoryPublication {id:string;source_node_id:string;source_revision:number;target_workspace_id:string;target_partition_id?:string;target_node_id:string;status:string;revision:number;operation_id?:string}
export interface MemoryReceipt {id:string;status:string;accepted_sequence:number;indexed_sequence:number;stage?:string;failure_code?:string;retry_count:number;dead_letter:boolean;generation_id?:string;stream_id:string;events:{message_id:string;conversation_id?:string}[]}
export interface MemoryJob {id:string;task_type:string;status:string;attempt:number;failure_code?:string;next_retry_at?:string}
export interface WorkspaceActor {actor_id:string;display_name:string;kind:string;revision:number}
export interface MemoryControl {next_cursor?:string;actors:WorkspaceActor[];sources:{id:string;revision:number;speaker_actor_id:string;status:string}[];receipts:MemoryReceipt[];jobs:MemoryJob[];operations:MemoryOperation[];limit:number}
export interface Integration {id:string;name:string;kind:string;agent_id:string;external_space_id:string;allowed_event_types:string[];revision:number}
export interface Partition {id:string;agent_id:string;audience_actor_ids:string[];revision:number}
export interface BackgroundCall {id:string;role:string;model_alias:string;model_version:string;job_id:string;attempt:number;status:string;broker_usage_id:string;provider_request_id:string;input_tokens:number;output_tokens:number;reported_cost:string;currency:string;usage_authority:string;failure_code?:string}
export interface RecordDetail {node_id:string;content:string;revision:number;subject_actor_ids:string[];source_refs:SourceRef[];generation_id:string;status:string}
export interface SourceDetail {id:string;revision:number;speaker_actor_id:string;parts:{type:string;text?:string;author?:{actor_id:string}}[]}

export interface MemoryCapabilities {status:string;runtime:{ai_backend:string;role_aliases:Record<string,string>;embedding:{model_version:string;dimensions:number;fingerprint:string};usage_authority:string};limitations:string[]}
