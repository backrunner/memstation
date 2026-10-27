# MemStation

## 1. 文档定位

本文件是 MemStation 的**执行级规范**，用于统一产品目标、技术基线、开发计划、工程约束和架构边界。

- `AGENTS.md` 优先于 `.plan/` 中的旧表述
- `docs/` 仅保留面向用户或开发者的可复用指南、正式契约及发布所需素材；每轮验证报告、复核记录、程序化 JSON、计划原型和验证截图归入被 Git 与 Docker 忽略的 `.agents/archive/`，不得强制加入版本控制。
- 公开 README 和正式文档不得链接本地忽略归档。评测脚本与方法说明保留在源码中，原始模型回复、数据集和凭据继续使用 `.artifacts/` 等私有运行目录；迁移已有文件时保留内容与来源清单。
- `.plan/` 仍然是背景材料，但如与本文件冲突，以本文件为准
- 项目目标是**一次性实现完整可上线的 v1.0**，不是 MVP、不是最小闭环 Demo
- 所有依赖必须使用**写入时的最新稳定版本**，禁止在规范里继续使用 `latest`
- 所有运行时模型名都必须配置化，禁止把易失效的模型名写死在核心设计里
- **当前阶段不保留 legacy 或向后兼容实现。** 允许直接破坏旧契约；统一使用当前正式的数据模型、接口、配置和交互。禁止为旧版本保留双轨策略、兼容开关、旧路由/字段别名、旧格式读取器、隐式身份补齐、静默回退或仅服务历史版本的迁移/适配代码。修改契约时同步更新服务端、Manager、SDK/脚本、fixture、测试与正式文档；旧输入明确拒绝，不通过改名保留旧逻辑。此规则优先于本文件历史实施记录和旧计划中的兼容要求。
- 历史事实、来源追溯、事实替代链和时间查询是正式产品能力，不属于旧版本兼容；对外 OpenAI/Anthropic 等明确要求的协议适配也不属于旧版本兼容。超时、重试和服务故障降级按当前唯一契约实现，不得切回已移除的旧策略。移除兼容代码不等于授权删除现有运行数据；已有评测快照与失败证据继续留在被忽略的本地归档。
- **评测禁止作弊或泄漏。** 记忆摄取只能使用声明的原始来源，检索/规划/证据选择/回答只能使用当前问题和已授权记忆；标准答案、标注证据、题目类别、历史判分和评测样本标识不得作为模型语义输入或决定产品检索路径。禁止针对题目、人物或数据集 ID 写规则、答案型记忆、标签衍生查询或答案缓存；来源记录使用每次请求的临时不透明标识映射。标准答案只交给冻结的离线评分器，标注证据只用于结果后的诊断；oracle 轨道单独标注、不得混入正式结果。实验先固定样本、输入模态、预算、模型和判分协议，再观察结果，所有失败及执行错误保留分母；不得选择性重试、删题、修改答案/判分以追分，或用已调优样本冒充未见测试集。纯文本与图片/图片描述轨道明确分开；全上下文和 oracle 成绩仅为经验诊断，不等于已证明的理论上限，未达到目标必须如实报告。
- 上述事实来源约束不禁止通用语言理解、计算及对已给事实的常识性解释，但不得借此补入被测人物未记载的经历、关系、偏好或行踪。读者策略必须明确区分观察与解释，先固定策略再做完整样本对照，并把空记忆诊断单独报告；改善读者提示词不等于改善记忆召回，禁止混报两者收益。

---

## 2. 项目目标

MemStation 是一个**个人使用优先**，同时面向未来团队协作和商业化扩展的 **AI Gateway + Memory System + Admin Plane**。

v1.0 的最终目标：

1. 作为统一 AI 网关，对外兼容主流协议，对上游兼容多厂商模型。
2. 完整记录并结构化管理用户对话、记忆、事实、印象、Key Memory 和 Token 使用。
3. 在代理链路中实现可配置的记忆检索、注入和工具调用增强。
4. 提供可运营的管理面板、GraphQL API、gRPC API、运维接口、可观测性和部署体系。
5. 以个人场景可落地为前提，同时保留未来多用户、分布式、集群化和商业化扩展能力。

项目前景：

- 作为团队内部统一 LLM 入口，集中控制模型、密钥、成本和审计
- 作为“长期记忆层”嵌入聊天产品、Agent 产品和知识工作流
- 作为独立记忆服务，为外部应用提供检索、注入和用户画像能力
- 先服务个人自托管和高阶个人工作流，再在后续版本扩展到团队协作、集群部署和商业化 SaaS

---

## 3. v1.0 交付范围

### 3.1 必须完成的产品能力

| 能力域 | v1.0 要求 |
|--------|-----------|
| 代理协议 | 入站支持 OpenAI Chat Completions、OpenAI Responses API、Anthropic Messages；出站支持 OpenAI、Anthropic、Gemini |
| 会话管理 | 支持显式结束、超时结束、消息阈值结束、Token 阈值结束；支持 SSE 透传与非流式代理 |
| 对话持久化 | 持久化 Conversation、Message、Usage、原始协议元信息、工具调用与结构化内容块 |
| 主记忆 | Summary、Keyword、Embedding、向量检索、图关联、时间衰减排序 |
| 高级记忆 | Impression、Fact、Key Memory、关联边、版本替代关系、人工修正 |
| 注入能力 | System Prompt 注入、Context 注入、只读记忆工具注入、写入类工具受严格开关和校验保护 |
| 管理面板 | Dashboard、对话、记忆、用户、配置、健康与统计页面；服务端会话登录 |
| 管理接口 | GraphQL Query/Mutation、Memory REST API、Admin REST API、gRPC Gateway/Memory/Admin API、健康检查、指标导出 |
| 安全 | API Key 哈希存储、上游密钥加密存储、审计日志、速率限制、PII 日志脱敏 |
| 运维 | OpenTelemetry、OTLP 导出、结构化日志、版本化迁移、Docker/Compose、K8s 基线 |

### 3.2 范围边界

- **v1.0 默认目标是个人自托管、单用户、单实例优先**
- **但数据模型、任务模型和服务边界必须为未来多用户、分布式和集群化保留升级路径**
- **Tenant / Organization / Billing / Quota 是后续商业化阶段路线，不是 v1.0 阻塞项**
- GraphQL 在 v1.0 只做 **Query + Mutation**，不把 Subscriptions 作为首发要求
- 向量存储必须走统一 `VectorStore` 抽象，不能把“是否有 embedding 字段”做成运行时 Ent Schema 分支

---

## 4. 统一架构基线

### 4.1 Monorepo 与部署形态

```text
memstation/
├── apps/
│   ├── server/   # Go 核心服务
│   └── manager/  # SvelteKit 管理面板
├── .plan/
└── AGENTS.md
```

- `apps/server` 是唯一的业务核心，承担 gateway、api、grpc、worker、scheduler、migrate
- `apps/manager` 独立部署，通过管理接口和 GraphQL 访问 `apps/server`
- Go 服务以单二进制交付，但必须支持不同角色独立启动

### 4.2 服务角色

`memstation` CLI 至少包含以下命令：

- `serve`: HTTP 网关 + REST + GraphQL + gRPC
- `worker`: 异步任务消费
- `scheduler`: 定时任务和 outbox relay
- `migrate`: Atlas 迁移执行
- `seed`: 初始化默认管理员、系统配置、模型能力注册表

### 4.3 包结构规范

推荐的 `apps/server` 包边界：

```text
cmd/memstation/                # CLI 入口
ent/                           # Ent schema + generated code
graph/                         # GraphQL schema + resolvers
internal/app/                  # 应用装配
internal/bootstrap/            # config, logger, telemetry, db, redis
internal/transport/http/       # REST handlers, adapters, middleware
internal/transport/graphql/    # GraphQL wiring
internal/transport/grpc/       # gRPC wiring, interceptors, handlers
internal/service/              # 业务编排
internal/store/sql/            # Ent repositories
internal/store/vector/         # VectorStore 实现
internal/cache/                # L1/L2 cache
internal/provider/             # OpenAI/Anthropic/Gemini 上游适配
internal/worker/               # asynq task handlers
internal/security/             # auth, crypto, rate limit, audit
internal/observability/        # metrics, tracing, logging helpers
internal/modelcap/             # 模型能力注册表
```

约束：

- Handler 只做协议解析和响应格式化，不写业务逻辑
- Service 负责编排，不直接拼 SQL
- Ent/SQL 和 VectorStore 必须通过 store 层暴露
- GraphQL resolver 必须薄，不允许承载复杂业务逻辑

---

## 5. 规范化的数据模型

### 5.1 核心关系实体

v1.0 必须包含以下实体：

- `User`
- `APIKey`
- `UserSetting`
- `Conversation`
- `Message`
- `MemorySummary`
- `MemoryKeyword`
- `Impression`
- `Fact`
- `KeyMemory`
- `TokenUsage`
- `AuditEvent`
- `OutboxEvent`
- `MemoryNode`
- `MemoryRelation`

### 5.2 向量层实体

向量层必须独立于关系图模型：

- 统一抽象名：`MemoryEmbedding`
- 主键建议：`embedding_id`
- 唯一索引：`(user_id, node_id, embedding_version)`

最重要的设计修正：

1. **Ent Schema 不允许根据运行时配置增删字段。**
2. `VectorStore` 是必须项，`pgvector` 只是其中一个实现。
3. 使用 `pgvector` 时，向量数据放在专用向量表中，由 Atlas SQL migration 管理。
4. 图层必须落成统一的 `MemoryNode + MemoryRelation + MemoryEmbedding` 三层模型，而不是依赖分散的隐式多对多边表。
5. Summary / Impression / Fact / KeyMemory 作为业务实体存在，同时一对一映射到统一 `MemoryNode`。

### 5.3 必补字段

当前规划文档缺少或定义不足的字段，v1.0 必须补齐：

| 实体 | 必补字段 |
|------|----------|
| `Conversation` | `request_format`, `upstream_provider`, `mode`, `finalize_status`, `summary_status`, `analysis_status`, `metadata` |
| `Message` | `content_blocks`, `text_content`, `tool_calls`, `tool_result`, `provider_payload`, `sequence_num` |
| `MemoryNode` | `user_id`, `node_type`, `entity_id`, `importance`, `decay_factor`, `embedding_status`, `created_at`, `updated_at` |
| `MemoryRelation` | `user_id`, `src_node_id`, `dst_node_id`, `relation_type`, `weight`, `source`, `created_at` |
| `MemorySummary` | `node_id`, `summary_version`, `source_conversation_id`, `topic_label` |
| `Fact` | `node_id`, `superseded_by_id`, `valid_from`, `valid_to`, `confidence` |
| `KeyMemory` | `node_id`, `source`, `review_status`, `last_confirmed_at` |
| `UserSetting` | `chat_model`, `summary_model`, `analysis_model`, `tool_write_enabled`, `max_injection_tokens` |
| `AuditEvent` | `actor_type`, `actor_id`, `action`, `target_type`, `target_id`, `payload`, `created_at` |
| `OutboxEvent` | `event_type`, `aggregate_type`, `aggregate_id`, `payload`, `status`, `available_at`, `processed_at` |

### 5.4 必须遵守的数据规则

- 所有业务实体必须显式关联 `user_id`
- 所有可人工编辑的数据至少支持 `updated_at`
- 用户可修改或可删除的对象应支持软删除或审计删除
- `MemoryKeyword` 与 `User` 的边关系必须完整定义，不能出现 schema 引用不闭合
- Embedding 版本必须显式化，便于未来重建索引和重嵌入
- 在线图查询必须始终以 `user_id + candidate node set + bounded depth` 为边界，避免无界深度遍历

---

## 6. 代理与记忆链路规范

### 6.1 入站协议

v1.0 必须支持：

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `GET /v1/models`

同时必须提供一套独立 gRPC transport，直接复用统一业务链路，至少覆盖 Gateway、Memory、Admin 和健康检查的正式 RPC。

要求：

- 流式和非流式都必须可用
- 所有协议都要先归一化为内部模型，再走统一业务链路
- 返回格式必须保持原协议兼容，不能把 OpenAI/Anthropic 的输出混成自定义格式

### 6.2 内部统一请求模型

内部模型必须支持：

- 多段 content blocks
- tool definitions
- tool call / tool result
- provider metadata
- stream lifecycle events
- usage 信息

因此，内部模型不能只用“`[]messages + text content`”简化表达。

### 6.3 会话结束与异步处理

会话完成后必须进入以下链路：

1. 写入 Conversation 结束状态
2. 生成 `OutboxEvent`
3. Outbox relay 投递 asynq 任务
4. Summary Worker 产出摘要和关键词
5. Embedding Worker 产出向量
6. Analysis Worker 产出 Impression / Fact / 关联边
7. Key Memory Worker 产出 Key Memory 更新

图落库要求：

- Summary / Impression / Fact / KeyMemory 写入后必须同步创建 `MemoryNode`
- 所有关联边写入统一 `MemoryRelation`
- Embedding 一律挂到 `node_id`
- 事实替代链通过 `relation_type=supersedes` 建模

### 6.4 必须的可靠性规则

- **禁止**“DB commit 后直接 enqueue”作为唯一保障手段
- 必须使用 **outbox pattern + idempotent worker**
- asynq 任务必须带唯一键，避免重复投递和多实例重复消费
- Conversation timeout / explicit end / threshold end 三条链路必须能去重

### 6.5 Analysis Agent 约束

ReAct Agent 可以保留，但必须受控：

- 最大工具轮次：`2-3`
- 必须有 token budget 和超时
- 语义记忆仅由配置的 LLM 生成；禁止关键词、模板、启发式提取或评分兜底。ReAct 如启用，失败只能回退到同样受验证的单轮 LLM 结构化抽取；模型不可用或结果校验失败时任务失败并重试，不生成替代记忆。
- Agent 不允许直接修改核心事实，写入必须走验证层

### 6.6 Tool Injection 规则

- 只读工具可以默认开启：`recall_memory`, `get_key_memory`
- 写工具默认关闭，需用户级显式开关
- 写工具只能写入允许的 Key Memory 白名单键
- 所有工具写入必须记录 `AuditEvent`

---

## 7. 检索、排序与缓存规范

### 7.1 排序

最终排序公式仍使用三要素：

`semantic_score × importance × time_decay`

但要求：

- 先由 `VectorStore` 返回候选 `node_id`
- 再由 SQL/业务层补全实体、图关系和时间衰减
- 在线热路径默认只做 `1-2 hop` 邻域扩展
- 深层图遍历只用于后台分析或管理面板
- 不能把所有排序逻辑耦合在某一个后端的 SQL 方言里

### 7.2 缓存

采用两级缓存，但要明确边界：

- L1：进程内热点缓存
- L2：Redis 分布式缓存
- 向量检索缓存、Key Memory 缓存、用户配置缓存可启用
- TTL 只能作为性能优化，不可承担一致性语义

### 7.3 缓存一致性

