FROM golang:1.26.1 AS build
WORKDIR /src
COPY . .
WORKDIR /src/apps/server
RUN --mount=type=cache,target=/go/pkg/mod     --mount=type=cache,target=/root/.cache/go-build     go build -o /out/memstation ./cmd/memstation

FROM gcr.io/distroless/base-debian12@sha256:9dce90e688a57e59ce473ff7bc4c80bc8fe52d2303b4d99b44f297310bbd2210
WORKDIR /app
COPY --from=build /out/memstation /usr/local/bin/memstation
COPY --from=build /src/apps/server/ent/migrate/migrations ./ent/migrate/migrations
EXPOSE 8080
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/memstation"]
CMD ["serve"]
