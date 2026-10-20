# syntax=docker/dockerfile:1
FROM node:22.20.0-bookworm-slim AS dashboard
WORKDIR /src/dashboard
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate
COPY dashboard/package.json dashboard/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile --ignore-scripts
COPY dashboard ./
COPY VERSION /src/VERSION
RUN pnpm build

FROM scratch AS dashboard-artifact
COPY --from=dashboard /src/dashboard/dist/ /

FROM golang:1.26.5-bookworm AS build
ARG APP_VERSION
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN test -n "$APP_VERSION" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X github.com/verdantflarehub/verdantflare-app-world/internal/buildinfo.Version=$APP_VERSION" -o /out/world ./cmd/world

FROM build AS integration-build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -tags integration -o /out/world-integration.test ./internal/world
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -tags integration -o /out/world-migrations.test ./migrations

FROM scratch AS integration-artifact
COPY --from=integration-build /out/world-integration.test /
COPY --from=integration-build /out/world-migrations.test /
COPY --from=integration-build /src/internal/world/testdata /testdata

FROM gcr.io/distroless/static-debian12:nonroot
ARG APP_VERSION
LABEL org.opencontainers.image.version=$APP_VERSION
COPY --from=build /out/world /app/world
COPY --from=dashboard /src/dashboard/dist /app/dashboard
ENV WORLD_LISTEN=0.0.0.0:8092
ENV WORLD_DASHBOARD_DIR=/app/dashboard
EXPOSE 8092
USER 65532:65532
ENTRYPOINT ["/app/world"]
CMD ["serve"]
