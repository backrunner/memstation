# MemStation — 部署与开发环境文档

## 1. 开发环境目标

本地开发环境必须能一键拉起：

- `apps/server`
- `apps/manager`
- PostgreSQL
- Redis
- 基础 telemetry

说明：

- 这套环境首先服务个人自托管和单机开发
- 未来集群化部署通过独立的 K8s / 多实例编排演进

建议方式：

- DevContainer
- Docker Compose

---

## 2. 本地工具链基线

| 工具 | 版本基线 |
|------|----------|
| Go | 1.26.1 |
| Node.js | 24.11+ |
| pnpm | 10.32.1 |
| turbo | 2.8.16 |
| PostgreSQL | 16+ |
| Redis | 7+ |

---

## 3. DevContainer 组成

开发容器建议包含：

- Go toolchain
- Node.js / pnpm / turbo
- Atlas CLI
- Air
- PostgreSQL client

示例服务：

- `app`
- `postgres`
- `redis`

管理面板和 server 共用同一个工作区，但分别运行。

---

## 4. 环境变量

## 4.1 核心配置

```env
MEMSTATION_MODE=gateway
MEMSTATION_PORT=8080
MEMSTATION_ADMIN_TOKEN=memstation-local-admin-token
MEMSTATION_MASTER_KEY=memstation-local-master-key

DATABASE_URL=postgres://memstation:memstation_dev@postgres:5432/memstation?sslmode=disable
REDIS_URL=redis://redis:6379/0

VECTOR_STORE_BACKEND=pgvector

OPENAI_API_KEY=
ANTHROPIC_API_KEY=
GEMINI_API_KEY=

CHAT_MODEL=
SUMMARY_MODEL=
ANALYSIS_MODEL=
EMBEDDING_MODEL=

CONVERSATION_TIMEOUT_MINUTES=15
MAX_CONTEXT_TOKENS=8192
MAX_INJECTION_TOKENS=2048
```

## 4.2 安全要求

- 生产环境密钥必须通过 secret 注入
- 不把真实密钥提交到仓库
- 管理员 token 仅用于 bootstrap / 服务端管理链路

---

## 5. 启动命令

推荐在本地直接使用：

```bash
./scripts/dev-reset-stack.sh
```

```bash
# 根目录
pnpm install
turbo dev

# 仅 server
cd apps/server && air

# 仅 manager
cd apps/manager && pnpm dev

# worker
cd apps/server && go run ./cmd/memstation worker --all

# scheduler
cd apps/server && go run ./cmd/memstation scheduler
```

---

## 6. 代码生成与迁移

```bash
cd apps/server

# Ent / gqlgen
go generate ./...

# migration
atlas migrate diff --env ent
atlas migrate apply --env ent
```

要求：

- 生产只允许版本化迁移
- 生成代码和 migration 必须一起提交

---

## 7. Docker 交付

## 7.1 server 镜像要求

- 使用 Go 1.26 builder
- 产出单二进制
- 默认运行 `serve`
- Worker 和 scheduler 通过 command 覆盖启动

## 7.2 manager 镜像要求

- 使用 Node 24
- 采用 `@sveltejs/adapter-node`
- 产出独立 Node 服务

## 7.3 Compose 生产基线

至少包含：

- `memstation`
- `manager`
- `postgres`
- `redis`

并要求：

- `postgres` 有持久化卷
- `redis` 有持久化策略
- `memstation` 依赖 readiness 合法启动

说明：

- `docker-compose` 是 v1.0 个人自托管和单机部署的默认交付形式
- 集群化部署不是当前默认运行形态，但架构上必须可升级

---

## 8. 生产发布流程

建议顺序：

1. 执行数据库迁移
2. 发布 server
3. 检查 readiness
4. 发布 worker / scheduler
5. 发布 manager

禁止：

- 应用启动时自动隐式建表代替 migration

---

## 9. 备份与恢复

生产环境必须准备：

- PostgreSQL 备份
- 配置与 secret 备份
- 向量重建流程
- 关键审计日志保留策略

说明：

- 向量数据丢失可以重建
- 关系数据和审计数据不能靠“重新推导”替代
