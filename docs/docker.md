# Dockerでのビルドと起動

Go APIとFlutter Webを1つの非rootコンテナにする。repo rootで実行する。

## ビルド

Dockerを起動してbuildする。Node/Flutterの画面とGo binaryだけを最終imageへコピーする。

```sh
docker build -t rangle:local .
```

既定は`RANGLE_DEMO=false`・`RANGLE_API_URL=/api/v1/clips`。URLが空でも同一originのAPIを使う。配備用は引数を明示する。

```sh
docker build --build-arg RANGLE_DEMO=false \
  --build-arg RANGLE_API_URL=/api/v1/clips -t rangle:local .
```

依存lockfileを先にコピーしてnpm/Dart層を再利用する。Dart cacheは通常layerに置く（GHAはcache mountを保存しない）。[Docker cache](https://docs.docker.com/build/ci/github-actions/cache/)

## ローカルで起動する

```sh
docker run --rm -p 127.0.0.1:8080:8080 -e PORT=8080 rangle:local
```

`http://localhost:8080/`がlanding、`/demo/`がUI、`/healthz`がhealth。公開portはこのPC内だけで、終了はCtrl+C。最小起動にはcatalogがないため、health200でもfeedは読めない。

公開済みmediaを使う例（`media.example.com`は置換）：

```sh
docker run --rm -p 127.0.0.1:8080:8080 -e PORT=8080 \
  --mount type=bind,src="$(pwd)/.local/catalog.json",dst=/data/catalog.json,readonly \
  rangle:local -ui-root /app/web -catalog /data/catalog.json \
  -asset-origin https://media.example.com
```

別ターミナルで確認する。

```sh
curl --fail 'http://127.0.0.1:8080/api/v1/clips?limit=1'
```

`/demo/`で動画・自由視点を開く。preview/sceneはcatalogのURL、manifestは配信契約用JSON。API障害・不正URLはerror、空catalogは空feedとなり、sampleへfallbackしない。[catalog形式](api-delivery.md)

私有scene・重み・実験出力はimageへ入れない。Web設定はbuild時に固定され、`docker run -e RANGLE_API_URL=...`では変わらない。

## 独立したデモビルド

```sh
docker build --build-arg RANGLE_DEMO=true -t rangle:demo .
docker run --rm -p 127.0.0.1:8080:8080 -e PORT=8080 rangle:demo
```

`/demo/`で研究sampleを開く。外部HTTP(S)動画/sceneにはbuild引数`RANGLE_PREVIEW_URL`/`RANGLE_SCENE_URL`を使う。API modeでは使わない。[Flutter起動](../apps/web/README.md#apiを使わないデモ)

## imageと配信設定の対応

| 記録するもの | 確認箇所 |
| --- | --- |
| source/image | commit、CI run、registryの`repository@sha256:...` |
| Web/API | build引数、`/demo/`のoriginと`/api/v1/clips` |
| media | runtime `-asset-origin`とAPIの各URL |
| catalog | mountの`snapshots/<sha256>.json`、file SHA、clip id/revision |

ローカル確認は`docker image inspect rangle:local --format '{{.Id}}'`と`sha256sum .local/catalog.json`。image IDとregistry digestは別物。runtime/mountは[stg配備](stg-deployment.md)、昇格・smoke・記録は[CI/CD](cicd.md)に従う。

## Goサーバーの待受け

優先順は`-listen`→`0.0.0.0:$PORT`→既定`127.0.0.1:4174`。Cloud Runの公開範囲・認証は別途設定する。

## テスト

Go1.27.3/Python3を用意し、共有fixture・ML oracle・publisherを含む完全なcheckoutで実行する。

```sh
go test -race ./...
go vet ./...
```

## 手順の範囲

build・ローカル起動まで。cloud資源作成・media公開は行わない。AR送信・Cloud Run昇格は[CI/CD](cicd.md)、画面・WebGL・CORSは別途確認する。