- 写入后必须主动失效 L2
- 再通过 Redis Pub/Sub 或 stream 通知各实例清理 L1
- 远程失效消息是**最佳努力**，所以所有缓存必须有 TTL
- 排名分数、Top N 结果必须容忍短时间最终一致

### 7.4 向量模型策略

这是现有规划中的关键修正：

- `pgvector` 的维度不能和“每个用户自定义 embedding 模型”同时无成本成立
- **v1.0 规定 embedding model 是部署级配置，不做 per-user embedding model**
- 更换 embedding model 时，必须触发重嵌入任务和索引切换

---

## 8. 安全、审计与合规

### 8.1 认证模型

- 用户侧通过 MemStation API Key 访问代理接口
- 管理面板通过服务端会话访问管理接口
- gRPC proxy RPC 通过 metadata `authorization: Bearer <MemStation API Key>` 鉴权，gRPC admin RPC 通过 metadata `x-admin-token` 鉴权
- `MEMSTATION_ADMIN_TOKEN` 只用于服务间 bootstrap / 管理登录校验
- **严禁把 admin token 存入浏览器 `localStorage`**

### 8.2 密钥管理

- 用户 API Key：存储 `prefix + hash`，只显示一次明文
- 上游 OpenAI / Anthropic / Gemini 密钥：必须加密存储
- 建议实现：`AES-256-GCM` + 主密钥轮换机制

### 8.3 审计

以下动作必须写 `AuditEvent`：

- 创建 / 删除 API Key
- 修改用户设置
- 人工编辑 / 删除记忆
- Tool 写入记忆
- 管理员登录、登出和敏感配置修改

### 8.4 安全基线

- 代理接口按 `api_key` 和 `user_id` 做速率限制
- 日志禁止输出完整 prompt、完整 token、原始密钥和敏感 PII
- Manager 使用 `httpOnly + secure + sameSite` session cookie
- 对外错误响应不泄露内部堆栈

---

## 9. 可观测性与运行基线

### 9.1 可观测性技术路线

- Traces: OpenTelemetry
- Metrics: OpenTelemetry Metrics，经 OTLP 导出到外部系统
- Logs: `slog` + OTel bridge
- SQL 观测: `otelsql`

关键修正：

- `entgo.io/contrib/entoas` 不是数据库观测方案，不能作为 DB instrumentation
- Ent 查询链路需要通过 `database/sql` instrumentation 和 service span 补齐

### 9.2 核心 SLO

v1.0 默认目标：

| 指标 | 目标 |
|------|------|
| 非流式代理额外开销 P95 | `< 150ms` |
| 流式首包额外开销 P95 | `< 250ms` |
| 记忆检索 P95 | `< 250ms` |
| Summary 任务完成时间 P95 | `< 60s` |
| 分析任务完成时间 P95 | `< 120s` |
| 任务积压恢复 | `15 分钟内恢复到正常水位` |

### 9.3 健康检查

必须拆分：

- `/health/live`
- `/health/ready`

修正：

- 不依赖 NGINX Plus 专有 `health_check` 指令
- Docker Compose、K8s、Nginx OSS 均使用通用健康检查方式

---

## 10. 技术栈与版本基线

以下版本为 **2026-03-13** 的最新稳定基线，用于初始化项目。

### 10.1 后端与基础设施

| 类别 | 依赖 | 版本 |
|------|------|------|
| Go runtime | `go` | `1.26.1` |
| HTTP Router | `github.com/go-chi/chi/v5` | `v5.2.5` |
| gRPC | `google.golang.org/grpc` | `v1.79.2` |
| Protobuf | `google.golang.org/protobuf` | `v1.36.11` |
| ORM | `entgo.io/ent` | `v0.14.5` |
| Ent contrib | `entgo.io/contrib` | `v0.7.0` |
| GraphQL | `github.com/99designs/gqlgen` | `v0.17.88` |
| Queue | `github.com/hibiken/asynq` | `v0.26.0` |
| OpenAI SDK | `github.com/openai/openai-go/v3` | `v3.26.0` |
| Anthropic SDK | `github.com/anthropics/anthropic-sdk-go` | `v1.26.0` |
| Gemini SDK | `google.golang.org/genai` | `v1.50.0` |
| pgvector Go | `github.com/pgvector/pgvector-go` | `v0.3.0` |
| Qdrant Go | `github.com/qdrant/go-client` | `v1.17.1` |
| Milvus Go | `github.com/milvus-io/milvus-sdk-go/v2` | `v2.4.2` |
| OTel Core | `go.opentelemetry.io/otel` | `v1.42.0` |
| OTel slog bridge | `go.opentelemetry.io/contrib/bridges/otelslog` | `v0.17.0` |
| SQL tracing | `github.com/XSAM/otelsql` | `v0.41.0` |
| CLI | `github.com/spf13/cobra` | `v1.10.2` |
| Config | `github.com/spf13/viper` | `v1.21.0` |
| DB migration | `ariga.io/atlas` | `v1.1.0` |
| Redis | `redis:7` | `7.x` |
| PostgreSQL | `postgresql + pgvector` | `16+` |

### 10.2 前端与工具链

| 类别 | 依赖 | 版本 |
|------|------|------|
| Node.js | `node` | `24.11+` |
| Package manager | `pnpm` | `12.3.4` |
| Monorepo | `turbo` | `2.8.16` |
| App framework | `@sveltejs/kit` | `2.54.0` |
| UI runtime | `svelte` | `5.53.11` |
| Adapter | `@sveltejs/adapter-node` | `5.5.4` |
| UI primitives | `bits-ui` | `2.16.3` |
| Component system | `shadcn-svelte` | `1.1.1` |
| Styling | `tailwindcss` | `4.2.1` |
| Type system | `typescript` | `5.9.3` |
| Charting | `echarts` | `6.0.0` |
| Lint | `eslint` | `10.0.3` |

### 10.3 当前依赖基线（2026-10-23）

以上初始化表保留为历史背景，当前以 manifest / lockfile 与 升级说明（本地归档：`.agents/archive/docs/ui-dependencies-2026-10-23.md`） 为准：Go `1.27.3`、Node `26.12.1`、pnpm `12.15.0`、SvelteKit `3.1.0`、Svelte `5.57.2`、Bits UI `2.20.2`、adapter-node `6.0.0`、Vite `8.3.5`、Vitest `5.0.5`、TypeScript `6.0.3`。TypeScript 保持 `6.x`；其他依赖升级跟进官方 breaking changes，Atlas CE lint 工具等兼容性例外需在升级说明记录。

### 10.4 版本策略

- `go.mod`, `package.json`, Docker image 都必须写明确版本
- 禁止 `latest`
- 依赖升级必须附变更说明、兼容性评估和回归测试结果

---

## 11. 管理面板规范

### 11.1 功能范围

管理面板必须覆盖：

- Dashboard
- Conversations list/detail
- Memory list/detail/edit
- Key Memory CRUD
- User list/detail
- Token usage 与成本统计
- 系统配置与模型配置
- 健康、队列、缓存、任务状态

### 11.2 交互与鉴权

- Manager 使用服务端 session，不在浏览器保存 admin token
- 所有数据优先在 `+page.server.ts` 或 API routes 中获取
- 前端只消费最小必要数据，不直接暴露内部管理密钥

### 11.3 视觉规范

当前 `.plan/frontend-panel.md` 的配色过于默认化，v1.0 统一为：

- **专业、明亮、信息密度高的 ToB 风格**
- 默认浅色主题，暗色作为补充，不做暗色优先
- 不使用“紫色 + 默认深色”的泛化 AI 控制台风格
- 当前视觉采用留出边距的悬浮侧栏和粘性顶部工具栏，统一 14–18px 内容圆角、9px 表单控件和克制阴影；浅色画布与白色内容面建立层次。区块按任务组织，普通记录仍用行与分隔线，不以渐变、发光或悬停位移制造质感。移动导航保留焦点陷阱、触控尺寸和安全区域。
- 顶部 navbar、连接提示和内容容器共用 `--page-max-width` 与居中规则，由 `.app-main` 统一提供外侧边距；页面不得重复添加横向 padding 或独立收窄根节点。普通共享面板与区块间距通过 `--panel-padding` / `--section-gap` 调整，桌面和手机分别取值；表格 flush 布局和紧凑内部行按语义保留。
- 字体建议：`IBM Plex Sans` 或 `Geist`，代码字体 `JetBrains Mono`
- 组件风格强调监控、数据密度、运维信息可读性
- 禁止用圆点字符拼接 UI 文本；并列元数据使用 `MetadataRow` 的独立文本项与留白，说明使用自然语句。避免用另一种分隔符机械替代所有圆点；不增加装饰性图标、标签或卡片。
- 交互动线优先就地完成：搜索与提交对齐、当前项不重复导航，切换上下文保留适用栏目和筛选；离开有未保存内容的页面先保护草稿。工作区服务器搜索使用基于 Bits UI Popover 的 `WorkspacePicker`，普通表单选择仍统一使用 `SelectField`。
- 避免装饰性状态灯、渐变、发光、悬浮位移动效及重复胶囊标签；常规状态和元数据用文字呈现，填色提示用于失败、重试、历史事实等需要注意的状态
- 各端优先展示搜索、结果和主要操作，次要筛选按需展开并保留所选条件；不重复展示导航已有入口、无操作意义的正常状态或原始记录
- 同类按钮、输入框、表单和记录列表使用共享尺寸与间距；普通记录用分隔线组织，避免卡片层层嵌套。高频编辑内容优先，运行元数据和原始 JSON 按需展开；关系视图必须有可读标识与真实跳转入口
- 折叠区域统一使用基于 Bits UI 的 `CollapsibleSection`；选择器统一使用 `SelectField`，输入使用 `TextInputField` / `TextareaField`，普通操作按钮使用基于 Bits UI Button 的 `Button`。有对应 Bits UI primitive 时优先复用，禁止各页面重复实现折叠、Select 或原始表单控件；保持紧凑布局、键盘可用、草稿保留及 reduced-motion 支持。
- 筛选工具栏通过 `FilterToolbar` 复用 `CollapsibleSection`，禁止页面自行使用原生 `details/summary` 或重复展开样式。收起后必须保留表单值，隐藏区域校验失败时自动展开并定位；设计及属性见 `docs/manager-components.md`
- 移动端主要控件保持至少 44px 触控高度，文本输入避免 iOS 聚焦缩放；短屏弹窗可滚动，保存操作不得遮住最后一组字段，横向栏目切换应露出当前项

---

## 12. 开发规范

### 12.1 代码生成

- Ent schema 和生成代码必须同步提交
- gqlgen schema、resolver 生成代码必须同步提交
- 生产环境只允许使用版本化迁移，不允许运行时自动建表

### 12.2 测试矩阵

v1.0 需要以下测试层级：

- 单元测试：纯业务逻辑、排序、解析、校验、加密
- 集成测试：PostgreSQL、Redis、VectorStore、Ent migration
- 协议契约测试：OpenAI Chat/Responses、Anthropic Messages
- 端到端测试：Manager 登录、对话浏览、记忆检索、配置修改
- Benchmark smoke：repo-native golden dataset 的 retrieval/ranking smoke、LOCOMO adapter smoke；仅作为本地 / 手动 / 专用评测入口，不作为 CI 红线
- 外部 benchmark：LOCOMO / LongMemEval benchmark、ranking ablation、benchmark artifact 导出；不作为默认 CI / nightly 判定依据
- 压测：SSE 流式代理、并发记忆检索、Worker 堆积恢复
- 备份恢复演练：PostgreSQL 逻辑备份、Redis 快照恢复、计数校验与 artifact 导出

### 12.3 记忆评测与 Benchmark 基线

记忆 benchmark 是 v1.0 必做项，不是可选优化，也不属于 v1.1 才考虑的附加工作。但 benchmark 结果在当前阶段只作为观察、诊断和回归分析信号，不作为 CI / nightly 红线门禁。

CI / nightly 默认不执行 retrieval/ranking benchmark，不用 Recall、nDCG、stale fact rate 或 benchmark P95 判定红绿。评测脚本保留给本地、手动或专用评测环境；如确需临时启用硬门禁，必须显式设置 `MEMSTATION_ENFORCE_BENCHMARKS=true`，性能阈值还必须显式设置 `MEMSTATION_PERF_P95_MS`。

Benchmark 基线固定采用 **LOCOMO-first 方法论 + 混合数据集落地**：

- `MemStation-native golden dataset`：仓库内置、可分发、默认本地 smoke 使用
- `LOCOMO external benchmark`：外部下载、不可默认提交进仓库、用于本地 / 手动 / 专用评测环境

数据集与分发策略固定为：

- LOCOMO 可作为 v1 benchmark 的参考基线
- 截至 `2026-03-15`，LOCOMO 上游公开数据许可证为 `CC BY-NC 4.0`
- 因此原始 LOCOMO 数据不得默认提交到 MemStation 仓库
- 仓库只提交 adapter、schema、导入脚本、说明文档，不提交原始 LOCOMO 数据文件
- `MemStation-native golden dataset` 是默认本地 smoke 数据源，`LOCOMO external benchmark` 是本地 / 手动 / 专用评测增强基线

首期 benchmark 范围固定为 **召回 + 排序**，不把 answer quality / LLM judge 作为第一阶段门禁。

首期指标至少包含：

- `Recall@1/3/5/10`
- `Precision@3/5`
- `Hit@1/3/5`
- `MRR`
- `nDCG@5/10`
- `wrong-user leakage`
- `superseded/stale fact top-k rate`
- `relation expansion gain`

首期必须做 ranking ablation：

- `semantic_only`
- `semantic_importance`
- `full_no_relations`
- `full`

实现约束：

- benchmark 主执行逻辑以 Go 实现
- 目录固定为 `apps/server/tests/eval`
- 编排脚本固定放 `scripts/`
- 不新增 `memstation` 公共 CLI 子命令
- benchmark 必须基于现有 `MemoryEngine`、`VectorStore`、`MemoryRepository`
- 评测输出必须反映线上 `semantic_score * importance * time_decay` 和 relation expansion，而不是平行实现另一套排序逻辑

每次 benchmark 都必须输出机器可读 artifact，至少包含：

- query 结果明细
- score breakdown
- aggregate report
- failures / regressions 样例

### 12.4 CI 基线

v1.0 完整 CI 至少执行下列门禁；当前仓库如未全部补齐，也必须以后续实现将其补齐，不得降级：

1. `bash ./scripts/go/format.sh --check`
2. `go test ./...`
3. `golangci-lint run ./...`
4. `go generate ./...` 后执行 `git diff --exit-code`，不允许使用 `|| true`
5. `pnpm lint`
6. `pnpm test`
7. manager Playwright E2E
8. Atlas migration lint / dry-run
9. backup/restore drill dry-run

CI 基线不包含 retrieval/ranking benchmark、不执行 LOCOMO / LongMemEval benchmark，也不对 benchmark 指标设置红线。

