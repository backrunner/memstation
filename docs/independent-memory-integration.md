# 独立记忆服务部署与 Mimic 接入

MemStation 支持独立事件摄取、异步巩固、授权检索和来源生命周期。Mimic 继续使用自己的聊天 Runtime；不要把同一聊天再发送到 MemStation Gateway。接口字段和权限见 [契约 1.0](memory-service-contract.md)。

## 升级与初始化

1. 保留数据库、配置和当前独立删除清单的备份。暂停写入并依次运行版本化迁移；本次新增 `202610250002` 至 `202610250014`，不使用运行时自动建表。旧记录只保留在原 Personal Workspace，不推断群、昵称或人物归属。
2. 为 serve、worker、scheduler 配置同一数据库、Redis 和独立删除清单路径。Compose 已为这些角色共享 `deletion-journal` volume；它必须独立于数据库备份的恢复时间点。
3. 当前阶段只支持当前数据契约，不提供旧部署的自动迁移或兼容读取。新部署使用空数据库执行版本化迁移和 `memstation seed`；备份恢复必须保留原独立删除日志，并运行 `memstation seed --verify-recovery`。删除日志缺失时拒绝恢复就绪，不通过初始化开关绕过。
4. 启动 serve、worker、scheduler。恢复屏障未通过时数据面和 worker 保持不可用；capabilities 返回 `memory-disabled`，不会用临时内存仓库冒充可持久化服务。
5. 在 Manager 创建 group/project Workspace、Agent、权限和业务凭据，再配置 Integration 的平台命名空间、来源空间、观察 Agent 与允许事件。成员 Actor 不等于 API 授权。现有绑定身份字段不可直接改为另一个来源；先撤销旧观察权限，再明确配置新绑定。

`MEMSTATION_DELETION_JOURNAL_PATH` 指向共享的 JSONL 文件。`MEMSTATION_DELETION_JOURNAL_KEY` 为稳定的独立签名密钥；未设置时兼容使用 `MASTER_KEY`。从兼容模式改为独立配置时，须保留原签名密钥，不能直接换值使旧清单失效。文件必须有进程间锁与 fsync 语义；当前交付是单实例/共享持久卷部署，不宣称任意分布式对象存储具备相同语义。

## 外部 AI broker 配置

配置项在 [server/.env.example](../apps/server/.env.example)。生产 broker 由 Mimic 交付；MemStation 的模拟 broker 仅用于契约验收。

```dotenv
MEMSTATION_MEMORY_AI_BACKEND=broker
MEMSTATION_MEMORY_BROKER_BASE_URL=http://mimic-internal:18490/v1
MEMSTATION_MEMORY_BROKER_CREDENTIAL=<restricted-service-credential>
MEMSTATION_MEMORY_BROKER_SUMMARY_ALIAS=memory.summary
MEMSTATION_MEMORY_BROKER_ANALYSIS_ALIAS=memory.analysis
MEMSTATION_MEMORY_BROKER_EMBEDDING_ALIAS=memory.embedding
MEMSTATION_MEMORY_BROKER_RERANK_ALIAS=memory.rerank
MEMSTATION_MEMORY_BROKER_EMBEDDING_MODEL=<immutable-actual-model-revision>
MEMSTATION_MEMORY_BROKER_EMBEDDING_DIMENSIONS=<configured-dimension>
MEMSTATION_MEMORY_BROKER_EMBEDDING_FINGERPRINT=<index-fingerprint>
```

四个 role alias 必须明确且不同。broker 凭据只能访问这四种用途，不能调用聊天工作流、记忆检索或任意模型。MemStation 拒绝重定向，避免凭据被转发。独立部署仍可配置 `direct` 模式使用已有 provider registry；Mimic 组合部署选择 broker 后，记忆相关调用统一使用 broker，不另存模型厂商密钥。

| 路径（相对 base URL） | 契约 |
| --- | --- |
| `POST /chat/completions` | OpenAI-compatible 非流式 summary/analysis；model 是 alias |
| `POST /embeddings` | 单条 input、dimensions；响应 actual model、向量长度和指纹必须与 generation profile 一致 |
| `POST /memory/rerank/v1` | `version:1,model,query,documents:[{id,text}],top_n`；响应完整 results `[{document_id,score}]`，每个 ID 恰好一次，有限分数在 `[0,1]` |
| `GET /memory/calls/{call_id}` | 对账调用状态与 broker usage；实现形状见模拟 broker 和对账契约测试 |

每次网络尝试携带 `X-MemStation-Internal-Call: memory-broker-v1`、`X-MemStation-Broker-Version: 1`、Role、Workspace、Job、Attempt、Call-Id 和 OTel trace context。响应要求同版本、同 Call-Id、非空 Request-Id/Usage-Id，并提供 Model-Version；embedding 还需 `X-MemStation-Embedding-Fingerprint`。费用通过 Reported-Cost/Currency 返回，详情见 [broker 客户端](../apps/server/internal/service/aibroker/client.go) 与 [模拟响应](../apps/server/tests/testutil/mockbroker/handler.go)。

broker 是费用权威，MemStation 保存调用引用，不把同一 usage 再累计到 TokenUsage。真实重试有不同 call ID；网络结果未知保留 unknown，可在 Manager 对账。服务凭据限制由 broker 强制执行，不能只相信客户端 role Header。模拟 broker 覆盖此拒绝路径，但不能证明实际 Mimic broker 的权限实现。

