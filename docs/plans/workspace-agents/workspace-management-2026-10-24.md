# Personal Workspace management — 2026-10-24

本批交付 P4/P5 的第一个真实工作区管理入口：已有 Personal 工作区目录、名称和策略编辑、工作区内 Agent 管理、包含服务和工作区的可恢复 URL。仍未开放创建其他类型工作区，也不代表全部 P1/P4/P5 已完成。

## 后端契约

| 接口 | 行为 |
| --- | --- |
| `GET /api/admin/workspaces` | 管理员显式跨 owner 目录；支持 `user_id`、`q`、`after`、`limit`；默认 50、上限 100；真实 lookahead 游标；不通过读取创建虚构 owner 或 Workspace |
| `GET /api/admin/workspaces/:id` | 管理员查询已有空间；可指定 `user_id` 做额外归属校验；错误 owner／不存在／不支持的类型均拒绝 |
| `PUT /api/admin/workspaces/:id` | 必填 `user_id`、`name`、`capabilities` 数组、`expected_policy_version`；只允许更新当前 Personal 的名称和策略；空数组表示没有普通调用能力，遗漏／null 不代表清空 |
| `GET /api/v1/workspaces` | Agent 凭据读取真实名称、时间、策略版本和有效能力；不返回其他空间或完整管理策略 |

管理接口使用现有管理员会话和 `Cache-Control: no-store`。REST Handler 只解析契约；服务验证名称、能力和 Personal 边界；store 负责 SQL、行锁和审计。名称／策略更新共用已有 `policy_version` 做 CAS，成功时递增一次并更新 `updated_at`。修改状态、类型、归属、创建、归档、删除均未开放。没有新增依赖或数据库迁移。

有效能力保持 `credential ceiling ∩ current grant ∩ workspace policy`。收紧策略影响已有凭据后续鉴权和已有工具续接检查；重新放开不会突破凭据或 grant 上限，也不会恢复已撤销凭据。管理员会话不受 Agent 策略影响。配置开发 Key 在正式应用装配中也查询当前 legacy Agent、grant 和工作区策略，不再绕过本轮开放的策略编辑；无 repository 的嵌入式 Authenticator 兼容路径保持独立。

SQL 更新与 `update_workspace` 审计在同一事务，审计包含前后名称、能力和版本；审计失败整体回滚。签发凭据原有事务中的 workspace share lock 与更新行锁配合，拒绝根据旧策略批准的签发。目录只展示确定性 Personal ID，并排除尚不支持的其他空间。

## Manager

新增路线：

- `/t/:target/workspaces`：目录、搜索、owner 筛选和游标分页，筛选保留在 URL。
- `/t/:target/w/:workspace/settings`：名称、工作区权限、冲突处理及折叠元数据。
- `/t/:target/w/:workspace/access[/:agent]`：复用已有 Agent 管理／凭据 UI，owner 来自已验证 Workspace，链接持续保留 Target 和 Workspace。

Target 和 Workspace 初始数据使用 server load，通过既有同源 BFF 和服务端会话请求。未知服务不回落到最近服务；已登记但尚未保存的深链接服务会加入连接偏好，仍须验证该服务会话。缺少／过期会话显示重连，找不到空间或旧服务不支持接口显示明确失败，不伪装空目录。

`ScopeContext` 明确包含服务、空间及其 owner。工作区草稿按 Target ID、服务地址、owner、workspace、资源保存到当前 tab 的 sessionStorage，草稿携带原策略版本，不存凭据。切换时可保留草稿或继续编辑；冲突保留输入，先读取当前版本、检查策略，再明确使用草稿重试。晚到路由请求不能替换当前空间；Agent 页面按作用域重新挂载并取消旧请求。保存后同步刷新父级 server load，避免栏目切换显示旧策略。

保留共享表单和按钮尺寸、44px 移动输入、可横向滚动并露出当前项的栏目、分隔线目录；普通状态不用装饰色块。短屏保存按钮随页面滚动，不盖住字段。

## 验证

- 隔离 PostgreSQL 重放全部 17 个已有迁移；新测试在 PostgreSQL 和内存 fallback 均通过。覆盖 12 个同版本并发更新只有一个成功、跨 owner 拒绝、真实游标、非法策略、旧凭据能力收紧、旧策略签发拒绝、凭据上限保持、返回对象无可变别名、审计条数匹配和审计失败事务回滚、非 Personal 不可见。
- Admin／proxy HTTP 契约测试、完整 Go 测试、相关 race、format、lint 和生成一致性通过；生成文件在本轮 `go generate ./...` 前后无变化。开发 Key 策略／停用回归通过。
- Manager lint/check：0 errors、0 warnings；24 项 Vitest 含真实 Redis 通过；production build 通过。完整 Chromium／WebKit E2E **70 项通过**，包含新增 8 项工作区测试。
- 生产构建 + 真实 Go／PostgreSQL／Redis：登录、server-load、修改名称与权限、刷新持久化、审计、旧 Key 被拒绝、真实 Agent 有效权限、全新浏览器无法复用他人工作区数据均验证。
- 实际查看 1440px 桌面、390px、320px 和 844×390 WebKit 截图；未发现页面横向溢出，输入高度至少 44px，当前栏目可见。这是浏览器仿真，不是真实手机设备验证。

可复跑入口：`scripts/test/workspace-management-smoke.cjs`。先构建 Manager，准备隔离且已迁移的 Go 服务和 Redis，再使用与 Manager 基线一致的 Node 执行。必填 `MEMSTATION_TEST_TARGET_URL`、`MEMSTATION_TEST_ADMIN_TOKEN`、`MEMSTATION_TEST_MANAGER_REDIS_URL`；可选 Manager 端口及 artifact 目录。脚本创建唯一测试 owner、启动并退出一个生产 Manager；测试记录留给隔离数据库检查，不改已有 owner。不要把 bootstrap token 写入提交文件。

本轮证据目录：`.artifacts/workspace-management-20261024/`，含 `validation.json`、`runtime-validation.json`、迁移记录、生成一致性和运行截图。没有操作已有用户数据库，没有提交／推送／部署，也没有声称远端 CI、生产容量或容器部署通过。

## 后续边界

旧 Memory、Conversation、Usage、运维和 Playground 页面仍采用原路由；本批没有把它们伪装成已完成 Workspace 隔离的视图。全局 Workspace picker、全部旧路径重定向、完整读取／写入范围、GraphQL／gRPC Workspace 管理、私有／共享分区、SourceEvent／Subject／Run、群聊摄取与空间归档删除仍按总计划推进。

下一批应先补齐剩余实体和读取／写入的统一范围契约，再迁移记忆和对话页面，验证后才开放 project／group 等新工作区。当前目录管理的是已有 Personal 空间，不能据此宣称多群记忆隔离已经上线。

后续进度：同日已接入 [Workspace 记忆和对话浏览](workspace-browsing-2026-10-24.md)。旧页面保留，新范围视图先提供读取和来源跳转，编辑及完整路由迁移继续推进。
