# 独立记忆服务接入契约 1.0

本文描述当前实现的 MemStation 数据面和管理面。完整 Run 调度、context handle 和网关自动补群历史仍属于 [Workspace 架构方案](architecture.md) 的后续范围，不是本契约的可调用接口。Mimic 保留自己的 AI Runtime、短期窗口、参与判断、发送状态和上下文预算，独立摄取与检索不会再次代理经过 MemStation Gateway。

实现与验证记录见 [MS 验收表](implementation-status.md)，部署、broker 和恢复步骤见 [接入说明](../../independent-memory-integration.md)。

## 认证与范围

数据面使用 MemStation Agent API Key：`Authorization: Bearer <memory service credential>`。它与 MemStation 调用外部 AI broker 的凭据不同，两者均不是模型厂商密钥。数据面不接受 admin token。

| 字段 | 实际语义 |
| --- | --- |
| `X-MemStation-Workspace` | 已授权 Workspace ID；省略时使用凭据或 Agent 默认值。不能通过 body 指定 owner |
| `X-MemStation-Audience` | 可选 JSON：`{"kind":"workspace"}` 或 `{"kind":"direct","actor_ids":["actor-id"]}`。由宿主核心填写，不交给 LLM |
| `Idempotency-Key` | events batch、创建窗口、forget、retract、export 的稳定请求标识 |
| `partition_id` | 已绑定空间内的分区选择；空串为 Workspace 受众。私有分区同时要求被授权 Agent 和完整输出受众均匹配 |
| `expected_revision` | finalize、候选审核、发布撤回和管理修正的 CAS；不是任意覆盖开关 |

HTTP Header 同名多值和非法受众 JSON 均拒绝。gRPC 使用小写同名 metadata；授权先于读取，消息中的昵称、role、metadata 和任意 workspace/actor 文本不授予权限。候选在 top-k 之前按空间、分区和查询条件过滤；精排后重读来源/记录与当前授权。Admin 私有读取需要显式 partition，记录审计。

成员 Actor 与 Agent 权限分别建模；外部 Actor 身份由管理员绑定的 Integration 命名空间映射。相同人物跨空间不产生共享查询权限。独立记忆查询只读当前范围；`GET /api/v1/capabilities` 不枚举别的空间。

## 数据面接口

| HTTP | 权限 | 返回与语义 |
| --- | --- | --- |
| `GET /api/v1/capabilities` | 有效业务凭据 | 版本、有效能力、当前 access、runtime、限制；`ready` 或 `memory-disabled` |
| `POST /api/v1/events/batch` | `events.write` + Integration grant | 202 receipt；源事件、消息投影和 outbox 同事务，不调用文本模型 |
| `POST /api/v1/conversations` | `events.write` | 201 窗口；body 为 workspace/integration/external_space/thread/partition |
| `GET /api/v1/conversations/{id}` | `events.write` | 窗口 ID、stream、revision 和状态 |
| `POST /api/v1/conversations/{id}/finalize` | `events.write` | `{"expected_revision":n}`；202，重复版本封存幂等 |
| `GET /api/v1/ingestions/{id}` | `events.write` | 连续水位、generation、阶段、重试、DLQ；accepted 不表示 embedding 完成 |
| `POST /api/v1/memory/search` | `memory.read` | query、subject_actor_ids、date_from/date_to、top_n 等已有过滤；返回 records 和 trace |
| `GET /api/v1/memory/records/{node_id}` | `memory.read` | 节点对应业务记录及 generation/revision；隐藏的 ID 返回 404 |
| `GET /api/v1/sources/{message_id}` | `sources.read` | 当前有效 revision 的正文、主体和引用；失效正文不可读取 |
| `POST /api/v1/memory/candidates` | `candidates.write` | 工具候选，不直接写事实；明确 kind/content/subject_actor_ids/source_refs |
| `GET /api/v1/memory/candidates[/{id}]` | `candidates.write` | 候选及处理结果；列表 `after` 为上一页最后 ID，最多 100 条 |
| `POST /api/v1/sources/retract` | `sources.retract` | `{"source_refs":[...]}`，返回可追踪删除 operation |
| `POST /api/v1/memory/forget` | `memory.forget` | source_refs/node_ids/actor_id/workspace + partition_id；先阻断读取，再异步清理 |
| `POST /api/v1/exports` | `memory.export` | 当前选定分区导出 operation；结果保留 24 小时，下载再次复核来源 |
| `GET /api/v1/operations/{id}` | 对应删除/撤回/导出权限 | read_blocked、online_cleaned、journal_persisted、backup_status、失败、到期时间及结果 |
| `GET /api/v1/usage` | `usage.read` | 当前范围 broker 调用；`after` 为上一页最后 ID，最多 100 条 |
| `POST /api/v1/usage/{id}/reconcile` | `usage.read` | 向 broker 对账 unknown 调用；结果未知不宣称零费用 |

