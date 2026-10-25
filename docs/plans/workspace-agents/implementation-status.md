# 独立记忆服务实施与验收记录

本轮交付 MemStation 侧 MS-01～10、数据/管理 transport 与 Manager 流程。Mimic 实际适配器、AI broker 和两项目部署联调仍为外部验收项，不能把本地模拟通过表述为两项目已经正式接通。

既有未提交的精排、评测和模型配置工作保留；实施前文件 hashes 与 diff 在本地 `.artifacts/mimic-integration/baseline/`。依照 [架构](architecture.md) 与 [当前契约](contracts.md) 实现，不扩展完整 Run/远程 Agent 调度。

## MS 验收矩阵

| 编号 | 已实现 | 本地验收证据与边界 |
| --- | --- | --- |
| MS-01 | group/project/Personal、统一 AccessContext、Agent + 受众私有分区、SQL/vector/图/缓存/来源/附件/generation 隔离；响应前授权与来源复核 | `TestWorkspaceMemoryDataIsolation` 覆盖内存和真实 PostgreSQL；独立管线覆盖同 owner 双群、同名人物、私聊/群聊、撤权/撤回发生在精排中途。私有管理读取须显式分区并审计 |
| MS-02 | Integration、来源空间和观察权限；事件/消息/receipt/outbox 同事务；整批幂等；多个观察者共享来源身份 | `TestIndependentIngestionAtomicIdempotency`；真实 PostgreSQL/Redis/broker 管线无需聊天回复即可 indexed，同键异 payload 无部分提交 |
| MS-03 | 可信 ActorIdentity/WorkspaceMember、speaker/mention/reply/quoted author/revision、Unicode code point 证据；Actor 独立事实身份 | `TestIndependentIngestionActorAndUnicodeReferences`、拒绝矛盾 quote author、可信引用作者校验、analysis Actor 身份/片段测试；未知作者不归 owner |
| MS-04 | 5 分钟/100 条/输入预算窗口、CAS finalize、迟到新窗、编辑修复窗、generation 连续水位、任务重试/DLQ 重放 | 并发封存、空窗口、source repair SQL 测试；真实 Redis 最大重试耗尽进入 archived，管理重放原任务并完成；失败不能跨过 indexed 水位 |
| MS-05 | tombstone/read barrier、派生隔离、剩余来源重建、导出/副本失效、迟到任务与 generation 激活防复活 | source/Actor 删除、删除先于创建、publication 恢复测试；真实 pg_dump/pg_restore 证明旧正文先恢复，再由最新独立清单重新阻断/清理 |
| MS-06 | 授权 node ID 详情、独立 source 权限、trace/provider 分数定义、水位与降级；多 transport 复用服务 | HTTP/gRPC 受众/权限测试，64 位 revision/receipt/recall_trace；导出下载复查节点权威 revision。没有 sources.read 时不返回 segment/证据原文 |
| MS-07 | direct/broker 两模式、四个受限角色 alias、独立凭据、trace/job/attempt/call、版本化 rerank、embedding profile、usage 对账 | 模拟 broker 验证角色拒绝、不同真实重试调用、unknown、重定向拒绝、指纹不匹配、用量引用不二次计费；实际 Mimic broker 联调待外部交付 |
| MS-08 | candidate、审核/修正 CAS、明确推断确认、Actor 主体、群习惯证据、人工记录保留；批准发布副本与修正后重新批准 | candidate 并发冲突、发布无隐式原文/人物权限、禁止再发布；generation 重建保留人工修正/副本并检查并发 revision |
| MS-09 | capabilities 1.0、有效授权能力、memory-disabled；HTTP/typed gRPC/GraphQL 管理语义；旧 Personal 路径 | 故障关闭能力与 transport 契约测试；既有 Gateway/Personal 回归。capabilities 的 publishing/review 只对管理入口开启，直连不虚报 broker usage |
| MS-10 | scoped erase/export 进度、独立签名删除清单、恢复 readiness 屏障、导出到期清理、broker ledger/reconcile | 删除正文清理、Actor 别名清理、导出 revision/到期测试；真实旧备份恢复；备份物理到期由部署保留政策负责，状态不声称远端备份已擦除 |

