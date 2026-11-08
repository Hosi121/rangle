# syntax=docker/dockerfile:1.7
FROM node:24-bookworm AS web-toolchain
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git unzip xz-utils libglu1-mesa \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch 3.47.9 \
    https://github.com/flutter/flutter.git /opt/flutter
ENV PATH="/opt/flutter/bin:${PATH}"
WORKDIR /src/apps/web

# Dependency layers survive source edits. Dart's cache stays in this layer:
# GHA BuildKit cache export does not preserve cache mounts by default.
FROM web-toolchain AS web-deps
COPY apps/web/viewer/package.json apps/web/viewer/package-lock.json ./viewer/
RUN --mount=type=cache,id=rangle-npm,target=/root/.npm \
    npm ci --prefix viewer --no-audit --no-fund
COPY apps/web/pubspec.yaml apps/web/pubspec.lock ./
RUN flutter --suppress-analytics pub get --enforce-lockfile

FROM web-deps AS web-build
ARG RANGLE_API_URL=
COPY apps/web/ ./
RUN npm run build --prefix viewer \
    && flutter --suppress-analytics build web --release --no-pub --no-web-resources-cdn \
       --dart-define=RANGLE_API_URL="${RANGLE_API_URL}"

FROM golang:1.27.3-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN --mount=type=cache,id=rangle-go-build,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -mod=readonly -trimpath -o /out/rangle-server ./cmd/rangle-server

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=go-build /out/rangle-server /app/rangle-server
COPY --from=web-build /src/apps/web/build/web /app/web
ENTRYPOINT ["/app/rangle-server"]
CMD ["-ui-root", "/app/web"]
