# 実機で実験を行う手順

論文の 4 台構成（サイト出口ルータ 2 台、RA controller、エンドホストのエミュレータ）で実験を行う手順です。
1 台で完結させる方法は [README_ja.md](README_ja.md) を参照してください。

[English](TESTBED.md) / [日本語](TESTBED_ja.md)

> **状態: 未検証です。** 1 台で完結するモード（single）は最初から最後まで動かしています。
> 実機モード（testbed）は、実機なしで書いてレビューしただけで、一度も実行していません。
> 以下の順番どおりに進め、`./exp check` の出力を読み、最初の実行では細かい修正が必要になる
> 前提で使ってください。重要な結果は single モードの結果と突き合わせてください。

## 1. どこで何が動くか

```
 操作用マシン ── SSH ──┬─ controller   RA controller（API はポート 8080）
   ./exp …             ├─ router-a     gorad、NPTv6、上流 A
                       ├─ router-b     gorad、NPTv6、上流 B
                       └─ emulator     ブリッジ + ホスト用 namespace N 個

 サイト LAN: emulator の uplink ── スイッチ ── router-a の lan_if、router-b の lan_if
 管理用:     全マシンに SSH で届くこと。ただしルータの lan_if / wan_if と
             emulator の uplink は使わないこと
```

`./exp` は操作用マシンで実行します（controller のマシン自身でも構いません）。
他のマシンには SSH だけで接続し、そこでの処理はすべて `sudo` で実行します。
恒久的なインストールはしません。プロセスはバックグラウンドで起動し、ファイルはすべて
`workdir`（`/var/tmp/polaris-exp`）に置きます。

## 2. 必要なもの

| 場所 | 必要なもの |
|------|------------|
| 操作用マシン | Python 3.11 以上、`ssh`、`tar`、このリポジトリのチェックアウト（go-ra サブモジュール込み: `git submodule update --init`） |
| 対象の全マシン | `apt` のある Linux（スクリプトは Ubuntu 24.04 を想定）。操作用マシンからの SSH **鍵**ログイン。そのユーザーのパスワードなし `sudo` |
| ルータと controller | 初回ビルド用のインターネット接続。Go がツールチェーンとモジュールをダウンロードします（controller は `gcc` も必要。どちらも `setup` が `apt` で入れます） |
| 対象の全マシン | 時刻同期（chrony か NTP）。`T_withdraw` は、ルータの時刻と emulator の時刻の差を取ります |
| ルータ | SSH と controller からの gRPC（`mgmt_addr`）に使う**管理用インターフェース**。`lan_if` でも `wan_if` でもないこと。障害実験がこの 2 つを落とします |
| ルータ | サイトのプレフィクスから ISP のプレフィクスへの NPTv6 と、ISP のプレフィクスがルータに届くための設定（ND proxy、経路）。すでにある（`manage_nptv6 = false`）か、`setup` に NPTv6 のルール 2 本を入れさせる（`manage_nptv6 = true`）かを選びます。ND proxy は `setup` では設定しません |
| emulator | サイト LAN だけにつながった NIC（`emulator_uplink`）。ブリッジに接続され、その NIC 自身の IP 設定は使えなくなります |
| emulator | `CONFIG_IPV6_ROUTE_INFO` のあるカーネル（Ubuntu にはあります）。ない場合、RIO の経路は `rio-shim` が入れるので、カーネルの挙動の証拠にはなりません |

パスワードなし `sudo` がまだない場合（対象の各マシンで）:

```bash
echo "$USER ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/polaris-exp
```

実験が終わったら、このファイルは削除してください。

## 3. 最初の実行の前に

### 3.1 干渉するものを止める

`setup` は、各ルータで専用の `gorad`（gRPC はポート 50051）を、controller のマシンで専用の
controller（ポート 8080）を起動します。同じ役割のものがすでに動いていれば、先に止めます。