CI 中的 artifact / report upload 只作为诊断辅助步骤，必须使用短保留策略，且不得因为 GitHub artifact storage quota 等平台配额问题覆盖前面已经完成的正确性门禁结果。禁止把 `go test`、format、lint、generated diff、Atlas、contract、coverage、Playwright、backup/restore dry-run 等正确性门禁降级为非阻塞。

### 12.5 Nightly 基线

nightly 回归不是可选项，v1.0 必须预留并最终落地，但 nightly CI 默认聚焦运维和可靠性回归，不执行 retrieval/ranking benchmark 红线：

1. worker backlog / 恢复能力回归
2. backup/restore drill 与恢复 artifact
3. 任务积压、恢复耗时、备份恢复结果的机器可读 artifact

LOCOMO full benchmark、ranking ablation、模型与记忆排序变更前后的 benchmark 对比，保留在本地 / 手动 / 专用评测环境运行，不作为 GitHub CI / nightly 的红绿判定。

Nightly 的 worker recovery drill 与 backup/restore drill 必须保持阻塞；诊断 artifact 上传可以 best-effort，并使用短保留时间，避免 GitHub artifact storage quota 已满时把已经通过的 nightly drill 判定为失败。当前 GitHub nightly workflow 已移除，可靠性 drill 通过 `scripts/ops/` 下的脚本按需手动运行；若重新落地定时 nightly，应先检查同分支上一条已完成 nightly 的 `head_sha` 和 `conclusion`，只有同提交且上次成功才跳过重型 drill，失败不能算作已覆盖；手动 `workflow_dispatch` 必须保留强制运行能力。

### 12.6 代码质量规则

- 所有异步任务必须可重试、可幂等
- 所有外部调用都必须带超时、重试策略和 metrics
- 所有跨包 DTO 必须显式定义，禁止在 handler 中直接传 Ent entity 到前端
- 所有 provider adapter 必须有 contract tests
- Go 代码格式必须统一通过 `goimports + gofumpt`，并以 `scripts/go/format.sh` 为唯一仓库级入口
- Go lint 必须统一通过 `golangci-lint`，并以 `scripts/go/lint.sh` 为唯一仓库级入口

### 12.7 目录组织与职责边界

- 按业务职责和稳定依赖组织目录；新增文件前先检查同类代码归属，禁止持续向公共目录平铺不相关的实现，也不按每个小函数机械建目录。
- `internal/store/sql` 是分类入口，不承载仓库实现或旧路径转发。会话与事务 outbox 放 `conversation/`，身份与工作区放 `identity/`，记忆读写与图检索放 `memory/`，媒体放 `media/`，独立事件接入放 `ingestion/`，发布、擦除、导出等生命周期操作放 `lifecycle/`，AI 调用账本放 `aicall/`，代际处理放 `memorygeneration/`。
- 仅被 SQL 仓库复用的基础代码放 `sql/internal/`，按数据库操作、记录元数据和作用域职责细分；不允许把业务服务或其他模块的逻辑塞入通用 helper 包。跨仓库的事务操作明确接收同一 `*sql.Tx`，拆目录不得破坏锁顺序、权限校验、原子提交或 outbox 保障。
- Go 子目录必须对应真实、无循环依赖的 package。测试紧邻所属包；跨职责契约放独立 integration 测试，复用夹具放内部测试支持包，不能为共享测试而暴露仓库私有字段。
- Manager 通用组件按 `ui/`、`layout/`、`content/`、`charts/` 分类，业务组件按 `memory/`、`agents/`、`workspaces/`、`settings/`、`targets/`、`chat/` 分类；基础组件不依赖页面或业务组件。使用项目的 `#lib/` 导入，避免深层相对路径和无用途的聚合转发。
- 同样检查其他目录：不同职责混放时按模块整理；职责单一的 domain、worker 或 service 包不为减少文件数而强行拆分。生成代码遵循生成器布局，不手工搬动。
- 每次迁移必须同步更新调用方、测试、运维脚本、文档和路径配置，删除旧路径、兼容别名及重复实现；完成编译、lint 和受影响契约回归后才可声称整理完成。

### 12.8 提交规范

- Git commit message 必须遵循 `type: message` 形式，`type` 使用小写英文前缀，冒号后必须保留一个空格
- 当前项目约定的常用前缀至少包括：`fix`、`feat`、`init`、`chore`；如确有必要可扩展 `docs`、`refactor`、`test` 等语义化前缀
- 根初始化、仓库基线落地、首次导入大规模结构时优先使用 `init: ...`
- 新增用户可感知能力、接口、页面、链路时优先使用 `feat: ...`
- 修复缺陷、回归、数据一致性或可靠性问题时优先使用 `fix: ...`
- 不影响用户能力的工程清理、脚本、依赖、配置、门禁调整时优先使用 `chore: ...`
- 禁止使用不带前缀的提交信息，例如 `update`, `misc`, `wip`, `tmp`
- 单个 commit 应尽量聚焦单一主题；如同时包含功能、文档和质量门禁，应优先按主语义选择前缀，而不是堆叠多个类型

---

## 13. 开发 roadmap

下列里程碑全部完成后，才算 v1.0 完整版本。

| 里程碑 | 目标 | 完成标准 |
|--------|------|----------|
| R1 Foundation | Monorepo、配置系统、CLI、Telemetry、CI、DevContainer、Compose | 本地和 CI 可一键启动、测试和迁移 |
| R2 Data Plane | Ent schema、Atlas migration、Outbox、Audit、VectorStore 抽象 | PostgreSQL/Redis/pgvector 路径可用，数据模型闭环 |
| R3 Gateway Plane | 三种入站协议、三家上游适配、SSE、API key、usage 统计 | OpenAI Chat/Responses、Anthropic Messages 均通过契约测试 |
| R4 Memory Plane | Summary、Keyword、Embedding、Impression、Fact、Key Memory、排序注入 | 对话结束后记忆全链路自动完成且可检索 |
| R5 Admin Plane | GraphQL、Manager、统计、配置、审计、人工修正 | 管理面板可覆盖运营和排障主流程 |
| R5.5 Benchmark & Eval | `MemStation-native golden dataset`、`LOCOMO adapter`、retrieval/ranking benchmark runner、本地 / 手动 / 专用评测入口 | repo 内置 benchmark 与 LOCOMO benchmark 可在本地或专用评测环境跑通，recall/ranking 指标与失败样例可导出 artifact，score breakdown 可追踪到 `semantic_score / importance / time_decay / relation expansion`；默认 CI / nightly 不以 benchmark 指标判红绿 |
| R6 Hardening | 限流、加密、缓存、健康检查、备份恢复、压测、K8s 基线 | 达到 SLO、具备生产交付条件 |

v1.x 后续路线：

- 多组织 / 多租户
- 资源配额和账单
- 向量后端切换和双写迁移
- 更强的知识图谱分析与自动标签

---

## 14. 当前实现状态（as of 2026-10-27）

本节用于重启时快速恢复上下文；如后续实现进度发生变化，必须同步更新本节，而不能只改代码不改状态。

### 14.1 已完成

- 2026-10-27 综合基线与提交前复核：已核对 LOCOMO 单开发对话 152 题 raw／derived 各 114/152、combined 107/152、全文对照 128/152，LongMemEval 七类覆盖 37/42，MemoryArena low 数学 94/354、物理 46/86；这些完成范围不等于最新代码、全量数据集或理论上限成绩。35 题真实 Worker 混合集仍为 48/140 条完成答案，按成本要求停止，不补跑模型对照或付费诊断。复核修复预算重试失败／取消时丢失首次用量、空响应被当成功，以及证据上下文暴露存储坐标／附件原始字段的问题；Summary／Analysis／Key Memory 原始来源与事实 ID 在模型边界使用请求内临时编号，返回后恢复再校验，原始追溯数据不改。最终独立 PostgreSQL／Redis 全量 race 为 687 项测试／子测试通过、18 项显式 opt-in 跳过、无失败；Chromium／移动 WebKit 134 项 E2E、Node 26.12.1 下 pnpm lint／test／production build、Go format／lint、完整 go generate 幂等检查、Atlas lint／dry-run、备份 dry-run、Worker recovery 和 12 项 Python 测试通过。公开增量密钥扫描无真实凭据，验证材料留在忽略归档。完整范围、已知准确率及延迟缺口见被忽略的 `.agents/archive/review-push-2026-10-27/baseline.md`；提交推送与远端 CI 结果以该归档的最终记录为准，不把旧评测分数称为本次修复收益。

- 2026-10-27 成本与媒体输入：MemoryArena none 只作为模型对照，后续复用完整 low 基线，不以改善该组为优化目标；重复评测／自动精排及全文阅读追加调用已停，保留当前 48 条完成答案、274 段 Worker 结果及独立数据库备份。小集 runner 增加固定 SHA 与模型身份校验的 Arena 基线复用，缺失／不匹配时在模型调用前失败，原有失败原样保留；历史用量不计入本轮，预算／提示词不同不能作同预算因果对照。新增部署级 `MEMSTATION_MODEL_INPUT_MODALITIES`，按精确 provider:model 和传输格式决定原生媒体或文本省略标记，接通 gateway 流式／非流式、Summary／Fact、broker 及群聊 REST／gRPC 事件；媒体推断独立记录为 media_observation，禁止把标记或协议 JSON 当用户原话／caption。模型元数据确认本机当前 DeepSeek 声明 vision，私有 Ollama 配置启用图片；音频未声明支持。真实图片 Fact 冒烟单次生成通过（1,554 输入／1,197 输出 tokens，无 judge）；全量独立 PostgreSQL race 677 项通过、20 项跳过，协议与默认文本／媒体边界、12 项 Python 测试、Go lint／format 通过。首次 race 复用了带旧模拟端口 manifest 的测试库而被正确拒绝，保留失败后使用三个全新独立测试库通过。Manager 来源弹窗补齐媒体元数据展示，lint／check、35 项单测（1 跳过）及 Chromium／手机 WebKit 两项来源查看 E2E 通过，截图已检查。协议见 `docs/multimodal-input.md`；不把冻结文本轨道改称视觉成绩，未提交推送。

- 2026-10-27 Token 预算与真实 Worker 小集评测：去除大预算请求被静默重置为 4K 的行为，记忆 LLM 部署配置默认为初始 16K、显式截断后同输入一次扩到 64K、独立 300 秒期限；推理策略与预算进入 checkpoint identity，broker 生成期限独立于 embedding／精排。补齐 Summary、Fact／Impression／Item 的完整校验后一次 LLM 纠错，拒绝错误引用、非法时间和 assistant 事实证据，不接受或部分持久化被拒结果；原始会话日期保留到评测上下文。固定 LOCOMO 10、LongMemEval 7、MemoryArena 六条完整短任务链共 18 子题的 35 题开发集，四组 none／raw／derived／combined 共 140 条结果（后续 Arena none 复用原控制组）；全部原始历史经独立 PostgreSQL、真实 finalize／outbox／Summary／Analysis／Embedding handlers，不是只摄取答案会话。修复手动评测器跨请求作用域调度及跨用户失败传播，真实并发与失败隔离契约、独立 PostgreSQL 全量 race（657 项测试／子测试通过、17 项跳过）、Go lint／format、9 项 Python 测试通过，未关闭生产权限校验。首批调度失败及第二批真实校验错误／中断完整封存、保留 140 固定分母，不择优混分；修复后全批在成本策略变更时停止，自动静态 4B／0.6B／TopN=20 对照及全文阅读诊断取消，当前结果未完成，不宣称准确率提升或在线 SLO 达标。逐阶段检索耗时、来源覆盖、用量、任务尝试与全部错误留在被忽略的 `.agents/archive/memory-regression-2026-10-27/`；协议见 `apps/server/tests/eval/REGRESSION.md`，未提交推送。

- 2026-10-27 目录职责整理：`store/sql` 根层 65 个平铺文件按会话／事务 outbox、身份与工作区、记忆、媒体、事件接入、生命周期、AI 账本分包，沿用 memorygeneration；SQL 内部数据库／记录／作用域／夹具归入 internal，根层不留实现或兼容转发。调用方、跨仓库事务入口、测试及两个运维脚本同步迁移；Manager 29 个组件归入基础 UI、布局、内容、图表和业务目录，修正 `#lib` 导入及 ESLint 路径规则，组件正文／样式不变。Go 原有 619 个函数无遗漏，生产函数归一化对照无行为变化；独立 PostgreSQL 16.14 全量 race（639 项测试／子测试通过、16 项跳过）、Go lint／format、真实删除日志备份恢复、Manager lint／check／35 项单测（另 1 跳过）／production build 与 Chromium／移动 WebKit 134 项 E2E 通过。首次恢复使用主机 PostgreSQL 17 客户端触发 transaction_timeout 不兼容，保留失败记录后用匹配的 16 客户端完整通过，未绕过错误。第 12.7 节新增全仓目录组织规范；验证和原始源码留在被忽略的 `.agents/archive/store-organization-2026-10-27/`。low 评测二进制／787 份冻结源码独立保留，不受目录调整影响；未提交推送。


- 2026-10-27 外部评测接入（首轮覆盖评测完成）：新增显式手动 Go runner，复用真实 MemoryEngine／VectorStore、本地 embedding 和 llama.cpp 精排，隔离内存仓库，不连接用户数据库；提供固定数据 revision／hash 的下载与分层选样脚本、全预期分母／失败明细／官方会话 recall_any/all 与任务链宏微指标汇总。用户选择首轮 LongMemEval-S V1 七组各 6 题及 MemoryArena 完整数学 40 任务／354 子任务、物理 20 任务／86 子任务，各做独立无记忆对照，共 964 次回答；原始会话／自身既往问题与解答作为记忆，不运行摘要／Fact Worker，参考答案和 judge 反馈不进入记忆，后续背景不提前泄漏。LongMemEval 已完整判分：有记忆 37/42、无记忆 6/42，均无执行错误；36 道可回答题中官方 recall_all@10 为 35/36，进入 64,000 字节上下文后完整保留为 33/36。偏好题 3/6、跨会话汇总与时间排序各 5/6，其余组 6/6；检索 P95 含本地单槽排队约 132.5 秒，不满足在线 SLO，分层小样本不代表全量 500 题或生产摄取效果。首次并发精排超时批次完整归档后，固定题目／模型／预算／判分协议新开全批次，评测侧精排串行、推理期限 180 秒；只有 embedding 计算缓存复用。用户随后选择 low：high 批次停止并按完整文件哈希封存，已完成的 LongMemEval 不重跑；MemoryArena 另起全新 low 批次，完整 60 任务、880 次有／无记忆回答，不续接旧答案、不混分，只有 embedding 计算缓存复用。low 批次已完成全部 60 任务／880 次回答，无待执行或中断记录，约 3 小时 24 分钟。数学有／无记忆答对 94/354（26.55%）／76/354（21.47%），任务最后一步成功 8/40／3/40，平均任务完成比例 28.85%／22.75%；物理为 46/86（53.49%）／24/86（27.91%），任务最后一步成功 10/20／3/20，平均任务完成比例 59.38%／31.83%。248/880 次回答在统一 16,384 最终输出上限耗尽，均保留为执行失败；其中有记忆 111/440、无记忆 137/440。无检索／写入／判分执行错误，但输出耗尽和推理错误说明整体效果仍未达到目标；生产调用器的初始 8,192→截断后同输入一次扩到 16,384 规则两组一致、用量累计，非按判分重试。当前 Go 全量默认测试、eval race、lint、分母／隔离／顺序／缓存／取消边界与汇总指标测试通过；正式协议见 `apps/server/tests/eval/EXTERNAL-MEMORY.md`，high 明细／LongMemEval 结果在被忽略的 `.agents/archive/memory-benchmarks-2026-10-27/`，新批次在 `.agents/archive/memoryarena-low-2026-10-27/`。仅宣称首轮数学／物理覆盖批次完成，不宣称 MemoryArena 全部环境、官方基线复现或整体质量验收，未改默认服务模型、未提交推送。

