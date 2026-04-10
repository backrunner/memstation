FROM golang:1.26.1

ARG NODE_VERSION=24.11.1
ARG PNPM_VERSION=10.32.1

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl git procps xz-utils \
  && rm -rf /var/lib/apt/lists/*

RUN arch="$(dpkg --print-architecture)" \
  && case "$arch" in \
    amd64) node_arch="x64" ;; \
    arm64) node_arch="arm64" ;; \
    *) echo "unsupported architecture: $arch" >&2; exit 1 ;; \
  esac \
  && curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" -o /tmp/node.tar.xz \
  && tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1 --no-same-owner \
  && rm -f /tmp/node.tar.xz \
  && corepack enable \
  && corepack prepare "pnpm@${PNPM_VERSION}" --activate
