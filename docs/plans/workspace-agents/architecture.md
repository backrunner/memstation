# 工作区与多 Agent 架构

本文中的新增字段、服务和行为均为目标设计。现有实现事实限定在下表，不能根据现有 metadata 字段推定功能已经存在。

## 1. 当前基础与差距

| 当前实现 | 扩展要求 | 代码入口 |
| --- | --- | --- |
| API Key 查询返回 UserID，schema 有 scopes 但运行时未形成 Agent／Workspace 授权 | 返回完整调用主体，逐操作校验授权 | `internal/security/auth.go`、`internal/service/apikey/service.go` |
| UnifiedMessage 有 role、内容和 metadata | 稳定 sender、source message、revision、occurred_at | `internal/domain/gateway.go` |
| 自动注入按 UserID 检索 | 按授权空间、可见分区、主体和输出受众检索 | `internal/service/memory/engine.go` |
| Fact lineage 主要按 user 与 fact_key；Key Memory 按 user 与 key | 将空间、主体、适用条件和可见分区加入身份 | `internal/service/analysis/service.go`、`internal/store/sql/postgres.go` |
| Entity alias 有 entity_scope | 继续用于名称消歧，不承担授权；增加稳定 Actor 绑定 | `internal/domain/graph.go`、`internal/store/sql/entity_alias.go` |
| 会话历史按快照／增量检测持久化 | 新模式使用追加事件，不能用并发历史快照互相覆盖 | `internal/store/sql/postgres.go`、`internal/service/conversation/service.go` |
| generation 激活和回收按用户 | 每个 Workspace 独立激活、补偿和回收 | `internal/service/memorygen/service.go` |
| Manager 直接请求 Target，顶部只有 Target 选择 | 引入工作区上下文和服务端数据访问 | `apps/manager/src/routes/+layout.svelte`、`src/lib/api/client.ts` |

以上入口相对 `apps/server`，Manager 行除外。已存在的图方向、证据、时间历史、版本修正和别名增强必须保留，不能以新模型替换时丢失这些能力。

## 2. 数据模型

所有业务数据继续显式携带 `user_id`。服务端生成内部 ID；外部 ID 只能作为命名空间内的映射。所有可编辑对象包含时间戳和 revision；删除使用软删除或审计删除。

| 实体 | 必需字段与约束 |
| --- | --- |
| Workspace | `id,user_id,kind,name,status,revision,policy_version,default_settings`；kind 为 personal/project/group/custom；归档保留可读历史，停止新普通写入 |
| Actor | `id,user_id,kind,display_name,status,revision`；kind 为 human/agent；不是登录账号 |
| ActorIdentity | `user_id,identity_namespace_id,external_actor_id,actor_id,verification_status`；唯一键为 user＋namespace＋external_actor_id |
| Agent | `id,user_id,actor_id,name,status,default_workspace_id,settings,revision`；一对一关联 agent Actor；同一 Agent 可有多把凭据和多个实例 |
| AgentCredential | 扩展现有 APIKey：`agent_id,default_workspace_id,capability_ceiling,expires_at,revoked_at`；只返回一次明文，认证保留 credential_id |
| WorkspaceMember | `user_id,workspace_id,actor_id,display_name,joined_at,left_at,metadata`；群成员信息不自动产生 API 权限 |
| WorkspaceGrant | `user_id,workspace_id,agent_id,capabilities,revision`；唯一 user＋workspace＋agent；无记录即无授权 |
| Integration | `id,user_id,kind,name,identity_namespace_id,status,revision`；记录来源和受信任连接，不等于 Agent |
| IntegrationAgentGrant | `user_id,integration_id,agent_id,workspace_id,allowed_event_types,revision`；限定哪些 Agent 可代表该来源陈述外部身份与修改消息，不能只凭 events.append 冒充平台连接器 |
| WorkspaceSource | `user_id,workspace_id,integration_id,external_space_id`；来源映射唯一，两个 Agent 可观察同一映射 |
| Conversation | 增加 `workspace_id,source_thread_id,context_mode,revision,last_sequence,window_start,window_end`；创建后不可换归属 |
| AgentRun | `id,user_id,workspace_id,conversation_id,agent_id,parent_run_id,external_run_id,status,attempt,started_at,finished_at`；父子 Run 同 owner/workspace，不能借父 Run 扩权 |
| SourceEvent | `user_id,workspace_id,integration_id,event_id,event_type,source_message_id,source_revision,occurred_at,received_at,sender_actor_id,observed_by_agent_id,run_id,payload_hash,payload`；不可变事件 |
| Message | 增加 `workspace_id,sender_actor_id,source_event_id,source_message_id,source_revision,reply_to_message_id,occurred_at,run_id`；role 仍是协议角色，不代替身份 |
| MemorySubject | 稳定主体注册：`id,user_id,kind,actor_id,workspace_id,resource_key`；actor/workspace/resource 三种互斥类型；跨 generation 保持稳定 |
| MemoryCandidate | 抽取结果及证据、目标分区、expected_revision、处理状态；可接受、拒绝、保留冲突；候选不会进入普通事实注入 |
| MemoryPublication | `source_memory_id,source_lineage_id,source_node_id,source_generation_id,source_revision,target_workspace_id,target_node_id,status,approved_by,reason`；稳定来源身份和版本快照同时保留；发布内容与来源引用分开授权 |