`workspace:true` 的 forget 指整个选定 partition，不隐式擦除不可见私有分区。删除所有分区需要逐分区显式管理操作。导出权限不自动包含原文读取：缺少 `sources.read` 时剔除原文 segment、证据正文和源 metadata，保留派生内容与 sourceRefs。普通 recall/inspect 同样遵守这个边界。

能力键包括 `server_isolation`、`multi_workspace`、`private_audience`、`independent_ingestion`、`actor_attribution`、`batch_idempotency`、`source_retraction`、`forget`、`source_read`、`processing_watermarks`、`candidates`、`candidate_review`、`memory_inspect`、`recall_explanation`、`usage`、`broker`、`export`、`publishing`。隔离粒度为服务端 owner + Workspace + partition；发布和批准只对有管理权限的入口声明。存储或删除恢复不可用时全部关闭。直连模式不虚报 broker 用量能力。

## 事件、来源与窗口

管理员先配置 Integration、外部空间绑定、观察 Agent 和允许的事件类型。一次 batch 只能使用一个 Workspace、Integration、来源流和 partition。

```json
{
  "workspace_id": "ws-group-a",
  "integration_id": "integration-chat",
  "external_space_id": "platform-room-a",
  "thread_id": "topic-1",
  "events": [{
    "event_id": "event-9001",
    "type": "message.created",
    "source_message_id": "platform-message-501",
    "source_revision": 1,
    "speaker": {"kind":"human","external_id":"member-42","display_name":"小林"},
    "occurred_at": "2026-10-25T10:00:00Z",
    "parts": [{"type":"text","text":"我最近开始学习摄影"}]
  }]
}
```

`parts.type` 当前为 text/quote/mention。quote 可以带 `author`（同 speaker 结构）或 `source`；mention 使用已验证 `actor_id`；`reply_to` 使用 SourceRef。未知作者保持 `kind:unknown`，不能提供伪 Actor。已知 quote source 必须在同空间和可读分区内，引用文本和原作者从可信来源校验，不能用 A 的昵称把 B 的事实归给 A。尚未摄取的引用须等待来源就绪，不能猜测跨空间 ID。

SourceRef 是 `workspace_id/message_id/revision/part_index/span`。span 为单个内容块内 Unicode code point 的 `[start,end)`，不是 UTF-8 字节或 JavaScript UTF-16 索引。模型抽取使用有效原文片段，并验证 subject_actor_ids；原话、引用和 model_inference/human_attestation 通过 support_kind 区分。

消息身份不包含观察 Agent。相同 Integration 的多个观察者重投同 event ID + payload 返回 duplicate；同键不同 payload 整批 409，没有部分提交。每批 1–100 个事件，至多 64 个 parts/事件、64 KiB/part、1 MiB 文本；HTTP 整体 2 MiB。source revision 单调，旧编辑不倒退当前投影。删除先于创建会留下 tombstone，迟到消息不复活。

`message.edited` 提供新的完整投影和 event_id。`message.deleted` 不带正文，事务直接失效源与派生记录；不依赖语义模型可用性。bot speaker 必须携带 `delivery_status:"confirmed"` 和 `delivery_receipt`；草稿、unknown 发送和内部计划不能作为 utterance。工具/人工观察通过 candidate 提议，不伪造群消息。

窗口默认 5 分钟空闲或 100 条消息封存，还受 `MEMSTATION_INGESTION_WINDOW_BUDGET` 约束；目前预算采用包含结构字段的 UTF-8 序列化字节上界，保守限制模型输入，而非宣称精确 tokenizer 计数。封存后的新消息进入新窗口。编辑/撤回会隔离旧窗口，剩余有效来源进入修复窗口重新验证。

```json
{
  "id":"receipt-id","workspace_id":"ws-group-a","stream_id":"stream-id",
  "generation_id":"generation-id","accepted_sequence":12,"indexed_sequence":8,
  "status":"processing","stage":"analysis.generate","retry_count":0,"dead_letter":false,
  "events":[{"event_id":"event-9001","status":"accepted","message_id":"internal-source-id","sequence":12,"conversation_id":"window-id"}]
}
```

