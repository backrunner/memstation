# MemStation — 多用户与多租户演进文档

## 1. 当前目标

MemStation v1.0 的正式目标是 **个人使用优先的单用户完整版本**。

这意味着：

- 默认部署形态是单用户、单实例、自托管
- 但所有核心表和服务边界都必须保留 `user_id` 与未来多用户升级能力
- 未来如商业化，需要平滑升级到多用户、分布式和集群化部署

---

## 2. 演进阶段

| 阶段 | 范围 | 说明 |
|------|------|------|
| v1.0 | 个人单用户 | 默认单用户单实例，自托管优先 |
| v1.x | 多用户单租户 | 小团队或托管部署，按用户隔离 |
| v2.x | 多租户集群化 | Tenant / Organization / Quota / Billing / Cluster |

---

## 3. v1.0 升级兼容模型

## 3.1 基本规则

虽然默认是个人单用户部署，但所有核心实体仍必须关联 `user_id`：

- APIKey
- UserSetting
- Conversation
- Message
- MemorySummary
- MemoryKeyword
- Impression
- Fact
- KeyMemory
- TokenUsage
- AuditEvent
- OutboxEvent

## 3.2 查询隔离

所有读操作设计上都必须能够通过用户上下文过滤：

- REST handler
- GraphQL resolver
- repository / Ent query
- VectorStore similarity search

## 3.3 写入隔离

所有写操作设计上都必须从认证上下文拿到 `user_id`，而不是信任客户端传入。

---

## 4. 认证与用户解析

## 4.1 代理接口

当前个人模式下，代理接口可以默认映射到一个主用户；但认证链路仍必须支持解析为 `user_id`。

流程：

1. 验证 key hash
2. 读取关联 `user_id`
3. 将 `user_id` 注入 request context

## 4.2 管理接口

管理面板使用服务端 session。  
服务端可以绑定管理员用户或系统管理员身份。

---

## 5. UserSetting 设计

未来多用户模式下，每个用户可以独立配置：

- 聊天模型
- 摘要模型
- 分析模型
- 是否开启记忆注入
- 是否开启工具注入
- 是否允许写工具
- 上游 API Key

v1.0 明确限制：

- 不提供 per-user embedding model
- embedding model 为部署级配置

理由：

- 避免 pgvector 维度冲突
- 降低重嵌入和检索复杂度
- 保证缓存和索引策略稳定

---

## 6. VectorStore 隔离

所有向量查询必须带用户过滤。

推荐统一接口：

```go
type SimilaritySearchParams struct {
    UserID   string
    EntityTypes []string
    Vector   []float32
    TopN     int
    MinScore float64
}
```

实现要求：

- pgvector 通过 `user_id` 条件过滤
- Qdrant / Milvus 可额外使用 namespace / partition

---

## 7. Privacy Policy 约束

Ent Privacy Policy 可以作为第二层保护，但不能替代接口层鉴权。

要求：

- context 中必须有 `user_id`
- policy 顺序必须先 allow/filter，再 fallback deny
- GraphQL 不得绕过 policy

---

## 8. 未来多租户预留

v2.x 演进时再新增：

- `Tenant`
- `Organization`
- 成员角色
- 资源配额
- 账单维度

当前预留方式：

- 表结构和接口保持 `user_id` 为一等公民
- 审计、usage、配置都按用户聚合
- 后续可在用户之上再加 tenant 归属

---

## 9. 不采用的旧方案

以下旧方案不再作为长期架构基线：

- “把单用户写死到 schema 和服务边界里”
- “per-user embedding model”

这些思路会削弱未来商业化和集群化可扩展性，因此不作为长期基线。
