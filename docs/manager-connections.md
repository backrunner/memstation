# Manager 服务连接与部署

Manager 通过自己的同源 API 访问 Go 服务。浏览器中的 Target 偏好只包含名称和服务器登记的地址；管理员 token 仅用于一次登录交换，随后清空输入。Go 的管理员 session cookie 加密保存在 Manager 的 Redis 中，浏览器仅持有 Manager 的不透明会话 cookie。

此层只负责连接、会话与协议转发。Workspace、Agent、记忆权限和业务校验仍由 Go 服务负责。

## 运行配置

以下配置在 Manager 启动时读取，不再作为 Vite 构建参数：

```sh
# URL 是 Manager 进程可访问的 Go 地址，未必能从用户浏览器直接访问。
export MEMSTATION_MANAGER_TARGETS='[{"id":"local","label":"Local","baseUrl":"http://127.0.0.1:8080"}]'
export MEMSTATION_MANAGER_REDIS_URL='redis://127.0.0.1:6379/1'
# 首次生成后持久保存到部署环境的 secret；重启和多副本使用同一个值。
export MEMSTATION_MANAGER_SESSION_KEY="$(openssl rand -hex 32)"
export ORIGIN='https://manager.example.com'
export NODE_ENV='production'
export BODY_SIZE_LIMIT='32M'
node apps/manager/server.mjs
```

| 配置 | 行为 |
| --- | --- |
| `MEMSTATION_MANAGER_TARGETS` | JSON 数组，最多 50 项。每项有稳定 `id`、可选 `label`、必需 `baseUrl`。空数组表示没有可连接服务。 |
| `MEMSTATION_MANAGER_REDIS_URL` | 生产环境必须配置；可使用独立 Redis DB，避免误清除服务端任务／缓存。 |
| `MEMSTATION_MANAGER_SESSION_KEY` | 64 位十六进制字符串，AES-256-GCM 密钥；Redis 模式必须配置。更换后原目标会话失效，用户需要重新连接。 |
| `BODY_SIZE_LIMIT` | adapter-node 的上传限制，使用 `32M` 与转发层一致；Docker 镜像已设置。 |
| `ORIGIN` | Manager 运行入口的外部访问源，包括 scheme 和非标准端口；反向代理部署应显式设置。 |
| `MEMSTATION_MANAGER_DEV_PROXY_TARGET` | 开发兼容入口：未提供 targets 时，登记这一个 Go 地址。 |
| `PUBLIC_MEMSTATION_DEFAULT_TARGET` | 旧配置兼容：在上述两个配置均缺失时登记一个目标，不再授权浏览器自行添加任意地址。 |

生产运行使用 `pnpm --filter @memstation/manager start` 或上面的 `server.mjs`。入口兼容 adapter-node 6 的 origin 推断，覆盖客户端伪造的协议／主机头；默认 HTTPS，有本地 HTTP 需求时显式设置 ORIGIN。优雅退出会等待请求结束并关闭 Redis。直接运行生成的 `build/index.js` 不提供这些兼容行为。

开发模式没有 Redis 时使用进程内会话，重启后重新连接。生产模式缺少 Redis 或合法密钥时返回不可用状态，不降级到内存或浏览器凭据。

Manager cookie 为 `httpOnly + SameSite=Strict`，生产环境始终使用 `Secure`；远程部署需 HTTPS。目标地址不得包含用户名、密码、查询串或片段。会话绑定包含稳定 ID 和标准化后的目标地址；修改目标地址会使原登录不可复用，需要重新连接。

## 目标地址与网络策略

- 服务器登记的字面 IP 和 `localhost` 可直接使用，方便本地部署。
- 其他域名默认只能解析到公共地址。私网域名可以设置精确 `addresses` IP 允许列表，或使用 `allowPrivateNetwork: true` 允许 RFC1918 / ULA 私网。
- Compose 使用 `http://memstation:8080` 和 `allowPrivateNetwork: true`；这是 Docker 服务发现，不要求浏览器解析该名称。
- 每个请求验证全部 DNS 结果，并固定实际连接 IP，保留原 Host 和 TLS SNI；不跟随重定向，不转发任意外部 URL。
- 用户只能选择登记服务，无法通过 localStorage、请求参数或连接 URI 扩大服务器允许的目标集合。服务器地址策略不会暴露到客户端。

## 连接与恢复

登录通过 `/manager/session/:target`，业务访问通过 `/manager/api/targets/:target/*`。转发限定为现有 Admin、GraphQL、Gateway、Memory 与 health 路径；登录／登出／session 不能绕过会话入口。

每个浏览器、每个 Target 分别保存上游会话。有效期最多 8 小时，同时受 Go 会话到期限制。共享 Redis 和密钥的 Manager 副本可以恢复同一个浏览器会话。Redis 故障不会回退到未认证访问，恢复后可重新检查连接；Redis 数据丢失则需要重新登录。

目标返回 401 时仅移除该次请求使用的会话；迟到响应不能移除较新的登录。退出或删除连接先删除 Manager 绑定，再尽力调用 Go 登出接口记录审计；目标离线不会阻止本地退出。浏览器中删除偏好不会删除 Go 数据或服务器登记项。登录限制为每个客户端地址、每个 Target 每分钟 60 次，多副本通过 Redis 共享计数。

所有浏览器修改请求必须同源，跨源 fetch 被拒绝。若部署于可信反向代理后，需要按 adapter-node 的配置正确传递客户端地址；不得信任公共客户端可伪造的地址头。默认按连接源地址限流。

代理保留 SSE 流、取消信号、文件内容类型与范围响应，不透传上游 cookie；上传请求上限 32 MiB，登录请求／响应上限 16 KiB，业务请求总时限 120 秒，登录检查 15 秒。上传文件响应带 sandbox CSP，防止 HTML / SVG 获得 Manager 页面执行权限。反向代理应禁用流式缓冲，允许相应请求大小和时限。

## 旧连接迁移

先把浏览器中需要保留的目标地址登记到 `MEMSTATION_MANAGER_TARGETS`，再启动新 Manager。匹配地址的旧名称、选择偏好会映射到服务器 ID；未登记的地址不会继续使用。原来直接存在浏览器的 Go cookie 不会用于新链路，每个服务首次需要重新输入管理员 token。

连接弹窗保存失败会保留非敏感输入并展示错误，token 每次尝试后清空；等待期间禁止重复保存或更换编辑对象。多个服务可以分别连接和断开。全部断开时若 Manager 无法清除某个绑定，会保留连接列表并显示重试提示，不把失败当作退出成功。

工作区目录、策略管理、访问授权、记忆与会话页面使用明确的 Target/Workspace URL，
通过 Manager 服务端获取数据。范围、受众和事件摄取能力见
[独立记忆服务契约](memory-service-contract.md)，部署和自动化接入检查见
[接入说明](independent-memory-integration.md)。
