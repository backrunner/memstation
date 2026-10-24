# 会话、Key Memory 分页与导航迁移

日期：2026-10-24。接续 [检索与切换](workspace-search-2026-10-24.md)，仍只开放已有 Personal Workspace。

## 列表契约

新增管理员会话接口，不改变旧接口的响应结构：

- `GET /api/admin/workspaces/:workspace/conversations/page`
- `GET /api/admin/workspaces/:workspace/memory/keys/page`

两者接受 `q`、`limit`（1–100，默认 100）、`after`，以及可选的 owner／workspace 一致性约束；返回 `{items,next_cursor}`。Manager 每页 30 条。重复参数、损坏／不匹配游标、不适用的 generation／kind 和未知参数返回 400；错误 owner、未开通空间和不支持的空间不可读取。

筛选在数据库 LIMIT 前执行，按 `updated_at DESC, id COLLATE "C" DESC` 排序；游标绑定 owner、Personal workspace、列表种类和规范化查询条件。会话可按 ID／provider／protocol／mode／结束状态子串查找；Key Memory 可按 ID／key／value／category／review status 查找。`%`、`_` 作为字面字符处理，不作为 SQL 通配符。Key Memory 排除软删除记录，独立于记忆重建 generation。

会话先取有界 parent 集合，再统计这些会话的 message count／token usage；不会加载所有会话、消息正文或逐条调用详情接口。Memory 和 Conversation store 均提供 PostgreSQL、内存和 hybrid 实现，service 负责校验和游标，handler 不直接访问 SQL。

分页不是跨请求数据库快照。新消息或编辑会改变排序位置，页面明确提示刷新首屏；不承诺并发更新期间绝对无重复／遗漏。子串查询未新增全文索引，有界返回不等于有界扫描；本地正确性验证不等于生产规模性能证明。

## Manager

- 会话与 Key Memory 列表由 server load 获取完整范围筛选结果，移除最近 100 条的浏览器筛选。URL 保留查询和游标，刷新／后退可恢复；修改条件回首屏，错误游标保留输入并提供 First page。
- 复用共享分页组件，下一页／首屏链接至少 44px，320／390px 不横向溢出。切换工作区清除游标并保留通用查询。
- 侧栏、Dashboard、用户页、用量、任务来源及旧记忆／会话记录链接改为工作区入口。当前工作区内的主导航保留空间；不同时高亮 Workspaces 和子栏目。
- `/t/:target/open/...` 是兼容入口解析器：显式 owner 解析到其实际 Personal workspace；没有 owner 且有多个空间时进入选择页，并保留要打开的栏目／记录。不存在的 owner 不回退 admin；不猜测或自行创建空间。
- 旧复数记忆类型映射为新单数类型，详情保留 generation；当前页游标和错误 workspace 提示不能跨范围带入。摘要／Fact／检索结果 DTO 保留服务端返回的 owner，避免旧列表丢失记录归属。

旧 URL 仍作为兼容视图保留。新详情提供 **Review history and metadata**／**Processing and rebuild controls**，继续访问既有审计、关系和重建能力；这些链接通过 `manager_target` 固定注册服务并携带明确 owner，避免与审计记录筛选的 `target_id` 冲突。Manager 校验目标、选择对应连接，在目标会话就绪前不渲染旧页面业务数据；换服务离开当前记录，避免相同 ID 被解释为另一个服务的数据。没有把这些高级兼容页面声称为全部迁入新的 server-load 页面。

## 验证与边界

本地 artifact：`.artifacts/workspace-pagination-20261024/`。真实服务脚本：`scripts/test/workspace-pagination-smoke.cjs`，仅允许使用临时的 `workspace-page-*` fixture owner。

- 隔离 PostgreSQL 重放现有 17 项迁移；内存和 PostgreSQL 测试覆盖超过 100 条旧记录、同时间／中文 ID、消息与用量统计、跨 owner／列表游标、字面通配符、非法参数和删除 Key。
- Go 全量测试、相关 SQL／Admin／Memory／HTTP 包 race、仓库格式及 lint 通过；Manager 30 项单测包含真实 Redis 会话测试。
- Manager lint／Svelte 检查无错误和警告，production build 通过；构建仍提示既有大于 500 kB 的 chunk。
- 生产构建 + 真实 Go／PostgreSQL／Redis：两个 Personal owner 各 106 条会话和 106 条 Key，验证 30 条分页、旧记录筛选、跨 owner 游标拒绝、错误页恢复、明确 owner 解析和多空间选择、条件删除后不再返回。审计／重建兼容链接固定服务，匿名 SSR 不包含记录正文。
- 真实服务浏览器核对桌面、WebKit 320／390px 截图，无页面横向溢出，操作按钮至少 44px。模拟移动浏览器不是物理手机验证。
- 完整 Playwright 回归 93 项通过、1 项发现审计筛选与服务参数冲突；改用独立 `manager_target` 后，审计筛选、跨服务导航、历史修正、来源重建和 Dashboard 的 7 项相关回归全部通过。完整与修复后报告分别见 `manager-e2e.log`、`manager-e2e-fix.log`。

本批无 schema、迁移或生成代码变更，未重跑生成器／Atlas lint／备份恢复；未操作已有用户数据库，未运行远端 CI、提交、推送或部署。群／项目空间、Subject／SourceEvent／Run、共享／私有可见性、其他 transport 的新管理契约和高级兼容视图的完整迁移仍待后续实现。