- 2026-10-27 零净收益深入排查：确认旧选择器平均丢弃初始 20 条中的约 16 条，后续补查平均实际新增选中记录不足 1 条；新增有界、作用域过滤后的精排候选池与显式 EvidenceSeed，保留初始证据，补查三池最多 150 条、中间选择 32KB、最终上下文 16KB，公开 Search TopN 不变。初始候选扩充完整对照为 31/44→31/44、9/16→9/16、12/16→11/16，未证明净质量收益；引用账本阅读也未证明收益，额外补查及注入候选扩充均不提升为默认。发现问答强制 JSON 将 GLM 35/76 个回答记为格式失败，普通 QA 统一为文本答案，真实结构化规划/抽取/判分仍严格校验；新增部分失败原始输出追踪、显式 temperature（含零值）及多协议流式契约、连接故障熔断。Ollama 自动升级中断的首批完整保留为基础设施失败，恢复后独立整批重跑；相同冻结上下文、两种阅读模型均 high/T0、相同 judge 的 304 个问答/空记忆组合全部完成且无执行错误：DeepSeek 分组 34/44、9/16、12/16，共 55/76；GLM 为 33/44、9/16、11/16，共 53/76；两模型空记忆各 0/76。当前仅为重复使用的开发/回归组固定上下文诊断，首组 combined、另两组 raw，不能视为全量 LOCOMO、纯召回提升、独立判分或理论上限；零空记忆命中不证明无预训练污染。跨轮证据完整性、事件身份与计数、计划/完成、来源指代和图片缺失仍待解决，未更改默认服务模型。最终源码完整 PostgreSQL race、Go lint/format、协议与边界回归和 784 文件冻结指纹审计通过；原始数据未改，报告及私有输出留在被忽略的 `.agents/archive/locomo-plain-reader-2026-10-27/`，未提交推送。

- 2026-10-26 无泄漏证据补查与受控对照：显式 `RefineEvidence` 改为仅问题规划、1–2 次保留工作区/私有分区/代次与时间过滤的补查，再由 LLM 选择互补证据；移除基于窄候选集的 sufficient 提前结束及旧实验实现。共享证据格式化在预算计算前替换持久化标识，真实来源映射留在模型输入之外；补齐空库、跨作用域、无代次 Key Memory、完整记录预算和选择合法性回归，规划/筛选 token 用量累计。固定 44 题开发组和另一段对话 16 题，Top20/16KB、相同模型与判分协议执行两轮完整对照；第一轮 33/44→35/44、9/16→9/16（增强组含 1 个输出预算错误），当前仅问题规划版 31/44→31/44、9/16→9/16，120 个题目/方案组合均执行成功，开发组新增答对 4 题同时回退 4 题。当前增强证据准备 P95 约 36.4/40.2 秒，不能视为质量或性能验收，不改默认网关路径。独立纯文本全原文诊断为 37/44（含 1 个执行错误）与 9/16，仅问题均为 0；这些结果为同模型家族、已使用过的小样本诊断，不是全量 LOCOMO、独立评判或理论上限。已确认选择丢失跨事件证据、证据齐全但阅读遗漏、计划/完成与计数归并歧义，以及仅图片含关键细节的问题；标注引用命中不等于语义证据完整，所有失败保留分母。最终源码完整 PostgreSQL race、Go lint/format 与边界审计通过；实验、来源哈希、失败明细和限制保存在被忽略的 `.agents/archive/locomo-aspect-evidence-2026-10-26/` 与 `.agents/archive/locomo-query-coverage-2026-10-26/`，禁止按题定制、泄漏标注或把分轨诊断混入正式分数。

- 2026-10-26 当前契约复核与失败题重测：复现并修复 LOCOMO raw 原文邻居路径遗漏显式 Agent/工作区而返回 access denied，新增回归；完整 PostgreSQL race、Go lint/format 与 diff 检查通过。原评测库只读导出至隔离快照，58 张表逐表计数/行哈希一致，重测连接强制只读；固定既有抽取、文档向量和关系，以当前引擎重新检索、精排、问答和评判。conv-26 的 122 个旧失败题目/模式组合（64 道题）恢复 32 个，分 raw 4/38、derived 8/38、combined 20/46；最近 10 个多跳错题恢复 1 个，12 个成功对照保住 11 个，出现 1 个 derived 回退。134 个组合均完成判分、精排正常，无执行错误；这是失败子集和同模型家族自评，不是全量准确率，也未重跑抽取。另两题独立诊断在相同 Top20/16KB 预算下启用邻居半径 2 后均正确，未改默认配置。候选池遗漏、跨轮证据覆盖及纯文本缺失图片细节仍有缺口；所选子集检索 P95 raw/derived/combined 约 2.98/3.25/4.10 秒，质量和 SLO 尚未验收。明细见 `.agents/archive/locomo-failure-replay-2026-10-26/`；原数据及历史成绩未改动，未提交推送。

- 2026-10-26 当前契约清理：项目不保留 legacy 或向后兼容实现。排序改为唯一的相关性优先契约，移除策略切换和混合权重，失败时使用同一套有界混合召回信号；移除 Legacy agent 标识、开发 Key 与旧 Key 的隐式身份补齐、旧 GraphQL Key mutation、旧任务格式、旧 Blob 目录读取、缺失来源基准/快照作用域的推断和升级专用删除日志开关；Provider、模型引用与记忆类型只接受当前规范写法。Manager 改为结构化工作区导航，删除旧路径转换与 manager_target 桥接，凭据必须显式选择 Agent。生成代和记录标识统一包含工作区，修正关系写入作用域、实际激活代选择和按来源会话清理片段；Key Memory 图任务补齐 node_id，向量删除任务必须携带版本条件并校验节点状态，防止旧任务删除重建后的向量。迁移仅支持空数据库初始化，删除 39 条旧数据转换/修复语句并重算校验。完整 PostgreSQL 测试与 race、Go format/lint、340 份生成代码重生成一致性、Atlas lint/dry-run（34 migrations / 291 statements）、Manager lint/check/build、35 项单测（另 1 项跳过）及 Chromium/移动 WebKit 134 项 E2E 通过；备份恢复仅验证 dry-run，未执行完整容器恢复演练。已有运行数据和评测快照未改动，明细与历史比较来源留在 `.agents/archive/no-legacy-2026-10-26/`；此前双策略实验不代表当前代码的 LOCOMO 准确率，未提交推送。

- 2026-10-26 Mimic 实际适配联调：mention 支持现有 Author 的 Integration 外部身份解析并校验与显式 actor_id 一致，capabilities 声明 external_actor_mentions；修复 records 路由的不透明 Node ID 恰好一次 URL 解码。实际 Mimic TypeScript transport、受限 broker 和统一 AI Runtime 已与独立 PostgreSQL/Redis/MemStation HTTP/worker 跑通双群同名、首次 mention、水位、授权召回、跨群 ID 拒绝与来源删除；确定性模型四角色 20 次上游请求对应 20 条唯一 Mimic 用量。Go 全量、相关 lint、format 和显式真实依赖契约 173 项测试/子测试通过。未修复 broker 客户端缺失元数据严格校验与 idle append 事务边界，未调用真实付费模型/QQ，不称生产质量/容量或备份恢复联合验收完成。详见本地归档 `.agents/archive/docs/mimic-adapter-2026-10-26.md`；正式接入契约已同步。

- 2026-10-26 LOCOMO 架构与排序诊断：只读专用数据库、冻结候选与时钟，逐分数/顺序/上下文完整复现 456 条 control；混合轨道有标注题的引用覆盖在精排池为 92.72%，最终打包降为 74.22%，提高精排权重并去除会话加分/重要性倍率的诊断策略为 90.61%（非回答准确率）。全部 32 道混合多跳题重新配对问答，现有排序 11/32、实验排序 20/32，10 改善/1 回退，64 次均完成且无执行错误；仍限同一开发对话、同模型自评，未切换默认排序或宣称全量成绩。确认该代实体/已确认别名均为零、图贡献未进入精排池，补充固定版本 Hindsight/EverOS/Graphiti/Mem0 实现对照及分阶段验收。修复 Episode 注入落入最低未知类型优先级的问题，保留 Key Memory 优先，预算回归修改前失败、修改后通过；该修复不属于上述 QA 路径。Go 全量、memory/eval race、format 与专项 lint（0 issues）通过。诊断、来源指纹与可执行复现见本地归档 `.agents/archive/locomo-architecture-2026-10-26/report.md`，逐题私有 artifact 保留。

- 2026-10-26 文档归档整理：验证/复核 MD、评测 JSON、计划与原型、验证截图和品牌生成记录迁入 `.agents/archive/`，保留逐文件来源及哈希清单；Git/Docker 明确排除本地归档。独立记忆服务契约提为 `docs/memory-service-contract.md`，正式指南保留配置、接口和复现方法，历史执行结果单独留档；README 与评测说明移除本地归档链接，AGENTS/本地计划入口更新路径。公开文档 105 处、归档 148 处本地链接全部通过，JSON/图片/静态素材哈希和原型依赖路径检查通过；72 份归档均被忽略且未被跟踪。本轮仅文档与归档，不改产品逻辑或删除私有原始评测数据。

- 2026-10-26 LOCOMO 扩展回放：最终代码固定二进制在全新专用 PostgreSQL 重新处理 conv-26 全部 19 个会话，383 条派生文档均保留原始会话日期；602 次 Worker 尝试有两次失败后恢复，无最终摄取失败。全部 152 道 category 1–4 问题的 raw / derived / combined 自评为 114/152、114/152、107/152，五条回答格式失败计入分母；另执行完整 92,397 字节文本对照为 128/152，含两条执行失败。确认某正确 episode 精排第一却被融合排序挤到 23/28 位；全体中 reranker 第一名被排除 Top 20 的 derived / combined 分别 24/41 次，不等于均为错题。检索 P95 约 2.97/3.81/5.90 秒，未达 SLO。Go 全量、format、lint 通过；本轮仅评测与文档，未改产品默认值、未提交推送部署，未称全量 LOCOMO 或独立保留集。格式恢复/子调用追踪、融合排序、跨会话关联和十对话验证仍待处理，详见 扩展评测（本地归档：`.agents/archive/docs/locomo-expanded-2026-10-26.md`）。

- 2026-10-26 System 末尾模块选择：修复多个短模块共享最大滚动位置时 Database connection 被强制高亮为 Overrides 的问题。明确锚点选择在目标可见时优先保留，正文滚动操作恢复可见区跟随；异步表单挂载后补做有效初始 hash 定位。修改前复现旧缺陷，修改后 25 项 Chromium/WebKit 相关回归、新场景 6 次重复、lint（0 warnings）、35 项单测、production build 和 diff 检查通过。验证包含键盘、表单字段上开始滚动、历史、视口变化和手机深链接，使用 Manager/HTTP fixture，见 滚动修复记录（本地归档：`.agents/archive/docs/plans/workspace-agents/manager-scroll-2026-10-26.md`）。

- 2026-10-26 System 顶部与平滑切换：表单通过 overflow: clip 修复遮罩越过圆角的方形白边，遮罩从 18px 扩为 36px 且仅在内容滚入吸顶区域后显示。页面范围内启用原生锚点平滑滚动，保留键盘、重复点击与返回历史，减少动态效果时即时定位。23 项 Chromium/WebKit 相关回归、lint（0 warnings）、35 项单测、production build 和 diff 检查通过；实际滚动中间位置与 reduced-motion 即时定位有 artifact。使用 Manager/HTTP fixture，见 滚动修复记录（本地归档：`.agents/archive/docs/plans/workspace-agents/manager-scroll-2026-10-26.md`）。

- 2026-10-26 LOCOMO 失败恢复与来源时钟：确认 Ollama 模型默认 high thinking 未被内部请求控制，新增显式 reasoning effort、受控格式/引用纠正、逐批持久化检查点、首个结果并发协调、来源修订和写入策略隔离；内部批次元数据不向 API 暴露。专用数据库的 conv-26 全十九个 session 完成，阶段报告含 84 episode、123 Fact，原始 v2 自评 raw 3/4、derived / combined 各 1/4，证明运行成功不等于质量提高。补齐显式来源 session date 的写入、证据包与 Manager 展示，修正共用引用序号映射；冻结候选的日期对照恢复时间题，但总分未净增。相同日期上下文的独立 reader 配对实验，raw 3/4→4/4，derived / combined 各 2/4→3/4，均为四道固定问题、同模型严格 v3 自评，不能外推全量或归为召回增益。保留非 JSON 观察文件导致中止的旧尝试，修复后整组重跑；新评测器保存任务尝试和无效回复，不因坏 JSON 中断后续计数。Go 全量、相关 race/lint、真实 PostgreSQL/Redis/broker 与来源日期持久化、Manager 检查/单测/构建和桌面/移动专项通过。默认 episode / 代理策略未切换，完整基准、独立验证集、端到端容量/SLO 和语义核验仍未验收。详见 失败诊断与恢复（本地归档：`.agents/archive/docs/locomo-reliability-2026-10-26.md`）。

- 2026-10-26 Manager 侧栏间距：分组留白从 24px 收紧为 12px，桌面/手机顶部留白统一为 8px；品牌区固定 64px 并禁止 flex 压缩，滚动边缘按实际位置渐隐。桌面/短屏/手机实测间距一致，手机链接至少 44px、末项可达；14 项 Chromium/WebKit 相关回归、lint（0 warnings）、production build 和 diff 检查通过。验证使用 Manager/HTTP fixture，见 界面调整记录（本地归档：`.agents/archive/docs/plans/workspace-agents/manager-modern-2026-10-26.md`）。

- 2026-10-26 System 目录跟随与遮罩：正文增加吸顶区下方渐隐，目录文字左对齐并根据实际滚动模块设置 aria-current，不改写 URL；短屏/手机目录按溢出方向显示边缘遮罩，当前项仅在目录内部自动露出。修复 WebKit 尺寸观察通知导致手动目录滚动被拉回的问题。专项 6 项、相关 Chromium/WebKit 19 项回归、lint（0 warnings）、35 项单测、production build 和 diff 检查通过；实际 Edge 连续滚动已检查。仅 Manager/HTTP fixture 验证，非物理手机或 Mimic 联调，见 滚动修复记录（本地归档：`.agents/archive/docs/plans/workspace-agents/manager-scroll-2026-10-26.md`）。