MemorySubject 让“人、项目、文件或其他实体”有稳定身份。现有 generic Entity 的 MemoryNode 仍是某个 generation 中的记忆表示，通过 subject_id 关联，不把重建后的 node_id 当作人物身份。一个记忆可以用 MemorySubjectLink 关联多个提及主体，但 Fact／Key Memory 有明确主 subject_id。

### 2.1 现有记忆与索引的扩展

Summary、Segment、Impression、Fact、Key Memory、generic item、MemoryNode、MemoryRelation、MemoryEmbedding、Keyword、Evidence、原始素材与 blob 均需有可校验的 Workspace 归属。不能只给顶层 MemoryDocument 增加字段。

记忆公共字段：`workspace_id,subject_id,visibility,private_agent_id,created_by_agent_id,source_revision,revision,applicability`。

- `visibility=workspace`：该空间中有读取权限的 Agent 可见。
- `visibility=agent_private`：只有指定 Agent 和经过审计的管理员检查路径可见。生产者本身不自动决定可见性。
- 跨 Workspace 不使用 `public=true` 或全账号 wildcard；通过发布在目标空间形成独立记录。
- Summary、关键词、向量和图关系不得扩大输入的可见范围；混合私有与共享材料时按分区分别生成产物。
- `applicability` 保留代码分支、提交、环境、时间等适用条件；分支条件不同的事实不互相 supersede。

索引与一致性：

1. 父表建立 `(user_id,workspace_id,id)` 唯一约束，子表使用对应复合外键或事务内等效校验，防止只按裸 ID 连接。
2. 新 Fact lineage 唯一身份为 `(user_id,workspace_id,visibility_partition,subject_id,fact_key,applicability_key)`；revision 在此身份内递增。
3. Key Memory 同样按 owner、workspace、partition、subject、key 唯一；可选字段使用明确规范值，不能因 NULL 绕过唯一性。
4. 事件唯一键为 `(user_id,workspace_id,integration_id,event_id)`；同键同 hash 为重复，同键异 hash 返回冲突。编辑使用新的 event_id 和递增 source_revision。
5. 原始消息身份为 `(user_id,workspace_id,integration_id,source_message_id)`，不包含观察 Agent，从而避免重复证据。
6. MemoryEmbedding 继续使用专用向量表；过滤 owner、workspace、partition、generation、model/version 后返回候选。
7. 一条普通 MemoryRelation 两端必须同 owner/workspace，且 generation 兼容。跨空间发布引用使用专用表，不纳入普通图遍历。
8. 所有列表使用稳定游标分页和上限；空间、Agent、事件与记忆数量增长不得变成无界列表。

