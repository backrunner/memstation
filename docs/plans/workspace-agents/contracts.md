# 工作区与多 Agent 接入契约

以下均为拟实现契约。原有 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1/models` 保留协议兼容；新增控制字段不会原样转发给上游模型服务。

## 1. 认证与作用域

API Key 认证返回 owner user、agent、credential 及权限上限。调用方不能通过 `user_id`、`agent_id`、model 参数或消息中的文字切换身份。Agent 多把凭据支持独立撤销和默认 Workspace。

标准协议扩展头：

| Header | 含义 |
| --- | --- |
| `X-MemStation-Workspace` | 内部 Workspace ID；必须通过授权校验 |
| `X-MemStation-Session` | 保留现有语义，映射 Conversation；省略时按默认创建策略生成 |
| `X-MemStation-Run` | 本次执行 ID；存在时须与当前 Agent／空间／会话匹配 |
| `X-MemStation-Context` | 可选的服务端上下文准备结果 ID，绑定作用域、源版本、协议与有效期 |
| `Idempotency-Key` | Gateway／原生写接口的幂等标识；同键异 payload 返回 409 |
| `If-Match` | 修改型接口的 expected revision；缺失或过期不能无条件覆盖 |

原有 Mode、Inject-Context、Inject-Tools、Memory-Persist、Source-Retain、Tool-Write 和 End-Conversation 继续有效，不能扩大权限。Workspace 选择顺序为显式参数 → 凭据默认 → Agent 默认；没有可用默认时返回 `workspace_required`，不能任意取第一个或搜索整个账号。只有迁移生成的 legacy key 有自动个人默认映射。

普通 JSON API 在 URL／body 中携带 workspace_id，若同时提供 Header 则必须一致。服务端先授权再查对象；客户端传 actor_id 是来源身份或被描述主体，不是认证身份。外部 sender 只能通过已授权 Integration 映射。

## 2. 原生 API 清单

| 接口 | 作用 | 主要契约 |
| --- | --- | --- |
| `GET /api/v1/workspaces` | 列出当前 Agent 可访问空间 | 返回有效能力、默认空间和 cursor；不返回其他账号目录 |
| `POST /api/v1/conversations` | 创建会话 | workspace、context_mode、可选 source_thread；内部 ID 服务端生成 |
| `POST /api/v1/conversations/{id}/finalize` | 封存当前处理版本 | expected revision／最后 sequence；重试幂等 |
| `POST /api/v1/runs` | 创建执行 | workspace、conversation、可选 parent／external ID |
| `PATCH /api/v1/runs/{id}` | 上报状态／完成／心跳 | 状态转移和 revision 校验；不等于控制远程进程 |
| `POST /api/v1/events/batch` | 批量摄取消息与执行事件 | 同空间、同来源、事务提交、逐条 accepted/duplicate |
| `GET /api/v1/ingestions/{id}` | 查看已提交摄取与派生处理进度 | accepted watermark、indexed watermark、失败阶段 |
| `POST /api/v1/memory/search` | 扩展现有检索 | 空间、主体、适用条件；访问范围由服务端解析 |
| `POST /api/v1/memory/context` | 准备有界上下文 | 返回上下文 handle、来源版本、协议输入投影和 token 估算 |
| `POST /api/v1/memory/candidates` | 提交候选记忆 | subject、证据、visibility、expected revision；非直接写事实 |
| `GET /api/v1/memory/candidates/{id}` | 查询处理结果 | pending/accepted/conflict/rejected 及可见原因 |
| `GET/PUT/DELETE /api/v1/memory/keys[/{key}]` | 扩展现有 Key Memory | 必带解析后的空间／subject／partition；写入开关及白名单继续生效 |
| `/v1/files` 及关联读写 | 扩展原始素材接口 | 记录所属 Workspace／partition，签名 URL 和下载也受权限约束 |

Space／Agent／Integration 创建、授权、身份合并、候选批准、共享发布、generation 激活、归档和删除采用 Admin REST 与 GraphQL Mutation。API 路由表及 GraphQL schema 在 P0 固定，不能到 UI 开发阶段才临时添加无鉴权端点。

所有列表使用 cursor 和 limit，默认 50、上限 200。批次上限及上下文 token budget 由服务配置约束；不能用超大请求绕过单事件限制。

## 3. 群消息摄取示例

Integration 与 WorkspaceSource 由管理员预先绑定；key 授权机器人向目标空间摄取。发送者的 external ID 属于 Integration 的身份命名空间。

```json
{
  "workspace_id": "ws_group_a",
  "integration_id": "int_chat",
  "events": [
    {
      "event_id": "evt_9001",
      "type": "message.created",
      "source_message_id": "msg_501",
      "source_revision": 1,
      "sender_external_id": "member_42",
      "occurred_at": "2026-10-23T10:00:00Z",
      "content_blocks": [{ "type": "text", "text": "我最近开始学习摄影" }]
    },
    {
      "event_id": "evt_9002",
      "type": "message.created",
      "source_message_id": "msg_502",
      "source_revision": 1,
      "sender_external_id": "member_57",
      "reply_to_source_message_id": "msg_501",
      "occurred_at": "2026-10-23T10:00:03Z",
      "content_blocks": [{ "type": "text", "text": "我更喜欢画画" }]
    }
  ]
}
```

事务提交后返回 202，表示源消息已接受，不表示画像或 embedding 已完成：

```json
{
  "ingestion_id": "ing_701",
  "workspace_id": "ws_group_a",
  "conversation_id": "conv_801",
  "accepted_through_sequence": 12,
  "memory_indexed_through_sequence": 8,
  "events": [
    { "event_id": "evt_9001", "status": "accepted", "message_id": "m_501" },
    { "event_id": "evt_9002", "status": "accepted", "message_id": "m_502" }
  ]
}
```

`message.edited` 指向同一个 source_message_id，携带新 source_revision、完整替换内容和新的 event_id；`message.deleted` 创建 tombstone。已观察到更高 revision 时，迟到旧事件保留审计但不倒退投影。未知 reply_to 先作为待解析引用，不能跨空间猜测或绑定。

工具事件使用 `tool.started`／`tool.completed`，包含 run_id、tool_call_id、执行状态、允许保留的输出摘要和产物引用。工具命令中可能存在密钥，应在连接器和服务端两层执行日志／存储策略；不把整段 stdout 自动复制成事实。

## 4. 网关模式

### 4.1 普通客户端历史

客户端继续发送标准协议 body，通过 headers 选择 Workspace／Conversation／Run。没有新字段的老客户端使用 legacy key 的默认个人空间，原有工具、流式事件和返回格式保持兼容。

旧版依靠 session 名称持续写入的客户端需内部维护版本／串行化：服务端绑定外部 session 到 `(user,workspace,agent,external_session_id)`；新的协作 Conversation 由创建 API 返回内部 ID。相同 session 文本不能导致另一个 owner 或 Agent 的 Conversation 被 upsert 改归属。

### 4.2 由事件日志准备上下文

1. 先摄取触发消息，等待源写入成功。
2. 调用 `POST /api/v1/memory/context`，提供 workspace、conversation、run、trigger_message_ids、协议和 token budget。
3. 服务端按输出受众和权限加载近期源消息与可用记忆，返回 `context_id`、有效期、source watermark、`protocol_input` 与 `input_digest`。
4. 客户端将返回的协议输入字段传给对应 `/v1/*`，附 `X-MemStation-Context`；模型等非内容参数仍可按允许配置选择。
5. Gateway 重新验证权限、来源存活和 digest，复用准备结果，不重复追加已摄取的人类消息；保存新的助手输出、工具事件和 usage。

Context handle 绑定 owner/agent/workspace/run/protocol/policy_version 和源 revision，默认有效期 60 秒，可配置上限；过期或被撤回来源使其失效。`input_digest` 覆盖协议投影中的消息／输入字段。客户端更改这些字段必须重新准备 context，不能用一个旧 handle 为任意新历史背书。

上下文准备对异步索引采用清晰的一致性语义：近期源消息必须覆盖触发 sequence，长期记忆可落后并返回 indexed watermark。默认不无限等待 Worker；需要等候时客户端显式传有限超时。新消息进入近期上下文不代表已经完成长期画像。

Context response 的协议输入投影让没有“自动补历史”能力的 SDK 也能使用，三个协议各自保持合法格式。显式私有／共享标记、成员昵称和来源由服务端渲染为普通内容；用户消息不会升级为 system 指令。

## 5. 独立记忆模式

代码／个人 Agent 可以保留自己的模型调用路径：创建或恢复 Run → 发送消息和工具事件 → 调用 memory search/context → 自行组织模型请求 → 将结果和产物继续作为事件上报。可直接使用 REST／gRPC，不强制经过 MemStation Gateway。

接入包必须提供以下可运行示例，模型名称从环境或配置读取：

- 两个独立 Agent 凭据访问同一项目 Workspace，执行与审查互相交接。
- 单机器人向两个群 Workspace 摄取多人消息，只有触发消息需要回复。
- 个人 Agent 发布一条明确批准的偏好到项目空间。
- 老 OpenAI-compatible 客户端只改 Base URL 和 key，不发送新 headers。

首版不增加公共 memstation benchmark CLI 子命令，也不要求专用 SDK 或 MCP 才能接入。若追加 MCP 适配器，它只能复用相同 AccessContext 和业务接口，不能形成额外的全账号记忆通道。

## 6. 修改与错误语义

| HTTP／gRPC | 代码／语义 | 客户端行为 |
| --- | --- | --- |
| 400 / InvalidArgument | `workspace_required`、无效 subject、body/header 不一致 | 修正输入，不自动换空间 |
| 401 / Unauthenticated | key 失效、过期、已撤销 | 重新配置凭据 |
| 403 / PermissionDenied | 能力不足或禁止的目标 | 显示有效范围，不重试扩权 |
| 404 / NotFound | 对象不存在或调用方不可见 | 不泄露跨空间对象存在性 |
| 409 / Aborted | `revision_conflict`、`idempotency_conflict`、会话模式冲突 | 获取可见的新版本／重新准备 context；保留用户草稿 |
| 412 / FailedPrecondition | Context 失效、来源已撤回、空间归档 | 重新解析上下文或停止操作 |
| 429 / ResourceExhausted | 限流／并发预算耗尽 | 遵循 Retry-After 和抖动退避 |
| 503 / Unavailable | 存储／权限源不可用 | 有界重试；不能退回不带空间的查询 |

GraphQL 使用等价稳定 `extensions.code` 与 request_id，不能把错误吞为成功空列表。普通权限错误不泄露当前服务内部堆栈。批处理结构错误返回事件索引和安全字段名，不回显完整消息。

管理员编辑 Fact／Key Memory／授权／空间配置必须携带 expected revision。冲突返回有权查看的 current revision 和差异信息，UI 保留未保存输入；覆盖需要重新审查并提交，不提供无版本的快捷绕过。

## 7. Transport 与 DTO 同步

- 扩展 UnifiedRequest、UnifiedMessage、SearchInput、VectorSearchInput、RelationQuery、KeyMemoryQuery 和所有 repository 方法，明确区分解析前请求与已授权上下文。
- gRPC 在现有 Gateway/Memory/Admin 服务中追加类型与 RPC；protobuf 增量使用新 field numbers，不复用旧字段。上下文可放 typed message 或 metadata，但多处同时声明必须一致。
- GraphQL 增加 Workspace、Agent、Actor、Run、Grant、Candidate、Publication 及 cursor connections；resolver 保持薄层。
- Admin DTO 返回 `effective_permissions`、`capabilities`、`revision`、分页和明确更新时间，方便 Manager 在旧服务或禁用能力下正确降级。
- 旧服务没有 Workspace capability 时，Manager 显示“个人模式／需要升级”，保留旧查看流程，不显示虚构 Workspace，也不发送未支持写操作。
- Manager 服务端会话与 gRPC admin bootstrap 规则沿用 AGENTS.md；Agent 数据面不能携带 admin token。
