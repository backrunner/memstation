# Personal Workspace memory editing — 2026-10-24

本批把记忆文档修正与 Key Memory CRUD 接入已存在的 Personal Workspace。管理员沿用服务器会话和同源 BFF，URL 中的 Workspace 决定 owner；不开放 group/project，也不表示多群画像或完整 P1–P6 已完成。

## 接口与写入规则

以前缀 `/api/admin/workspaces/:workspaceID/memory` 提供：

| 方法／路径 | 行为 |
| --- | --- |
| `PUT /documents/:kind/:id` | 修正文档正文；需要 `content`、`generation_id`、`expected_updated_at` |
| `GET /keys`、`GET /keys/:id` | 工作区 Key Memory 列表／详情，列表最多 100 条 |
| `POST /keys` | 新建，参数 `key`、`value`、`category`、`review_status`；已有有效 key 返回 409 |
| `PUT /keys/:id` | 更新 value/category/review_status，需要 `expected_updated_at`，不允许改 key |
| `DELETE /keys/:id` | 按稳定 ID 删除，请求 JSON 必须带 `expected_updated_at` |

`expected_updated_at` 必须原样使用读取响应的 RFC3339Nano 字符串。Manager 不经 JavaScript Date 转换，避免丢失 PostgreSQL 微秒精度。数据库在更新条件中比较快照；写入时间保证向前推进。Key Memory 的创建／更新／删除在同一数据库 key 锁内核验 ID 与快照，旧 REST、工具和 Worker 的 Upsert 也经过同一锁，能使新接口的旧快照失效。删除后重建不能被先前页面删除；PostgreSQL 保留既有软删除记录并复用其 ID，时间前置条件继续隔离旧页面。

条件校验、业务写入、embedding revision、outbox 和审计处于同一 PostgreSQL 事务；审计失败会回滚。事实编辑沿用数据库历史快照触发器。输入是严格单个 JSON 对象，未知字段／超量请求／空正文／无前置条件被拒绝；正文上限 128 KiB，请求体上限 256 KiB。审核状态沿用 `pending / approved / rejected`。可显式清空 category。

文档支持 summary、impression、fact、event、task、procedure、artifact、entity、agent_action；segment 是保留来源，不提供编辑。被替代的历史 Fact 在 UI 和数据库条件中均拒绝修改。generation 必须与记录一致；明确选中的非 discarded generation 可以修正，不暗中改写 active generation 的其他记录。已登记 discarded generation 使用生命周期锁拒绝写入。Key Memory 不属于 generation，带 generation 筛选的 key 请求会被拒绝。

新接口保留既有 Personal 范围与会话保护；不存在／跨 owner 记录返回 404，过期快照返回 409，普通 Agent Key 没有管理员权限。旧接口保持兼容，不代表旧接口已经要求版本前置条件。开发内存 fallback 也做互斥条件校验和审计，但持久 outbox／事务回滚保证属于 PostgreSQL 路径。

## Manager

文档详情提供 Edit memory；`/t/:target/w/:workspace/keys` 提供列表、创建页和详情编辑。编辑优先显示正文，分类与审核状态其次，删除操作需要二次确认；有草稿或冲突时不能删除。新建使用完整可滚动页面，窄屏和短横屏能到达最后字段与保存按钮。

草稿仅存于当前标签页的 sessionStorage，键包含 Target ID、服务地址、owner、workspace、generation、文档类型和 ID。刷新、离开后返回可恢复；跨范围不会复用草稿。409 后保留输入，必须加载当前内容并明确选择“Use draft against this version”后再保存；不会自动重试覆盖。新建同名 key 提示另选名称，不变成隐式覆盖。保存期间阻止重复操作和页面切换，组件销毁后的迟到响应不修改当前视图；存储不可用时保留离开警告。

此次修复 Manager HTTP 转发中带 JSON 的 DELETE 缺少消息长度的问题：按已限制大小的实际字节数重算 Content-Length，不信任调用方提供的长度。增加真实 HTTP 转发回归测试。

列表当前是相关性／时间排序的前 100 条，文字过滤只作用于这批结果；完整分页与检索仍待实现。

## 验证

artifact 目录 `.artifacts/workspace-memory-edits-20261024/`，汇总 `validation.json`，真实运行报告 `runtime-validation.json`。

- Go 全量 `go test ./...`、format、lint（0 issues）通过；SQL store／Memory service／HTTP 在真实 PostgreSQL 上的 race 通过。
- 全部 9 类可编辑文档的 PostgreSQL 写入通过；12 并发文档编辑、12 并发 key 创建、12 并发 key 编辑均只有一次成功。旧接口写入使快照失效；跨 owner／Workspace、generation 不匹配、重复创建、过期删除和删除后重建保护通过。
- 真实数据库注入审计失败，文档、key 更新、key 删除及 outbox 均验证回滚；HTTP 契约覆盖未知字段、多 JSON、超量载荷、前置条件缺失、无效审核值、错误 owner 和 generation。
- PostgreSQL 16.14 的隔离空库重放既有全部 17 项迁移。本轮未新增 schema、迁移或依赖，没有重跑 Atlas／代码生成门禁。
- Manager lint/check 0 errors／0 warnings，25 项 Vitest（含真实 Redis 和 DELETE 消息长度）通过；完整 Chromium 52 + WebKit 28，共 80 项 E2E 通过；生产构建通过，仍有既有大 chunk 提示。
- 生产 Manager + Go/PostgreSQL/Redis 联调通过：文档保存、409 草稿保留、显式重载／重试、刷新持久化、跨空间写入 404、Key Memory create-only／分类清空／旧删除冲突／再次加载后删除、准确创建删除审计计数、无会话 SSR 隔离。
- 实际查看桌面冲突界面和 WebKit 320／390px、844×390 运行截图；无横向溢出，字段 ≥44px、文本 ≥16px，短横屏保存按钮可滚动到达。浏览器仿真不是实体手机验证。

首次 E2E 暴露 DELETE 请求体未正确标记长度导致 400，已修复并通过真实 HTTP 单测、全量 E2E 和真实 Go 服务验证；文档 fixture 缺少 generation 的测试数据也已补齐。不是通过放宽冲突／范围断言使测试通过。

运行入口：`scripts/test/workspace-memory-smoke.cjs`。需要隔离 Go/PostgreSQL/Redis 和生产 Manager build，提供 `MEMSTATION_TEST_TARGET_URL`、`MEMSTATION_TEST_ADMIN_TOKEN`、`MEMSTATION_TEST_MANAGER_REDIS_URL`、`MEMSTATION_TEST_WORKSPACE_EDIT_FIXTURES`。fixture 为两个不同 owner 的数组，每项包含 owner/workspace/document/generation；脚本仅允许 `workspace-edit-` 前缀的待编辑 owner，会修改测试文档并创建／删除测试 key，不能用于已有用户数据库。

## 下一步

统一范围 picker、旧路由迁移、Workspace 内检索／历史筛选与分页；随后推进非 Personal 可见性分区、Subject／SourceEvent／Run、群聊摄取和画像验收。当前不要据此开放 group/project 创建。

本批未操作已有用户数据库、远端 CI、提交／推送或部署。临时生产 Manager、Go 进程和隔离 PostgreSQL／Redis 容器在验证后关闭；保留本地报告与截图。
