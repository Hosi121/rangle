# CDNのブラウザ契約確認（#125）

2026-11-10、配備済みpresentationの公開app/media originを使い、WebとBlob Workerからの読み取りを確認した。汎用platform moduleの配備適合、CDN hit率・origin負荷（#126）、容量受入（#35）は対象外。

## 対象と結果

| 条件 | 記録 |
| --- | --- |
| 時刻 | 初回01:57:28–01:57:44 UTC、修正後02:11:15–02:11:21 UTC |
| app / media | `https://app.rangleplay.stream` / `https://media.rangleplay.stream` |
| browser | Chromium 146.0.7678.0、Playwright Test 1.65.0、headless、新規context、credentials omit |
| asset / revision | 合成fixture `cdn-contract-125-a524ea61-060a-4d6d-ac6b-a18cf2cd3036` / `v1`、`assets/<asset>/v1/full/probe.bin` |
| bytes | 0から255までの256byte。SHA256 `40aff2e9d2d8922e47afd4648e6967497158785fbd1da870e7110266bf944880` |
| ETag | `"e2c865db4162bed963bfaa9ef6ac18f0"` |
| generation | 初回 `1794275855234178`、最終再確認 `1794276677815828`。再作成も同じbytesのみ |
| 範囲 | 1つの合成object、64 HTTP要求・2MiB・能動検証15分を上限。49要求を試験側で追跡（browser内部preflightを別途許容）、media応答body累計3679byte。負荷試験・invalidationなし |

| 確認 | 合否・観測 |
| --- | --- |
| HTTP GET/HEAD/Range/304/416 | 成功。HEAD length256/body0、Range `bytes=0-31` は206・length32・`Content-Range: bytes 0-31/256`、条件一致304/body0、範囲外416 |
| Web/Worker | それぞれGET200・HEAD200・Range206・条件付きGET304。JSからETag/Content-Length/Content-Range/Cache-Controlを参照でき、全文・部分bytesのhash一致 |
| preflight | `range,if-none-match` が許可される。単にOPTIONS200だけでは合格にしない |
| 通常key / 無関係query | baseと初めて使う`?campaign=contract-a`・`?unused=contract-b`が同一bytes・Age820。queryがkeyを分断しない設定と整合 |
| generation query | `?generation=1794276677815828` は同一bytesの200でAgeなし。baseのAge820と異なり、別keyへの初回取得と整合 |
| Origin | app・別Origin・OriginなしのHTTP要求すべて同一bytes、ACAO `*`。このfixtureでは`Vary: Origin`なし。Originごとの認可や内容選択をしない |
| TTL / 変換 | CDNは`public,max-age=86400`、Content-Encodingなし、length/hash一致。uploadの`no-transform`はFORCE_CACHE_ALL応答には残らない。変換禁止はcompression DISABLEDと取得bytesで確認した範囲に限定 |
| 未公開→公開 | 新規keyへ404を受けた後、create-only uploadで同じURLが200。長期negative cacheは残らなかった。24時間経過や全edgeでの測定ではない |
| 清掃 | 作成したgenerationだけを条件付き削除。生存objectなし。256byteの無害なCDN cache、GCS soft-deleteは既存保持期限まで残り得る |

最初の試験では通常GET/HEAD/Rangeまで成功し、条件付きfetchがCORSで失敗した。GCSの許可headerに`If-None-Match`/`Range`を追加するとpreflightは通ったが、CDN生成304にACAOがなく引き続き失敗した。media URL matcherにACAO `*` と必要なExpose-Headersを`replace=true`で設定した後、Web/Workerとも304を取得できた。app/APIのmatcherやprivate bucketへCORSを追加していない。

## 再現手順

公開appのDevToolsで実行するか、Playwrightでappの`/`を開いて`page.evaluate`から実行する。localhost originやHTTP clientだけの試験では代替しない。再実行時は対象project/bucket、公開可能なfixture、request/byte/時間上限を定め、既存の予算・期限内で行う。

1. UUIDを含む新しい`assets/<id>/v1/full/probe.bin`を選び、CDNの404を一度確認する。
2. `bytes(range(256))`を生成し、既存media bucketへ`gcloud storage cp --if-generation-match=0 --content-type=application/octet-stream --cache-control=public,max-age=86400,no-transform`で作成する。objectのgenerationを記録する。既存keyを上書きしない。
3. HTTPで200/HEAD/Range/If-None-Match/416と、Origin・Access-Control-Request-Method GET・Access-Control-Request-Headers `range,if-none-match` を付けたOPTIONSの**CORS header**を確認する。
4. 公開app originで次の関数を使い、`GET {}`、`HEAD {}`、`GET {Range:'bytes=0-31'}`、`GET {'If-None-Match':etag}`の順に、WebとWorker双方で比較する。304のbodyは空であり、`response.ok`はfalseでも正常。

```js
async function read({url, method, headers}) {
  const r = await fetch(url, {method, headers, credentials: 'omit'});
  const body = await r.arrayBuffer();
  const digest = await crypto.subtle.digest('SHA-256', body);
  return {
    status: r.status, headers: Object.fromEntries(r.headers), bytes: body.byteLength,
    sha256: [...new Uint8Array(digest)].map(x => x.toString(16).padStart(2, '0')).join(''),
  };
}
// Web: await read({url, method: 'GET', headers: {}})
// Worker uses the same function, rather than a different network client.
const source = `const read = ${read}; onmessage = async e => postMessage(await read(e.data));`;
const workerUrl = URL.createObjectURL(new Blob([source], {type: 'text/javascript'}));
const worker = new Worker(workerUrl);
// Set worker.onmessage, then worker.postMessage({url, method, headers}) for each case.
// Finally: worker.terminate(); URL.revokeObjectURL(workerUrl);
```

5. HTTPでAgeが正になるまで最大6.5秒・有限回確認し、未使用の無関係query・generation query・別Origin・Originなしを比較する。Ageは共有keyと整合する観測であり、全cache fillやhit率の証明には使わない。
6. `gcloud storage rm 'gs://<bucket>/<object>#<generation>' --if-generation-match=<generation>`で自分が作成した世代だけ削除する。失敗時もfinallyで清掃する。invalidationや既存資産の削除は不要。

Terraform mockは許可header、mediaだけへのCORS付与、private/appへの非適用、LIVEの両cache policyへの適用を検証する。実CDN生成304の回帰はこのブラウザ確認が必要。release gateへの組込みは#34で扱う。
