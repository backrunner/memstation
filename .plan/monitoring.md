# MemStation — 可观测性与监控设计文档

## 1. 目标

MemStation 的可观测性必须覆盖：

- 请求链路
- 上游模型调用
- 数据库访问
- Redis / 队列
- 记忆检索
- Worker 执行
- 管理操作与审计

技术基线：

- OpenTelemetry
- OTLP Export
- `slog`
- `otelsql`

---

## 2. Telemetry 组件

| 域 | 方案 |
|----|------|
| Traces | OpenTelemetry Trace |
| Metrics | OpenTelemetry Metrics + OTLP Export |
| Logs | `slog` + OTel bridge |
| SQL Instrumentation | `otelsql` |
| Redis Instrumentation | go-redis OTel instrumentation |
| HTTP Instrumentation | Chi middleware |

说明：

- `entgo.io/contrib/entoas` 不是数据库观测方案，不用于 DB instrumentation

---

## 3. 关键指标

## 3.1 代理指标

- `proxy.request.total`
- `proxy.request.duration`
- `proxy.upstream.duration`
- `proxy.overhead.duration`
- `proxy.stream.first_token`
- `proxy.error.total`
- `proxy.request.active`

标签：

- `provider`
- `model`
- `mode`
- `status_code`

## 3.2 记忆指标

- `memory.search.duration`
- `memory.search.results_count`
- `memory.injection.duration`
- `memory.injection.token_count`
- `memory.store.total`
- `memory.embedding.pending`

## 3.3 Worker 指标

- `worker.task.processed`
- `worker.task.duration`
- `worker.task.queue_wait`
- `worker.task.retries`
- `worker.queue.size`
- `worker.deadletter.size`
- `outbox.pending`

## 3.4 数据库与缓存指标

- `db.query.duration`
- `db.connections.active`
- `db.connections.idle`
- `vector.search.duration`
- `cache.hit.total`
- `cache.miss.total`
- `cache.latency`

## 3.5 业务指标

- `tokens.input.total`
- `tokens.output.total`
- `tokens.cost.total`
- `analysis.impressions.extracted`
- `analysis.facts.extracted`
- `analysis.relations.created`
- `audit.events.total`

---

## 4. Trace 设计

## 4.1 代理请求 Trace

```text
proxy.request
├── auth.validate
├── protocol.parse
├── memory.search
│   ├── embedding.query
│   ├── vector.search
│   ├── sql.hydrate
│   └── memory.rank
├── memory.inject
├── provider.request
│   ├── provider.connect
│   ├── provider.first_token
│   └── provider.stream
├── conversation.persist
└── outbox.write
```

## 4.2 Worker Trace

```text
worker.summary.generate
├── conversation.load
├── summary.chunk
├── llm.summary
├── summary.persist
└── enqueue.followups

worker.analysis.extract
├── summary.load
├── llm.analysis
├── relation.persist
└── enqueue.followups

worker.embedding.generate
├── entity.load
├── embedding.generate
├── vector.upsert
└── embedding.mark_done
```

## 4.3 Span 属性

建议统一带：

- `memstation.user_id`
- `memstation.conversation_id`
- `memstation.entity_type`
- `memstation.entity_id`
- `memstation.provider`
- `memstation.model`
- `memstation.mode`

---

## 5. 日志规范

## 5.1 原则

- 全部使用结构化日志
- 默认日志带 trace/span 上下文
- 日志为排障服务，不承载业务存储语义

## 5.2 级别

| 级别 | 用途 |
|------|------|
| `ERROR` | 上游失败、任务失败、不可恢复错误 |
| `WARN` | 重试、降级、超时、部分失败 |
| `INFO` | 请求完成、任务完成、配置变更 |
| `DEBUG` | 开发调试与局部排障 |

## 5.3 脱敏规则

禁止输出：

- 完整 API Key
- 完整上游 provider 密钥
- 完整敏感 prompt
- 完整 PII

---

## 6. 健康检查

必须暴露：

- `GET /health/live`
- `GET /health/ready`

`/health/ready` 至少检查：

- PostgreSQL
- Redis
- VectorStore
- Outbox relay 状态

---

## 7. 仪表盘建议

必须提供以下观测视图：

1. 代理总览
2. Token / 成本
3. 记忆系统
4. Worker 与队列
5. 数据库 / 缓存
6. 审计与管理动作

---

## 8. 告警建议

| 告警 | 条件 |
|------|------|
| 代理错误率过高 | 5 分钟窗口内错误率 > 5% |
| 流式首包异常 | TTFT P95 超阈值 |
| 队列积压 | 队列持续增长且超过阈值 |
| outbox 积压 | `outbox.pending` 持续非零且增长 |
| 死信非空 | 死信队列 > 0 |
| 数据库连接逼近上限 | 活跃连接 > 90% |
| 向量检索变慢 | `vector.search.duration` P95 超阈值 |

---

## 9. 生产要求

- tracing / metrics / logs 导出都必须可配置
- 默认通过 OTLP 导出到 Collector 或外部后端（例如 Sentry、Tempo、Datadog 等）
- readiness 失败时必须停止接收新请求
- graceful shutdown 期间必须优先保护长连接和 inflight 请求
