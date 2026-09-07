# MemStation Quick Start

这份文档只回答两件事：

1. 通过 `pnpm` 怎么启动
2. 哪些配置可以在 Manager 里改，哪些仍然必须在启动前准备好

## 1. 推荐启动方式

### 方式 A：前后端联调开发

适合改 Go 服务和 Manager 页面，要求：

- Docker
- Node.js `24.11+`
- pnpm `12.3.4`

先安装依赖：

```bash
pnpm install
```

然后启动：

```bash
pnpm dev
```

这个命令会做这些事：

- 启动 `workspace` devcontainer
- 自动带起 `postgres` 和 `redis`
- 自动注入开发所需的基础环境变量，不需要手动配置 `DATABASE_URL`、`REDIS_URL`、`MEMSTATION_ADMIN_TOKEN`、`MEMSTATION_MASTER_KEY`
- 在 devcontainer 里执行 `memstation migrate`
- 启动后端 watch 脚本，Go 代码变更后自动重启
- 同时启动 Manager 的前端调试服务

启动后访问：

- Server: `http://127.0.0.1:8080`
- Manager Debug: `http://127.0.0.1:5173`
- PostgreSQL: `127.0.0.1:5432`
- Redis: `127.0.0.1:6379`

默认开发登录信息：

- `MEMSTATION_ADMIN_TOKEN=memstation-local-admin-token`
- `MEMSTATION_DEV_API_KEY=ms-local-dev`
- `MEMSTATION_MASTER_KEY=memstation-local-master-key`

说明：

- `pnpm dev` 现在就是 devcontainer-first 的 0 配置开发入口
- 数据库、Redis、基础 token 和默认端口都由 devcontainer / compose 默认值自动接好
- `pnpm dev` 主要面向联调开发，只启动 `server + manager debug`
- 它不会额外启动 `worker` 和 `scheduler`
- 如果你在改记忆异步链路、队列恢复或完整运营链路，优先用下面的方式 B

## 2. 完整本地栈

适合需要完整服务角色时使用。

```bash
pnpm dev:reset
```

这个命令会：

- 清空本地 Postgres / Redis 开发卷
- 重新执行 migration
- 重新执行 seed
- 启动 `memstation + worker + scheduler + manager`

启动后访问：

- Server: `http://127.0.0.1:8080`
- Manager: `http://127.0.0.1:4173`

说明：

- 这是 destructive reset，会删本地开发卷
- 更适合“验证完整链路”而不是“前端热更新联调”

## 3. 其他常用启动命令

只启动 devcontainer：

```bash
pnpm dev:container
```

已经进入 devcontainer 后，在容器内启动联调：

```bash
pnpm dev:workspace
```

只启动后端 watch：

```bash
pnpm dev:server
```

## 4. Manager 里能改什么

你的理解是“多数运行时配置可以通过 Manager 去改”，这个方向是对的，但不是“所有配置都能在 Manager 里改”。

Manager 当前适合管理的内容：

- 上游 Provider 的 `base URL`
- 上游 Provider 的 `API key`
- `chat / summary / analysis / embedding model`
- `default provider`
- `tool_write_enabled`
- `max_injection_tokens`
- `conversation timeout`
- `provider timeout`
- `message threshold`
- `token threshold`
- `rate limit`
- `cache TTL`
- `cors_allowed_origins`
- `session_cookie_cross_site`
- 部分运行时字段的 live apply / restart pending 状态

Manager 里的入口大致是：

- `Settings / Providers`
- `Settings / Models`
- `Settings / System`

## 5. 哪些配置在 devcontainer 模式下是自动的

当你使用：

```bash
pnpm dev
```

下面这些基础配置已经按 devcontainer 开发基线自动注入，不需要你手动设置：

- `DATABASE_URL=postgres://memstation:memstation_dev@postgres:5432/memstation?sslmode=disable`
- `REDIS_URL=redis://redis:6379/0`
- `MEMSTATION_PORT=8080`
- `MEMSTATION_GRPC_PORT=9090`
- `VECTOR_STORE_BACKEND=pgvector`
- `MEMSTATION_ADMIN_TOKEN=memstation-local-admin-token`
- `MEMSTATION_MASTER_KEY=memstation-local-master-key`
- `MEMSTATION_DEV_API_KEY=ms-local-dev`
- `MEMSTATION_CORS_ALLOWED_ORIGINS=http://127.0.0.1:5173,http://localhost:5173`
- `MEMSTATION_SESSION_COOKIE_SECURE=false`
- `MEMSTATION_SESSION_COOKIE_CROSS_SITE=false`

也就是说，`pnpm dev` 目标就是“开箱即用、尽量 0 配置”。

你真正通常只需要在 Manager 里补：

- Provider API key
- Provider base URL
- chat / summary / analysis / embedding model
- default provider 和其他运行时参数

## 6. 哪些配置仍然属于启动前配置

下面这些不是靠 Manager 启动后再补的，它们属于基础设施或 bootstrap 配置。

但是注意：

- 在 `pnpm dev` 的 devcontainer 模式下，它们已经由开发脚本自动提供
- 只有在手动运行、CI、生产部署或自定义容器编排时，你才需要自己显式配置

这些项包括：

- `DATABASE_URL`
- `REDIS_URL`
- `MEMSTATION_ADMIN_TOKEN`
- `MEMSTATION_MASTER_KEY`
- `MEMSTATION_PORT`
- `MEMSTATION_GRPC_PORT`
- `OTEL_EXPORTER_OTLP_ENDPOINT`
- `MEMSTATION_LOG_LEVEL`

原因很简单：

- Server 需要先连上数据库和 Redis 才能启动
- Manager 登录本身依赖 `MEMSTATION_ADMIN_TOKEN`
- 上游密钥加密存储依赖 `MEMSTATION_MASTER_KEY`
- 端口、日志、Telemetry 也都属于服务启动前配置

## 7. 推荐使用习惯

日常开发建议这样分：

1. 改前端、改网关接口、改服务逻辑：

```bash
pnpm dev
```

2. 验证完整异步链路、worker、scheduler、seed 数据：

```bash
pnpm dev:reset
```

3. 启动后先登录 Manager，再补运行时配置：

- 先配 `Settings / Providers`
- 再配 `Settings / Models`
- 最后看 `Settings / System` 里哪些字段是 live apply，哪些字段需要 restart

## 8. 一个最短可用流程

```bash
pnpm install
pnpm dev
```

然后打开 `http://127.0.0.1:5173`：

1. 用 `memstation-local-admin-token` 登录
2. 到 `Settings / Providers` 配上 OpenAI / Anthropic / Gemini 的 `API key`
3. 到 `Settings / Models` 填模型名
4. 到 `Settings / System` 选默认 Provider
5. 开始调试

如果你需要完整异步链路，再切到：

```bash
pnpm dev:reset
```
