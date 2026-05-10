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

## systemd デプロイ

### インストール手順

**1. サブモジュールを初期化**

```bash
git submodule update --init
```

**2. NPTv6 パラメータを設定して setup を実行**

```bash
sudo WAN_PREFIX="2001:db8:wan::/64" \
     LAN_PREFIX="fd00:cafe::/64"    \
     WAN_IF="eth0"                  \
     ./setup.sh
```

`setup.sh` は以下の環境変数を受け付けます：

| 変数名 | デフォルト値 | 説明 |
|--------|-------------|------|
| `INSTALL_DIR` | `/opt/go-ra-server` | インストール先ディレクトリ |
| `GRPC_ADDR` | `localhost:50051` | gorad の gRPC リッスンアドレス |
| `WAN_PREFIX` | `2001:2f8:1c0:7040::/64` | NPTv6 の WAN 側 IPv6 プレフィックス |
| `LAN_PREFIX` | `2001:cafe:dead:beef::/64` | NPTv6 の LAN 側 IPv6 プレフィックス |
| `WAN_IF` | `enp2s0` | WAN 側ネットワークインタフェース |

**3. RA 設定を編集**

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
| `DeleteInterface` | RA インスタンスを削除する（設定済みの場合は goodbye RA を送信）|

```bash
# grpcurl を使った簡易確認
grpcurl -plaintext localhost:50051 gora.v1.GoRAService/GetStatus
```

完全な API 定義は [`go-ra/api/gora/v1/gora.proto`](go-ra/api/gora/v1/gora.proto) を参照してください。