## Manager 与交付入口

现有 Workspace 页面补齐 group/project 创建、Agent/grant、Integration、私有分区受众、Actor/来源 revision、摄取水位、失败任务重放、候选修正与 CAS 草稿、发布预览/更新/撤回、删除/导出、broker 对账和 generation 重建/激活/丢弃。列表分页分别保存游标，避免一个集合截断另一个集合。组件沿用服务端 session、共享表单、CollapsibleSection 和移动端布局。

- [部署、broker、迁移与恢复说明](../../independent-memory-integration.md)
- `scripts/integration/check-independent-memory.sh`：真实 PostgreSQL/Redis 契约入口与机器可读报告。
- `scripts/integration/two-groups.py`：使用业务凭据的双群运行样例，不调用 Gateway。
- `scripts/ops/check-memory-recovery.sh`：真实 pg_dump/pg_restore + 独立删除清单；支持明确标记的 dry-run。
- `apps/server/tests/contract/cmd/mockbroker`：可独立启动的确定性 broker。

## 验证记录

验证使用专用本地 PostgreSQL 数据库、每测试 schema、独立 Redis 队列，不修改现有开发/评测数据库。

- Go 全量含真实 PostgreSQL/Redis 管线；安全、SQL、worker、broker 和集成路径另做 race 检查。最终日志与脚本报告在 `.artifacts/mimic-integration/`。
- Manager 完整 Playwright **110/110** 通过，含 Chromium、WebKit 和移动 WebKit；桌面/移动独立记忆流程截图已人工查看。`pnpm lint`、`pnpm test`、生产 build 通过；构建保留现有大 chunk 提示。
- Go format、golangci-lint、生成一致性按仓库入口检查。生成检查使用临时 Git index 对比当前改动基线，保留原工作区和真实 index。
- Atlas CE 兼容工具 lint 和全量迁移 dry-run；新增迁移在真实集成 schema 重放。旧 Docker 运维恢复脚本做 dry-run；本轮真实恢复通过 PostgreSQL 16 原生 dump/restore 工具完成，不把 Docker dry-run 当真实 Docker 演练。
- 集成脚本与恢复脚本保存 JSON/JSONL；模拟输出不证明真实语义质量、生产吞吐量或成本。没有为本轮加入质量 benchmark CI 红线。
- 独立启动编译后的 serve、worker、scheduler 与模拟 broker，在另一个空 PostgreSQL 数据库完成迁移、初始化、`seed --verify-recovery`、管理 API 配置双群和 `two-groups.py` 实际运行。首次进程验收暴露模拟 broker 遗漏摘要评分 schema；已修复模拟端，并在自动集成测试中启用与真实装配一致的 Scorer，未放宽生产校验。最终运行报告为 `.artifacts/mimic-integration/runtime3/report.json`。

外部未验收项：实际 Mimic 适配器/broker、部署凭据限制、真实上游模型与生产容量、远端 CI。完整 Run 管理和远程 Agent 调度不在本次范围。单实例的独立清单需共享持久卷，跨机分布式日志服务没有在本轮交付。

## 提交前复查

本次提交在独立检出中验收，未纳入同时进行的 LOCOMO、来源切片、候选融合和模型配置 UI 工作。必要的 finish_reason 与有界截断重试随 broker 文本适配提交。

复查修复：历史检索保留合法时间投影，同时继续阻断已撤回身份；详情正文与 revision 使用同一查询快照；可信删除不要求平台增加消息 revision；Actor 名称仅从当前可见分区来源读取；broker 无效 embedding/rerank 结果显式标记 degraded，并保留真实完成调用的费用引用；补齐 Key Memory 与节点索引的 Ent 声明。

新增回归包括 `TestHistoricalRecallPreservesProjectionButRequiresLiveIdentity`、`TestInspectReturnsBodyAndRevisionFromSameSnapshot`、`TestIndependentIngestionDeletionDoesNotRequireNewMessageRevision`、`TestActorOverviewDoesNotLeakPrivateDisplayName` 和 `TestRecallExposesDegradedBrokerResultWithoutDiscardingFallback`。独立版本 Manager 110 项 E2E 通过；本地检查日志在 `.artifacts/mimic-review/`。
