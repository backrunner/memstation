# MemStation 本地调试指南

这份指南只覆盖日常开发最常用的路径。所有命令都从仓库根目录执行。

## 1. 选择启动方式

### Apple Silicon：Apple Container

前置条件：Apple Container `1.0.0+`、Node.js `24.11+`、pnpm `10.32.1`。

```bash
pnpm dev:apple
```

它会启动 PostgreSQL、Redis、workspace container、可热重载的 server 和 Manager。

```text
Manager       http://127.0.0.1:5173
HTTP / API    http://127.0.0.1:8080
gRPC          127.0.0.1:9090
PostgreSQL    127.0.0.1:5432
Redis         127.0.0.1:6379
```

停止或彻底重置：

```bash
pnpm dev:apple:stop   # 保留数据库和 Redis 数据
pnpm dev:apple:reset  # 删除容器、network 和开发数据
```

### Docker：devcontainer

前置条件：Docker、Node.js `24.11+`、pnpm `10.32.1`。

```bash
pnpm dev
```

端口与 Apple Container 相同。希望使用稳定的本地 HTTPS 地址时运行：

```bash
pnpm dev:container:portless
```

Manager 地址为 `https://memstation-manager.localhost`。

### 一次启动全部服务角色

下面的命令会清空开发数据，重新执行 migration 和 seed，然后启动 server、worker、scheduler 与 Manager：

```bash
pnpm dev:reset
```

此时 Manager 地址是 `http://127.0.0.1:4173`。这是验证完整异步记忆链路最省事的方式，但不适合保留本地数据。

## 2. 第一次打开 Manager

访问 Manager 后使用本地 admin token 登录：

```text
memstation-local-admin-token
```

开发默认 API Key 是：

```text
ms-local-dev
```

然后在 Manager 的 Provider 和 Model 设置中配置至少一个可用的上游模型。不要把真实密钥提交到仓库。

Compose reset 会把随机生成的本地密钥写入 `.env.memstation-dev`，此时应使用该文件中的 `MEMSTATION_ADMIN_TOKEN`，而不是上述默认值。

## 3. 确认服务正常

```bash
curl -fsS http://127.0.0.1:8080/health/live
curl -fsS http://127.0.0.1:8080/health/ready
```

推荐的人工冒烟流程：

1. 在 Playground Chat 发起一次对话。
2. 在 Conversations 中确认消息、usage 和 provider metadata 已保存。
3. 等待异步处理后，在 Memory 中检查 Summary、Fact、Entity 和 Key Memory。
4. 在 Tasks / Queues 中检查任务状态，在 Audit 中检查人工修改记录。

## 4. 调试异步记忆链路

`pnpm dev` 和 `pnpm dev:apple` 默认只运行热重载 server 与 Manager。要处理 outbox、embedding、analysis 和 Key Memory 任务，还需要同时运行 worker 与 scheduler。

Apple Container 启动后，另开两个终端：

```bash
container exec --interactive --workdir /workspace/memstation/apps/server \
  memstation-dev-workspace go run ./cmd/memstation worker

container exec --interactive --workdir /workspace/memstation/apps/server \
  memstation-dev-workspace go run ./cmd/memstation scheduler
```

Docker devcontainer 启动后，另开两个终端：

```bash
pnpm dlx @devcontainers/cli@0.85.0 exec --workspace-folder . \
  bash -lc 'cd /workspace/memstation/apps/server && go run ./cmd/memstation worker'

pnpm dlx @devcontainers/cli@0.85.0 exec --workspace-folder . \
  bash -lc 'cd /workspace/memstation/apps/server && go run ./cmd/memstation scheduler'
```

改动 Go server 代码会触发 `serve` 自动重启；worker 和 scheduler 改动后需要手动重启对应进程。Svelte 文件由 Vite 自动刷新。

## 5. 常用测试

先安装锁定版本的依赖：

```bash
corepack enable
pnpm install
```

日常提交前：

```bash
bash ./scripts/go/format.sh --check
bash ./scripts/go/lint.sh
(cd apps/server && go test ./...)
pnpm --filter @memstation/manager lint
pnpm --filter @memstation/manager test
pnpm --filter @memstation/manager build
```

按需运行：

| 目标 | 命令 |
|---|---|
| 协议契约 | `pnpm test:contract` |
| Manager 浏览器 E2E | `pnpm test:e2e:manager` |
| 记忆召回与排序 smoke | `pnpm test:eval` |
| 性能 smoke | `pnpm test:perf` |
| Worker 恢复 smoke | `pnpm test:recovery` |
| 备份恢复 dry-run | `pnpm test:backup:dry-run` |
| 常规全量门禁 | `pnpm test:all` |

Playwright 首次缺少浏览器时：

```bash
pnpm --filter @memstation/manager test:e2e:install
```

LOCOMO 评测使用 `pnpm test:eval:locomo`，它需要外部网络且会使用受许可证约束的外部数据，不属于日常门禁。

## 6. Schema 与生成代码

修改 Ent、GraphQL 或 protobuf 定义后运行：

```bash
pnpm generate
git diff --exit-code
```

`git diff --exit-code` 应在已提交基线或已暂存预期修改后使用，用来确认生成器没有留下意外差异。数据库结构必须通过 `apps/server/ent/migrate/migrations/` 中的版本化 migration 更新。

需要从空库验证 migration 时：

```bash
pnpm dev:reset
```

注意：该命令会删除本地开发数据。

## 7. 常见问题

- Manager 返回 `502`：确认 `:8080` 的 server 已启动，或检查 `MEMSTATION_MANAGER_DEV_PROXY_TARGET`。
- 对话已保存但没有生成记忆：确认 worker 和 scheduler 都在运行，并查看 Tasks / Queues。
- Provider 请求失败：检查 Provider 密钥、base URL、模型映射和 server 终端日志。
- `ready` 检查失败：先检查 PostgreSQL 和 Redis，再检查 migration 是否完成。
- 端口被占用：Apple Container 可通过 `MEMSTATION_APPLE_HTTP_PORT`、`MEMSTATION_APPLE_GRPC_PORT`、`MEMSTATION_APPLE_MANAGER_PORT` 等变量覆盖端口。
- pnpm 版本不一致：使用 `corepack prepare pnpm@10.32.1 --activate`。
- 本地状态难以判断：先保留当前日志，再执行对应 runtime 的 stop/reset；reset 会删除开发数据。