- 2026-10-26 System 滚动修复：移除栏目导航 148px 与锚点 150px 的固定偏移，共享 PageHeader 通过 ResizeObserver 发布实际高度，导航和锚点统一消费；悬浮工具栏/保存栏间隙增加画布遮挡，防止表单露出。短屏导航独立滚动，并在最大高度中扣除页面底部留白，避免滚到底部时被父容器上推。修改前复现导航重叠 13.1875px，修改后普通滚动、放大文字及 1100×400 页面末尾均保持 12px 间距；17 项 Chromium/WebKit 相关回归、lint、35 项单测、production build 和 diff 检查通过。实际 Edge 栏目跳转/连续滚动已检查；仅 Manager，无 Go/数据库修改。见 滚动修复记录（本地归档：`.agents/archive/docs/plans/workspace-agents/manager-scroll-2026-10-26.md`）。

- 2026-10-26 Manager 全站容器对齐：navbar、连接提示和内容使用同一 1600px 居中容器，统一外侧边距并移除重复的横向 padding；共享面板/区块留白按桌面和手机 token 调整。修复 Chat 长协议选项挤入相邻字段，所有共享输入/Select 可在所属网格内收缩。完整 Chromium/WebKit 124 项回归通过，46 个页面入口在 320–2560px 七档宽度下共 644 次布局检查，左右边界误差 0px、整页及字段越界均为 0；lint、35 项单测、production build、diff 检查通过。使用运行中的 Manager 和确定性 fixture，非物理手机或真实 Mimic 联调。逐页坐标、截图和边界见 全站对齐记录（本地归档：`.agents/archive/docs/plans/workspace-agents/manager-layout-2026-10-26.md`）。

- 2026-10-26 Manager 现代化：悬浮侧栏与粘性顶部工具栏采用统一边距、圆角和轻阴影；Workspace 选择与栏目合为上下文条，任务视图使用分段导航。Memory service 将处理水位与后台任务归为主区域，待审核/失败形成可操作的独立区域，来源与用量降为次级详情；统一受众控件、失败行及其他四个视图的内容表面。补齐缩窄视口时当前栏目自动露出、选择器对齐与手机紧凑标题。lint、35 项单测、production build、完整 120 项桌面/WebKit 回归通过，最后的导航细节另有 16 项专项复测全部通过；实际 Edge 桌面/390px 已检查，无 Go/数据库变更。设计与证据边界见 现代化记录（本地归档：`.agents/archive/docs/plans/workspace-agents/manager-modern-2026-10-26.md`）。

- 2026-10-26 实验性语义事件层：新增默认关闭的 `episode`、配置开关、版本化类型约束迁移、LLM 结构化抽取、来源/人物/时间原话校验及策略绑定 checkpoint，接入既有存储、向量、默认检索、generation carry、来源编辑/撤回和 Manager 筛选/详情。补齐长中文引用偏移及新旧来源序号区分。Go 全量、相关 race/lint、真实 PostgreSQL/Redis/broker、Atlas lint/dry-run 与桌面/手机界面专项通过。五条合成来源的真实模型功能验证为 episode-only 4/5、冻结抽取后全部派生类型 5/5，没有匹配关闭基线，不能视为 LOCOMO 增益。真实 conv-26 生产回放在第八个 session 因 16,384-token 输出耗尽中止，完成 7/19；全部失败与未执行答案保留。改为每批最多 12 段/6,000 字节后，该 39 段 session 单独复测通过，生成 10 条 episode，但 summary+analysis 仍耗时 224.74 秒、接口报告 106,098 tokens（含重试），未达到分析 SLO。完整新策略回放、质量对照、跨批归并、历史迁移与容量验收仍待完成。详见 实现与验证（本地归档：`.agents/archive/docs/semantic-episodes-validation-2026-10-25.md`）；本条更新前文该功能尚未实现的历史状态。

- 2026-10-25 Memory service 界面重组：从堆叠长页改为动态、审核、共享、接入、数据五个任务视图，默认突出连续处理水位、待审核和失败任务；表单按需弹窗，Actor/Agent/目标工作区优先显示名称，内部 ID 与模型配置收进详情。共享支持搜索选择与预览，修正保留草稿并显式加载/选择最新 CAS revision，接入可暂停/恢复，私有收件人复用 Bits UI Checkbox。已实际检查桌面和 375×480 短屏滚动；lint、35 项单测和构建通过，完整 118 项浏览器回归有 117 项通过，修正手机测试选择隐藏侧栏入口的问题后 6 项专项复测全部通过。仅 Manager/fixture 调整，不代表真实 Mimic 或物理手机验收；细节见 界面整理记录（本地归档：`.agents/archive/docs/plans/workspace-agents/memory-service-design-2026-10-25.md`）。

- 2026-10-25 来源切片完整性：移除 part 索引正文的 400 字节截断，新增有界 UTF-8 切片、完整字段哈希与原文/渲染字节区间，补齐多文本块/工具结果与原文和 artifact 并存；Worker 验证实际来源并从对应切片生成证据预览，同源切片/OCR/caption 不增加独立佐证数。新写入采用 `dialogue_window_v2_lossless`，默认正文仍限 1,200 字节。完整 Go、相关 race/lint、独立 PostgreSQL 长文写入/向量/召回/替换清理及 PostgreSQL/Redis/broker 服务回归通过。默认生产 carry rebuild 保留原文，不自动重新切分历史记录；旧数据未迁移，snapshot replay 测试不等同生产历史升级。未实现 LLM episode、未新增 LOCOMO QA 或生产容量成绩，详见 `.agents/archive/docs/source-slices-validation-2026-10-25.md`。

- 2026-10-25 LOCOMO 核心证据保护：共享 `SelectRefinedEvidence` 按 planner 引用保留完整/部分支持证据，固定 Top N；来源打包优先保护核心，明确记录选择和上下文预算造成的遗漏。冻结 33 题的计划/候选、两组均重新回答和严格 v3 评分，核心保留由 102/106 到 106/106，引用覆盖仍为 78.65%；旧策略 24/33、新策略 25/33，旧策略有一次回答格式失败，双方完成评分的 32 题均为 24/32，不能称 QA 增益。Go 全量、memory/eval race、相关 lint、format 通过，默认 Search/Inject 不变。确认固定窗口的 part 正文有 400 字节索引截断，后续需有界无损来源切片、LLM 语义单元及真实 Worker 三轨验证；本轮未实施这些写入改造、未重测检索性能或跑全量 LOCOMO。详见 `.agents/archive/docs/locomo-core-evidence-validation-2026-10-25.md`。

- 2026-10-25 独立记忆服务与 Mimic 接入契约：MemStation 侧 MS-01～10 已实现，支持 group/project、服务端 AccessContext、Agent 与输出受众共同约束的私有分区、Integration/Actor、独立事件事务与窗口、连续 receipt 水位、候选/CAS 修正、可撤回发布副本、scoped erase/export、独立签名删除清单与恢复屏障。HTTP、typed gRPC、GraphQL 管理和 Manager 接入/来源/任务重放/审核/发布/删除/用量/generation 流程复用服务层；broker 模式统一 summary/analysis/embedding/rerank，锁定 embedding 指纹并保存权威用量引用。真实 PostgreSQL/Redis 管线、DLQ 重放、旧 pg_dump/pg_restore 删除重放、Go 全量与相关 race、format/lint、生成一致性、Atlas 新增 13 项 lint 与迁移重放、前端 lint/test/build 和 Manager 110 项桌面/移动 E2E 通过。提交前复查修复历史投影、详情 revision 一致性、同 revision 删除、私有昵称展示、broker 降级追踪并同步 Ent 索引；独立检出验证不包含并行 LOCOMO/精排实验。历史条目中的 Personal-only 限制以本条及 MS 验收表（本地归档：`.agents/archive/docs/plans/workspace-agents/implementation-status.md`） 为准；Run/远程调度仍不在本轮范围。实际 Mimic 适配器与 broker 联调、生产模型质量/容量和远端 CI 未由本地模拟证明，不能称两项目已正式接通。配置、契约与恢复步骤见 [接入说明](docs/independent-memory-integration.md)。

- 2026-10-25 高分产品实现复核：固定读取 EverOS 多轮检索/抽取/配置/回答与 judge、Mem0 当前 OSS 与 README，并核对 Hindsight、Membase、Zep、OctaMem 官方资料。确认语义 episode、分方面召回及核心证据选择与现有 raw 实验的差异；7 道补查题中 2 道丢失 planner 已判支持的材料。当前 EverOS 多轮路径不用 cross-encoder；Mem0 README 明确 92.5 为托管 Top 200 成绩，修正前文 SDK 等价性表述。新增手动冻结答案 judge probe：同一 DeepSeek 下，132 条答案逐字不变，公开 EverOS 提示把四组严格 v3 的 25/26/26/28 判为 25/27/29/29（各 /33）；保留解析失败的两组中间 artifact，第二组原始输出离线按上游规则重解析，无选择性重试。这个差异不算引擎增益；下一步优先语义证据单元、核心证据保护及生产三轨验证。详见 `.agents/archive/docs/locomo-competitor-implementation-audit-2026-10-25.md`，没有第三方完整系统复现或新增全量 LOCOMO 成绩。

- 2026-10-25 LOCOMO 有界查缺对照：新增共享 `RefineEvidence`，一次 LLM 证据计划、最多两次同权限/过滤/generation 补查，严格计划校验、共同截止时间及独立成本/精排追踪；默认 Search/Inject 不变。固定 33 题四组原 v2 自评为 25/25/25/28，修正备选答案评分歧义后整组重评为基线 25、coverage reader 26、加补查 26、完整历史 28（均 /33）；两套评分与全部回答保留。补查引用覆盖 75.52%→78.65%，证据准备 P95 约 3s→20.7s，没有额外 QA 增益，保持默认关闭。Go 全量、memory/eval race 与 lint、最终实现 33 条真实上下文回放和 132 条重评不可变性校验通过；下一步为有限同源邻接、答案证据核验及标签/推断争议审查，尚未全量 LOCOMO 或 SLO 验收。详见 `.agents/archive/docs/locomo-refinement-validation-2026-10-25.md`。

- 2026-10-25 独立记忆服务接入正在实施：已写入 Workspace/受众范围、可信事件摄取、来源与引用修订、候选 CAS、发布副本、独立删除清单与恢复屏障、存活来源重建、HTTP/gRPC 数据操作及外部 AI broker 调用账本。已通过的定向 PostgreSQL 和模拟 broker 测试、剩余 Manager/GraphQL/生命周期/联合验收工作见 实施状态（本地归档：`.agents/archive/docs/plans/workspace-agents/implementation-status.md`）。MS-01～10 尚未整体交付，多群、私有和发布的生产能力声明保持关闭；不能称 Mimic 已正式接通。

- 2026-10-25 LOCOMO 来源证据实验：实现共享 `PrepareEvidence`、权限/版本受限来源邻接、整条证据预算和原文轮次去重；事件来源固定 revision/part，旧会话固定抽取时 write revision，历史查询不读当前可变来源。生产注入新增显式实验配置，因固定 21 题自评由普通上下文 17/21 降至最终 15/21，默认保持旧注入策略；20 道有标注题引用覆盖 80.83%→85.83%，输入 token 约 4.5 倍，不能视为质量提升。保留全部五组运行及去重未生效的中间尝试；Go 全量、相关 race/lint、隔离 PostgreSQL 权限/版本/引用作者测试及 21 题上下文逐字回放通过。下一步为有限 LLM 查缺、枚举/推断政策与更大范围验证，详见 `.agents/archive/docs/locomo-evidence-validation-2026-10-25.md`。

- 2026-10-25 LOCOMO P1 候选修复：内存/PostgreSQL 补齐 Fact/Event/Segment 等正文词法召回，相关性排序先于 SQL 限量并保留权限/类型/generation 过滤；候选预取与最终 Top N 分开，精排成功只从完整评分池返回，失败保持整批回退。新增 dense/lexical 通道名额保留及 preliminary 消融入口。真实开发 12 题与另一对话验证 9 题合计 QA 自评仍为 16/21，20 道有标注题引用覆盖 73.33%→80.83%，一题改善、一题回退；检索 P95 约 2.9s，未达到 SLO。Go 全量测试、受影响包竞态/lint及隔离 PostgreSQL 回归通过；来源补全、结构化证据包、全量 QA 尚未完成。详见 `.agents/archive/docs/locomo-p1-validation-2026-10-25.md`。

- 2026-10-25 LOCOMO P0 评测基础：QA runner 对 raw/持久化引擎统一装配生产 reranker；增加 question-only/full-history/gold-evidence 诊断、禁止截断参考线、prompt/context/model 指纹、精排状态、固定失败/中断尝试、输出锁及分组/配对统计。隔离 PostgreSQL Worker 回放六条查询均精排成功，eval 竞态测试通过；真实四题自评对照与 raw 精排运行完成，仍不是全量质量成绩。复现和范围见 `.agents/archive/docs/locomo-p0-validation-2026-10-25.md`。

- 2026-10-25 LOCOMO 上限研究：扩展核查 ByteRover、Membase、Mnemon、Counterparts 等高分方案及子集/修订标签/Recall 宣称；固定新增来源与代码摘要，确认 QA runner 未装配 reranker、正文词法覆盖不足、候选预算与精排预算脱节、池外评分尺度及上下文来源字段遗漏等限制。形成 oracle 对照、证据覆盖、LLM 查缺与逐项消融计划，见 `.agents/archive/docs/locomo-ceiling-gap-analysis-2026-10-25.md`；本轮仅研究与文档，未完成这些引擎改造，未新增全量 QA 成绩。

- 2026-10-25 可配置精排：增加 OpenRouter 与本地兼容 rerank 服务，保留 Cohere/Jina；Manager 分离服务选择与模型 ID，支持命名空间、关闭和独立凭据配置。本机 llama.cpp 0.7.0 使用已下载 Qwen3-Reranker-4B Q8_0，按官方转换方式补齐 yes/no 分类头并完成真实 REST 检索验证，生成与 embedding 仍走 Ollama。异常/不完整响应整批回退，区分应用、跳过、禁用、降级。11 条固定 LOCOMO 候选诊断 Hit@5 45.45%→63.64%，Hit@1 不变；热检索约 1.3s，未达到 250ms SLO，不能视作整体 LOCOMO 达标。接入、复现及边界见 `docs/reranking.md`。

