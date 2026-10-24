# Development Stack

MemStation keeps a resettable local Docker Compose stack for development.

Devcontainer debug flow
- `pnpm dev`: starts the `workspace` devcontainer, applies server migrations inside the container, runs backend watch restart on `:8080`, and starts the manager Vite debug server on `:5173`.
- `pnpm dev:container`: only boots the devcontainer workspace plus PostgreSQL and Redis.
- `pnpm dev:container:portless`: runs the same in-container full-stack flow, then registers a Portless alias so the manager is available at `https://memstation-manager.localhost`.
- `pnpm dev:workspace`: runs the in-container full-stack debug command directly when you are already inside the devcontainer.
- In this flow, `DATABASE_URL`, `REDIS_URL`, `MEMSTATION_ADMIN_TOKEN`, `MEMSTATION_MASTER_KEY`, and local cookie defaults are injected automatically from the devcontainer Compose baseline so local debug stays close to zero-config.

Portless full-stack flow
- `pnpm dev:portless`: starts local PostgreSQL and Redis from Compose, applies server migrations, runs the MemStation server with source-watch hot restart on `:8080`, and starts the manager through Portless at `https://memstation-manager.localhost`.
- `pnpm dev:manager:portless`: starts only the Portless manager proxy. Use this when a backend is already running at `MEMSTATION_MANAGER_DEV_PROXY_TARGET`, which defaults to `http://127.0.0.1:8080`.
- `MEMSTATION_DEV_INFRA=0 pnpm dev:portless`: skips Docker infra startup and assumes PostgreSQL and Redis are already reachable through `DATABASE_URL` and `REDIS_URL`.
- `MEMSTATION_SKIP_DEV_MIGRATE=1 pnpm dev:portless`: skips the migration step for quick restarts.
- A `502 Bad Gateway` from `https://memstation-manager.localhost/manager/api/targets/...` usually means the manager proxy cannot reach the configured backend target.

Reset flow
- `./scripts/dev/reset-compose-stack.sh`: tears down the current stack, removes Postgres and Redis volumes, recreates the baseline database, runs `memstation migrate`, runs `memstation seed`, and starts server, worker, scheduler, and manager.
- `./scripts/dev/reset-compose-stack.sh --no-seed`: same reset flow without reseeding.
- `./scripts/dev/reset-compose-stack.sh --no-manager`: starts only server-side services after reset.
- `./scripts/dev/reset-compose-stack.sh --no-start`: resets storage and reapplies schema without starting the app services.

Local env file
- The reset script stores local development secrets in `.env.memstation-dev`.
- If `.env.memstation-dev` does not exist, the script generates a fresh admin token, master key, and Manager session encryption key automatically. Existing files receive a Manager key if the setting is absent.
- You can override the path with `MEMSTATION_ENV_FILE=/path/to/file ./scripts/dev/reset-compose-stack.sh`.

Services
- Devcontainer debug server: `http://127.0.0.1:8080`
- Devcontainer debug manager: `http://127.0.0.1:5173`
- Compose manager: `http://127.0.0.1:4173`
- PostgreSQL: `127.0.0.1:5432`
- Redis: `127.0.0.1:6379`

Manager sessions
- The production Compose Manager requires `MEMSTATION_MANAGER_SESSION_KEY` (64 hexadecimal characters) in the environment file. For manual Compose startup, generate it once with `openssl rand -hex 32` and keep it across restarts; do not reuse the Go master key.
- Manager connects to `http://memstation:8080` inside Compose. Select the registered **Local** service in the UI; the browser no longer needs to reach that address directly.
- Redis DB 1 stores encrypted target sessions. Redis failure fails closed; development Vite without Manager Redis configuration uses memory and requires reconnecting after a restart.
- For a reverse proxy, set Manager `ORIGIN` to the external HTTPS origin. See [Manager connection configuration](../../docs/manager-connections.md) for target registration, network policy, migration and cookie settings.

Notes
- `deploy/compose/docker-compose.yml` keeps local-development defaults only. Rotate secrets before sharing or deploying.
- Empty `MEMSTATION_CORS_ALLOWED_ORIGINS` is intentional: the server reflects the incoming `Origin` for both manager/admin APIs and public APIs. Set a comma-separated whitelist to restrict browser callers.
- The reset flow is destructive for the selected Compose project because it removes the development Postgres and Redis volumes.
