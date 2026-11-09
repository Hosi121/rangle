# Dockerでのビルドと起動

GoサーバーとFlutter Webの画面を、1つのコンテナイメージにまとめる。

## ビルド

Docker Desktopを起動し、リポジトリのルートで実行する。

```sh
docker build -t rangle:local .
```

Node・Flutterで画面とJavaScript Viewerをビルドし、Goでサーバーの実行ファイルを作る。
最終イメージには完成品だけをコピーし、非rootユーザーで実行する。

既定は `RANGLE_DEMO=false`、`RANGLE_API_URL=/api/v1/clips` のAPIビルド。URL引数を空で渡しても同一originのAPIを使い、サンプルへ切り替えない。配備用imageでは以下のbuild引数を明示する。CIもAPI URLを渡す。`/demo/` のbaseは既存HTMLの指定を保つ。依存lockfileをsourceより先にコピーし、npm/Dartの依存層を再利用する。Dart cacheはBuildKitの通常layerへ保持する。GHA cacheはcache mount自体を保存しないためである。[Docker cache](https://docs.docker.com/build/ci/github-actions/cache/)

```sh
docker build --build-arg RANGLE_DEMO=false \
  --build-arg RANGLE_API_URL=/api/v1/clips -t rangle:local .
```

## ローカルで起動する

```sh
docker run --rm -p 127.0.0.1:8080:8080 -e PORT=8080 rangle:local
```

ブラウザで http://localhost:8080 を開く。
http://localhost:8080/healthz が `ok` を返すことも確認する。

このポート公開設定では、このPC内から接続する。
停止するには、実行中のターミナルでCtrl+Cを押す。

landingは `/`、Web UIは `/demo/` で開く。`/demo/` は配信pathであり、デモモードの指定ではない。この最小起動ではcatalogがないためAPIの読込エラーを表示する。healthが200でも公開フィードの接続確認にはならない。

API向けimageの確認には公開許諾済みcatalogと外部asset-originをサーバー引数へ設定する。次は `.local/catalog.json` のpreview・scene・配信manifestと資産が、指定media originへ公開済みであることを前提とする（`media.example.com` は実際の公開先へ置き換える）。catalogの形式と公開手順は [API配信](api-delivery.md) を参照。

```sh
docker run --rm -p 127.0.0.1:8080:8080 -e PORT=8080 \
  --mount type=bind,src="$(pwd)/.local/catalog.json",dst=/data/catalog.json,readonly \
  rangle:local -ui-root /app/web -catalog /data/catalog.json \
  -asset-origin https://media.example.com
```

別のターミナルでAPIを確認する。

```sh
curl --fail 'http://127.0.0.1:8080/api/v1/clips?limit=1'
```

`http://localhost:8080/demo/` でAPIのクリップを開き、通常動画と「自由視点で見る」を確認する。`preview_url` / `scene_url` はAPIのcatalogから読み込む。返された `manifest_url` はHTTPで取得してrevision・資産URLを確認する配信契約で、Flutterの動画へ直接渡すURLではない。APIの停止・不正なWeb URL設定は読込エラー、空catalogは空フィードとなり、研究サンプルへ切り替えない。

本番のruntime引数・mountはIaCが管理する。私有scene・重み・実験出力をimageへコピーしない。Web設定はビルド時に固定されるため、`docker run -e RANGLE_API_URL=...` では変更できない。

## 独立したデモビルド

```sh
docker build --build-arg RANGLE_DEMO=true -t rangle:demo .
docker run --rm -p 127.0.0.1:8080:8080 -e PORT=8080 rangle:demo
```

`/demo/` で内蔵の研究サンプルを開ける。HTTP(S)の外部動画・sceneを使う場合だけ、build時に `RANGLE_PREVIEW_URL` / `RANGLE_SCENE_URL` を追加する。APIモードではこの2引数は使わない。[Flutterからの起動例](../apps/web/README.md#apiを使わないデモ)も参照。

## imageと配信設定の対応

配備ごとに次の組を記録する。tagだけでimageを特定しない。

| 項目 | 確認する場所 |
| --- | --- |
| Git commit / image digest | CI runのcommit・build出力と配備summaryの `repository@sha256:...` |
| Web設定 | 同じcommitのDockerfileとworkflowのbuild引数。通常は `RANGLE_DEMO=false` / `RANGLE_API_URL=/api/v1/clips` |
| Web / API origin | 開いた `/demo/` のoriginと、そのoriginの `/api/v1/clips`。APIを別originへbuildした場合はそのURL |
| media origin | runtimeの `-asset-origin` と、APIが返す `preview_url` / `scene_url` / `manifest_url` |
| catalog版 | readonly mountした `snapshots/<sha256>.json` のpath・ファイルSHA256と、APIのclip `id` / `revision` |

ローカルでは `docker image inspect rangle:local --format '{{.Id}}'` と `sha256sum .local/catalog.json` も記録できる。ローカルimage IDとregistry digestは別の識別子なので、配備にはregistryのdigestを使う。stgのruntime mount・media originは [配備手順](stg-deployment.md)、digest昇格とAPI smokeは [CI/CD](cicd.md) を参照。WebGL・再生・CORSの画面確認は別途必要。

## Goサーバーの待受け

- PORT環境変数がない場合は、従来どおり127.0.0.1:4174で待ち受ける。
- PORTがある場合は、0.0.0.0:PORTで待ち受ける。
- 明示的な-listen引数がある場合は、そちらを優先する。

Cloud Runでは、渡されたPORTを使って接続を受け付ける。
アクセスの公開範囲や認証は、Cloud Run側でも設定する。

## テスト

Goの作業用イメージを作成し、Linux環境で検証する。

```sh
docker build --target go-build -t rangle-go-build .
docker run --rm rangle-go-build sh -c "go test -race ./... && go vet ./..."
```

## この変更の範囲

Dockerイメージの作成と、ローカルでの起動に対応する。
Artifact Registryへの送信とCloud Runへの昇格条件は [CI/CD](cicd.md) を参照する。コンテナのbuildはクラウド資源作成やメディアの公開を行わない。