- 2026-10-25 LOCOMO 高分框架对照：核对 Hindsight、EverMemOS/EverOS、Zep/Graphiti、Mem0、OctaMem 与 MemMachine 的论文、官方报告和固定版本代码，区分 QA/Recall、模型/预算和托管/开源版本；形成多路候选、统一精排、证据预算、有限补查及场景整合的分阶段方案，见 研究与改造路线（本地归档：`.agents/archive/docs/locomo-frameworks-2026-10-25.md`）。先补引擎精排诊断与批次验证：区分禁用、跳过、成功和降级，拒绝未知/重复 ID、缺失字段、非有限/越界分数和不完整批次，三条搜索路径保留取消语义；OTel 与 eval artifact 记录实际执行状态。新的精排模型、多路融合和真实质量增益尚未落地或验收，不能把方案称为已达到 SOTA。

- 2026-10-25 LOCOMO 生产回放落地：新增独立 PostgreSQL、真实 finalization/outbox/Worker、三轨检索与 QA 手动 runner，锁定 manifest、保存断点、保留失败分母并阻止并发评测混用数据库。真实回放确认推理模型耗尽 8,192 输出 token 后正文为空，OpenAI Chat 适配器保留结束原因，Memory LLM 对明确截断做一次有上限的预算扩展重试；不生成规则记忆、不放宽引用校验。新增预算、隔离、无答案泄漏、续跑和独立重评契约测试。隔离 pilot 完成 19/19 session、419 轮与 403 个任务；12 题三轨回答全部完成。v2 自评原文 8/12、派生与混合 11/12，但推断题保守口径为 10/12，且实际上下文不等量，不能称总体准确率或竞品优势；原始评分及全部重评均保留。实际 pilot 范围与限制见 回放跟进（本地归档：`.agents/archive/docs/locomo-pipeline-2026-10-25.md`）。质量 benchmark 仍不作为默认 CI 门禁，完整 LOCOMO QA 与竞争差距尚未验收。

- 2026-10-25 LOCOMO 根因诊断：冻结 1,532 题的前十条顺序与分数完整复现，R@3 55.27%、候选 R@40 83.86%；原始轮次轨道绕过 LLM 抽取，关键词／精排／关系／会话加分均为零。全量固定查询指令与图片 caption 对照未改善 R@3。修复明确证据 ID 语法，覆盖增至 1,536 题，R@3 仍为 55.19%；新增 coverage artifact 和可缓存真实向量的手动诊断入口。对照 Mem0 固定版本逐题文件及 OctaMem 官方说明，区分 QA 与证据召回、托管与开源、公开宣称与可核算 artifact；生产改良尚未完成。根因、实现差距和后续顺序见 LOCOMO 诊断（本地归档：`.agents/archive/docs/locomo-diagnosis-2026-10-25.md`），默认 CI 不运行质量基准。

- 2026-10-25 真实模型评测：新增真实 Gateway/outbox/Worker 来源回放和 PostgreSQL HTTP 容量 runner，保留全部失败分母、默认注入／仅派生／无记忆对照及实测峰值并发。修复性能评测漏传历史／过滤参数、阈值失败丢报告、provider 错误被 lexical fallback 掩盖；LLM 提示补齐通用条目字段和精确替代身份约束，严格解析改为原子替换，最多一次模型 schema 纠正且不放宽语义校验。Go 全量（独立 PostgreSQL）、相关 race、format／lint、pnpm lint／test 通过；CI 增加阻塞 production build。真实 Qwen 原生 R@3 95.83%，LOCOMO 1,532 查询 R@3 55.27%；LongMemEval 仅分层 12 条。最终来源回放默认注入 27/27、仅派生 26/27、无记忆 3/27，保留此前失败轮次；10k 重复合成文本容量测试在低到达率下满足 250ms，但真实八路并发 P95 590–660ms 不达标。不能据此称整体质量／性能验收完成；报告与后续范围见 真实效果复核（本地归档：`.agents/archive/docs/memory-quality-2026-10-25.md`）。

- 2026-10-25 CI 重跑复核：远端 40124703812 复现两项 WebKit 失败。折叠内容取消会裁剪表单点击区域的高度动画，保留透明度和箭头反馈；移动端清空筛选增加空值断言，旧导航迁移测试等待目标页面实际渲染后再发起下一次导航。修改前重复验证复现导航竞争，修改后两项 WebKit 共 24 次重复通过，59b33ee 完整远端 CI 40126926964 通过；后续真实模型效果／性能复核见上述独立报告。

- 2026-10-25 开源准备：保留 Apache-2.0，核对历史、配置示例、模拟数据、品牌资产及依赖许可，补齐第三方 MPL 源码入口和模型／数据许可边界；扩展 Git 与 Docker 的敏感文件排除。重整中英文 README，使用可复现的当前 Manager 桌面／手机 fixture 截图，增加保留数据的完整 Compose 启动说明、Issue／PR 模板和公开检查记录。GitHub 描述、主题、安全报告与公开状态通过发布步骤设置并另行验证；检查范围见本地归档 `.agents/archive/docs/open-source-audit-2026-10-25.md`，发布政策见 [开源说明](docs/open-source.md)。

- 2026-10-25 LLM-only 提交前复核：修复摘要／分析／Key 检查点被并发结果覆盖、检查点反序列化复用 map 导致的竞态，改为同来源保留首个已验证结果并读取持久化版本；内存仓库深拷贝检查点。补齐旧事实上下文预算、已替代事实过滤、分块局部关系键、空自动写入白名单及按键读取 Key 上下文；新增迁移修正事实历史快照的来源元数据形状；LLM 重放在身份锁内检查人工修改版本，冲突时保留当前正文和向量，内存仓库同步保护。WebKit 浏览测试按视口拆分并等待确认框退出，保留所有断言与 45 秒单项时限；完整本地回归 108 项通过。推送后的远端 CI 在 runner 启动前受 GitHub 账户执行限制阻止，不能视为最终 CI 通过。验证与范围见 复核记录（本地归档：`.agents/archive/docs/llm-memory-review-2026-10-25.md`）。

- 2026-10-24 记忆生成改为 LLM-only：摘要／主题／关键词、事实／画像／通用条目／语义关系、Key Memory 合并及评分共用配置化模型链路，删除关键词和模板兜底；Worker、generation rebuild、Playground 预览同步切换。保留严格 schema、原文引用、作用域、替代链和预算校验；失败进入任务重试，已验证结果持久化复用，Key 更新使用条件写入保护人工修改。新增三项元数据迁移，Ent 同步；完整 Go／独立 PostgreSQL、相关 race、format／lint／生成一致性、Atlas、恢复演练及真实 Ollama／Manager／重启召回验证通过。现有历史记忆不自动删除或重建；真实 Ollama 联调和验证边界见 [LLM 记忆生成](docs/llm-memory.md)。

- 2026-10-24 Ollama 真实中文联调发现并修复记忆链路按字节截断导致的非法 UTF-8：摘要／分段、工具内容首尾压缩、事实分析与长摘要分块改用共享 UTF-8 字节边界工具，保留原有字节预算；新增中文与 emoji 回归。4B Q8 本地向量实际写入 PostgreSQL，失败的分析任务经重试恢复；Go 全量（独立 PostgreSQL 测试库）、相关回归、format 与 lint 通过。本机模型、凭据和测试服务配置仅存放在忽略的本地文件中。

- 2026-10-24 提交前累积复核：修复 Gateway metadata 复用污染、可信 Owner 归一化晚于配置读取，Manager IPv6 特殊地址压缩／补零判定，以及关联 Key Memory 错走普通文档路径；新增回归先复现再修复。Go 全量／相关 race／格式／lint／生成一致性／契约、Manager 31 项单测／98 项完整 E2E 及关系跳转 8 项复测／生产构建、Atlas 17 项重放与本批 4 项 lint、coverage、Worker recovery、备份 dry-run、真实 PostgreSQL dump/restore 31 表计数和 Go／Redis 生产 Manager 联调通过。当前仍仅 Personal，验证与限制见 累积复核记录（本地归档：`.agents/archive/docs/plans/workspace-agents/review-2026-10-24.md`）。

- 2026-10-24 Manager UI 整理：工作区切换改为就地展开的 Bits UI Popover，统一箭头／焦点／搜索／当前项，显式展示 owner，重复选择当前空间保持详情和代次；切换其他空间保留栏目／通用筛选和既有草稿保护。新增 MetadataRow，清理 Manager 所有圆点拼接文本，调整 Key Memory 搜索及主操作对齐，修正审计加载态空结果误提示。Manager lint／类型检查／生产构建、98 项完整 E2E 及审计修正后 3 项复测通过，29 项单测通过，1 项独立 Redis 测试跳过；桌面和移动布局已核对。仍仅开放 Personal。实现和验证见 UI 整理记录（本地归档：`.agents/archive/docs/plans/workspace-agents/manager-ui-refinement-2026-10-24.md`）。

- 2026-10-24 会话／Key Memory 分页与导航：新增工作区游标分页和数据库范围内子串筛选，Manager 每页 30 条，去除最近 100 条窗口；会话统计仅聚合当前页，不逐条读取正文。主导航、用户页、Dashboard 和记录链接进入明确 Target／Workspace，未知 owner 不回退 admin，多空间先选择；高级审计／重建保留可达且固定服务的兼容入口。Go 全量／相关 PostgreSQL race／format／lint、Manager 30 项单测／lint／生产构建和真实 Go／PostgreSQL／Redis 联调通过；完整浏览器回归 93 项通过，审计参数冲突修复后 7 项相关复测通过，320／390px WebKit 布局已核对。仍仅开放 Personal；实现与验证见 分页与导航记录（本地归档：`.agents/archive/docs/plans/workspace-agents/workspace-pagination-2026-10-24.md`）。

- 2026-10-24 通用折叠组件统一：升级 CollapsibleSection 的标题／图标／计数、展开状态、边框与焦点、动画裁切和移动尺寸，样式归入组件；三处原生详情／筛选与所有现有折叠区域统一复用，补齐隐藏表单字段校验时自动展开与聚焦。Manager lint／类型检查、26 项单测及生产构建通过；90 项浏览器回归中 1 项旧选择器歧义修正后，相关 5 项复测通过，桌面及 320／390px 截图已核对。实现与验证边界见 [Manager 组件规范](docs/manager-components.md)。

- 2026-10-23 共享控件与依赖升级：所有折叠区域统一为 Bits UI `CollapsibleSection`；所有 Select（含 target、provider route 与统计筛选）统一为 `SelectField`，普通按钮统一 `Button`，输入与聊天编辑器使用共享 Input / Textarea。统一表单桌面 36px、移动端至少 44px 触控、焦点、草稿保留与 reduced-motion；SvelteKit 3 / adapter-node 6、Bits UI 2.20.2、Vitest 5、Node 26.12.1、Go 1.27.3 等完成迁移，TypeScript 固定 6.0.3。Manager lint/check、15 项单测、production build、Chromium 36 + WebKit 12 项 E2E、Go 全量及隔离 PostgreSQL 集成、format/lint、重复生成一致性、Atlas lint/dry-run、恢复 smoke 与备份 dry-run通过；版本、兼容性例外及验证边界见 `.agents/archive/docs/ui-dependencies-2026-10-23.md`。

- 2026-09-17 图记忆增强：新增数据库取边预算、关系方向/可信度与路径解释；精确引用和上下文推断分开记录，同源证据去重；新增事实有效时间与系统记录时间查询、迟到事实插入和人工修改历史快照；实体别名支持作用域、候选/确认/撤销、循环及跨用户/跨代校验、事务审计，Manager 提供操作入口。迁移、接口与边界见 `docs/graph-memory.md`；历史快照从迁移开始，历史查询当前限定 Fact，生产量级性能与真实模型召回增益仍需独立评测。

