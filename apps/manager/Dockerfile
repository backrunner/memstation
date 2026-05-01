FROM node:24.11.1-alpine AS deps
WORKDIR /app
COPY package.json pnpm-workspace.yaml turbo.json ./
COPY apps/manager/package.json apps/manager/package.json
RUN corepack enable && pnpm install --filter @memstation/manager... --frozen-lockfile=false

FROM node:24.11.1-alpine AS build
WORKDIR /app
ARG PUBLIC_MEMSTATION_DEFAULT_TARGET=
ENV PUBLIC_MEMSTATION_DEFAULT_TARGET=$PUBLIC_MEMSTATION_DEFAULT_TARGET
COPY --from=deps /app/node_modules ./node_modules
COPY --from=deps /app/apps/manager/node_modules ./apps/manager/node_modules
COPY . .
RUN corepack enable && pnpm --dir apps/manager build

FROM node:24.11.1-alpine AS runtime
WORKDIR /app/apps/manager
ENV HOST=0.0.0.0
ENV PORT=3000
COPY --from=build /app/node_modules /app/node_modules
COPY --from=build /app/apps/manager /app/apps/manager
EXPOSE 3000
CMD ["node", "build/index.js"]
