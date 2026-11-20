# go-ra-server エージェント

[go-ra](https://github.com/YutaroHayakawa/go-ra) をベースにした IPv6 ルータ広告エージェントです。polaris システムにおいて、各ルータノード上に配置されます。

エージェントは `gorad`（go-ra デーモン）を実行し、ローカルネットワーク上のクライアントへ IPv6 ルータ広告（RA）を送信するとともに、実行時ポリシー管理のための gRPC API を公開します。[RA Controller](../controller) がこの gRPC エンドポイントに接続して RA 設定を動的に変更します。また、LAN 側プレフィックスを WAN から到達可能にするため、NPTv6 プレフィックス変換と NDP プロキシも設定します。

`setup.sh` スクリプトが以下の一連のセットアップを自動化します：
1. IPv6 カーネルパラメータの設定
2. NPTv6 ルールの設定（LAN ↔ WAN プレフィックス変換）
3. NDP プロキシ（`ndppd`）の起動
4. `gorad` バイナリのビルドとデプロイ
5. systemd サービスのインストールと起動

## コンポーネント

| パス | 役割 |
|------|------|
| `go-ra/` | go-ra ソース（git サブモジュール — YutaroHayakawa/go-ra のフォーク） |
| `systemd/go-ra-server.service` | systemd ユニットファイル |
| `setup.sh` | 初回セットアップスクリプト |
| `setup.conf.example` | `setup.sh` の設定ファイルの見本（`setup.conf` にコピーして編集） |

`setup.sh` 実行後、`config.yaml` が `$INSTALL_DIR`（デフォルト: `/opt/go-ra-server`）にデプロイされます。

## 前提条件

- systemd を使用する Linux
- Go（`gorad` のビルドに必要）
- SNPT/DNPT ターゲットをサポートする `ip6tables`（NPTv6 に必要）
- `ndppd`（NDP プロキシデーモン）

## クイックスタート（手動 / 開発用）

```bash
# サブモジュールを初期化（未実施の場合）
git submodule update --init

# デーモンをビルド
cd go-ra
go build -o gorad ./cmd/gorad

# RA 設定を編集
cp config.yaml my-config.yaml
# ... インタフェース・プレフィックス等を編集 ...

# root として実行（生 ICMPv6 ソケットが必要）
sudo ./gorad -f my-config.yaml

# 別ターミナルで状態確認
gora status
```

## 設定

すべての RA 設定は `config.yaml`（`$INSTALL_DIR/config.yaml` にデプロイ）で定義します。

```yaml
interfaces:
- id: 1
  name: eth0
  raIntervalMilliseconds: 600000   # 10 分（RFC 4861 の非要請 RA 最大間隔）
  currentHopLimit: 64
  managed: false
  other: false
  routerLifetimeSeconds: 1800
  preference: medium
  reachableTimeMilliseconds: 30000
  retransmitTimeMilliseconds: 1000
  mtu: 1500

  prefixes:
  - prefix: "2001:db8::/64"
    onLink: true
    autonomous: true
    validLifetimeSeconds: 2592000    # 30 日
    preferredLifetimeSeconds: 604800 # 7 日

  routes:
  - prefix: "2001:db8:1::/48"
    lifetimeSeconds: 3600
    preference: high

  sendGoodbye: true
```

全オプションのリファレンスは [`go-ra/config.yaml`](go-ra/config.yaml) を参照してください。

## ポリシー RA の送信元アドレス

コントローラが送らせるポリシー RA は、ベースライン RA と送信元アドレスを
共有してはいけません。ホストは送信元アドレスごとにデフォルトルータのエントリを
1 つ持つため、共有すると互いの優先度と lifetime を上書きします。
LAN 側インターフェースに 2 つ目のリンクローカルアドレスを付け、
ポリシー RA 専用にします。

`setup.sh` は、`POLICY_LL`（と `LAN_IF`）が設定されているときにこれを行います。
設定ファイルでの例：

```
LAN_IF=eth1
POLICY_LL=fe80::face:1
```

サービスの起動前に毎回 `POLICY_LL` を `LAN_IF` に割り当てる systemd の drop-in を
インストールし、`config.yaml` のベースラインのインスタンスに、インターフェース本来の
リンクローカルアドレスを `sourceAddress` として書き込みます
（リンクローカルが 2 つあると、省略時にどちらが使われるかは決まっていないため、明示が必要です）。
残る手作業は 1 つです。RA コントローラで、そのルータの **Policy RA source address** に
`POLICY_LL` を設定してください。

既存の `config.yaml` を残す場合は、ベースラインのインスタンスに `sourceAddress` を
自分で追加してください。無い場合は `setup.sh` が警告します。

`POLICY_LL` はリンク上で一意である必要があります。未使用の `fe80::/64` のアドレスなら何でも構いません。
コントローラは、ベースラインと同じアドレスをポリシー用に指定した場合、適用を拒否します。

## systemd デプロイ

### インストール手順

**1. サブモジュールを初期化**

```bash
git submodule update --init
```

**2. 設定ファイルを書いて setup を実行**

```bash
cp setup.conf.example setup.conf
nano setup.conf                      # インターフェース、プレフィクス、ベースライン RA、ポリシー用アドレス
sudo ./setup.sh --print-config       # 設定と、書き出す config.yaml を表示するだけ(何も変更しない)
sudo ./setup.sh
```

`setup.sh` は `./setup.conf` があれば読み込みます（別のファイルは `-c FILE`）。
設定は root の動作を決めるので、ファイルの所有者は root か `sudo` を実行したユーザーで、
グループや他人から書き込めない状態である必要があります（`chmod go-w setup.conf`）。
そうでない場合、`setup.sh` は読み込みを拒否します。
ファイルは 1 行に 1 つの `KEY=VALUE` です。同じ名前の環境変数があればファイルより優先されるので、
1 つだけ変えたいときは従来どおり `sudo RA_INTERVAL_MS=400000 ./setup.sh` のように指定できます。
`WAN_IF`、`WAN_PREFIX`、`LAN_PREFIX` には既定値がありません。どれかが未設定の場合、`setup.sh` は
何も変更する前に停止します。

チェックアウトは、自分だけが書き込めるディレクトリに置いてください。`setup.sh` とその設定ファイルは
root として実行・読み込みされます。

| 設定 | デフォルト値 | 説明 |
|------|-------------|------|
| `WAN_IF` | （必須） | WAN 側ネットワークインタフェース |
| `WAN_PREFIX` | （必須） | NPTv6 の WAN 側 IPv6 プレフィックス。ISP からこのルータに委譲された /64 |
| `LAN_PREFIX` | （必須） | NPTv6 の LAN 側 IPv6 プレフィックス。ベースライン RA で広告する |
| `LAN_IF` | （空） | ホスト側のインタフェース。設定すると、`setup.sh` がベースライン RA の設定を書き出す |
| `RA_INTERVAL_MS` | `600000` | ベースライン RA の送信間隔（MaxRtrAdvInterval。gorad はこの固定間隔で送る） |
| `ROUTER_LIFETIME_S` | `1800` | ベースライン RA の Router Lifetime |
| `RA_MTU` | `0` | MTU オプション（AdvLinkMTU）。`0` で省略 |
| `RDNSS` | （空） | DNS サーバ（カンマ区切り）。空ならオプションを付けない |
| `RDNSS_LIFETIME_S` | `1800` | RDNSS オプションの lifetime |
| `PREFIX_VALID_S` / `PREFIX_PREFERRED_S` | `2592000` / `604800` | プレフィクスの lifetime |
| `CUR_HOP_LIMIT` | `64` | ホストに広告する hop limit |
| `REACHABLE_TIME_MS` / `RETRANS_TIME_MS` | `0` / `0` | `0` はルータが指定しないことを表す |
| `POLICY_LL` | （空） | ポリシー RA の送信元アドレス（上記）。`LAN_IF` が必要 |
| `OVERWRITE_CONFIG` | `no` | `yes` で `config.yaml` を書き直す。以前のファイルは `config.yaml.bak-<日時>` として残す |
| `INSTALL_DIR` | `/opt/go-ra-server` | インストール先ディレクトリ |
| `GO` | （自動で探す） | Go コンパイラのパス。`sudo` は `PATH` を引き継がないので、`setup.sh` は `/usr/local/go/bin` と実行ユーザーの Go のディレクトリも探す。別の場所にある場合は `GO` で指定する。ビルドは root ではなく、`sudo` を実行したユーザーの権限で行う |
| `GRPC_ADDR` | `localhost:50051` | gorad の gRPC リッスンアドレス |

ベースライン RA の既定値は、RFC 4861（6.2.1 節）と RFC 8106 の既定値です。

**3. RA 設定**

`LAN_IF` を設定すると、`setup.sh` はベースライン RA だけの `config.yaml` を書き出します。
内容は、全ホスト宛てに `LAN_PREFIX` を優先度 medium で広告し、設定があれば MTU と RDNSS の
オプションを付け、経路は含めない、というものです。ポリシーの設定はコントローラが gRPC で追加し、
このファイルには保存されません。

既存の `config.yaml` は、`OVERWRITE_CONFIG=yes` を指定しない限り変更されません。
`LAN_IF` を設定しない場合は go-ra の見本の `config.yaml` が置かれます。これは例
（インタフェース `eth0`、文書用プレフィクス、見本の経路）なので、使う前に編集が必要です。

```bash
sudo nano /opt/go-ra-server/config.yaml
sudo systemctl restart go-ra-server
```

### 日常的な操作

```bash
# 状態確認
sudo systemctl status go-ra-server

# 起動 / 停止 / 再起動
sudo systemctl start   go-ra-server
sudo systemctl stop    go-ra-server
sudo systemctl restart go-ra-server

# ライブログストリーム
sudo journalctl -u go-ra-server -f

# 最後の起動以降のログ
sudo journalctl -u go-ra-server -b

# RA デーモンの状態確認（インタフェース・送信カウント）
gora status
```

### アップデート

```bash
# 1. バイナリを再ビルド
cd go-ra && go build -o gorad ./cmd/gorad && cd ..

# 2. デプロイして再起動
sudo install -m 755 go-ra/gorad /opt/go-ra-server/gorad
sudo systemctl restart go-ra-server
```

### インストールパスの変更

```bash
sudo sed -i 's|/opt/go-ra-server|/your/path|g' \
  systemd/go-ra-server.service
```

その後、`INSTALL_DIR=/your/path` を指定して `setup.sh` を再実行してください。

## gRPC API

`gorad` は gRPC サーバ（デフォルト: `localhost:50051`）を公開します。RA Controller はこれを使用して、デーモンを再起動せずに実行時の RA ポリシーを変更します。

| RPC | 説明 |
|-----|------|
| `GetStatus` | 全 RA インスタンスの実行状態を返す（state、TX カウンタ）|
| `ListInterfaces` | 現在動作中の `InterfaceConfig` 一覧を返す（本フォークで追加。コントローラの RA Interface Detail パネルが使用）|
| `AddInterface` | インタフェース上に新しい RA インスタンスを追加する |
| `UpdateInterface` | 既存の RA インスタンスを更新する |
| `DeleteInterface` | RA インスタンスを削除する（設定済みの場合は goodbye RA を送信。`skip_goodbye` 指定時は送らない）|
| `SendRA` | 設定をインストールせずに RA を 1 通送る。コントローラが特定ホストから経路やデフォルトルータを取り下げるために使う |

```bash
# grpcurl を使った簡易確認
grpcurl -plaintext localhost:50051 gora.v1.GoRAService/GetStatus
```

完全な API 定義は [`go-ra/api/gora/v1/gora.proto`](go-ra/api/gora/v1/gora.proto) を参照してください。
