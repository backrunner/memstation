# MemStation — API 设计文档

## 1. 概述

MemStation 提供四类接口：

1. 代理 API
2. GraphQL API
3. 记忆 REST API
4. 管理 REST API

Base URL:

```text
http://<host>:<port>
```

---

## 2. 认证模型

## 2.1 代理 API

```http
Authorization: Bearer ms-xxxxxxxxxxxxxxxx
```

说明：

- API Key 绑定到用户
- 所有代理请求隐式携带 `user_id`

## 2.2 管理 API / GraphQL

管理面板推荐使用服务端 session。  
外部管理客户端可使用 Bearer Token：

```http
Authorization: Bearer <MEMSTATION_ADMIN_TOKEN>
```

---

## 3. 代理 API

## 3.1 通用请求头

| Header | 类型 | 说明 |
|--------|------|------|
| `X-MemStation-Mode` | string | `transparent` / `inject`，默认 `transparent` |
| `X-MemStation-Session` | string | 客户端自定义会话 ID |
| `X-MemStation-End-Conversation` | boolean | 显式结束对话 |
| `X-MemStation-Inject-Context` | boolean | 是否注入 Context |
| `X-MemStation-Inject-Tools` | boolean | 是否注入只读工具 |
| `X-MemStation-Tool-Write` | boolean | 是否允许写工具，默认关闭 |
| `X-MemStation-Memory-Persist` | boolean | 是否允许本次会话落记忆，测试/Playground 默认应为 `false` |
| `X-MemStation-Provider` | string | 强制指定上游 provider |

补充规则：

- `X-MemStation-Memory-Persist=false` 时，本次会话不应生成或写入 `MemorySummary`、`MemoryKeyword`、`Impression`、`Fact`、`KeyMemory`
- `X-MemStation-Memory-Persist=false` 的优先级高于 `X-MemStation-Tool-Write=true`
- 该规则必须由 MemStation server 在 gateway / finalize / worker 链路统一保证

## 3.2 OpenAI Chat Completions

### `POST /v1/chat/completions`

语义：

- 入参兼容 OpenAI Chat Completions
- 支持 `stream=true`
- 请求会被归一化到内部模型

附加行为：

- transparent 模式仅记录
- inject 模式会执行记忆检索与注入

## 3.3 OpenAI Responses API

### `POST /v1/responses`

语义：

- 入参兼容 OpenAI Responses API
- 支持文本、工具、流式事件
- 内部会映射到统一模型，再格式化回 Responses 语义

## 3.4 Anthropic Messages

### `POST /v1/messages`

语义：

- 入参兼容 Anthropic Messages API
- 支持工具、系统指令、流式返回

## 3.5 模型列表

### `GET /v1/models`

返回：

- 可用入站模型
- 上游 provider
- 能力标签
- 是否支持工具
- 是否支持流式

---

## 4. GraphQL API

## 4.1 端点

```text
POST /graphql
GET  /graphql/playground   # 仅开发环境
```

## 4.2 设计原则

- v1.0 只支持 Query 和 Mutation
- 实体主查询尽量依赖 Ent + entgql
- 复杂检索由自定义 resolver 承担
- 结果必须受用户隔离策略约束

## 4.3 核心 Query

```graphql
type Query {
  node(id: ID!): Node
  nodes(ids: [ID!]!): [Node]!

  me: User!
  users(first: Int, after: Cursor, where: UserWhereInput): UserConnection!
  user(id: ID!): User

  conversations(first: Int, after: Cursor, where: ConversationWhereInput, orderBy: ConversationOrder): ConversationConnection!
  memorySummaries(first: Int, after: Cursor, where: MemorySummaryWhereInput, orderBy: MemorySummaryOrder): MemorySummaryConnection!
  impressions(first: Int, after: Cursor, where: ImpressionWhereInput): ImpressionConnection!
  facts(first: Int, after: Cursor, where: FactWhereInput): FactConnection!
  keyMemories(first: Int, after: Cursor, where: KeyMemoryWhereInput, orderBy: KeyMemoryOrder): KeyMemoryConnection!
  auditEvents(first: Int, after: Cursor, where: AuditEventWhereInput): AuditEventConnection!

  searchMemories(input: SearchMemoriesInput!): SearchMemoriesResult!
  tokenUsageStats(input: TokenUsageStatsInput!): TokenUsageStats!
  systemOverview: SystemOverview!
}
```

## 4.4 核心 Mutation

