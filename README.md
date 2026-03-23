# MemStation

MemStation 是一个面向个人自托管优先的 `AI Gateway + Memory System + Admin Plane`。

当前仓库已经落地完整的 server-first 基线：

- Go 单体服务负责 Gateway、Memory、Admin、Worker、Scheduler、Migration、Seed
- SvelteKit Manager 作为独立管理面板，可直连任意指定的 MemStation 后端
- 对外兼容 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages
- 对上游支持 OpenAI、Anthropic、Gemini
- 记忆链路支持 Summary、Keyword、Embedding、Impression、Fact、Key Memory、图关系、时间衰减、时间分层
- Benchmark、契约测试、E2E、恢复演练、备份恢复脚本和单机部署基线均已落地

## 仓库结构

```text
memstation/
├── apps/
│   ├── server/   # Go 核心服务
│   └── manager/  # SvelteKit 管理面板
├── deploy/       # Compose / K8s / OTel / Nginx 基线
├── scripts/      # 开发、测试、benchmark、恢复脚本
├── .github/      # CI 工作流
├── .devcontainer/
├── .plan/
└── AGENTS.md     # 当前权威规范
```

`AGENTS.md` 是当前最高优先级的执行规范，优先于 `.plan/` 中的历史文档。

## 已实现能力

### Gateway

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `GET /v1/models`
- SSE 流式透传与非流式代理
- API Key 鉴权、速率限制、Usage 统计、Provider 路由

### Memory

- 异步记忆写入：`conversation.finalize -> summary.generate -> embedding.generate -> analysis.extract -> keymemory.reconcile`
- 业务记忆实体：Summary、Keyword、Impression、Fact、Key Memory
- 图结构：`MemoryNode + MemoryRelation + MemoryEmbedding`
- 会话切片：对用户/助手对话做 segment 化写入，并建立 `follows_segment`、`summarized_into` 等图关系
- 时间能力：`importance * time_decay` 排序、`activity_at`、`time_tier`、temporal graph
- 记忆检索：向量候选 + SQL hydrate + 1-2 hop relation expansion + 混合 key memory 注入
- Memory REST：
  - `POST /api/v1/memory/search`
  - `GET /api/v1/memory/keys`
  - `PUT /api/v1/memory/keys/:key`
  - `DELETE /api/v1/memory/keys/:key`

### Admin Plane

- GraphQL Query/Mutation
- Admin REST：overview、users、conversations、memory、config、providers、queues、tasks、cache、audit
- 服务端 session 登录，浏览器不存储 admin token
- 管理面板已覆盖：
  - Dashboard
  - Conversations list/detail
  - Memory workspace、detail、manual edit
  - Key Memory 管理
  - Users / API Keys / Settings
  - Token statistics
  - Provider / Model / System settings
  - Health / Cache / Queues / Tasks / Audit
  - Playground Chat / Memory Preview / Protocol Playground

### Eval / Ops

- Repo-native retrieval/ranking benchmark
- LOCOMO adapter 与 nightly benchmark 脚本
- Contract tests、Go E2E、Manager Playwright E2E
- Worker backlog recovery smoke/nightly
- Backup / export / restore drill
- Docker Compose 单机交付基线
- K8s baseline manifests
- OTel / OTLP / structured logging / health endpoints

## 技术栈

- Backend: Go `1.26.1`, Chi, Cobra, Ent, gqlgen, Asynq, PostgreSQL 16 + pgvector, Redis 7, OpenTelemetry
- Frontend: SvelteKit, Svelte 5, Bits UI, shadcn-svelte, Tailwind 4, ECharts, Playwright, Vitest
- Monorepo: pnpm `10.32.1`, Turbo

## 快速开始

### 方式一：推荐，本地一键重置并启动整套开发栈

前置条件：

- Docker / Docker Compose

执行：

```bash
pnpm dev:reset
```

这个命令会：

- 清空本地 Postgres / Redis 开发卷
- 重新应用 migration
- 重新执行 seed
- 启动 server、worker、scheduler、manager

默认地址：

- Gateway / Admin API: `http://127.0.0.1:8080`
- Manager: `http://127.0.0.1:4173`
- PostgreSQL: `127.0.0.1:5432`
- Redis: `127.0.0.1:6379`

更多说明见：

