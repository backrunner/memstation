# Workspace 检索、历史查询与切换

日期：2026-10-24。范围：已有 Personal Workspace。本文接续 [记忆编辑](workspace-memory-editing-2026-10-24.md)，不代表完整多工作区扩展已完成。

## 已落地

- 记忆文档列表改为数据库内容／ID 筛选和游标分页，不再只过滤最近 100 条。Manager 每页 30 条，接口最多 100 条；排序为 `updated_at DESC, memory_type DESC, id DESC`。同时间、多类型及中文 ID 有确定顺序，SQL 使用 C 排序与内存实现一致。
- 每个类型分支先限制 owner／Personal workspace／generation，再筛选和应用游标，最后 LIMIT；返回最多 `limit + 1` 条，额外一条仅用来判断下一页。`%` 和 `_` 是普通字符。软删除的 Key Memory 不再从文档列表出现。
- 游标绑定 owner、workspace、generation、规范化类型集合和查询条件。下一页固定第一页面对的 generation；切换条件、跨 owner 使用或损坏的游标返回 400。游标只是位置提示，每次 SQL 仍执行范围过滤，不作为权限凭证。
- 新增工作区内记忆检索页，复用现有 MemoryEngine、VectorStore、排名和历史 Fact 查询，不增加另一套排序算法。支持类型、结果上限、活动时间、事实有效时间 `as_of` 和系统获知时间 `known_at`。
- 历史查询只支持 Fact，结果直接显示不可编辑的历史快照。进入详情的动作标为“Open current record”，明确可能看到后续修正内容；不会把历史正文当作当前可编辑版本。
- Manager 通过 server load 和同源 BFF 执行查询，URL 保存条件；SSR 内部 POST 显式携带同源 Origin，仍经过原有会话和目标校验。无浏览器管理密钥。失败保留条件，不显示成空结果。
- 工作区切换器支持服务端搜索和分页，展示名称与 owner。保留当前栏目和通用筛选，清除记录、游标、generation、owner／subject 参数；详情返回目标工作区的对应列表。关闭、重新打开及迟到请求不会覆盖当前结果；沿用编辑器的忙碌拦截和离开草稿提醒。
- 历史筛选默认折叠，已选条件保持可见并参与提交。桌面／移动端复用共享表单，主控件 44px、移动输入 16px；短屏切换弹窗可滚动。

## 接口

以下接口使用管理员服务端会话，workspace 必须存在，且与可选 `user_id` 相符：

| 接口 | 参数及结果 |
| --- | --- |
| `GET /api/admin/workspaces/:workspace/memory/documents/page` | `q`、`kind` 逗号分隔、`limit` 1–100、`after`、`generation_id`；返回 `{items,next_cursor,generation_id}` |
| `POST /api/admin/workspaces/:workspace/memory/search` | URL 只接受 `user_id`、`workspace_id`、`generation_id`；JSON 接受 `query`、`top_n` 1–100、`memory_types`、`date_from`、`date_to`、`as_of`、`known_at`；返回现有 SearchResult |

检索 JSON 拒绝未知字段和尾随对象，最多 16 KiB，查询最多 1,024 字节。时间使用带时区的 RFC3339；活动时间起点不能晚于终点。历史查询未指定类型时自动限定 Fact，显式指定其他类型返回 400。搜索错误不暴露底层数据库／上游错误。

普通检索的空类型集合包含分段和 Key Memory；历史查询复用现有有界候选与排序，不声称穷举所有历史事实。结果数量是实际返回的排名集合大小，不是完整命中总数。

新 Manager 路由：`/t/:target/w/:workspace/memory/search`。旧文档 GET 和旧页面保留兼容，新分页接口不改变旧调用方的响应结构。

## 一致性与剩余工作

分页固定 generation，但不是数据库跨请求快照；并发编辑可能让记录移动到其他页。页面提示刷新首屏，未承诺并发编辑期间无漏项／重复。内容子串筛选尚未加入专用全文或 trigram 索引；生产规模延迟仍须另测，不能把有界返回行数称为有界数据库扫描量。

本批未修改 schema、迁移或生成代码。历史快照从已有 fact-history migration 开始，不补造之前的历史。检索可用性和语义效果仍受部署的 embedding／rerank 配置影响；本地确定性验证不是模型质量或生产 SLO 证明。

后续已完成会话与 Key Memory 服务端分页、主导航与旧记录入口迁移，详见 [分页与导航记录](workspace-pagination-2026-10-24.md)。高级审计／重建兼容视图、其他 Workspace 类型、Subject／SourceEvent／Run、共享／私有可见性和 GraphQL／gRPC 新管理契约仍待实施；不能提前开放 group／project。

## 验证

验证记录在 `.artifacts/workspace-search-20261024/`，运行脚本为 `scripts/test/workspace-search-smoke.cjs`。测试仅使用隔离 PostgreSQL／Redis 和临时 Go 服务，未修改已有用户数据库。

- 隔离 PostgreSQL 16.14：17 项现有迁移完整重放；旧记录筛选、跨类型同时间／中文 ID 分页、跨 owner／generation 游标、字面通配符与删除 Key 回归通过。
- Go：配置该测试数据库后的 `go test ./...`，SQL／Memory／HTTP 三包 `-race`，仓库 format 和 lint（0 issues）通过；最后的普通检索默认包含分段调整另跑 HTTP 回归通过。
- Manager：lint／check 无错误和警告，27 项 Vitest（含真实 Redis 会话）通过；完整 Chromium 54 + WebKit 30 = 84 项 E2E 通过，最终筛选折叠／切换器响应与草稿专项再验证。生产构建通过。
- 生产构建 + 真实 Go／PostgreSQL／Redis：页间无重复、超过 100 条仍找到旧记录、参数失败可恢复；历史原文在事实修正后保留，链接打开当前新正文；跨工作区游标 400、body owner 覆盖 400、切换清代次与匿名 SSR 无私有结果通过。
- 浏览器实际检查：桌面、WebKit 320／390／844px，截图存于上述 artifact 目录；无页面横向溢出，主输入至少 44px／16px，短屏 picker 可滚动。浏览器模拟不等于物理手机验证。
- 无 schema／生成代码变更，本批未重跑 Atlas lint／生成器／备份恢复；未运行远端 CI、外部模型质量评测或生产规模性能测试。未提交、推送或部署。