```bash
# 各ルータで
sudo systemctl stop gorad radvd 2>/dev/null     # サイト LAN に RA を送っているもの
# controller のマシンで
sudo systemctl stop ra-controller 2>/dev/null   # または docker compose down
```

`setup` が起動する `gorad` は、ベースライン RA（サイトのプレフィクス、優先度 medium、設定した
Router Lifetime）を 1 つ広告するだけです。普段の RA 設定に入れているオプション（RDNSS、MTU、
他のプレフィクス）は、実験中は出ません。

### 3.2 設定を書く

```bash
cd experiments
cp config/testbed.example.toml config/testbed.toml
```

| 項目 | 書く内容 |
|------|----------|
| `machines.controller`、`machines.emulator`、`router.ssh` | SSH の接続先（`user@host`）。管理用ネットワークで届くもの |
| `machines.emulator_uplink` | emulator の、サイト LAN につながる NIC。**SSH に使っているインターフェースは絶対に書かない** |
| `site.hosts`、`policy.g1_hosts` | 論文では 200 と 50 |
| `site.lan_prefix` | ルータがホストに広告するサイトのプレフィクス |
| `site.manage_nptv6` | ルータがすでに変換しているなら `false`。`setup` にルールを入れさせるなら `true`（`teardown` で消えます） |
| `router.isp_prefix` | そのルータの ISP の /64。サーバから見えたアドレスで ISP を判定するのに使います |
| `router.mgmt_addr` | **controller** がそのルータに届くアドレス（gRPC、ポート 50051） |
| `router.lan_if`、`router.wan_if` | ルータ上のインターフェース名 |
| `router.fail_cmd`、`router.restore_cmd` | そのルータの上流を切る・戻す方法（既定: `ip link set <wan_if> down` / `up`）。スイッチのポートや別の機器を操作したい場合に使います |
| `upstream.kind` | 実 ISP なら `real`。testbed 内に ISP とサーバの模擬があるなら `emulated` |
| `upstream.urls.default` | 実 ISP の場合: `curl -6` で取得する URL。クライアントのアドレスを返すもの（例: `https://ifconfig.co/ip`）。ホストがどの ISP から出たかの判定に使います |
| `upstream.urls.d1` | 任意。アドレスが `policy.d1` の中にある URL。1 つ、または D1 のプレフィクスごとに 1 つずつ並べたリスト。パケット数だけで判定します |
| `policy.default_dst` | D1 の**外**で、ping に応答するアドレス。障害実験と transition 実験が連続して ping します |
| `policy.d1_dst` | D1 の**中**で、ping に応答するアドレス。transition 実験に必要です。既定値（先頭プレフィクスの先頭アドレス）は、インターネット上では応答しません |
| `experiments.failover.testbed_confirmed` | 5 節が済むまで `false` のまま |

`policy.d1` の既定は、論文の Microsoft Teams の 7 プレフィクスです。その中に、サイトから ping が
通るアドレスがない場合は、transition 実験を省くか、ping の通るホストを含むプレフィクスを
`policy.d1` に設定し、その旨を論文に書いてください。

### 3.3 何も変更せずに確認する

```bash
./exp -c config/testbed.toml check
```

確認結果を 1 行ずつ表示します。どのマシンにも変更は加えません。

| 確認項目 | `FAIL` の意味 |
|----------|---------------|
| access | SSH か `sudo` が使えない |
| clock | （警告）操作用マシンとの時刻差が、測定誤差で説明できる範囲を超えている |
| packages、build | 足りないものがあり入れられない、または Go のモジュールプロキシに届かない |
| lan_if、wan_if、baseline address | インターフェースが存在しない、落ちている、リンクローカルアドレスがない |
| port | gRPC のポート（ルータ）か API のポート（controller）がすでに使われている。3.1 節を参照 |
| management path | SSH が `lan_if` か `wan_if` 経由でルータに届いている。確認できるのは IPv6 のセッションだけなので、IPv4 の場合は手で確認する |
| uplink | emulator の uplink が存在しない、または SSH がそれを使っている |
| reach Router(x) | controller からルータの `mgmt_addr` に ping が通らない |

