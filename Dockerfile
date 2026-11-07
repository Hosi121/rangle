# Flutterの画面とJavaScript Viewerを作る環境
FROM node:24-bookworm AS web-build

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git unzip xz-utils libglu1-mesa \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 --branch 3.47.9 \
    https://github.com/flutter/flutter.git /opt/flutter

ENV PATH="/opt/flutter/bin:${PATH}"

WORKDIR /src/apps/web
COPY apps/web/ ./

RUN npm ci --prefix viewer
RUN npm run build --prefix viewer

RUN flutter --suppress-analytics pub get --enforce-lockfile
RUN flutter --suppress-analytics build web --no-web-resources-cdn

# Goの実行ファイルを作る環境
FROM golang:1.27.3-bookworm AS go-build

# コンテナ内の作業場所
WORKDIR /src

# ビルドに必要なコードをコピー
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Linuxで動くGoの実行ファイルを作る
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -o /out/rangle-server \
    ./cmd/rangle-server

# アプリを動かすための最小限の環境
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app

# 完成したGoとWeb画面を持ってくる
COPY --from=go-build /out/rangle-server /app/rangle-server
COPY --from=web-build /src/apps/web/build/web /app/web

ENTRYPOINT ["/app/rangle-server"]
CMD ["-ui-root", "/app/web"]