### 2.2 Workspace 身份与生命周期

代码仓库的路径、remote、worktree、分支仅作为 WorkspaceSource 的映射和上下文。用户明确选择绑定关系；不根据同路径或同 remote 自动合并授权。平台命名空间若仅提供群内局部用户 ID，身份映射必须包含群；不得假设跨群全局唯一。

Agent 禁用阻止新请求并撤销其凭据；其既有共享记忆不因停用自动删除。私有记忆转交必须显式审计。Workspace 归档停止摄取与普通写入、保留历史；清除采用可观察的异步删除任务，先停止读取和新任务，再清理 SQL、向量、缓存、blob 和派生产物。User 删除覆盖其所有空间。Actor 解绑／合并可撤销且保留历史来源，不重写原话。

## 3. 授权与请求上下文

统一解析得到 `AccessContext`：owner、agent、credential、workspace、conversation、run、output_audience、allowed_partitions、policy_version。它由服务端构造，不能从客户端 JSON 原样反序列化当作授权。

权限建议：`gateway.invoke`、`events.append`、`memory.read`、`memory.propose`、`memory.edit`、`memory.publish`、`workspace.manage`。普通 Agent 默认只有显式授权的必要项；工具写入还须同时满足用户与空间开关、Agent capability、Key Memory 键白名单及验证层。

```text
有效能力 = credential 上限 ∩ WorkspaceGrant ∩ Workspace policy
允许检索 = 当前空间可见分区 ∩ 本次输出受众允许范围
```

群成员的 Actor ID、请求中指定的 subject、模型生成的工具参数，都不能变成新的授权主体。外部 sender 仅由 IntegrationAgentGrant 允许的连接器映射；未知 external_actor_id 可建立未确认的来源身份，但不能自动合并到已验证的人物。普通 Agent 只能上报自身行动或明确授权的外部事件；编辑与撤回也须校验来源权限。默认 Gateway 请求必须落到一个 Workspace。多个已授权空间的管理员汇总属于明确的管理查询，不能隐式用于普通 Agent 回复。

所有入口使用同一策略：HTTP、gRPC、GraphQL、Manager、只读工具、写入工具、原始文件、历史版本、导出、计数、调试信息。服务拒绝不匹配的 conversation/run/workspace 引用，跨用户或不可见 ID 对外返回不泄露对象存在性的错误。

撤权不能依赖缓存 TTL。授权检查读取当前 policy version／权威状态；缓存只保存数据结果，命中后仍检查范围。管理员检索模拟 Agent 权限时必须按目标 Agent 的策略执行，不能把管理员全量读取结果标成该 Agent 可见。

## 4. 消息与执行摄取

### 4.1 事务和时序

每个 batch 只属于一个 Workspace 与 Integration。先验证整批 schema、归属和事件 ID；结构错误整批拒绝。有效批次在同一事务中去重、追加 SourceEvent、更新消息投影、分配 Conversation sequence、写入 Audit／Outbox。返回每条事件 accepted/duplicate；数据库失败整批回滚。首版默认上限 100 事件和 2 MiB JSON，可配置，媒体走 blob API，不塞入批次。

保留平台 occurred_at 和服务器 received_at。Conversation sequence 表示服务接收顺序，不冒充平台真实因果顺序；reply_to 和显式因果引用单独记录。乱序编辑依据 source_revision；缺失的平台 revision 由连接器用稳定事件顺序映射，不能用客户端时钟简单覆盖。

多个 Agent 上报同一 Integration 来源事件只产生一条业务事件，可另记观察记录。同源去重只减少重复证据，不能把私有来源变成共享证据。

### 4.2 会话窗口与 Run

群 Workspace 长期存在。按来源线程形成逻辑会话，再按最大消息数、token 数、空闲超时和最大窗口时长形成不可变处理窗口。窗口阈值可配置，初始沿用现有阈值；连续活跃群必须有最大时长，避免一直等不到总结。

