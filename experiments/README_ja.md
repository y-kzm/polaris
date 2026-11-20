# Polaris 実験スクリプト

実験環境の構築から、論文の各実験の実施、結果の表の出力までを行うスクリプトです。

[English](README.md) / [日本語](README_ja.md)

## クイックスタート

Linux マシン（Ubuntu 24.04 を想定）で、root で実行します。

```bash
git submodule update --init          # agent/go-ra を取得（初回のみ）
cd experiments
sudo ./exp setup                     # パッケージ、バイナリ、ルータ 2 台、ISP 2 つ、ホスト 200 台
sudo ./exp run all                   # 全実験（1 時間程度）
sudo ./exp teardown                  # 作ったものをすべて削除
```

まず動作を確かめるには、小さい設定（ホスト 20 台、20 分程度）を使います。

```bash
sudo ./exp -c config/quick.toml setup
sudo ./exp -c config/quick.toml run all
```

各実験は表を表示し、同じ内容と生データを `results/<実験名>_<日時>/` に保存します。
`FAIL` になった実験があると、`run` は 0 以外の終了コードを返します。

## コマンド

| コマンド | 動作 |
|----------|------|
| `./exp check` | `setup` の前に各マシンを確認する（接続、時刻、インターフェース、使用中のポート）。何も変更しない |
| `./exp setup [--rebuild]` | 不足パッケージの導入、`gorad` と controller のビルド、ネットワークの作成、サービスの起動を行い、全ホストにベースライン RA が届くまで待つ |
| `./exp run <実験名>... \| all` | 実験を実施する（下の表） |
| `./exp list` | 実験の一覧を表示する |
| `./exp status` | ルータ、そのアドレス、現在の RA パラメータを表示する |
| `./exp teardown` | プロセスを止め、`setup` が作ったものをすべて削除する |

`-c <ファイル>` で設定を選びます。既定は `config/single.toml` です。

## 実験

| 名前 | 論文での位置 | 測るもの | 出力 |
|------|--------------|----------|------|
| `verify` | V-A 節 | ポリシー適用の前後で、全ホストの経路表と、D1 の各プレフィクスおよびデフォルト宛先への first-hop ルータ | `first_hop_before.csv`、`first_hop_after.csv`、各グループ 1 台の経路表 |
| `forwarding` | 査読者 1 の指摘 4 | 全ホストから `curl`。サーバから見えた ISP と、両ルータの上流インターフェースを通った TCP パケット数（ホスト別・方向別） | `forwarding_hosts.csv` |
| `load` | V-B 節 | RA のフレームサイズと、ホスト側リンク上の RA のビットレート（式 3・4 との比較） | `load.json` |
| `spread` | V-C 節、指摘 5 | 1 巡のユニキャスト RA の最初と最後の時間差。ルータ側とホスト側リンクの両方 | `spread_rounds.csv` |
| `overwrite` | 指摘 1 | ポリシー RA が専用の送信元アドレスを使う場合と、ベースラインと同じアドレスを使う場合の、デフォルトルータ優先度の時間変化 | `overwrite.json` |
| `transition` | 指摘 2 | ping を流したままポリシーを変更・削除したときの、損失数と経路変更の順序 | `summary.md` の表 |
| `failover` | V-D 節、指摘 3 | 4 種類の障害での切り替え。検知・取り下げ・ホスト処理の時間に分ける | `failover_hosts.csv`、`timeline_<シナリオ>.csv` |

各実験の手順・測定方法・判定の基準は **[EXPERIMENTS_ja.md](EXPERIMENTS_ja.md)** にまとめています。

どの結果ディレクトリにも、`summary.md`（表示した内容）、`meta.json`（設定、カーネル、
git のリビジョン、RA パラメータ）、`raw/`（キャプチャとログ）が入ります。

### `failover` の詳細

Router(b) を 4 通りの方法で止め、それぞれ `trials` 回繰り返します。