- 2026-09-09 Logo 再设计：通过 Replicate CLI / `openai/gpt-image-2.5-flare` 生成 3 组 low 草案与 medium 精修，采用“记忆终端”纯图形：拱形站点入口、脑状记忆回路与汇入底座；保留 1024px 透明 PNG 原图、完整提示词与 prediction ID，同步 96/512px PNG、16/32px favicon、180px Apple touch icon、共享 BrandMark 和中英文 README，替换旧叠层 SVG；README 提供深色背景变体，导出可本地重生成，无需调用模型；Manager lint/check 与 production build 通过，更新 fixture 截图并验证 320/390px 品牌布局和资源读取
- 2026-09-09 品牌与开源准备：深绿色“记忆终端”图标统一用于 Manager 登录、桌面与移动导航、favicon、Apple touch icon 和中英文 README；提供 512px PNG、统一 BrandMark 组件与可重生成脚本。README 增加真实 fixture 截图、能力表、架构图、接入示例与文档入口，详细能力说明迁至 `docs/architecture.md`；新增 Apache-2.0 LICENSE / NOTICE、贡献与安全指南、第三方字体及外部数据集许可说明，三个 package manifest 和两种运行镜像同步许可证信息
- 2026-09-09 CI 故障复核：远端 CI 34354409173 / 34124859700 与 nightly 34196160611 都在 pnpm 12 不接受的 `--frozen-lockfile=false` 处失败，nightly 34321303206 的成功来自跳过。已修正 CI/nightly/Manager Docker/本地脚本安装参数、Go monorepo cache 路径与 nightly 成功条件；CI 新增手动入口，并迁移隔离数据库后启用真实 PostgreSQL 集成测试。干净 Docker 验证进一步复现旧 Corepack 查找不存在 `pnpm.cjs`，Docker/devcontainer/本地 fallback 改为 npm 安装固定 pnpm 12.3.4；新增 `.dockerignore` 排除凭据、本地数据和构建产物。详见 `docs/ci.md`
- 2026-09-09 可靠性回归：修复 recovery drill 在 256 对话任务扇出时因有界 channel 导致的自阻塞，演练队列随任务增长、完成计数并等待取消后的 Worker 退出；增加 nightly 规模、初始容量 1、任务数量与取消回归，竞态检测通过，256 对话 / 3,840 任务本地约 1.3 秒恢复。修复 backup fixture 缺失 Impression / Fact / Key Memory 节点导致的外键错误，真实 PostgreSQL 插入校验通过。Go 1.26.1 全量测试、format、lint、generated diff、Atlas lint/dry-run、PostgreSQL 专项、coverage（server 68.7%、Manager 39.76%）通过；Manager lint/check、15 项单测、production build、隔离端口的 Chromium 33 + WebKit 9 项完整 E2E 通过，320/390px 品牌布局及资源读取通过
- 2026-09-09 graph / eval 复核：关系 generation 改为双端一致性校验，拒绝跨代与跨用户写入并隔离旧冲突边；待应用的 generation backfill migration 改为只补齐可确定的关系、保留历史边，真实 PostgreSQL 16.14 空库迁移重放、关系隔离与迁移幂等/保留回归通过；未操作已有用户数据库
- 2026-09-09 评测正确性修复：LOCOMO 原始对话轨道不再索引人工标注事件摘要及上游生成记忆，统一 sample principal 并保留 speaker/date；LongMemEval 索引去除 `has_answer`/question type 标签；修复 nDCG 截断分母、重复结果加分、relation hit 去重、QA 重复 ID 与覆盖率报告；repo-native/perf benchmark tests 改为显式运行，默认 Go 正确性测试不执行 benchmark 阈值门禁
- 2026-09-09 已核对 LOCOMO / LongMemEval / LongMemEval-V2 / MemoryAgentBench 官方仓库并记录固定 revision；当前代码支持 LOCOMO raw-dialogue retrieval 与 cleaned LongMemEval V1 retrieval / QA，未把 V1 分数称为 V2 成绩。方法、验证和范围见 `apps/server/tests/eval/REVIEW-2026-09-09.md`；本地 20 条 native 与 1,532 条 LOCOMO 离线评测已导出 artifact，确定性向量结果仅验证执行链路
- Go lint 工具从无法读取当前 Go export data 的 v1.64.8 升级至明确版本 v2.13.2，使用官方配置迁移保留原有 correctness linters / exclusions，入口仍为 `scripts/go/lint.sh`；完整 Go 测试与 lint（0 issues）通过，新的数据库回归使用隔离测试实例；报告输出和工具消息判断完成等价的静态检查修正
- 2026-09-09 Manager 视觉调整：合并重复主题覆盖，采用浅色导航与中性表面，移除装饰状态灯和渐变；常规状态、关键词、检索分数与 lineage 版本改为文字元数据，保留异常提示；Dashboard 使用汇总数字行与用户记忆数量表，修复移动端图表及导航横向溢出
- 2026-09-09 Manager 交互与布局补强：统一共享表单的 SSR 标签关联、帮助/错误描述与键盘操作；修复 inactive tab 被布局样式显示并拦截点击、审计筛选焦点丢失与旧响应覆盖、图表时间范围不刷新及卸载后的异步初始化、章节面包屑错误路径；移动导航使用受控 Dialog 约束焦点，target 操作补齐触屏菜单，确认操作等待期间禁止重复提交及 Escape 关闭
- 2026-09-09 Manager 页面整理：修正表单跨列、筛选栏与操作区对齐、长内容及 JSON 溢出；System / Providers 固定保存区，Audit / Tasks / Queue detail 支持键盘选择行；Key Memory 改为弹窗创建，列表直接显示内容并折叠原始记录
- 2026-09-09 Manager 移动端与信息密度补强：Conversations / Users / Audit / Memory / Key Memory / Tasks 使用可折叠筛选；移除 Memory workspace 重复集合卡片与列表常驻审计标签，最近记忆只请求所需的 3 类、最多 4 条记录；保留搜索失败后的输入和重试入口
- 移动端增加 44px 主要触控区域与 16px 文本输入；平板保持双列表单，手机配置数值采用双列、保存操作固定到底部；弹窗跟随 visual viewport，记忆导航在视口变化后露出当前项，显式链接焦点声明与关闭后焦点恢复修复 WebKit 移动端键盘及触屏导航问题
- 修复任务队列选择被 URL 重置、重复请求及旧响应覆盖、取消全部任务状态仍返回数据；Memory / Key Memory / Conversations / Users 的读取补齐挂载去重和作用域校验；修复放大触控区后的 Provider 行窄屏溢出
- 2026-09-09 UI 组件与页面组织重构：统一旧 button 别名到共享按钮体系、表单混排对齐与 12/13/14px 信息层级；自托管 Google Fonts v23 IBM Plex Sans Latin variable 字体并附 SIL OFL，无新增 npm 依赖；列表改为分隔行，列表危险操作弱化，确认弹窗仍保留明确危险色
- System 新增共用 SettingsSection 与栏目跳转，运行参数优先、数据库配置独立分区；Providers 的标题、路由选择与编辑行统一排列；Models 精简重复 provider 清单，Users 与运维汇总统一数字栏，Tasks 的 generation / manual 表单按需展开
- Memory detail 优先展示编辑内容，运行字段与 metadata / audit payload 可展开；移除发光圆形图和重复关系列表，关联节点采用可读、可点击的有界关系视图；fixture 关联记录字段已同步正式 RelatedMemory JSON 契约，并新增用户作用域跳转回归
- 本轮 UI 验证：Manager lint / Svelte check（0 errors / 0 warnings）、Vitest（15/15）、production build 通过；Playwright 使用 production preview 与正式 fixture，Chrome `33/33`、WebKit iPhone 13 模拟 `9/9` 分别完整通过；移动回归包含触控事件、导航焦点恢复、横竖屏切换、筛选保留、请求竞态、空状态、弹窗创建删除、底部保存区、栏目跳转保留草稿和关联节点用户作用域，已加入 CI；记忆人工修正回归同时验证审计 payload 展开及刷新后持久化。26 个主要路由在 1440 / 1024 / 768 / 390 / 320px 与 844×390 横屏共 156 次布局检查未发现整页横向溢出或浏览器运行错误；截图与验证摘要位于本地 `.artifacts/ui-quality/`，使用 fixture 数据，尚未进行真实 iOS 设备验收
- server-first 业务核心已落地，`apps/server` 已具备 gateway、memory、admin、worker 基础能力
- manager 已按 `.agents/plan/frontend-panel.md` 完成 v1.0 全量核对，可直连指定 MemStation target；Dashboard、Playground、Conversations、Memory、Users、Usage、Settings、Health、Queues、Tasks、Cache、Audit 均具备正式操作入口
- manager 已统一为专业、明亮、高密度的 ToB 运维界面；Dashboard 补齐 Key Memory 与失败任务信号，Conversations 补齐 provider/mode/finalize/date 筛选，Users 补齐排序，Audit 补齐日期筛选，Cache 恢复主导航入口
- manager 关键删除、撤销、重跑、generation 激活和 override 清理操作已统一二次确认并提示审计；Summary、Impression、Fact 人工修正及 Key Memory CRUD 均有真实 fixture E2E 覆盖
- manager 真实 fixture E2E 已覆盖 dashboard、memory、playground、settings、audit、tasks 等关键路径，2026-07-11 本地结果为 `19/19`；桌面 `1440x1000` 与移动端 `390x844` 已完成视觉复核
- `apps/server/tests/eval` 已落地 repo-native golden dataset、LOCOMO adapter、artifact writer、perf smoke 与 benchmark runner
- worker backlog recovery drill、artifact writer、CI smoke 与 nightly recovery 回归已落地
- backup/export/restore drill、Compose 恢复校验文档、CI dry-run 与 nightly 实际演练入口已落地
- CI / nightly artifact 上传已收紧为短保留、非阻塞的诊断步骤，避免平台 artifact quota 影响已经通过的可靠性门禁结论；定时 nightly 已增加同提交覆盖检查，避免无代码更新时重复消耗 Actions 额度
- fresh dev baseline migration 已折叠为单一 init migration，本地可通过 `./scripts/dev/reset-compose-stack.sh` 一键重置 Compose 栈、重放 migration 与 seed
- `pnpm dev` 已切换为 devcontainer-first 联调入口：先启动 `workspace` devcontainer，再在容器内并发运行后端 watch 热重启与 manager 调试服务
- 本地可通过 `pnpm dev:container:portless` 继续把 server、manager、PostgreSQL、Redis 全部跑在 devcontainer 内，并用 Portless alias 暴露 `https://memstation-manager.localhost`
- 本地 full-stack 调试可选通过 `pnpm dev:portless` 同时启动热重载 MemStation server 与 Portless Manager；后端已运行时仍可用 `pnpm dev:manager:portless` 只启动 Manager 代理
- Apple Silicon 本机可通过 `pnpm dev:apple` 使用 Apple Container 1.0.0 启动 workspace、PostgreSQL、Redis 与 full-stack debug；`pnpm dev:apple:stop` 保留数据卷，`pnpm dev:apple:reset` 同时清理持久化开发数据
- HTTP CORS 已统一覆盖 manager/admin API 与普通 API；`cors_allowed_origins` 为空时默认反射当前请求 `Origin`，配置后按白名单限制
- 手动 LOCOMO benchmark 默认会从 Snap Research 官方公开地址下载 `locomo10.json`，仍允许通过环境变量覆盖数据源
- CI 已补齐 `golangci-lint`、严格 generated diff 校验、Atlas migration lint/dry-run、manager Playwright、coverage gate；retrieval/ranking benchmark 已从 CI / nightly 红线中移除，仅保留本地 / 手动评测入口
- K8s baseline 已补齐 server / worker / scheduler deployment，以及 ConfigMap / Secret 示例清单
- 根目录 `README.md` 已补齐，覆盖当前架构、接口、运行方式、测试入口和部署基线
- `serve` 已并行暴露 HTTP/GraphQL 与独立 gRPC 端口，`apps/server/proto/memstation/v1/api.proto` 已落地 Gateway、Memory、Admin、Health 的正式 proto 定义与生成代码
- Go 代码质量门禁已统一为 `scripts/go/format.sh` + `scripts/go/lint.sh`，并接入 `apps/server` 本地脚本与 CI 的 format check / golangci-lint
- 记忆切片 rewrite 的 stale segment 清理链路已补强，能够按 `conversation_id + keep set` 删除残留 segment graph node、relation 与 vector，避免重试或侧表已覆盖后的脏数据残留
- graph generation 生命周期已闭环：completed inactive generation 不再占用 in-progress 槽位，conversation-scoped rebuild 可继续后续重建；新增 discarded 终态、Admin/Manager discard 入口和 worker 中止语义，激活后保留当前代与上一代并清理更旧 graph/vector 数据，每个 conversation 的未引用 source snapshot 默认保留 3 份
- 人工编辑与 Key Memory 写入已改为关系数据、`embedding_revision` 和 outbox 同事务提交；embedding worker 使用 revision CAS，检索只读取 `embedding_status=ready` 的 node，旧任务晚到不会覆盖新向量；Key Memory graph sync 和 vector delete 同样通过可重试 outbox 执行
- `202607110001_graph_memory_hardening.sql` 已补齐 relation 双向索引、常用维度的 HNSW expression index、graph 引用复合外键及 relation self-loop/type/weight check；`MemoryNode / MemoryRelation / MemoryEmbedding / MemorySummary / Impression / Fact` 的 Ent schema 已同步 generation/revision/index 定义，不再保留 schema/migration 双轨漂移
- generic entity 已使用 `entity_kind + normalized title` 形成 generation-scoped canonical identity，同一 generation 内可跨 conversation 合并 alias、mention conversation 和 evidence，同时保持不同 generation 之间的 rebuild 隔离
- graph hardening migration 已在 Apple Container 1.0.0 的真实 `pgvector/pgvector:pg16` 空库完整重放；PostgreSQL planner 已确认命中 `idx_memory_embeddings_hnsw_cosine_3`，embedding revision CAS 与 generation discard 的 PostgreSQL integration tests 均通过
- 通用 Agent Memory 融合已落地：新增 `memory_items` 统一承载 `event`、`task`、`procedure`、`artifact`、`entity`、`agent_action`，所有泛化记录仍同步写入 `MemoryNode`、走 `MemoryEmbedding`，并保留 Fact / Key Memory 的既有语义边界
- analysis worker 已加入受控 `MemoryWritePolicy`/classifier，从 summary、tool calls/results、message blocks 与 artifacts 提升 durable generic memory；raw payload 仍保留在 source snapshot/message 层，写入类 tool 仍不直接修改核心 fact/key memory
- 记忆注入已补强为结构化 User Profile / Active Tasks / Relevant Procedures / Relevant Facts / Relevant Events / Relevant Agent Actions / Relevant Artifacts / Relevant Entities / Relevant Memories / Relevant Impressions 格式，并按 Key Memory、Task、Procedure、Fact、Event、Agent Action、Artifact、Entity、Summary/Segment、Impression 的固定优先级装包
- Manager / REST / gRPC / GraphQL 已扩展通用 memory DTO 与 related memory 字段，Manager 新增 Agent Memory 集合页并在详情页展示 metadata、evidence 与 typed relations
- Fact 语义提取、分类和替代判断由 LLM 完成；仅允许引用原文及明确的同属主旧事实 ID，写入侧继续校验 `fact_key / lineage_group_id / revision_seq + supersedes_fact_ids`，同步旧 fact 状态与专用 `supersedes` 边。旧 fallback extraction 已移除。
- Fact lineage 已产品化到存储、检索和管理面板：搜索默认折叠到 current head，GraphQL / gRPC / REST / Manager 返回或展示 `lineage_path`、`superseded_alias_node_ids`、`revision_depth` 与 current-head 状态
- Summary worker 使用 UTF-8 安全的来源字节分块、分层 LLM 合并和持久化检查点，写入 chunk metadata；不再使用原先的 token 估算与模板摘要路径。
- Gateway / Memory / Worker 热路径已补充 Go `Benchmark*` 函数，`go test -bench=.` 可发现并执行非流式代理、流式代理、注入、记忆检索、outbox relay 等基准
- 2026-07-11 graph hardening 最终复核已确认 `scripts/go/format.sh --check`、`scripts/go/lint.sh`、`go test ./...`（77 packages / 223 tests）、`go generate ./...` generated diff、Manager lint/Svelte check、Vitest（15/15）、production build 与 Playwright E2E（19/19）通过

### 14.2 未完成

- 2026-10-26 诊断排序尚未成为产品默认值；需在其他未调优对话及独立 judge 验证各类别、预算与延迟后再决策。多跳问题在当前 50 条精排池中的引用覆盖仅 81.51%，仍需按子问题保留候选、带权限的有限补查、跨会话实体/事件投影、来源修订失效与生产 Inject/评测证据路径对齐；图引用覆盖不等于语义支持，图片/邻接语境和标注歧义需单独审计且不得剔除分母。实现入口与验收见 `.agents/archive/locomo-architecture-2026-10-26/report.md`。

- 2026-10-26 LOCOMO 大样本验证仍限一个开发对话，尚无十个对话的完整 QA 或独立 judge；融合排序可将正确精排第一名挤出 Top 20，混合轨道在 152 题上低于 raw。scorer / QA 的受控格式恢复与私有失败子调用追踪未统一，跨批事件归并、历史来源迁移及本地 4B 精排延迟仍待验收；细节与未剔除的失败见 `.agents/archive/docs/locomo-expanded-2026-10-26.md`。

