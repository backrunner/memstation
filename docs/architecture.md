# Architecture and capabilities

[← README](../README.md) · [工程规范](../AGENTS.md)

MemStation 的业务核心位于 `apps/server`。Manager 独立部署，通过后端的管理接口访问数据。

## 已实现能力

### Gateway

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `GET /v1/models`
- gRPC `GatewayService.ListModels`
- gRPC `GatewayService.Complete`
- SSE 流式透传与非流式代理
- API Key 鉴权、速率限制、Usage 统计、Provider 路由

### Memory

- 异步记忆写入：`conversation.finalize -> summary.generate -> embedding.generate -> analysis.extract -> keymemory.reconcile`
- 业务记忆实体：Summary、Keyword、Impression、Fact、Key Memory，以及承载 `event/task/procedure/artifact/entity/agent_action` 的通用 `memory_items`
- 图结构：`MemoryNode + MemoryRelation + MemoryEmbedding`
- Generation 生命周期：rebuild 在 inactive generation 隔离执行，支持显式 activate/discard；激活后保留当前代与上一代并回收更旧 graph/vector 数据，conversation source snapshot 默认保留最近 3 份未引用版本
- 写入一致性：人工编辑与 Key Memory 写入通过 transactional outbox 驱动 embedding、graph sync 和 vector delete；`embedding_revision` CAS 防止旧任务覆盖新向量，在线检索只读取 ready node
- 图存储约束：PostgreSQL 以复合外键和 check constraint 保证同用户 node 引用、relation endpoint/type/weight 完整性，并为 relation 双向遍历和常用 embedding 维度提供索引与 HNSW ANN 路径
- Entity 归并：generic entity 以 `entity_kind + normalized title` 形成 generation-scoped canonical identity，同一 generation 内跨 conversation 合并 aliases、mentions 和 evidence
- 会话切片：对用户/助手对话做 segment 化写入，并建立 `follows_segment`、`summarized_into` 等图关系
- 时间能力：`importance * time_decay` 排序、`activity_at`、`time_tier`、temporal graph
- 记忆检索：向量候选 + SQL hydrate + 1-2 hop relation expansion + 混合 key memory 注入
- 记忆注入：结构化 User Profile / Active Tasks / Relevant Procedures / Relevant Facts / Relevant Events / Relevant Agent Actions / Relevant Artifacts / Relevant Entities / Relevant Memories / Relevant Impressions，上下文装包按 Key Memory、Task、Procedure、Fact、Event、Agent Action、Artifact、Entity、Summary/Segment、Impression 分区优先级执行
- Fact 写入：LLM 从原始来源提取事实、语义键和分类，按 `fact_key` / `lineage_group_id` / `revision_seq` 聚合版本；supersession 写入必须带明确 lineage 锚点和可解析目标，并在新 fact supersede 旧 fact 时同步 current head、`superseded_by_id` 与专用 `supersedes` 图边
- Fact 检索与管理：默认折叠到 current head，搜索 / GraphQL / gRPC / Manager 均返回 lineage metadata，包括 `lineage_path`、`superseded_alias_node_ids` 与 `revision_depth`
- Summary 写入：LLM 生成摘要、主题、关键词与评分，UTF-8 安全分块，每批来源最多 16,000 bytes，单来源片段最多 12,000 bytes，最多 32 批；多块摘要分层通过 LLM 合并，无规则兜底。详见 [LLM 记忆生成](llm-memory.md)
- Memory REST：
  - `POST /api/v1/memory/search`
  - `GET /api/v1/memory/keys`
  - `PUT /api/v1/memory/keys/:key`
  - `DELETE /api/v1/memory/keys/:key`
- Memory gRPC：
  - `MemoryService.Search`
  - `MemoryService.ListKeyMemories`
  - `MemoryService.UpsertKeyMemory`
  - `MemoryService.DeleteKeyMemory`

### Admin Plane