水位是 stream + partition + generation 下连续完成的前缀，不能跨过失败任务。状态为 accepted/processing/complete/failed；故障包含 next_retry_at、failure_code、dead_letter。重试退避从 5 秒指数增长、上限 1 小时；DLQ 在管理端重放原任务 ID。来源过期的任务终态为 skipped，不继续重试。最新原消息可通过授权 source 读取，不需等异步索引。

## 记忆、检索与生命周期

统一记录返回 `workspace_id/subject_actor_ids/kind/content/source_refs/visibility/valid_from/valid_to/revision/status`，保留原有 `type`、`node_id`、`generation_id`。kind 优先使用明确的候选业务类型，旧记录保留原有业务 type；不重新推断历史人的归属。visibility 为 workspace/private，具体受众政策通过管理接口读取。现有 summary/fact/impression/key_memory 和扩展条目继续使用同一 MemoryNode、版本替代链与向量引擎。

检索支持 query_id、明确 query、subject_actor_ids 和时间过滤。trace 包含 query_id/provider_request_id/provider_calls/latency_ms/returned_count/eligible_count/watermarks/watermarks_partial/degraded/failure_code/score_definition。provider 分数保留其原始语义；相似度和精排分数不是事实可信度。injected_count、usedRefs 和回答效果由 Mimic 记录。失败允许宿主使用自己的短窗；不能扩大 scope 搜索。

candidate 的 kind 为 fact/preference/event/relationship/group_habit/summary/inference。待确认 inference 需要管理员给 confirmed_kind 才能批准进入正式记录。批准/拒绝使用 expected_revision；修正候选同时携带 target_node_id/target_revision。相互冲突的 CAS 不覆盖用户草稿。

发布由管理员明确批准目标副本。目标不继承源原文/Actor 档案/图访问权；没有显式 actor_mapping 时主体为目标 Workspace。来源变化暂停副本，使用 replaces_publication_id 和 expected_publication_revision 重新批准。发布副本不能再次发布；撤回副本也有独立删除 operation 和恢复清单。

forget/retract 首先阻断源、派生节点、发布副本和相关导出；后台清理向量、关系、别名、快照、附件和缓存。多来源记录先整体隔离，剩余来源重新抽取后才恢复。Actor 删除清除该范围贡献及不用的展示名/外部身份，保留其他空间或分区仍使用的身份。迟到 worker、索引重建和恢复都检查权威失效状态。

## 管理与 transport

HTTP 管理根为 `/api/admin/workspaces/{workspace}/lifecycle`：records/sources/ingestions/candidates/review、publications/retract、forget、exports、operations、usage/reconcile、overview、jobs/{id}/replay、capabilities、generations/rebuild/activate/discard。Integration、partition 和 Agent/grant 在 `/api/admin/workspaces/{workspace}` 下。全部复用业务 service，管理登录采用服务端 session。

管理 overview 使用独立集合位置编码的 `after`，返回 `next_cursor`，不会用某一列表的 ID 跳过另一列表。候选、发布和用量使用 `after=最后ID`。配置目录返回本空间的 Integration/partition 绑定，不列出他人空间。Manager 包含分区选择、接入配置、Actor/来源、水位/失败重放、候选/CAS、发布预览/撤回、删除/导出与 broker/generation。

gRPC 使用增量 `MemoryDataService` 与 `WorkspaceAdminService`；已有 `MemoryService.Search` 复用检索链。消息、receipt、revision、水位均用正式 protobuf 字段；SearchResponse 的 `recall_trace` 保留精确 int64，旧 `trace` Struct 仅作兼容展示，客户端不要从浮点 Struct 读取水位。管理 schema 为 `graph/schema/memory_admin.graphqls`，Query/Mutation 复用相同服务；GraphQL Long 承载 revision/sequence。

| HTTP | gRPC | 稳定含义 |
| --- | --- | --- |
| 400 | InvalidArgument | 无效事件/字段/候选 |
| 401 | Unauthenticated | 无效业务凭据 |
| 403 | PermissionDenied | 无权限或不可用受众 |
| 404 | NotFound | 不存在或当前不可见 |
| 409 | AlreadyExists | idempotency_conflict |
| 409 | Aborted | revision_conflict |
| 429 | ResourceExhausted | 限流 |
| 503 | Unavailable | memory_unavailable / memory_recovery_pending |

GraphQL 使用对应 `extensions.code`，不以空列表掩盖权限或处理错误。旧 Personal Gateway 协议继续兼容；旧数据只归原 Personal 空间，不猜测群、Actor 或私有受众。新的来源读取与独立摄取必须显式授权。