更换 embedding 实际模型/维度/指纹必须创建新 generation 并重建，再激活。已有 profile 不会随 alias 偷换；不匹配直接失败。含人工修正/发布副本的空间采用保留权威记录并重新 embedding 的重建模式；激活再次检查来源、revision、覆盖率和失败任务，避免覆盖并发修正。

## Mimic 适配器顺序

1. 核心持有 Workspace、受众和 Integration 绑定，先 probe 有效能力。不要从 LLM 参数读取可信 scope。
2. Mimic 在本地完成输入保留策略；将 ready 消息提交 events/batch，保留本地 outbox 直到确认 receipt。每个源事件和 revision 使用稳定标识。无需机器人回复即可摄取；机器人输出必须有 confirmed delivery receipt。
3. 轮询 ingestions，分别显示 accepted 与 indexed。窗口默认 5 分钟/100 条，或保守的序列化 UTF-8 输入预算先到；不会在 HTTP 摄取请求内调用文本模型。
4. 参与判断通过后，用明确 query、Actor、时间和核心绑定的受众检索。检索失败使用 Mimic 自己的短窗，不能无 scope 重试。原文权限独立；需要 source 阅读时显式授予 `sources.read`。
5. 源撤回直接传播；候选写入不替代事实。删除 operation 区分读取阻断、在线清理和备份保留。Mimic 自己记录 injected/usedRefs/回答效果，不能把 returned_count 当采用成功。

私有分区要求 Agent 和输出受众都被授权；群聊 audience 无法读仅限私聊的记忆。跨群共享使用管理批准的副本，目标不继承源消息和图权限。删除整个 Workspace 时须逐个明确分区；不可用一个数据面请求擦除调用者不可见的私有范围。

## 可运行验收入口

使用专用 PostgreSQL 与 Redis 测试库，测试建立/清除自己的 schema 与队列，不要指向业务库。默认 Go 测试在缺少外部测试配置时会跳过对应集成案例，因此须使用下面的显式入口确认外部依赖真正运行：

```sh
export MEMSTATION_TEST_DATABASE_URL='<dedicated-postgresql-url>'
export MEMSTATION_TEST_REDIS_URL='<dedicated-redis-url>'
bash scripts/integration/check-independent-memory.sh
bash scripts/ops/check-memory-recovery.sh
```

前者保存 contracts.jsonl/report.json；后者使用 `pg_dump`、`pg_restore` 做真实旧备份恢复和最新删除清单重放。客户端工具需匹配目标 PostgreSQL 主版本；不要把新客户端产生的服务器不支持的 SET 错误忽略掉。恢复测试需要可创建/删除测试 schema 的测试账号。

模拟 broker 可独立运行：

```sh
cd apps/server
# 从本地私密环境设置此值；不要复用数据面的 Agent key。
export MEMSTATION_MOCK_BROKER_CREDENTIAL='<local-test-credential>'
go run ./tests/contract/cmd/mockbroker -listen 127.0.0.1:18490
```

配套 fixture embedding 配置为实际 model `fixture-embedding-v1`、维度 `3`、指纹 `fixture-embedding-v1:3`，四个 alias 使用上文默认值。fixture 生成确定性输出，只用于验证管线，不能作为真实模型质量或容量证据。

[双群脚本](../scripts/integration/two-groups.py) 使用已配置的两个测试空间和业务凭据。设置 `MEMSTATION_URL` 及两组 `MEMSTATION_WORKSPACE_A/B`、`MEMSTATION_KEY_A/B`、`MEMSTATION_INTEGRATION_A/B`、`MEMSTATION_SOURCE_SPACE_A/B`，运行 `python3 scripts/integration/two-groups.py`。它检查 probe、重复摄取、封存、水位、检索和跨群 ID 不可见，不调用 Gateway，也不需要 admin token。serve、worker、scheduler 必须运行，凭据需 events.write、memory.read、sources.read。`--dry-run` 只检查样例流程，不证明环境已连通。

## 删除清单与备份恢复

1. 停止对外服务与后台写入，恢复数据库和必要附件。不要恢复一个较旧的删除清单覆盖当前清单。
2. 提供最新独立清单及原签名密钥，执行 `memstation seed --verify-recovery`。它验证链顺序、签名和数据库锚点，重放缺少的删除，清理缓存，不创建初始化账号。
3. 缺失、截断、不匹配或无法验证完整性的清单会阻止 readiness；先恢复正确清单，不使用初始化选项绕过。
4. 确认 operation 的 `read_blocked`、`online_cleaned`、`journal_persisted`，再开放服务。旧 generation、迟到任务和发布副本仍受失效状态约束。

`backup_status` 表示旧备份须按部署保留政策过期且恢复受清单约束，**不声称远端备份正文已经物理擦除**。部署方负责独立清单的可靠存储、备份保留时间和最终销毁。导出正文保留 24 小时，到期后台清除；读取导出时再次检查来源与 revision，失效即撤销结果。

验收应包含 PostgreSQL/Redis + broker 契约、独立启动 serve/worker/scheduler 后的双群样例、恢复 CLI，以及 Manager 桌面/移动端流程。真正接通 Mimic 时，使用实际适配器、受限 broker 凭据和真实部署完成双群、撤回、对账与恢复验收；模拟测试不能代替实际模型质量、容量或跨项目联调。
