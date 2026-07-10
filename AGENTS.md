# MemStation

## 1. 文档定位

本文件是 MemStation 的**执行级规范**，用于统一产品目标、技术基线、开发计划、工程约束和架构边界。

- `AGENTS.md` 优先于 `.plan/` 中的旧表述
- `.plan/` 仍然是背景材料，但如与本文件冲突，以本文件为准
- 项目目标是**一次性实现完整可上线的 v1.0**，不是 MVP、不是最小闭环 Demo
- 所有依赖必须使用**写入时的最新稳定版本**，禁止在规范里继续使用 `latest`
- 所有运行时模型名都必须配置化，禁止把易失效的模型名写死在核心设计里

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
- 失败时必须回退到单轮结构化抽取模式
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
| Package manager | `pnpm` | `10.32.1` |
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

### 10.3 版本策略

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
- 字体建议：`IBM Plex Sans` 或 `Geist`，代码字体 `JetBrains Mono`
- 组件风格强调监控、数据密度、运维信息可读性

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

Nightly 的 worker recovery drill 与 backup/restore drill 必须保持阻塞；诊断 artifact 上传可以 best-effort，并使用短保留时间，避免 GitHub artifact storage quota 已满时把已经通过的 nightly drill 判定为失败。定时 nightly 应先检查同分支上一条已完成 nightly 的 `head_sha`，如当前提交已经被覆盖则直接成功跳过重型 drill；手动 `workflow_dispatch` 必须保留强制运行能力。

### 12.6 代码质量规则

- 所有异步任务必须可重试、可幂等
- 所有外部调用都必须带超时、重试策略和 metrics
- 所有跨包 DTO 必须显式定义，禁止在 handler 中直接传 Ent entity 到前端
- 所有 provider adapter 必须有 contract tests
- Go 代码格式必须统一通过 `goimports + gofumpt`，并以 `scripts/go/format.sh` 为唯一仓库级入口
- Go lint 必须统一通过 `golangci-lint`，并以 `scripts/go/lint.sh` 为唯一仓库级入口

### 12.7 提交规范

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

## 14. 当前实现状态（as of 2026-07-11）

本节用于重启时快速恢复上下文；如后续实现进度发生变化，必须同步更新本节，而不能只改代码不改状态。

### 14.1 已完成

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
- 通用 Agent Memory 融合已落地：新增 `memory_items` 统一承载 `event`、`task`、`procedure`、`artifact`、`entity`、`agent_action`，所有泛化记录仍同步写入 `MemoryNode`、走 `MemoryEmbedding`，并保留 Fact / Key Memory 的既有语义边界
- analysis worker 已加入受控 `MemoryWritePolicy`/classifier，从 summary、tool calls/results、message blocks 与 artifacts 提升 durable generic memory；raw payload 仍保留在 source snapshot/message 层，写入类 tool 仍不直接修改核心 fact/key memory
- 记忆注入已补强为结构化 User Profile / Active Tasks / Relevant Procedures / Relevant Facts / Relevant Events / Relevant Agent Actions / Relevant Artifacts / Relevant Entities / Relevant Memories / Relevant Impressions 格式，并按 Key Memory、Task、Procedure、Fact、Event、Agent Action、Artifact、Entity、Summary/Segment、Impression 的固定优先级装包
- Manager / REST / gRPC / GraphQL 已扩展通用 memory DTO 与 related memory 字段，Manager 新增 Agent Memory 集合页并在详情页展示 metadata、evidence 与 typed relations
- Fact fallback extraction 已使用规范化 category taxonomy 和轻量多事实输出，并通过 `fact_key / lineage_group_id / revision_seq + supersedes_fact_ids` 在写入侧同步旧 fact 的 `is_superseded / superseded_by_id` 与专用 `supersedes` lineage relation；supersession 写入已收紧为必须携带明确 lineage 锚点和可解析目标，不再从旧事实指针或旧 topic ID 反推
- Fact lineage 已产品化到存储、检索和管理面板：搜索默认折叠到 current head，GraphQL / gRPC / REST / Manager 返回或展示 `lineage_path`、`superseded_alias_node_ids`、`revision_depth` 与 current-head 状态
- Summary worker 已加入 `summary_chunk_max_tokens=6000`、`summary_chunk_overlap=600` 的长上下文 chunking 入口，并写入 chunk metadata
- Gateway / Memory / Worker 热路径已补充 Go `Benchmark*` 函数，`go test -bench=.` 可发现并执行非流式代理、流式代理、注入、记忆检索、outbox relay 等基准
- 此前已确认通过的本地门禁包括：`pnpm lint`、`pnpm --filter @memstation/server format:check`、`pnpm --filter @memstation/server lint`、`go test ./...`
- 2026-07-11 本地复核中，既有 `scripts/go/format.sh --check`、`scripts/go/lint.sh`、`go test ./...` 通过结论未被本轮 Manager 改动影响；本轮已重新确认 manager lint、Svelte check、Vitest、production build 与 Playwright E2E 通过

### 14.2 未完成

- 当前没有已确认的 v1 功能缺口；R1-R6 与 R5.5 所要求的仓库内实现均已落地
- 原始 LOCOMO 数据仍然不会提交进仓库；手动 benchmark 可从上游公开地址拉取，运行仍受外部网络与许可证约束
- backup/restore 实际演练仍要求运行环境具备 Docker Compose 与镜像拉取能力；在无 Docker 环境下只能执行 dry-run 形态校验
- 压测与覆盖率门禁已具备基线实现，但后续仍应随着功能扩展逐步提高阈值，而不是长期停留在当前数值
- 2026-04-25 本地复核中曾发现既有 format / lint 问题；截至 2026-07-11，后续复核已确认 `scripts/go/format.sh --check` 与 `scripts/go/lint.sh` 通过
- 2026-06-27 复核 GitHub nightly 最近失败点为 `actions/upload-artifact` 存储配额已满；worker recovery nightly drill 与 backup/restore nightly drill 本身均通过。workflow 已将诊断 artifact 上传改为 3 天保留与非阻塞，并在定时 nightly 中加入同提交覆盖检查，后续仍需定期清理远端 Actions artifact 存储。
- 当前主要剩余工作已从“补功能”转为“持续提高工程门禁和运维阈值”，而不是继续扩写新的 v1 范围

### 14.3 重启检查点

后续重启时默认按以下顺序恢复工作，而不是重新漫无目的盘点仓库：

1. 在具备 Docker 的 CI / nightly 环境里持续观察 backup/restore 与 worker recovery artifact，按结果调高阈值
2. 在本地或专用评测环境观察 repo-native smoke、LOCOMO / LongMemEval benchmark、perf smoke artifact；指标用于诊断与趋势分析，不作为 CI 红线
3. 如果新增记忆类型、排序信号或 relation 规则，先更新 `apps/server/tests/eval/testdata/` 和 smoke expectations
4. 如果新增 manager 核心模块，同步补 Vitest、Playwright 和 coverage gate
5. 如果后续引入集群化或多节点恢复方案，把现有单实例 drill 扩展为跨节点恢复演练，而不是另起一套脚本

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

## 15. 已确认的规划文档漏洞与纠正

以下问题已经被本文件修正，后续实现不得继续沿用旧表述：

1. 队列技术以 `asynq` 为准，不再保留 `BullMQ` 表述。
2. API 设计必须纳入 `POST /v1/responses`，不能只支持 Chat Completions。
3. Go 版本基线统一提升到 `1.26.1`，不再混用 `1.22+ / 1.23+`。
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
