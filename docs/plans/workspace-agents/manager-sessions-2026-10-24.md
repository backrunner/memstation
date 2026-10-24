# Manager 服务连接落地（2026-10-24）

本批实现 P4 的服务端连接前置层：服务器登记 Target、服务端持有加密会话、同源管理／Gateway 请求，以及对应连接 UI。Workspace picker、作用域路由、其他工作区写入和群聊摄取仍未开放，不能据此认为 P4 或完整工作区功能已经完成。

## 已实现

- SvelteKit server load 读取服务器 Target 登记，浏览器只能选择登记服务。旧 localStorage 的地址和名称按登记地址迁移，只保存偏好，不保留 admin token。
- 浏览器持有随机 Manager cookie；Go 管理员 cookie 以 AES-256-GCM 加密保存在 Redis，按浏览器、目标 ID 和地址绑定。生产必须配置 Redis／32 字节密钥；开发允许进程内会话。
- Admin REST、GraphQL、Playground 三种协议与文件访问统一经同源 API。保留流式首块、取消、上游状态与必要响应头；禁止透传 cookie，文件响应增加 sandbox CSP。
- 服务器验证目标和 DNS 允许策略，固定实际连接地址，不跟随重定向；拒绝跨源请求、路径穿越、未知目标和会话接口绕行。登录限流、请求大小与外部请求时限有明确上限。
- Redis 的队列超时不能限制已发送请求，因此另加总时限；无响应时关闭连接并返回不可用，后续请求可以恢复连接。不降级认证，也不无限堆积等待命令。
- 新增生产 `server.mjs` 运行入口，处理 adapter-node 6 的 origin 配置及可信头、上传大小和优雅关闭；退出时关闭 Redis。Compose、Nginx、开发脚本和密钥初始化同步调整。
- 连接保存等待认证结果，失败保留非敏感草稿，token 每次尝试后清空。保存期间锁定编辑对象，删除失败留在确认框显示错误，全部断开失败时保留偏好供重试；切换／重连后的旧响应不能改写新会话。401 显示重新连接入口。
- 移除浏览器内保存 bootstrap token 的 Map。连接弹窗复用共享表单与操作尺寸，短屏可滚动，标题和关闭按钮并列；原“Workspace”导航组改称“Browse”，避免误指尚未上线的工作区实体。

部署、旧连接迁移及全部配置见 [Manager 服务连接](../../manager-connections.md)。此次只新增固定依赖 `@redis/client@6.3.0`；已通过 npm registry 确认版本及 Node >=20 的兼容要求，未批量升级其他依赖。

## 验证与证据

本地日志、截图和结果位于 `.artifacts/manager-session-20261024/`。最终检查使用 Node 26.12.1 和 pnpm 12.15.0；早期诊断运行使用本机 Node 26.5.0，未将其作为唯一发布验证。

- Manager lint / Svelte check：0 errors、0 warnings；Vitest 24 项，包括真实 Redis 多实例读写、加密、到期、迟到 401、目标地址变更、DNS／转发／取消和上传限制；production build 通过。
- Chromium 与移动 WebKit 完整回归 62 项通过，覆盖连接、Agent、记忆、设置、三种协议 Playground、草稿与窄屏布局。结果可在 `validation.json` 和 `e2e-final.log` 复核。
- 构建后的两个 Manager 进程 + 真实 Redis + 真实 Go 服务验证：登录／用户读取、凭据签发撤销、600 KiB multipart 上传／下载／删除、重启后会话恢复、实例共享、浏览器隔离、Redis 暂停后 503 与自动恢复、会话记录消失后拒绝访问、正常退出。
- 已检查 1440px 桌面、390px、320px 和 844×390 短屏连接弹窗截图。短屏可以滚动到保存按钮，非敏感草稿和操作可达。
- 新增可重跑的 `scripts/test/manager-session-smoke.cjs`，只用于隔离 Go 测试服务和可丢弃 Redis。脚本会暂停 Redis 6.5 秒，必须显式启用；不应用于线上服务。

复跑前启动有默认 `admin` 用户的隔离 Go 服务、独立 Redis 并构建 Manager，然后从仓库根目录运行：

```sh
MEMSTATION_TEST_TARGET_URL=http://127.0.0.1:18185 \
MEMSTATION_TEST_ADMIN_TOKEN=your-test-admin-token \
MEMSTATION_TEST_MANAGER_REDIS_URL=redis://127.0.0.1:16389/4 \
MEMSTATION_TEST_MANAGER_REDIS_ALLOW_PAUSE=1 \
node scripts/test/manager-session-smoke.cjs
```

默认使用 4274／4275，端口已占用会失败，不接管其他进程。可用 `MEMSTATION_TEST_MANAGER_PORT` 改起始端口、`MEMSTATION_TEST_ARTIFACT_DIR` 改输出目录；脚本不会记录管理员 token 或原始 session cookie。

## 当前边界与下一步

这次真实 Go 联调使用隔离内存数据仓库，Redis 为真实实例；没有重跑 PostgreSQL 全链路、Docker／Nginx 容器部署（本机 Docker CLI 无 Compose 子命令）、远端 CI 或真实手机设备。Go 业务和数据库迁移本批没有改动。已完成的 PostgreSQL Personal 范围验证仍见前几批记录，不能据此推断其他 Workspace 已安全可用。

下一步是补齐 Workspace 管理 API 和全链路读取范围，再接入 ScopeContext、Workspace picker、可恢复的作用域 URL 与管理页面；完整私有／共享分区、SourceEvent、Subject、Run、空间归档删除继续按总计划实施。

后续进度：已有 Personal 管理 API、server-load 目录、settings/access 作用域 URL、草稿和冲突处理已在 [Workspace 管理批次](workspace-management-2026-10-24.md) 落地；本记录以上部分描述该连接批次完成时的范围。
