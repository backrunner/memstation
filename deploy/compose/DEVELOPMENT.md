# Development Stack

MemStation keeps a resettable local Docker Compose stack for development.

Reset flow
- `./scripts/dev-reset-stack.sh`: tears down the current stack, removes Postgres and Redis volumes, recreates the baseline database, runs `memstation migrate`, runs `memstation seed`, and starts server, worker, scheduler, and manager.
- `./scripts/dev-reset-stack.sh --no-seed`: same reset flow without reseeding.
- `./scripts/dev-reset-stack.sh --no-manager`: starts only server-side services after reset.
- `./scripts/dev-reset-stack.sh --no-start`: resets storage and reapplies schema without starting the app services.

Local env file
- The reset script stores local development secrets in `.env.memstation-dev`.
- If `.env.memstation-dev` does not exist, the script generates a fresh admin token and master key automatically.
- You can override the path with `MEMSTATION_ENV_FILE=/path/to/file ./scripts/dev-reset-stack.sh`.

Services
- Server: `http://127.0.0.1:8080`
- Manager: `http://127.0.0.1:4173`
- PostgreSQL: `127.0.0.1:5432`
- Redis: `127.0.0.1:6379`

Notes
- `deploy/compose/docker-compose.yml` keeps local-development defaults only. Rotate secrets before sharing or deploying.
- The reset flow is destructive for the selected Compose project because it removes the development Postgres and Redis volumes.
