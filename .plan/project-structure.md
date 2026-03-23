# MemStation — 项目结构文档

## 1. Monorepo 结构

MemStation 使用 Turborepo 管理 monorepo，包含两个独立 workspace：

- `apps/server`
- `apps/manager`

两者通过运行时 API 通信，没有编译时耦合。

```text
memstation/
├── apps/
│   ├── server/
│   │   ├── cmd/memstation/
│   │   ├── ent/
│   │   ├── graph/
│   │   ├── internal/
│   │   │   ├── app/
│   │   │   ├── bootstrap/
│   │   │   ├── transport/
│   │   │   │   ├── http/
│   │   │   │   └── graphql/
│   │   │   ├── service/
│   │   │   ├── store/
│   │   │   │   ├── sql/
│   │   │   │   └── vector/
│   │   │   ├── cache/
│   │   │   ├── provider/
│   │   │   ├── worker/
│   │   │   ├── security/
│   │   │   ├── observability/
│   │   │   └── modelcap/
│   │   ├── gqlgen.yml
│   │   ├── go.mod
│   │   └── Dockerfile
│   └── manager/
│       ├── src/
│       │   ├── routes/
│       │   ├── lib/
│       │   │   ├── api/
│       │   │   ├── components/
│       │   │   ├── charts/
│       │   │   ├── stores/
│       │   │   └── utils/
│       │   └── app.html
│       ├── package.json
│       └── Dockerfile
├── .plan/
├── AGENTS.md
├── package.json
├── pnpm-workspace.yaml
└── turbo.json
```

---

## 2. Server 目录职责

| 路径 | 职责 |
|------|------|
| `cmd/memstation` | CLI 入口 |
| `ent` | Ent schema、生成代码、迁移配置 |
| `graph` | GraphQL schema 与 resolver |
| `internal/bootstrap` | 配置、日志、telemetry、数据库、Redis 初始化 |
| `internal/transport/http` | REST handlers、协议适配、middleware |
| `internal/transport/graphql` | GraphQL 入口装配 |
| `internal/service` | 业务编排层 |
| `internal/store/sql` | Ent repository |
| `internal/store/vector` | VectorStore 实现 |
| `internal/cache` | L1/L2 cache |
| `internal/provider` | OpenAI / Anthropic / Gemini 适配 |
| `internal/worker` | asynq workers、outbox relay、scheduler |
| `internal/security` | auth、crypto、rate limit、audit |
| `internal/observability` | metrics、tracing、logging |
| `internal/modelcap` | 模型能力注册表 |

---

## 3. Manager 目录职责

| 路径 | 职责 |
|------|------|
| `src/routes` | 页面和路由 |
| `src/lib/api` | 直连目标 MemStation 的 REST / GraphQL / Gateway 客户端 |
| `src/lib/components` | 通用 UI 组件 |
| `src/lib/charts` | 图表组件 |
| `src/lib/stores` | 客户端状态 |
| `src/lib/utils` | 工具函数 |

要求：

- 页面优先 SSR
- 管理认证通过目标 MemStation 服务端 session
- `manager` 不作为 API proxy
- `manager` 可以连接任意指定的 MemStation target
- SvelteKit server 层只做页面壳层和静态资源，不承载业务接口

---

## 4. Workspace 独立性

| 维度 | `apps/server` | `apps/manager` |
|------|---------------|----------------|
| 语言 | Go | TypeScript |
| 包管理 | `go mod` | `pnpm` |
| 构建 | `go build` | `svelte-kit build` |
| 热重载 | `air` | `pnpm dev` |
| 测试 | `go test` | `pnpm test` |

规则：

- `manager` 不直接依赖 `server` 源码
- `manager` 与 `server` 的交互只通过 HTTP / GraphQL / Gateway
- 所有业务接口最终都由 `apps/server` 提供，不从 `apps/manager` 再接一层 BFF

---

## 5. 根目录脚本

根 `package.json` 负责 Turbo 调度，至少包含：

- `dev`
- `build`
- `lint`
- `test`
- `generate`

---

## 6. 生成与提交规则

- Ent 生成代码必须提交
- gqlgen 生成代码必须提交
- migration 必须提交
- 自动生成物与源 schema 不允许长期漂移
