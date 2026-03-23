# MemStation — 需求文档

## 1. 项目定位

MemStation 是一个面向生产环境的 **AI Gateway + Memory System + Admin Plane**。  
v1.0 目标不是最小实现，而是一次性实现可交付、可运维、可扩展的完整版本。

### 1.1 产品定义

| 维度 | 定义 |
|------|------|
| 产品类型 | AI 网关 + 长期记忆系统 + 管理平面 |
| 核心语言 | Go |
| 管理面板 | SvelteKit |
| 仓库模式 | Turborepo Monorepo |
| 部署形态 | Docker / Docker Compose / Kubernetes |
| 数据平面 | PostgreSQL + Redis + VectorStore |
| 当前目标 | v1.0 个人自托管优先，默认单用户单实例 |

### 1.2 运行模式

MemStation 支持两种运行模式：

1. `gateway`
   暴露代理接口、记忆接口、GraphQL、管理接口。
2. `standalone`
   仅暴露记忆管理、GraphQL、管理接口，不承担代理职责。

### 1.3 v1.0 范围边界

v1.0 必须完成：

- 个人自托管完整版本
- OpenAI Chat / OpenAI Responses / Anthropic Messages 入站兼容
- OpenAI / Anthropic / Gemini 上游适配
- 记忆提取、检索、注入、管理
- 管理面板、审计、安全、可观测性、部署基线

v1.0 不作为阻塞项：

- SaaS 多租户计费
- Tenant / Organization 资源配额
- GraphQL Subscriptions
- 十亿级向量规模优化
- 真正的多节点生产集群

同时必须预留：

- `user_id` 级隔离
- 无状态服务边界
- 共享 Redis / PostgreSQL / VectorStore 接口
- outbox + worker 幂等机制

---

## 2. 功能需求

## 2.1 代理协议与网关能力

### 2.1.1 入站协议

系统必须支持以下对外接口：

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `GET /v1/models`

要求：

- 流式和非流式均可用
- 保持原协议兼容，不修改调用方语义
- 所有入站请求先归一化到内部模型，再进入统一业务链路

### 2.1.2 上游协议

系统必须可代理到以下上游：

- OpenAI
- Anthropic
- Gemini

要求：

- 支持用户级 API Key 和系统默认 API Key 两级解析
- 支持用户级聊天模型、摘要模型、分析模型偏好
- Embedding 模型为部署级配置，不做 per-user 自由切换

### 2.1.3 请求模式

每个代理请求必须支持两种模式：

1. `transparent`
   只代理、记录、异步生成记忆。
2. `inject`
   在发送上游前执行记忆搜索与注入。

每个请求还必须可独立控制：

- 是否注入 Context
- 是否注入只读工具
- 是否允许写工具

## 2.2 会话管理

### 2.2.1 对话生命周期

系统必须支持：

- 显式结束
- 超时结束
- 消息阈值结束
- Token 阈值结束

对话结束后必须形成可追踪的终态：

- `active`
- `ending`
- `ended`
- `timeout`
- `failed`

### 2.2.2 消息持久化

每条消息必须持久化：

- 角色
- 文本内容
- 多段 content blocks
- 工具调用和工具结果
- 协议原始元信息
- token 使用信息
- 顺序号

## 2.3 记忆系统

### 2.3.1 主记忆

系统必须在对话结束后异步生成：

- `MemorySummary`
- `MemoryKeyword`
- 向量索引

要求：

- 支持长对话分片
- 支持多主题拆解
- 支持重嵌入和版本化

### 2.3.2 高级记忆

系统必须支持：

- `Impression`
  用户对人/事/地点/对象的主观印象
- `Fact`
  与用户强相关的客观事实
- `KeyMemory`
  用户稳定环境信息和可注入属性

要求：

- 支持记忆关联边
- 支持事实替代链
- 支持人工修正和审计记录

### 2.3.3 记忆检索

系统必须提供：

- 向量召回
- 时间衰减排序
- 记忆关联补全
- Top N / Top K 选择

检索接口必须支持：

- 自然语言 query
- 记忆类型过滤
- 时间范围过滤
- 最低分数过滤
- 返回关联对话或片段

### 2.3.4 记忆注入

系统必须支持三种注入形态：

1. System Prompt 注入
2. Context 注入
3. Tool Injection

其中：

- 只读工具可默认开启
- 写工具默认关闭
- 写工具只能写入白名单 Key Memory
- 所有工具写入必须审计

## 2.4 管理平面

### 2.4.1 GraphQL API

GraphQL 用于：

- 图结构浏览
- 管理面板数据查询
- 记忆管理
- 用户配置修改

v1.0 范围：

- Query
- Mutation

### 2.4.2 管理 REST API

必须提供：

- 健康检查
- 系统配置
- Token / 成本统计
- Worker / 队列状态
- 审计与运维信息

### 2.4.3 管理面板

管理面板必须具备：

- Dashboard
- 对话列表与详情
- 记忆列表与详情
- Key Memory CRUD
- 用户管理
- 成本与 Token 统计
- 系统配置
- 健康与队列状态

## 2.5 安全与审计

系统必须实现：

- API Key 哈希存储
- 上游密钥加密存储
- 审计日志
- 请求级速率限制
- 结构化日志脱敏
- 管理员服务端会话登录

不允许：

- 管理 Token 存入浏览器 `localStorage`
- 日志输出完整密钥或敏感 PII

---

## 3. 非功能需求

## 3.1 性能

目标：

- 非流式代理额外开销 P95 `< 150ms`
- 流式首包额外开销 P95 `< 250ms`
- 记忆检索 P95 `< 250ms`
- 向量召回 + 排序整体在多用户高频场景下保持稳定

## 3.2 可靠性

系统必须满足：

- 使用 outbox pattern，避免提交后 enqueue 丢任务
- 异步任务幂等
- Worker 可重试
- 关键写入通过事务保证一致性
- 读路径允许最终一致，但状态必须可观测

## 3.3 可维护性

必须具备：

- 明确的模块边界
- 版本化迁移
- 自动代码生成
- 集成测试与协议契约测试
- 统一配置系统

## 3.4 安全性

必须具备：

- 多用户数据隔离
- 访问控制
- 审计追踪
- 密钥轮换能力
- 管理会话保护

## 3.5 扩展性

必须具备：

- 无状态 Gateway 水平扩展
- Worker 水平扩展
- Redis 共享队列
- PostgreSQL 读写扩展路径
- 可插拔 VectorStore

说明：

- 这些能力是未来商业化和集群化的架构准备，不要求 v1.0 默认以集群形态运行

---

## 4. 技术约束

| 约束 | 要求 |
|------|------|
| 后端语言 | Go |
| ORM | Ent |
| GraphQL | gqlgen + entgql |
| 队列 | asynq |
| 数据库 | PostgreSQL 16+ |
| 缓存/队列后端 | Redis 7+ |
| 向量层 | VectorStore 抽象，默认 pgvector |
| 管理面板 | SvelteKit 2 + Svelte 5 |
| 单仓模式 | Turborepo + pnpm |
| 迁移 | Atlas 版本化迁移 |
| 可观测性 | OpenTelemetry + OTLP Export |

---

## 5. v1.0 验收标准

当且仅当以下条件同时满足，v1.0 才算完成：

1. 三种入站协议均通过契约测试。
2. 完整对话可自动进入摘要、分析、向量化、Key Memory 链路。
3. 记忆检索、注入和管理面板全部可用。
4. 安全、审计、健康检查、指标、日志全部可运行。
5. Docker Compose 可以拉起完整环境。
6. K8s 基线部署和滚动更新策略可验证。