封窗原子记录最后 sequence 与源版本，追加 Outbox；新消息进入新窗口。迟到消息不覆盖旧快照，生成修正事件和新的处理版本。Conversation 显式结束、阈值与 scheduler 竞争时同版本只生成一次任务。

Run 是外部执行记录，不是 asynq Task。运行状态由具备授权的 Agent 上报，长时间无心跳显示“状态未知／最后报告时间”，不推断已成功或已取消。重试形成 attempt，子 Run 关联 parent_run_id；工具结果、代码产物及 usage 关联实际执行方。

### 4.3 事件模式与兼容快照模式

Conversation 的 context_mode 创建时固定为 `events` 或 `client_history`。

- events：摄取日志是事实来源；Gateway 的输入只是该日志的模型投影，不重复记为新的人类消息。
- client_history：兼容现有完整／局部历史请求；一个 Conversation 的写入通过版本或串行化保护。并发修改遇到旧 revision 返回冲突，不静默覆盖。
- 同一 Conversation 不混用两种来源规则。转换时新建会话并显式关联来源。
- 已完成的 Gateway request 重试返回已存结果；处理中返回可重试状态。上游 SSE 断开后不自动发起第二次收费生成；保存中断状态和已确认 usage，不承诺 exactly-once 上游调用。

## 5. 分析与记忆写入

保留 Summary → Embedding → Analysis → Key Memory 的现有服务边界，增加结构化证据归属。

1. 分析读取带 sender、subject 候选、引用、source revision 和可见范围的源事件快照。
2. 输出候选 Fact／Impression／Entity，区分自述、他述、引用、工具观察与模型推断。
3. 验证主体绑定、引用跨度、消息归属、可见范围、时间、代码适用条件及已撤回来源。
4. 与同 lineage 当前事实比较。仅有新的可靠证据和明确替代关系才提交新 revision。
5. 同时写入实体、MemoryNode、关系、证据、审计与 Outbox；写入 expected_revision 不匹配时保留候选或重算，不直接覆盖。

Agent 提交的陈述默认是候选；可信工具执行记录和明确自述也需经过上述校验。个人画像不能将助手生成的回答、系统提示或已注入记忆作为独立的人类证据。相同来源多次总结不增加独立支持数量。

语义提取仅使用 LLM，并需升级为 subject-aware：无法归属时只生成空间／话题记忆，不默认 `subject=user`。模型结构化提取失败时保留原始消息和已经验证的 LLM 结果，任务失败重试；禁止规则生成兜底事实或摘要，不为了填满画像强行产生人物事实。用户可人工修正并看到版本比较。

## 6. 检索、注入与共享

按照 AccessContext 对向量、关键词、历史查询和 fallback 路径进行前置过滤，之后再使用现有 MemoryEngine 排序与有界图扩展。保留 `semantic_score × importance × time_decay` 和现有 score breakdown，不引入平行排序实现。

关系查询、supersedes 追踪、alias、conversation expansion、证据 hydration 和重排输入都必须保持相同范围。服务降级时可以省略失效记忆，绝不能回退到 user 全量召回。直接 ID 查询、附件和导出也不能绕过过滤。

注入组合是当前空间近期事件、相关记忆、涉及主体的当前空间画像和已发布到当前空间的共享记忆。不能按所有成员把画像全部注入；由相关性、主体集合与 token budget 限制。调试结果说明实际使用了哪些来源、哪些候选因范围／版本／预算被排除；普通调用方不可看到未授权内容或其敏感标题。

输出受众约束覆盖 MemStation 准备的上下文、内置工具和返回数据。连接器须把准备结果绑定到对应目标会话，不得用于其他群发送；任意外部 Agent 已合法读取后自行转发的行为无法只靠记忆服务阻止，不能把服务内作用域测试解释为对所有外部行为的保证。