- GraphQL Query/Mutation
- Admin REST：overview、users、conversations、memory、config、providers、queues、tasks、cache、audit
- Admin / Health gRPC：`HealthService.GetStatus`、`AdminService.Overview`、`AdminService.ListConversations`、`AdminService.GetConversation`、`AdminService.GetConversationMemory`
- 服务端 session 登录，浏览器不存储 admin token
- Manager 默认浅色、中性配色与紧凑布局；常规状态和元数据以文字呈现，异常状态保留明确提示，减少装饰性指示器与重复徽章
- 管理面板已覆盖：
  - Dashboard：活跃对话、记忆、Key Memory、队列积压、失败任务、审计与趋势
  - Conversations：多用户聚合列表、组合筛选、timeline、usage、metadata、关联记忆与 requeue/rebuild
  - Memory：Workspace、Summary、Impression、Fact、Agent Memory、Key Memory 集合与详情；人工修正与 Key Memory CRUD
  - Users / API Keys / Settings / memory generation rebuild
  - Token statistics：时间范围、用户、provider 与高消耗对话
  - Provider / Model / System settings：草稿、校验、drift、diagnostics 与审计提示
  - Health / Cache / Queues / Tasks / Audit：危险操作统一二次确认
  - Playground Chat / Memory Recall / Protocol Playground

### Eval / Ops

- Repo-native retrieval/ranking benchmark 脚本（本地 / 手动评测）
- LOCOMO adapter 与外部 benchmark 脚本
- Contract tests、Go E2E、Manager Playwright E2E
- Worker backlog recovery smoke/nightly
- Backup / export / restore drill
- CI / nightly diagnostic artifacts use short-retention, best-effort uploads
- Docker Compose 单机交付基线
- K8s baseline manifests
- OTel / OTLP / structured logging / health endpoints

## Manager 设计边界

- Manager 是独立前端，不承载业务核心
- 所有实际管理能力都由 MemStation server 暴露
- Manager 可以保存多个 target，并在不同 MemStation 实例之间切换
- Manager 支持直接粘贴 `memstation://host:port?key=...` / `memstations://...` 形式的连接 URI；`key=` 必须是服务端生成的 Argon2id admin token hash，不应放入原始明文 admin token
- 一套 MemStation 服务视为一个运行环境，Manager target 只暴露 label、endpoint 和 admin token 连接信息
- Manager 默认采用专业、明亮、高密度的 ToB 运维视觉，桌面与移动端均可操作；Cache 等运维模块保留明确主导航入口
- 组件使用统一按钮、表单、列表间距及自托管 IBM Plex Sans 字体；System 提供分区表单与栏目跳转，Providers 保持固定操作排列。记忆详情优先编辑，关联记录支持直接跳转，原始字段和 JSON 按需展开
- 共享表单支持稳定的标签关联、键盘选择与错误提示；移动导航支持焦点约束及 Escape 关闭，target 操作提供触屏可用菜单
- Key Memory 通过弹窗创建，列表优先展示实际内容并折叠原始记录；长配置页提供固定保存操作，审计筛选保留输入焦点并防止旧请求覆盖新结果
- 各端将次要筛选按需展开，优先显示搜索与结果；Memory workspace 不再重复展示集合导航或拉取整组集合计数。搜索失败保留输入，可直接重试
- 手机主要控件提供 44px 触控高度与 16px 文本输入，System / Providers 的保存操作固定在底部；短屏弹窗随可视区域调整，平板表单保持双列。任务队列选择、筛选请求竞态与 WebKit 导航焦点均有回归覆盖
- 本地 full-stack 调试可通过 `pnpm dev:portless` 启动热重载 MemStation server 与 Portless Manager；如后端已单独运行，也可通过 `pnpm dev:manager:portless` 只启动 Manager 代理


## Graph retrieval and history

See [Graph memory](graph-memory.md) for bounded relation traversal, precise evidence, bitemporal fact snapshots and scoped, reversible entity aliases.