注意して読むべき警告: `RA daemons`（別の RA デーモンが動いている）、`upstream route`
（ルータがデフォルト経路を RA で学習していて、forwarding を有効にすると失う。`wan_if` に
`accept_ra=2` を設定する）、`NPTv6`（`manage_nptv6` が false なのに変換ルールが見つからない）、
`RIO support`、`policy.d1_dst`。

`FAIL` をすべて直してから先へ進みます。

## 4. 構築と実施

```bash
./exp -c config/testbed.toml setup          # 初回は数分（ビルド）
./exp -c config/testbed.toml status
./exp -c config/testbed.toml run verify     # まず一番軽い実験から
./exp -c config/testbed.toml run forwarding load spread overwrite transition
```

### `setup` が各マシンで変更するもの

| マシン | 変更 | `teardown` での扱い |
|--------|------|---------------------|
| 全部 | `workdir` を作る（モード 0700、root 所有）。足りないパッケージを入れる（`iproute2`、`tcpdump`、`curl`、`python3`、`iputils-ping`。ルータは `iptables`、ビルドするマシンは `golang-go`、`gcc`） | ディレクトリもパッケージも残る |
| ルータ | `gorad` をビルドする。`net.ipv6.conf.all.forwarding=1` を設定する。`lan_if` にポリシー用リンクローカルアドレス（`policy_ll`、既定は `fe80::face:a` / `fe80::face:b`）を追加する。`manage_nptv6` が true なら `ip6tables -t mangle` にチェーンを 2 つ（`PX_POST`、`PX_PRE`）追加する。`gorad` を `[::]:50051` で起動する | アドレスとチェーンを削除し、`gorad` を止める。**forwarding は 1 のまま** |
| controller | controller をビルドし、ポート 8080 で起動する。データベースは `workdir` に新規作成、定期再適用は無効 | 止める |
| emulator | 近隣テーブルの上限を上げる（`net.ipv6.neigh.default.gc_thresh1/2/3`）。ブリッジ `br-px` を作り、ブリッジと uplink の IPv6 を無効にして、uplink を接続する。ホスト用の namespace `pxh001…` を、固定の MAC アドレス `02:00:00:00:xx:xx` で作る | namespace とブリッジを削除する。**uplink は外れたまま、IPv6 無効のまま**。上限もそのまま |

ホストはリンクローカルアドレス `fe80::ff:fe00:<n>` を持ち、それ以外は実際のホストと同じく
RA から学習します。各ホストに設定するのは 1 項目だけです（`accept_ra_rt_info_max_plen=128`。
これがないと Linux は Route Information Option を無視します）。

### 実験ごとの注意

| 実験 | 実機での動き |
|------|--------------|
| `verify` | 経路表を読むだけ。上流に依存しません |
| `forwarding` | 実 ISP では、各ホストが `upstream.urls` に `curl -6` します。応答に含まれるアドレスから ISP を判定し（`isp_prefix`）、両ルータの `wan_if` でパケットを数えます |
| `load`、`spread` | ルータの `lan_if` と emulator のブリッジでキャプチャします。実機では、`spread` の 2 つの列の差が「間にあるネットワークの影響」になります。査読者の指摘 5 が求めている測定です |
| `overwrite` | 共有アドレスで `gorad` を起動し直し、終わったら戻します |
| `transition` | ポリシーを変更している間、全ホストから `policy.default_dst` と `policy.d1_dst` に ping します。両方が応答する必要があります |
| `failover` | 5 節を参照 |

各実行は `meta.json` に、各マシンのカーネル・OS・時刻差を記録します。論文のソフトウェア仕様の
表に使えます。

## 5. 障害実験（failover）

意図的に壊す実験は、これだけです。Router(b)（2 番目の `[[router]]`）に対して次を行い、
観測時間の後で元に戻します。