| シナリオ | 障害 | 断が終わるきっかけ |
|----------|------|--------------------|
| `withdraw` | 上流リンクが切れる。ルータ上の watchdog が検知して RA デーモンを止め、デーモンが lifetime 0 の最終 RA を送る | 取り下げ RA |
| `power_loss` | RA デーモンを強制終了し、同時に両インターフェースを落とす | ホストの近隣到達不能検知（NUD） |
| `daemon_crash` | RA デーモンを強制終了し、その後で上流リンクが切れる | Router Lifetime 切れ |
| `withdraw_lost` | `withdraw` と同じだが、ルータ自身が出す RA をすべて落とす | Router Lifetime 切れ |

障害の直前に Router(b) から出ていた各ホストについて、次を測ります。

| 値 | 始点 | 終点 | 時計 |
|----|------|------|------|
| `T_detect` | 障害を起こした時刻 | watchdog が検知した時刻 | ルータ |
| `T_withdraw` | watchdog が検知した時刻 | そのホスト向けの最後の取り下げ RA がホスト側リンクに出た時刻 | ルータ → ホスト側マシン |
| `T_host` | その RA がリンクに出た時刻 | ホストからデフォルト経路が消えた時刻 | ホスト側マシン |
| `outage` | — | そのホストの ping が途切れた最長時間 | ホスト側マシン |

表には、中央値・最小・最大、試行回数、観測時間内に復帰しなかったホストの数が出ます。
後ろの 2 シナリオは、1 試行が lifetime と同じだけかかるため、短い Router Lifetime
（`bound_lifetime_s`）を使います。

watchdog（`tools/watchdog.py`）は Polaris 本体ではなく、このスクリプト群の一部です。
上流インターフェースのキャリア断を検知します（`--mode ping` では上流ゲートウェイへの
ping の連続損失で検知します）。

## 2 つのモード

| | `single`（既定） | `testbed` |
|---|---|---|
| マシン | Linux 1 台。全ノードを network namespace で作る | 論文の 4 台構成を SSH で操作する |
| 上流 | 入口フィルタ付きの模擬 ISP 2 つと、サーバ | ルータがつながっている先（`upstream.kind = "real"`） |
| 時計 | 1 つ | マシンごと。chrony などで同期が必要 |
| 設定 | `config/single.toml` | `config/testbed.example.toml` をコピーして編集 |
| 動作確認 | 済み | **未確認** |

single モードの構成は次のとおりです。

```
ホスト pxh001… ─┐                       ┌─ px-ia (ISP-A) ─┐
                ├ br-lan ┬ px-ra ── wan ─┘                 ├ br-core ─ px-srv（サーバ）
                │        └ px-rb ── wan ─── px-ib (ISP-B) ─┘
px-ctl（controller）─ br-mgmt ─ px-ra, px-rb
```

各ルータはサイトのプレフィクスを自分の ISP のプレフィクスへ変換します（NPTv6）。
各 ISP は、送信元アドレスが自分のプレフィクス外のパケットを捨てます。
そのため、意図しないルータから出た通信には応答が返りません。

testbed モードには専用の手順書があります: **[TESTBED_ja.md](TESTBED_ja.md)**
（必要なもの、先に止めるもの、設定、`./exp check`、`setup` が各マシンで変更するもの、
障害実験の注意）。このモードは実機なしで書いたもので、一度も実行していません。

## 設定

`config/single.toml` に全項目と既定値があります。主な項目は次のとおりです。

| 項目 | 既定値 | 意味 |
|------|--------|------|
| `site.hosts` | 200 | 模擬ホストの台数 |
| `policy.g1_hosts` | 50 | ホスト 1〜n が G1（D1 を Router(a) 経由）。残りが G2（デフォルトを Router(b)） |
| `ra.baseline_interval_ms`、`ra.policy_interval_ms` | 400000、40000 | 論文の RA 送信間隔 |
| `ra.router_lifetime_s` | 1800 | Router Lifetime |
| `experiments.failover.trials` | 10 | `withdraw` と `power_loss` の試行回数 |
| `experiments.failover.bound_trials` | 5 | `daemon_crash` と `withdraw_lost` の試行回数 |

一部の実験（`load`、`spread`、`overwrite`、failover の lifetime 切れ待ちの 2 シナリオ）は、
数分で終わるように論文より短い RA 間隔を使います。使った値は設定ファイルと
各 `meta.json` に残ります。

