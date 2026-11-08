# Dockerでのビルドと起動

GoサーバーとFlutter Webの画面を、1つのコンテナイメージにまとめる。

## ビルド

Docker Desktopを起動し、リポジトリのルートで実行する。

```sh
docker build -t rangle:local .
```

Node・Flutterで画面とJavaScript Viewerをビルドし、Goでサーバーの実行ファイルを作る。
最終イメージには完成品だけをコピーし、非rootユーザーで実行する。

ローカルの既定は `RANGLE_API_URL` が空の内蔵デモ。実フィードAPIを使う配備用imageでは、以下のbuild引数を明示してDart defineへ渡す。CIもこの引数を必ず渡す。`/demo/` のbaseは既存HTMLの指定を保つ。依存lockfileをsourceより先にコピーし、npm/Dartの依存層を再利用する。Dart cacheはBuildKitの通常layerへ保持する。GHA cacheはcache mount自体を保存しないためである。[Docker cache](https://docs.docker.com/build/ci/github-actions/cache/)

```sh
docker build --build-arg RANGLE_API_URL=/api/v1/clips -t rangle:local .
```

## ローカルで起動する

```sh
docker run --rm -p 127.0.0.1:8080:8080 -e PORT=8080 rangle:local
```

ブラウザで http://localhost:8080 を開く。
http://localhost:8080/healthz が `ok` を返すことも確認する。

このポート公開設定では、このPC内から接続する。
停止するには、実行中のターミナルでCtrl+Cを押す。

引数なしでbuildしたimageは内蔵デモを表示する。landingは `/`、デモUIは `/demo/` で開く。フィードAPIや自由視点用の外部データは、この起動方法では設定していない。

API向けimageの確認には公開許諾済みcatalogと外部asset-originをサーバー引数へ設定する。本番のruntime引数・mountはIaCが管理する。私有scene・重み・実験出力をimageへコピーしない。

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