* `fail_cmd` で上流を切る（`withdraw`、`daemon_crash`、`withdraw_lost`）
* `gorad` を強制終了し、**`lan_if` と `wan_if` を落とす**（`power_loss`）
* 送信する RA を捨てる `ip6tables` ルールを入れる（`withdraw_lost`）

有効にする前に:

1. Router(b) への SSH と `mgmt_addr` が、`lan_if` と `wan_if` を**使っていない**ことを確認する。
   `./exp check` が確認できるのは IPv6 のセッションだけです。
2. 最初の実行では、Router(b) にコンソールで入れるようにしておく（IPMI、シリアル、近くにいる人）。
3. ルータ上で `wan_if` を落とす以外の方法で上流を切りたい場合は、`fail_cmd` / `restore_cmd` を
   設定する（ルータ上で root として実行されます）。
4. `experiments.failover.testbed_confirmed = true` にする。

まず、1 つのシナリオを少ない試行回数で動かします。

```toml
[experiments.failover]
testbed_confirmed = true
scenarios = ["withdraw"]
trials = 2
```

```bash
./exp -c config/testbed.toml run failover
```

問題がなければ、シナリオの一覧と試行回数を戻します。

ここでの `power_loss` は「RA デーモンを強制終了し、同時に両インターフェースを落とす」という意味です。
SSH で復旧できるように、マシン自体は動かしたままにします。実際に電源を切る場合は手作業で行い、
1 回分の測定と比べてください。

### 実行が中断された場合

Ctrl+C で止めた場合は、終了前に Router(b) を元に戻そうとします。操作用マシン自体が落ちた場合は、
Router(b) のインターフェースが落ちたまま、RA を捨てるルールが入ったままになることがあります。
復旧するには、Router(b) 上で次を実行します。

```bash
sudo ip link set <lan_if> up; sudo ip link set <wan_if> up
sudo ip6tables -D OUTPUT -o <lan_if> -p ipv6-icmp --icmpv6-type 134 -j DROP
```

その後、`./exp -c config/testbed.toml setup` をやり直します。きれいな状態から始まります。

## 6. 終了

```bash
./exp -c config/testbed.toml teardown
```

その後、手作業で次を行います。

* 自分の RA デーモンと controller を起動し直す（3.1 節）。
* emulator で、uplink の NIC の設定を戻す
  （`sudo sysctl -w net.ipv6.conf.<uplink>.disable_ipv6=0` のあと、普段のネットワーク設定）。
* ルータで、`net.ipv6.conf.all.forwarding` が元は 0 だった場合は戻す。
* `/etc/sudoers.d/polaris-exp` を作った場合は削除する。ビルドのキャッシュが不要なら `workdir` も削除する。

結果は、操作用マシンの `experiments/results/` にあります。

## 7. トラブルシューティング

| 症状 | 考えられる原因 |
|------|----------------|
| `setup` が `the controller cannot reach every router` で止まる | `mgmt_addr` が違う、または controller からポート 50051 への通信がファイアウォールで止められている |
| `N host(s) did not reach the 'none' state` | ホストにルータの RA が届いていない。uplink が別のネットワークにつながっている、スイッチが RA を止めている（RA Guard）、別の RA デーモンが応答している、のいずれか。emulator 上でホストを 1 台確認する: `sudo ip -n pxh001 -6 route show` |
| ホストに経路はあるが、片方のルータだけ `forwarding` が失敗する | そのルータの NPTv6 のルールか戻りの経路（ND proxy、ISP 側の経路）。ホストがサイトのプレフィクスのまま出て、ISP に捨てられている |
| `T_withdraw` が負の値や不自然な値になる | ルータと emulator の時計がずれている。`meta.json` の時刻差を確認する |
| emulator の `dmesg` に `neighbor table overflow` が出る | 上限が上がっていない。`setup` は sysctl に書ける場合に上げます |
