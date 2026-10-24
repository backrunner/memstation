# Personal Workspace browsing — 2026-10-24

本批把已有 Personal 工作区的记忆文档、对话及来源跳转接入 P4/P5 的作用域 URL。它交付浏览链路，不代表完整 Workspace 读写迁移或多群记忆隔离完成。

## 读取契约

以下管理员会话 GET 接口以 `/api/admin/workspaces/:workspaceID` 为前缀：

| 路径 | 内容 |
| --- | --- |
| `/conversations` | 最近对话列表，默认 100 条，`limit` 范围 1–100 |
| `/conversations/:id` | 对话、消息、当前或指定 generation 的分段、来源 part/artifact |
| `/conversations/:id/memory` | 对话在指定或当前 generation 的关联记忆 |
| `/conversations/:id/sources` | 保留来源快照 |
| `/memory/documents` | 文档列表，支持 `kind`、`generation_id`、`limit` |
| `/memory/documents/:kind/:id` | 文档、关系、证据及历史事实标识 |

先通过 identity service 解析数据库中已存在的 Workspace，再绑定 data scope，复用已有 admin/memory/generation service 和 repository。缺失空间不触发自动创建；非 Personal 保持关闭。`user_id` 只能额外校验 owner，不能替换路径中的工作区；不一致的 `workspace_id`、重复参数、未知筛选或越界 limit 被拒绝。所有响应 `no-store`，普通 Agent Key 不能取得管理员权限。

对话记忆和快照接口在读取集合前验证对话归属，避免把访问其他 owner 的对话返回为成功的空集合。文档、来源、图关系、历史与 generation 继续经过已落地的 Personal store 范围校验。此次未新增数据库迁移或依赖，也未更改旧接口契约。

## Manager

- `/t/:target/w/:workspace/memory[/:kind/:id]`
- `/t/:target/w/:workspace/conversations[/:id]`

页面初始读取在 `+page.server.ts` 完成，使用现有登记 Target、加密会话及同源 BFF。Workspace 父级加载失败时不发起记录读取；查询参数不能改换 owner；不存在记录、旧服务不支持、连接失败与真实空集合有不同提示。重试和重新连接会刷新读取依赖。

列表提供保存在 URL 的类型／文本筛选；文字明确说明文本筛选只作用于最近 100 条，不冒充全量检索或完整分页。全部文档类型显式包含 summary、segment、fact、impression、event、task、procedure、artifact、entity、agent_action。Key Memory CRUD 仍在旧页面。

记忆与来源对话、关联文档及当前事实的跳转保持 Target、Workspace 和 generation。详情优先显示内容、消息和证据，运行字段及原始记录按需展开。浏览页面不展示尚未接入范围契约的修改、重建、重试任务按钮。设置和 Agent 页继续使用已有写入流程。

栏目保持 44px 触控高度，并在切换时滚动露出当前项；窄屏筛选换行、长 ID 和内容可断行。SvelteKit 管理路由请求生命周期，旧页面的迟到响应不能替换新 Workspace。

## 验证记录

验证 artifact 位于 `.artifacts/workspace-browsing-20261024/`，汇总见 `validation.json`。

- 完整 `go test ./...`、format、lint（0 issues）通过；PostgreSQL 16.14 空库重放全部 17 项既有迁移，store/sql、HTTP、admin、memory 相关 race 通过。本轮没有 schema／生成代码变更。
- 新 HTTP 契约覆盖合法列表／详情、跨 owner 文档／对话及其子集合、缺失空间不创建、重复／不支持参数和 limit 越界；原 Workspace 设置与代理发现回归通过。
- Manager lint/check 0 errors／0 warnings，24 项 Vitest（含真实 Redis）通过；完整 Chromium 50 + WebKit 26，共 76 项 E2E 通过。最后的内容优先布局和详情标识调整后，工作区专项 14 项再次通过，生产构建通过。
- 生产 Manager + 真实 Go／PostgreSQL／Redis：读取两个隔离 owner 的文档和消息、来源双向链接、刷新、外来文档／对话／子集合 404、错误 owner URL、无会话浏览器 SSR 隔离均通过；原名称／策略编辑、审计和已有凭据收紧继续通过。
- 实际查看桌面与 WebKit 320px、390px、844×390 运行截图；无横向溢出，输入至少 44px，长 ID 不挤占记忆主内容。普通 generation 元数据使用文本，不使用常规状态色块。

联调初期两次 WebKit 连续整页重载分别停在页面 load／截图字体就绪等待，未发现 PostgreSQL 等待查询。尺寸验证改为独立浏览器上下文，继续保留页面可见、触控尺寸和截图检查，最终通过；这不被计作浏览器字体加载缺陷已修复。快速路由切换和迟到响应另外由 Chromium／WebKit E2E 覆盖。生产构建仍有既有的大 chunk 提示，未把它当作本轮性能验证。

真实服务脚本沿用 `scripts/test/workspace-management-smoke.cjs`，额外传入 `MEMSTATION_TEST_WORKSPACE_BROWSE_FIXTURES` 可覆盖浏览流程。文件为至少两个不同 owner 的 fixture 数组，每项包含 `owner`、`workspace`、`conversation`、`document`、`content`、`source_text`、`generation`；文档为已有 summary。只对这些记录执行读取，原脚本策略编辑仍使用新创建的唯一测试 owner。必须使用隔离测试数据库，fixture 不包含凭据。

## 后续进展

记忆编辑与 Key Memory 已在下一批接入，见 [记忆编辑与 Key Memory](workspace-memory-editing-2026-10-24.md)。以下保留本批完成时的后续范围。

## 下一步

补齐 Workspace 内的记忆编辑、Key Memory、检索／历史筛选及版本冲突处理，再推进统一 picker、旧路径迁移、分页查询和来源／任务完整视图。多空间私有／共享分区、Subject／SourceEvent／Run、群聊摄取及画像仍是后续门禁；本轮不能据此开放 group/project 创建。

会话列表仍复用现有读取实现，响应条数上限不等于数据库扫描上限。生产数据量下的分页、性能和容量验证须独立完成。本轮不涉及已有用户数据库、提交、推送或部署，浏览器仿真不能视为真实手机验证。