- GitHub nightly workflow 已移除，worker recovery 与 backup/restore 可靠性 drill 保留为 `scripts/ops/` 手动入口；如恢复定时回归需按 12.5 节重新落地 workflow
- Manager 干净容器构建已验证新 pnpm 安装入口和 lockfile 校验，但本机系统盘不足导致后续文件写入失败；完整容器构建与原生 Docker backup/restore 尚需在具备空间的 Docker 环境复验，本地已通过 backup dry-run 和真实 PostgreSQL fixture 校验
- pgvector ANN 与 relation 双向索引已落地并通过真实 PostgreSQL planner 验证；生产量级数据下的 `EXPLAIN (ANALYZE, BUFFERS)`、并发检索和 memory retrieval P95 仍需在专用压测环境持续验证
- 原始 LOCOMO 数据仍然不会提交进仓库；手动 benchmark 可从上游公开地址拉取，运行仍受外部网络与许可证约束
- backup/restore 实际演练仍要求运行环境具备 Docker Compose 与镜像拉取能力；在无 Docker 环境下只能执行 dry-run 形态校验
- 压测与覆盖率门禁已具备基线实现，但后续仍应随着功能扩展逐步提高阈值，而不是长期停留在当前数值
- 2026-06-27 复核 GitHub nightly 最近失败点为 `actions/upload-artifact` 存储配额已满；worker recovery nightly drill 与 backup/restore nightly drill 本身均通过。workflow 已将诊断 artifact 上传改为 3 天保留与非阻塞，并在定时 nightly 中加入同提交覆盖检查，后续仍需定期清理远端 Actions artifact 存储。
- 既有实现的剩余工作包括生产量级检索性能验证、外部 benchmark 与运维阈值持续收紧；2026-10-23 用户明确追加的 Workspace／多 Agent／Manager 扩展按 14.5 节计划推进，已完成 Personal Agent 基础和管理流程，完整工作区隔离尚未实现

### 14.3 重启检查点

后续重启时默认按以下顺序恢复工作，而不是重新漫无目的盘点仓库：

1. 在专用 PostgreSQL 压测环境用生产量级向量、relation 和并发查询执行 `EXPLAIN (ANALYZE, BUFFERS)`，持续验证 retrieval P95 与 ANN recall
2. 观察 embedding outbox retry/dead-letter、revision CAS 命中和 generation retention 的实际运行信号，发现异常时优先补可观测性与回归测试
3. 在具备 Docker 的 CI / nightly 环境里持续观察 backup/restore 与 worker recovery artifact，按结果调高阈值
4. 在本地或专用评测环境观察 repo-native smoke、LOCOMO / LongMemEval benchmark、perf smoke artifact；指标用于诊断与趋势分析，不作为 CI 红线
5. 如果新增记忆类型、排序信号或 relation 规则，先更新 `apps/server/tests/eval/testdata/` 和 smoke expectations
6. 如果新增 manager 核心模块，同步补 Vitest、Playwright 和 coverage gate
7. 如果后续引入集群化或多节点恢复方案，把现有单实例 drill 扩展为跨节点恢复演练，而不是另起一套脚本

### 14.4 当前开发计划（as of 2026-07-11）

当前开发计划不再以“补齐 v1 基础功能”为主，而是固定按以下顺序推进：

1. 工程纪律优先：
   所有 Go 改动默认先跑 `scripts/go/format.sh --check`、`scripts/go/lint.sh`、`go test ./...`，所有提交信息统一遵循 `type: message`
2. 质量门禁持续收紧：
   持续观察 CI / nightly 结果，逐步提高 coverage、recovery、backup/restore 的阈值；benchmark 指标只在本地或专用评测环境观察，不进入默认 CI 红线
3. 评测先行：
   凡是修改记忆类型、排序信号、relation expansion、注入逻辑或 temporal 规则，先同步更新 benchmark、smoke expectation 与相关回归测试
4. 运维基线深化：
   继续用现有 Compose / K8s / multi-target manager 基线承接后续集群化和多节点恢复需求，不另起一套平行架构
5. 文档与状态同步：
   后续只要出现里程碑、门禁、运行方式或当前进度变化，必须同步更新 `AGENTS.md` 与根 `README.md`

---

### 14.5 工作区与多 Agent 扩展计划（2026-10-23）

- 用户要求同时覆盖群聊机器人、代码／个人 Agent 工作区、多个 Agent 共用服务，以及完整 Manager UI 改良。
- 完整计划入口为 工作区与多 Agent 扩展计划（本地归档：`.agents/archive/docs/plans/workspace-agents/README.md`），包含架构、接入契约、Manager 信息架构与交互、迁移、实施顺序和验收矩阵；完整工作区功能仍未完成；2026-10-24 已开始 P1 基础实现，实际边界见下一条。
- 2026-10-24 首批落地：默认个人 Workspace、Actor、Agent、WorkspaceGrant 和凭据迁移；完整认证主体、到期／撤销／能力交集、HTTP／gRPC／工具权限校验、Agent 会话命名空间与会话写入版本保护；现有用户页支持 Agent Key、权限上限和有效期。其他工作区、私有记忆、群聊事件、画像、Run 和 Manager BFF／完整导航仍待实施，不能把本批视为 P1–P6 完成。接入、迁移、测试边界见 实现记录（本地归档：`.agents/archive/docs/plans/workspace-agents/implementation-2026-10-24.md`）。
- 2026-10-24 Agent 管理落地：用户页新增 Manage agents，提供服务端搜索／状态筛选／分页、Agent 创建与名称编辑、Personal 授权编辑、凭据权限上限／有效权限展示、单次密钥签发、单项撤销和停用／启用；版本冲突保留输入，路由切换丢弃旧响应，移动端主操作前置。凭据入库事务再次锁定并校验授权，避免并发收紧权限时按旧上限签发。Go 全量／相关包 race／真实 PostgreSQL 用例、前后端 lint、Manager 15 项单测及 56 项浏览器回归通过；浏览器使用 fixture，完整工作区隔离和 Manager BFF 仍待实施；边界见 Agent 管理记录（本地归档：`.agents/archive/docs/plans/workspace-agents/agent-management-2026-10-24.md`）。
- 2026-10-24 会话与任务基础落地：Conversation 增加可信 workspace／creator Agent 字段，Message／Usage 继承父空间；迁移回填、复合外键和不可变归属阻止跨属主／跨空间改挂。结束状态与 outbox 同事务提交，重复请求不重置已处理事件，超时在事务内重检；旧快照不覆盖 Worker 状态。全部 Worker handler 校验 Personal 作用域，任务 ID 按 owner／workspace 隔离，修复 queued 后发布失败无法恢复及内存仓库 metadata 并发读取竞态。完整记忆／向量／blob／缓存／generation 隔离和事件窗口仍待实施，未开放其他工作区；迁移、验证与限制见 会话作用域记录（本地归档：`.agents/archive/docs/plans/workspace-agents/conversation-scope-2026-10-24.md`）。
- 2026-10-24 记忆边界落地：节点／向量增加 workspace 字段、复合外键与不可变归属；Memory service／repository 拒绝错误 owner／workspace，直接读取、关键词、历史事实、图关系和向量候选在 LIMIT 前过滤 Personal 范围。缓存使用 owner／workspace 身份并校验命中数据；记忆写入防止 ID 改挂，向量写入与 ready 状态同事务。修复真实 PostgreSQL 暴露的多类型／会话记忆列表 SQL 错误；全量 Go、相关 race、format／lint、生成一致性、16 项迁移重放／Atlas、备份 fixture 与 256 会话恢复演练通过。generation／source／blob／可见性／生命周期隔离及 Workspace UI 仍待实施，其他空间继续关闭；详见 记忆作用域记录（本地归档：`.agents/archive/docs/plans/workspace-agents/memory-scope-2026-10-24.md`）。
- 2026-10-24 生命周期基础落地：generation／source snapshot／source message／generation source／blob／message part／artifact 增加 workspace 与复合归属；重建 SQL 移至 store，快照、创建、计数、激活与回收按 Personal 串行校验。补齐完成后发布失败的重试、迟到激活不回滚新版、废弃代际的文档／向量／分段写入保护；新文件使用工作区路径，PostgreSQL 自动清理保留其他会话和快照引用。17 项迁移重放／Atlas、完整 Go／相关 race／format／lint／生成一致性、29 条备份 fixture 插入及 256 会话恢复演练通过；未开放其他 Workspace，内存 fallback 无跨仓库保留引用事务保证，Manager BFF／Workspace UI、可见性、SourceEvent／Subject／Run 与空间归档删除仍待实施。详见 生命周期记录（本地归档：`.agents/archive/docs/plans/workspace-agents/lifecycle-scope-2026-10-24.md`）。
- 保留 User 所有权边界，新增 Workspace、Actor、Agent、Run 与显式授权；主体归属、共享／私有记忆、事件去重、并发版本冲突和单空间重建必须贯穿所有 transport、存储与工具路径。
- 2026-10-24 Manager 连接落地：P4 前置层已改为服务器 Target 登记、Redis AES-256-GCM 会话和同源 Admin／GraphQL／Gateway／文件转发；移除浏览器凭据 Map，保存失败保留非敏感草稿、切换和迟到 401 隔离、过期重连与移动短屏操作已覆盖。生产运行入口处理 origin、上传大小、请求超时和 Redis 关闭；真实 Go／Redis 联调验证重启、双实例共享、文件及 Redis 故障恢复。Go 本轮使用隔离内存仓库，未重跑 PostgreSQL／容器／远端 CI。完整 Workspace UI 和其他空间仍未开放，详见 Manager 连接记录（本地归档：`.agents/archive/docs/plans/workspace-agents/manager-sessions-2026-10-24.md`）。
- 2026-10-24 Workspace 管理入口：已有 Personal 目录／详情／名称和能力策略 REST、版本冲突与事务审计、真实代理工作区发现已实现；配置开发 Key 同样检查当前策略和 legacy Agent 状态。Manager 新增 `/t/:target/workspaces`、工作区 settings／access 路由及 server load，支持服务深链接、按范围保留草稿、冲突重载和 Agent 授权导航。隔离 PostgreSQL 迁移重放、并发／审计回滚／权限收紧、完整 Go 与 Manager 70 项 E2E、生产构建 + Go／PostgreSQL／Redis 联调及桌面／移动 WebKit 验证通过。其他类型工作区、完整 picker、记忆／对话等旧路径迁移、GraphQL／gRPC 新管理契约仍待实施。详见 Workspace 管理记录（本地归档：`.agents/archive/docs/plans/workspace-agents/workspace-management-2026-10-24.md`）。
- 2026-10-24 Workspace 浏览入口：新增 workspace 路径下的对话／文档列表、详情、关联记忆和来源 GET 契约，已有空间解析后绑定 Personal data scope，拒绝 owner 覆盖及跨空间记录／子集合读取。Manager 新增 memory／conversations server-load 路由、URL 筛选、范围内来源／关系跳转和移动栏目；最近 100 条浏览与全量检索明确区分。Go 全量及 PostgreSQL 相关 race、Manager 24 项单测／76 项 E2E 与最终布局专项 14 项、生产 Go／PostgreSQL／Redis 联调通过。记忆编辑、Key Memory、完整 picker／旧路径迁移和其他空间仍未开放到新视图，验证与下一步见 Workspace 浏览记录（本地归档：`.agents/archive/docs/plans/workspace-agents/workspace-browsing-2026-10-24.md`）。
- 2026-10-24 Workspace 记忆编辑：范围内文档正文修正与 Key Memory CRUD 已落地；以原始 updated_at 快照和显式 generation 做数据库条件写入，Key 创建／更新／删除共用锁，审计／embedding outbox 同事务，失败回滚。Manager 提供按 Target／Workspace／generation／记录隔离的标签页草稿、409 后加载当前内容与显式重试、分类清空和旧版本删除保护；修复 BFF DELETE 请求体长度。Go 全量／PostgreSQL 相关 race／format／lint、Manager 25 项单测／80 项 E2E／生产构建及真实 Go／PostgreSQL／Redis 和移动 WebKit 联调通过。完整 picker、检索／历史筛选、分页、旧路径迁移及其他空间仍待实施；详见 记忆编辑记录（本地归档：`.agents/archive/docs/plans/workspace-agents/workspace-memory-editing-2026-10-24.md`）。
- Manager 区分 Target／Workspace／账号与 Agent，遵守 11.2 节 server load／API routes 原则；旧 `.agents/plan/frontend-panel.md` 中“所有业务直连、禁止薄服务端转发”的冲突表述不再用于此次扩展。
- 本次不引入 Organization、Billing、集群部署或外部 Agent 调度平台；现有 CI、benchmark 边界和完整 v1 交付要求保持有效。

---

## 15. 已确认的规划文档漏洞与纠正

以下问题已经被本文件修正，后续实现不得继续沿用旧表述：

1. 队列技术以 `asynq` 为准，不再保留 `BullMQ` 表述。
2. API 设计必须纳入 `POST /v1/responses`，不能只支持 Chat Completions。
3. Go 版本基线统一提升到 `1.27.3`，不再混用 `1.22+ / 1.23+`。
4. Ent schema 不允许通过运行时配置切换 embedding 字段。
5. `MemoryKeyword` 与 `User` 的关系定义必须闭合。
6. Ent Privacy Policy 的规则顺序必须可执行，不能先 `AlwaysDeny` 再期望放行。
7. `Message` 结构必须支持 content blocks 和 tool payload，不能只存纯文本。
8. Manager 不得把 admin token 存在 `localStorage`。
9. 任务投递必须使用 outbox，不能只靠“事务提交后 enqueue”。
10. GraphQL v1.0 不把 subscriptions 当作默认能力。
11. DB instrumentation 以 `otelsql` 为准，`entoas` 不是替代品。
12. Nginx 健康检查示例必须使用通用能力，不能依赖 NGINX Plus 专有指令。
13. `embedding_model` 不做 per-user 自由切换，v1.0 统一为部署级配置。
14. 记忆 benchmark 是 v1.0 必做项，且采用 `repo-native golden dataset + LOCOMO external benchmark` 的混合策略。
15. 由于 LOCOMO 上游许可证限制，原始 LOCOMO 数据不得默认提交进仓库。
16. CI 与 nightly 不得默认把 benchmark smoke / full benchmark 纳入正式门禁；召回、排序和 benchmark 性能指标只作为本地 / 手动 / 专用评测信号，除非显式 opt-in，否则不得作为红绿判定。
17. R6 的 worker backlog recovery 与 backup/restore drill 也必须进入正式回归，而不是只留文档说明。

---

## 16. 实施原则

- 先统一规范，再开始代码实现
- 先打通完整链路，再做局部优化
- 先保证协议兼容与数据可靠，再追求高级智能行为
- 所有“未来可扩展”都必须建立在“今天就能实现、今天就能测试、今天就能运维”的设计上

- 2026-10-24 Workspace 检索与切换：文档列表新增数据库内容／ID 筛选和绑定 owner／workspace／generation／条件的游标分页，不再局限最近 100 条；同时间多类型排序稳定，软删除 Key 不再返回。范围内检索复用 MemoryEngine，支持活动时间与 Fact 双时间历史快照，历史正文只读并明确跳转当前记录。Manager 新增服务器搜索的工作区切换器，保留栏目／通用筛选、清除记录／游标／代次，维护草稿提醒与迟到响应边界；移动端折叠次要筛选并显示所选条件。Go 全量／PostgreSQL 相关 race／format／lint、Manager 27 项单测／84 项完整 E2E 加专项回归／生产构建和真实 Go／PostgreSQL／Redis 联调通过。会话与 Key Memory 列表分页、旧路由迁移、其他类型空间与 Subject／SourceEvent／Run 仍待实施；详见 检索与切换记录（本地归档：`.agents/archive/docs/plans/workspace-agents/workspace-search-2026-10-24.md`）。
