FROM golang:1.26.1 AS build
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod     --mount=type=cache,target=/root/.cache/go-build     go build -o /out/memstation ./cmd/memstation

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/memstation /usr/local/bin/memstation
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/memstation"]
CMD ["serve"]