```graphql
type Mutation {
  createAPIKey(input: CreateAPIKeyInput!): CreateAPIKeyResult!
  revokeAPIKey(id: ID!): Boolean!

  updateUserSetting(userID: ID!, input: UpdateUserSettingInput!): UserSetting!

  createKeyMemory(input: CreateKeyMemoryInput!): KeyMemory!
  updateKeyMemory(id: ID!, input: UpdateKeyMemoryInput!): KeyMemory!
  deleteKeyMemory(id: ID!): Boolean!

  updateMemorySummary(id: ID!, input: UpdateMemorySummaryInput!): MemorySummary!
  updateImpression(id: ID!, input: UpdateImpressionInput!): Impression!
  updateFact(id: ID!, input: UpdateFactInput!): Fact!

  requeueConversationAnalysis(conversationID: ID!): Boolean!
}
```

## 4.5 自定义记忆搜索

```graphql
input SearchMemoriesInput {
  query: String!
  topN: Int = 10
  includeConversation: Boolean = false
  includeSegments: Boolean = true
  memoryTypes: [MemoryType!]
  minScore: Float = 0.3
  timeRange: TimeRangeInput
}

type SearchMemoriesResult {
  memories: [SearchMemoryItem!]!
  total: Int!
  queryEmbeddingTokens: Int!
}

type SearchMemoryItem {
  id: ID!
  type: MemoryType!
  content: String!
  score: Float!
  effectiveScore: Float!
  importance: Float!
  decayFactor: Float!
  createdAt: Time!
  conversation: Conversation
  segments: [MessageSegment!]
  related: [RelatedMemory!]
}
```

---

## 5. 记忆 REST API

这些接口是 GraphQL 的 REST 快捷入口，方便非 GraphQL 客户端使用。

## 5.1 搜索记忆

### `POST /api/v1/memory/search`

```json
{
  "query": "上次聊过的日本旅行计划",
  "top_n": 10,
  "include_conversation": false,
  "include_segments": true,
  "memory_types": ["summary", "impression", "fact"],
  "min_score": 0.3
}
```

## 5.2 Key Memory 列表

### `GET /api/v1/memory/keys`

支持：

- `category`
- `top_k`
- `include_effective_score`

## 5.3 Key Memory Upsert

### `PUT /api/v1/memory/keys/:key`

用途：

- 手工创建或更新 Key Memory
- 自动写审计事件

## 5.4 Key Memory 删除

### `DELETE /api/v1/memory/keys/:key`

---

## 6. 管理 REST API

## 6.1 健康检查

### `GET /health/live`

返回进程是否存活。

### `GET /health/ready`

返回依赖组件是否准备完成：

- database
- redis
- vectorstore
- worker relay

## 6.2 系统概览

### `GET /api/admin/overview`

返回：

- 活跃对话数
- 最近任务状态
- 记忆规模
- 队列积压
- 关键错误计数

## 6.3 Token 统计

### `GET /api/admin/stats/tokens`

查询参数：

- `user_id`
- `start_date`
- `end_date`
- `group_by=day|week|month`

## 6.4 配置管理

### `GET /api/admin/config`

### `PUT /api/admin/config`

可修改：

- 聊天模型
- 摘要模型
- 分析模型
- 注入开关
- 超时阈值
- 排序参数

## 6.5 队列与任务

### `GET /api/admin/queues`

### `POST /api/admin/tasks/requeue`

## 6.6 审计

### `GET /api/admin/audit`

---

## 7. 典型响应

## 7.1 健康检查

```json
{
  "status": "ready",
  "version": "1.0.0",
  "mode": "gateway",
  "services": {
    "database": "ok",
    "redis": "ok",
    "vectorstore": "ok",
    "outbox": "ok"
  }
}
```

## 7.2 统一错误格式

REST API：

```json
{
  "error": {
    "code": "invalid_request",
    "message": "the 'query' field is required",
    "type": "validation_error",
    "param": "query"
  }
}
```

GraphQL：

```json
{
  "errors": [
    {
      "message": "unauthorized",
      "extensions": {
        "code": "UNAUTHORIZED"
      }
    }
  ]
}
```

---

## 8. API 设计约束

- 所有返回都必须带可观测上下文信息到日志链路
- 代理接口必须尽量保持协议兼容，不引入破坏性自定义字段
- 管理能力优先通过 GraphQL 做高密度查询，通过 REST 做运维动作
- 任何写操作都必须具备审计记录