- `deploy/compose/DEVELOPMENT.md`
- `deploy/compose/BACKUP_RESTORE.md`

### 方式二：手动运行

前置条件：

- Go `1.26.1`
- Node.js `24.11+`
- pnpm `10.32.1`
- PostgreSQL 16+
- Redis 7

初始化依赖：

```bash
pnpm install
```

参考环境变量：

```bash
cp apps/server/.env.example apps/server/.env
```

启动服务：

```bash
cd apps/server
go run ./cmd/memstation migrate
go run ./cmd/memstation seed
go run ./cmd/memstation serve
```

另开终端运行异步角色：

```bash
cd apps/server
go run ./cmd/memstation worker
go run ./cmd/memstation scheduler
```

启动管理面板：

```bash
cd apps/manager
pnpm dev
```

## 常用命令

### 根目录

```bash
pnpm dev
pnpm dev:reset
pnpm build
pnpm lint
pnpm test
pnpm test:contract
pnpm test:e2e:manager
pnpm test:eval
pnpm test:eval:locomo
pnpm test:eval:nightly
pnpm test:perf
pnpm test:recovery
pnpm test:backup:dry-run
pnpm test:coverage
pnpm test:all
pnpm generate
```

### Server CLI

```bash
cd apps/server
go run ./cmd/memstation serve
go run ./cmd/memstation worker
go run ./cmd/memstation scheduler
go run ./cmd/memstation migrate
go run ./cmd/memstation seed
```

## 核心接口

### 健康检查

- `GET /health/live`
- `GET /health/ready`

### Gateway

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `GET /v1/models`

### Memory / Admin

- `POST /api/v1/memory/search`
- `GET /api/v1/memory/keys`
- `PUT /api/v1/memory/keys/:key`
- `DELETE /api/v1/memory/keys/:key`
- `POST /graphql`
- `POST /api/admin/login`
- `POST /api/admin/logout`
- `GET /api/admin/overview`

更多接口可直接查看 `apps/server/internal/transport/http/router.go`。

## 配置说明

关键环境变量见 `apps/server/.env.example`，核心项包括：

- 运行与安全：`MEMSTATION_PORT`, `MEMSTATION_ADMIN_TOKEN`, `MEMSTATION_MASTER_KEY`, `MEMSTATION_DEV_API_KEY`
- 基础设施：`DATABASE_URL`, `REDIS_URL`, `VECTOR_STORE_BACKEND`
- 模型：`CHAT_MODEL`, `SUMMARY_MODEL`, `ANALYSIS_MODEL`, `EMBEDDING_MODEL`
- Provider 凭据：`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`
- Provider 端点：`OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, `GEMINI_BASE_URL`
- 运行阈值：`CONVERSATION_TIMEOUT_MINUTES`, `MAX_CONTEXT_TOKENS`, `MAX_INJECTION_TOKENS`, `MESSAGE_THRESHOLD`, `TOKEN_THRESHOLD`
- OTel：`OTEL_EXPORTER_OTLP_ENDPOINT`

Embedding 已接入真实 provider 调用，采用 OpenAI-compatible embedding 接口；模型与端点通过配置注入，不在代码中写死。

## Manager 设计边界

- Manager 是独立前端，不承载业务核心
- 所有实际管理能力都由 MemStation server 暴露
- Manager 可以保存多个 target，并在不同 MemStation 实例之间切换
- 这为后续集群化、多环境和远程运维预留了空间

## 测试与评测

当前仓库已覆盖：

- Go 单元测试
- 集成测试
- 协议契约测试
- Server E2E
- Manager Vitest + Playwright E2E
- Retrieval / ranking benchmark smoke
- LOCOMO external benchmark runner
- Performance smoke
- Worker backlog recovery drill
- Backup / restore drill

评测入口：

- `apps/server/tests/eval`
- `scripts/eval-memory.sh`
- `scripts/eval-memory-locomo.sh`
- `scripts/eval-memory-nightly.sh`

## 部署

基线部署资源位于：

- Compose: `deploy/compose`
- Kubernetes: `deploy/k8s`
- OTel Collector: `deploy/otel`
- Nginx: `deploy/nginx`

## 当前状态

按当前仓库实现，R1-R6 与 R5.5 的代码范围均已落地，适合作为 v1.0 的开发和迭代基线继续推进。
