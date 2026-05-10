# Polaris

エンドホストを変更しない、IPv6 サイトマルチホーミングのためのホストベースの
ポリシールーティングフレームワーク

ネットワーク管理者が、IPv6 サイトマルチホーミング環境において、
**エンドホストの設定を一切変更することなく**、各ホスト（あるいはホストグループ）の
トラフィックを特定の上位 ISP に振り分けることを可能にします。これは
[NPTv6](https://www.rfc-editor.org/rfc/rfc6296) と、
**動的に制御されるユニキャスト Router Advertisement** (RFC 4191) を組み合わせて
実現されます。

[English](README.md) / [日本語](README_ja.md)

## なぜ Polaris か

従来の IPv6 サイトマルチホーミングは、エンドホスト側での
**送信元アドレス選択**に依存して、使用する上位 ISP が決定されます。

Polaris は **ISP 選択の起点を「送信元アドレス選択」から「ファーストホップ
ルータ選択」へシフト** します。すべてのホストは単一の IPv6 プレフィクスを共有し
（各サイト出口ルータが NPTv6 で変換）、フレームワークは適切な DRP/RIO 値を
持つ **ホスト単位のユニキャスト RA** を送信することで、各ホストが使用する
サイト出口ルータを制御します。

> DRP: Default Router Preference
> RIO: Route Information Option

## アーキテクチャ

![architecture](./imgs/overview.png)

管理者は RA Controller の Web UI でポリシーを定義します。コントローラは
各ポリシーを各ルータ向けの gRPC `InterfaceConfig` にコンパイルし、対応する
`gora` デーモンへ push します。`gora` は対象ホストへユニキャスト RA を
送信し、デフォルトゲートウェイ（`DRP=high`）または経路情報（`RIO`）を
更新します。

## リポジトリ構成

| パス | 役割 |
|------|------|
| [`agent/`](agent/) | サイト出口ルータ用エージェント（`gora` デーモン、NPTv6 セットアップ、systemd ユニット）|
| [`agent/go-ra/`](agent/go-ra/) | go-ra ソース — git submodule、[YutaroHayakawa/go-ra](https://github.com/YutaroHayakawa/go-ra) からのフォーク |
| [`controller/`](controller/) | RA Controller — Go バックエンド、React/Vite フロントエンド、Neighbor / Endpoint Collector モジュール |
| [`laboratory/`](laboratory/) | 自己完結型 Docker Compose ラボ（ルータ 3 台、ホスト 10 台、コントローラ、Web サーバ）|

各サブディレクトリには専用の README があります。ビルド・デプロイの詳細は
そちらを参照してください。

## Polaris の動作モード

### 1. Docker ラボ

ルータ 3 台、ホスト 10 台、コントローラ、上流 Web サーバを含む再現可能な完全
環境。

```bash
git clone --recurse-submodules <this-repo>
cd polaris
docker compose -f laboratory/docker-compose.yaml up --build
# http://localhost:3000 を開く
```

デモ手順、検証スクリプト、Docker 固有の注意点については
[`laboratory/README_ja.md`](laboratory/README_ja.md) を参照してください。

### 2. コントローラ単独実行（実機ルータでのテスト用）

管理ホスト上で通常プロセスまたは systemd ユニットとしてコントローラを起動し、
事前にデプロイ済みの `gora` エージェントと連携させます。

```bash
cd controller
./server.sh                 # 開発モード（frontend :5173 / backend :8080）
```

systemd デプロイ、パラメータファイル形式、モジュール設定については
[`controller/README_ja.md`](controller/README_ja.md) を参照してください。

### 3. 本番用ルータ・エージェント

各サイト出口ルータにエージェントをデプロイします。NPTv6 / NDP プロキシ /
sysctl の設定および `gora` の systemd サービス化を自動で行います。

```bash
cd agent
git submodule update --init
sudo WAN_PREFIX=2001:db8:wan::/64  \
     LAN_PREFIX=fc00:cafe::/64     \
     WAN_IF=eth0  ./setup.sh
```

運用ガイドは [`agent/README_ja.md`](agent/README_ja.md) を参照してください。

## 主要な概念

| 用語 | 意味 |
|------|------|
| **Rule** | `(宛先プレフィクス集合) → (サイト出口ルータ)` のマッピング |
| **Group** | 複数ホストの link-local アドレスから成る名前付き集合 |
| **Policy** | Group と Rule の関連付け — グループ所属ホスト全員に適用される |
| **DRP** | Default Router Preference (RFC 4191) — `high` でそのルータが優先デフォルトゲートウェイになる |
| **RIO** | Route Information Option (RFC 4191) — 特定プレフィクス向けの経路をインストール |
| **NPTv6** | プレフィクス 1 対 1 変換 (RFC 6296) — ホストプライバシーを保ち、Ingress filter を回避 |

### Policy から RA への変換

ルータ *n* を対象とする各ポリシーに対し、コントローラはルールごとに
`InterfaceConfig` を 1 つ生成します:

- 宛先集合に `::/0` が含まれる → `preference = high`（DRP）を設定し、
  *n* を対象クライアントの優先デフォルトゲートウェイにする
- それ以外 → 各プレフィクスを `preference = medium` の `RIO` として
  送出し、特定プレフィクス向けの経路を *n* 経由でインストール
- 結果 RA を **ユニキャスト** で各メンバーホストへ送信

実装は
[`controller/backend/internal/engine/engine.go`](controller/backend/internal/engine/engine.go)
にあります。