## Route Information Option のカーネル対応

Linux が Route Information Option の経路を入れるのは、カーネルに
`CONFIG_IPV6_ROUTE_INFO` がある場合だけです（Ubuntu にはあります）。
`setup` はホストを置くマシンでこれを確認します。ない場合（Docker Desktop が典型）は、
各ホストで `rio-shim` を動かします。カーネルと同じ形で経路を入れる小さなプログラムです。
この場合の結果は「ルータが正しい RA を送っている」ことを示しますが、
カーネルの挙動の証拠にはなりません。どちらだったかは `meta.json` の `kernel_rio` に残ります。

## Linux マシンなしで試す

`dev/docker.sh` は、リポジトリをマウントした特権付きの Ubuntu コンテナを起動します
（macOS / Windows の Docker Desktop 用）。

```bash
dev/docker.sh ./exp -c config/quick.toml setup
dev/docker.sh ./exp -c config/quick.toml run all
docker rm -f polaris-exp-dev         # 終わったら
```

これはスクリプトの動作確認用です。Docker Desktop のカーネルでは `rio-shim` が必要なので
（上記）、論文に載せる結果には使えません。別の go-ra チェックアウトからビルドするには
`GO_RA_DIR` を設定します。

## ファイル

```
experiments/
├── exp                      コマンド本体
├── config/                  single.toml、quick.toml、testbed.example.toml
├── expkit/                  Python モジュール（標準ライブラリのみ）
│   ├── config.py            設定ファイル
│   ├── node.py              ローカルまたは SSH 先、namespace 内でのコマンド実行
│   ├── topology.py          ネットワークを作るシェルスクリプト
│   ├── provision.py         setup と teardown
│   ├── site.py              ルータ、controller の API、ホストの状態
│   ├── policy.py            シナリオと、それが作るべきホストの状態
│   ├── capture.py           tcpdump とその解析
│   ├── measure.py           ホストの ping と経路変更イベント
│   ├── report.py            結果ファイル
│   └── experiments/         実験ごとのモジュール
├── tools/                   各マシン上で動かすもの: hostprobe.py、watchdog.py、webserver.py
├── dev/                     Docker 用の補助スクリプト
└── results/                 出力（git 管理外）
```

## トラブルシューティング

- **`the site is not running`**: `./exp setup` を実行します。
- **`go-ra source not found`**: `git submodule update --init` を実行するか、`go_ra_dir` を設定します。
- **ビルドに失敗する**: 初回のビルドは Go のツールチェーンとモジュールをダウンロードします。
  インターネット接続が必要です。
- **`dmesg` に `neighbor table overflow` が出る、RA が届かないホストがある**:
  カーネルの近隣テーブルの上限は全 namespace で共通です。`setup` が上限
  （`net.ipv6.neigh.default.gc_thresh1/2/3`）を上げます。コンテナ内からは上げられないため、
  その場合は `dev/docker.sh` が Docker の VM 側で上げます。
- **実験を途中で止めた**: `./exp setup` をやり直します。きれいな状態から始まります。
- **`N host(s) did not reach the '…' state`**: メッセージに最初の差分が出ます。
  ホストは `ip -n pxh001 -6 route show`、RA デーモンのログは
  `/var/tmp/polaris-exp/proc/gorad-a.err` で確認します。

## 1 アドレス設計

`config/single-address.toml` は、提出版の論文の設計（各ルータが policy RA を baseline RA と同じリンクローカル
アドレスから送る、`site.single_address = true`）を再現します。`-c config/single-address.toml` で指定します。
`load` と `spread` は 2 種類の RA を送信元アドレスで区別するため、この設計では実行できません。`transition` は、
デフォルト経路の移動（既存エントリの優先度の書き換えだけ）については、経路変更の順序を判定しません。

## 実機での手動実行

`./exp` を使わず、実機で転送の確認と協調的な failover の 2 実験を手で行う手順は
[MANUAL_ja.md](MANUAL_ja.md) にあります。結果は `tools/manual_analyze.py` でキットと同じ形式にまとめます。