跨空间发布是显式的内容投影：显示目标空间、待发布内容、受众和来源依赖，允许删减内容后批准。目标只看到发布内容，默认不获得源消息、源人物档案或整个图的权限。源修正使发布项进入待更新状态；源撤回立即停用依赖项，不能靠异步重建前的旧缓存继续使用。发布引用按稳定 memory/lineage 身份和原始版本定位；被引用快照受保留保护，generation 回收不能静默断开依赖。源材料清除遵循明确的撤回或保留已批准正文策略，UI 显示实际来源可用性。

## 7. 缓存、generation 和可靠性

- 数据缓存键至少包含 owner、workspace、generation、visibility partition、查询条件和适用上下文；权限敏感结果包含 policy version。
- 写入后事务记录失效 Outbox；L2 主动失效并通知 L1；TTL 只承担容错性能语义。
- 删除、撤权、源撤回使用权威状态／版本检查阻止旧结果继续返回，再异步清理向量和派生数据。
- Workspace 各自有一个 active generation。重建使用 source watermark、不可变快照及后续增量补齐，激活前确认已追平并通过完整性校验。
- 激活在短事务中比较旧 active generation 与 watermark；失败保留旧代。上一代回退也必须满足最新源 revision／撤回约束，不能把已删除内容复活。
- 部署级 embedding 模型变化按 Workspace 扇出重嵌入；切换前仍校验模型版本与索引兼容性。
- 队列任务唯一身份包含 owner/workspace、阶段、源窗口版本、generation 与必要的可见分区；消费端数据库唯一性／CAS 是最终幂等保障，asynq TaskID 不是唯一保障。
- 多 Agent 限流和队列公平性按账号总量与 Agent／Workspace 并发预算控制；不是商业化 Billing／Quota 产品。

已接受事件在 Agent 凭据撤销后可由服务 Worker 继续处理既有数据；Worker 必须重新检查空间、来源存活和当前内容策略。新事件、旧工具授权及新的外部输出不能继续使用已撤销凭据。

## 8. Manager 服务端边界

以 AGENTS.md 的 server load／API routes 原则为准，纠正旧 frontend-panel 中“业务全直连且禁止薄代理”的冲突。Manager server 只做会话保管、目标选择、请求转发和 SSR DTO 装配，不复制 Go 的授权、记忆或 SQL 逻辑。

浏览器只持有 Manager 的 httpOnly／secure／sameSite session cookie。目标服务 session 保存在 Manager 服务端的会话存储；生产使用 Redis 保存最小、加密的短期凭据与 Target 绑定，开发可使用进程内存但重启需要重新登录。bootstrap admin token 只在登录交换时使用，后续不保留在浏览器 Map、storage、URL 或日志。已显示一次的 Agent API Key 也不进入草稿和持久化缓存。

Target 在服务端登记并配置允许访问的地址。请求只传 target_id，不能成为任意 URL 转发器；有意使用的本机／局域网地址需配置允许，DNS 解析与重定向受同一策略约束。单个 Target 失联不影响其他会话；切换 Target 必须重验对应会话。Manager Redis 会话丢失只导致重新登录，不丢业务数据。

## 9. 可观测性与数据保留

Audit／Trace 可以关联 workspace_id、agent_id、run_id、conversation_id、request_id、policy_version；禁止记录完整 prompt、原始凭据和敏感 PII。Metrics 使用有限标签和聚合，避免把每个 run/message ID 放入时序标签。

新增诊断覆盖授权拒绝、事件重复与冲突、写入 CAS 冲突、来源失效、generation 延迟、队列公平性及分空间用量。用量区分实际 Gateway 调用、后台分析与外部 Agent 自报数值；外部上报未验证成本不能合并成真实计费。

源删除、人物删除和空间清除必须列出受影响的派生记忆与发布项。源内容因保留策略不可用时，UI 明确显示“来源已清理”，不伪造可回溯性；原文不存在时不再声称能重建同等记忆。
