FROM node:26.12.1-alpine AS deps
WORKDIR /app
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml turbo.json ./
COPY apps/manager/package.json apps/manager/package.json
COPY apps/server/package.json apps/server/package.json
RUN npm install --global pnpm@12.15.0 && pnpm install --filter @memstation/manager... --frozen-lockfile

FROM deps AS build
WORKDIR /app
ARG PUBLIC_MEMSTATION_DEFAULT_TARGET=
ENV PUBLIC_MEMSTATION_DEFAULT_TARGET=$PUBLIC_MEMSTATION_DEFAULT_TARGET
COPY . .
RUN pnpm --dir apps/manager build

FROM node:26.12.1-alpine AS runtime
LABEL org.opencontainers.image.source="https://github.com/backrunner/memstation" \
      org.opencontainers.image.licenses="Apache-2.0"
WORKDIR /app/apps/manager
ENV HOST=0.0.0.0
ENV PORT=3000
COPY --from=build /app/node_modules /app/node_modules
COPY --from=build /app/apps/manager /app/apps/manager
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /usr/share/licenses/memstation/
EXPOSE 3000
CMD ["node", "build/index.js"]
