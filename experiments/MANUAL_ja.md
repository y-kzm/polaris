# 実機で 2 つの実験を手動で行う手順（最小改訂版用）

査読コメント R1-4（転送の確認）と R1-3（協調的な failover）の 2 実験を、論文の 4 台構成の実機で
**手動で**行う手順です。`./exp` による自動実行（`TESTBED_ja.md`）は使いません。
キットの道具（`tools/hostprobe.py`、`tools/watchdog.py`）と `tcpdump` を手で順に実行し、
最後に `tools/manual_analyze.py` で原稿用の形式にまとめます。

> **確認の範囲**: 集計スクリプトは、実験機（単一マシン）で取得した生データを使い、キット本体の集計と
> 結果が一致することを確認済みです。**実機での実行手順そのものは未検証**です。コマンドのインターフェース名や
> 名前空間名は環境に合わせて読み替えてください。

---

## 0. 前提

| 項目 | 内容 |
|---|---|
| 設計 | **1 アドレス設計**（提出版と同じ）。各ルータは baseline RA と policy RA を同じリンクローカルアドレスから送る。controller のルータ設定で policy source address は空にする |
| RA デーモン | `gorad` が停止時（SIGTERM）に Router Lifetime 0 の RA を送ること。go-ra の main（`b66c843` 以降）が必要。それより古い版は、停止時の RA が途中で切れる |
| エンドホスト | エミュレータ上のネットワーク名前空間（netns）。ホスト i（1〜200）の名前空間名が規則的であること |
| 時刻 | ルータ(b) とエミュレータの時計が同期していること（chrony）。failover の T_withdraw は 2 台の時計の差を含む |
| 管理用の経路 | SSH と controller ↔ ルータの通信が、**ルータ(b) の上流インターフェースを通らない**こと。failover でこのインターフェースを落とす |
| ツール | 各ルータとエミュレータに `tcpdump`、`python3`。エミュレータに `curl` |

### 名前空間名の指定

`hostprobe.py` は既定で `pxh001`、`pxh002`、… を使います。別の名前の場合は環境変数で指定します。

```sh
export PX_NS_FORMAT='pxh{:03d}'     # 既定。例: 名前が host1, host2, ... なら 'host{}'
```

以降のコマンドでは `sudo -E` で環境変数を引き継ぎます。

### 配置

```sh
# 手元 → 各マシンへ(パスは任意。以降 ~/px とする)
scp tools/hostprobe.py  <emulator>:~/px/
scp tools/watchdog.py   <router-b>:~/px/
```

### 記号

| 記号 | 意味 | 例 |
|---|---|---|
| `$WAN_A`、`$WAN_B` | ルータ(a)、(b) の上流インターフェース | `enp1s0` |
| `$BR` | エミュレータで、ホストの netns がつながるブリッジ | `br0` |
| `$DST` | D1 に含まれない宛先。ping に応答し、HTTP で接続元アドレスを返す | `2001:db8::1` |
| `$DST_D1` | D1 の中のアドレス。HTTP で応答する | `2603:1027::1` |

---

## 1. 準備（1 回だけ）

### 1.1 `site.json` を書く

手元の作業ディレクトリ（以降 `~/manual`）に作ります。

```json
{
  "hosts": 200,
  "g1_hosts": 50,
  "default_dst": "2001:db8::1",
  "ping_interval_s": 0.1,
  "victim": "b",
  "routers": {
    "a": {"name": "Router(a)", "isp": "ISP-A", "isp_prefix": "<ISP-A の /64>", "ll": "<ルータ(a) の LAN 側リンクローカル>"},
    "b": {"name": "Router(b)", "isp": "ISP-B", "isp_prefix": "<ISP-B の /64>", "ll": "<ルータ(b) の LAN 側リンクローカル>"}
  },
  "host_ll": "hosts_ll.txt",
  "machine": {"os": "Ubuntu 24.04.3 LTS", "kernel": "6.8.0-85-generic"},
  "ingress_drops": {"a": 0, "b": 0}
}
```

- `ll`: 各ルータが RA の送信元に使うリンクローカルアドレス。ルータで `ip -6 addr show dev <LAN 側> scope link`。
- `ingress_drops`: 上流網の ingress filter が破棄したパケット数。**読み取れない場合はこの行を消します**
  （その場合は 4.3 を参照）。

### 1.2 ホストのリンクローカルアドレスの一覧

エミュレータで実行し、`~/manual/failover/hosts_ll.txt` に置きます（failover の集計で使います）。

```sh
for i in $(seq 1 200); do
  ns=$(python3 -c "import os; print(os.environ.get('PX_NS_FORMAT','pxh{:03d}').format($i))")
  ll=$(sudo ip -n "$ns" -6 -o addr show scope link | awk '{print $4}' | cut -d/ -f1 | head -1)
  echo "$i $ll"
done > hosts_ll.txt
```

200 行あり、すべての行にアドレスが入っていることを確認します。

### 1.3 時計の確認

ルータ(b) とエミュレータの両方で実行します。

```sh
chronyc tracking | grep -E "System time|Last offset"
```

オフセットが 1 ms を大きく超える場合、T_withdraw と T_host の値は信頼できません
（集計時に負の値が出ると警告されます）。T_detect と途絶時間（outage）は影響を受けません。

---

## 2. R1-4: 転送の確認（forwarding）

「ポリシー適用前」「適用後」のそれぞれで、D1 内の宛先と D1 外の宛先に、全ホストから HTTP 要求を 1 回ずつ送ります。
合計 4 回の測定です。各測定で、ホスト i は送信元ポート `port-base + i` を使います
（NPTv6 は送信元プレフィクスを書き換えるので、上流側ではポートでホストを見分けます）。

| 測定 | タグ | port-base | 宛先 |
|---|---|---|---|
| 適用前、D1 | `before_D1` | 21000 | `$DST_D1` |
| 適用前、D1 外 | `before_default` | 22000 | `$DST` |
| 適用後、D1 | `after_D1` | 23000 | `$DST_D1` |
| 適用後、D1 外 | `after_default` | 24000 | `$DST` |

### 2.1 適用前の状態を作る

1. controller でポリシーを外して適用する（グループとルールを空にする）。
2. エミュレータで、全ホストの RA 由来の状態を消す。

```sh
for i in $(seq 1 200); do
  ns=$(python3 -c "import os; print(os.environ.get('PX_NS_FORMAT','pxh{:03d}').format($i))")
  sudo ip -n "$ns" link set eth0 down; sudo ip -n "$ns" link set eth0 up     # eth0 は netns 内のインターフェース名
done
sleep 10
sudo -E python3 ~/px/hostprobe.py snapshot --hosts 1,51 --dst $DST | python3 -m json.tool | head -40
```

デフォルト経路が 2 つ（両ルータ、`pref medium`）だけで、D1 の経路がないことを確認します。

### 2.2 1 回の測定（4 回繰り返す）

以下は `before_D1`（port-base 21000）の例です。タグ、port-base、宛先を表のとおりに変えて 4 回行います。

**(1) 両ルータでキャプチャを開始**（それぞれの端末で）

```sh
# ルータ(a)
sudo tcpdump -i $WAN_A -nn -tt -l -B 16384 'ip6 and tcp and portrange 21001-21200' > wan_a_before_D1.txt
# ルータ(b)
sudo tcpdump -i $WAN_B -nn -tt -l -B 16384 'ip6 and tcp and portrange 21001-21200' > wan_b_before_D1.txt
```

`listening on …` と表示されるまで待ちます。

**(2) エミュレータで要求を送る**

```sh
sudo -E python3 ~/px/hostprobe.py curl --hosts 1-200 --url "http://[$DST_D1]/" --port-base 21000 > curl_before_D1.json
```

URL がホスト名の場合は、`--resolve '<ホスト名>:443:[<アドレス>]'` で宛先アドレスを固定できます。

**(3) キャプチャを止める**: 両ルータで Ctrl-C。`0 packets dropped by kernel` を確認します。

### 2.3 ポリシーを適用する

1. controller で評価シナリオのポリシーを適用する（G1 = ホスト 1〜50 は D1 を Router(a)、G2 = 51〜200 はデフォルトを Router(b)）。
2. **適用の直後に** 2.2 の測定を `after_D1`、`after_default` の順に行う。

> **1 アドレス設計での注意**: G2 の `pref high` は、ルータ(b) の次の baseline RA で `medium` に戻ります。
> 測定の前後に次のコマンドで、ホスト 51 のデフォルト経路に `pref high` があることを確認してください。
> 測定中に `medium` に戻っていた場合は、次の policy RA（最長 40 秒）を待ってやり直します。
>
> ```sh
> sudo -E python3 ~/px/hostprobe.py snapshot --hosts 51 --dst $DST | python3 -m json.tool | grep -B2 -A2 pref
> ```

### 2.4 ファイルを集める

`~/manual/forwarding/` に次の 13 ファイルを置きます。

```
curl_before_D1.json   curl_before_default.json   curl_after_D1.json   curl_after_default.json
wan_a_before_D1.txt   wan_a_before_default.txt   wan_a_after_D1.txt   wan_a_after_default.txt
wan_b_before_D1.txt   wan_b_before_default.txt   wan_b_after_D1.txt   wan_b_after_default.txt
ports.json
```

`ports.json` は次の内容です（表と同じ値）。

```json
{"before_D1": 21000, "before_default": 22000, "after_D1": 23000, "after_default": 24000}
```

---

## 3. R1-3: 協調的な failover（10 試行）

ルータ(b) の上流が切れ、ルータ(b) 自身がそれを検知して RA デーモンを止め、Router Lifetime 0 の RA を送る場合を、
10 回測ります。各試行で、障害の時刻、検知の時刻、取り下げ RA の時刻、ホストの経路削除の時刻、エコー応答の途絶を記録します。

### 3.1 各試行の前の状態

- ポリシーが適用されていて、G2 がルータ(b) を使っている（ホスト 51 のデフォルト経路に `pref high`）。
- ルータ(b) で `gorad` が動いていて、その PID がファイルにある。

```sh
# ルータ(b)。gorad の起動方法に合わせて、どれか 1 つ
pidof gorad | awk '{print $1}' > /var/tmp/gorad.pid
systemctl show -p MainPID --value gorad > /var/tmp/gorad.pid      # systemd で動かしている場合
```

### 3.2 1 試行の手順

試行 `NN`（01〜10）ごとに行います。端末を 4 つ（エミュレータ 2、ルータ(a) 1、ルータ(b) 1 以上）使います。

**(1) エミュレータ: RA のキャプチャ**

```sh
mkdir -p trial_NN
sudo tcpdump -i $BR -nn -tt -l -e -v -B 16384 'icmp6 and ip6[40] == 134' > trial_NN/ra.txt
```

**(2) 両ルータ: 上流でエコー要求をキャプチャ**（どのホストがどちらのルータを使っていたかを知るため）

```sh
# ルータ(a)
sudo tcpdump -i $WAN_A -nn -tt -l -B 16384 'icmp6 and ip6[40] == 128' > wan_a.txt
# ルータ(b)
sudo tcpdump -i $WAN_B -nn -tt -l -B 16384 'icmp6 and ip6[40] == 128' > wan_b.txt
```

**(3) エミュレータ: 全ホストから ping を開始し、経路の変化を記録**

```sh
sudo -E python3 ~/px/hostprobe.py record --hosts 1-200 --dst $DST --interval 0.1 --outdir trial_NN/hosts &
while [ ! -f trial_NN/hosts/ready ]; do sleep 0.2; done; echo started
```

ホスト i は ICMPv6 のペイロード長 `56 + i` で送ります（上流側でホストを見分けるため）。

**(4) ルータ(b): 監視を開始**

```sh
sudo python3 ~/px/watchdog.py --wan $WAN_B --daemon-pidfile /var/tmp/gorad.pid --log watchdog.log &
```

`watchdog.log` に `armed` の行が出たことを確認し、**3 秒以上待ちます**。

**(5) ルータ(b): 障害を起こす**（時刻を記録してからインターフェースを落とす）

```sh
date +%s.%N > t_fail.txt; sudo ip link set $WAN_B down
```

**(6) 6 秒待って、すべて止める**

```sh
# エミュレータ
sudo pkill -TERM -f "hostprobe.p[y] record"     # ping と経路監視を止める([y] は sudo 自身に一致させないため)
# エミュレータ、両ルータ: tcpdump を Ctrl-C
```

`watchdog.log` に `detect link down` と `withdraw-signalled` の 2 行があることを確認します。

**(7) ファイルを集める**: `~/manual/failover/trial_NN/` に置きます。

```
trial_NN/t_fail.txt      ← ルータ(b)
trial_NN/watchdog.log    ← ルータ(b)(試行ごとに新しいファイルにする)
trial_NN/ra.txt          ← エミュレータ
trial_NN/wan_a.txt       ← ルータ(a)
trial_NN/wan_b.txt       ← ルータ(b)
trial_NN/hosts/          ← エミュレータ(ping_*.log と route_events.log)
```

### 3.3 次の試行の前に元へ戻す

1. ルータ(b): 上流を戻す。**`ip link set down` はそのインターフェースの IPv6 アドレスと経路を消します**。
   上流側のアドレス、デフォルト経路を、普段の設定方法（netplan など）で入れ直してください。

   ```sh
   sudo ip link set $WAN_B up        # このあと、アドレスと経路を入れ直す
   ping -6 -c 2 $DST                 # 上流への到達性を確認
   ```

2. ルータ(b): `gorad` を起動し直し、PID ファイルを更新する（3.1）。
3. エミュレータ: 全ホストの RA 状態を消す（2.1 のループ）。
4. controller: ポリシーを適用し直す。
5. ホスト 51 のデフォルト経路に `pref high`（ルータ(b)）があることを確認してから、次の試行へ。

---

## 4. 集計して原稿に反映する

手元（polaris のリポジトリがあるマシン）で行います。

### 4.1 集計

```sh
cd polaris/experiments
python3 tools/manual_analyze.py forwarding ~/manual/forwarding --site ~/manual/site.json --out ~/manual/results
python3 tools/manual_analyze.py failover   ~/manual/failover   --site ~/manual/site.json --out ~/manual/results
```

それぞれ表と `Result: PASS / FAIL` を表示し、`~/manual/results/forwarding_<日時>/`、`failover_<日時>/` に
キットと同じ形式（`summary.md`、`meta.json`、CSV）で書き出します。

### 4.2 確認すること

| 実験 | 期待される結果 |
|---|---|
| forwarding | 800 / 800 応答。`after` の D1 は G1 が Router(a) のみ、G2 が Router(b) のみで、反対側のルータは `0 / 0` |
| failover | 試行 10、測定数は約 1,700〜1,800（G2 の 150 台と、G1 のうちルータ(b) を選んでいた台数の 10 回分）、`not recovered` が 0 |

`FAIL` の行が出た場合は、内容を確認して該当の測定をやり直します。

### 4.3 原稿へ

```sh
cd MDPI_Electronics_REVISEv2_20261120
python3 data/make_values.py ~/manual/results
latexmk -pdf main.tex && pdflatex response_to_reviewers.tex
```

黄色の値、Table（転送の確認）、Table（切り替え時間）が実機の値に置き換わります。
その後、`main.tex` の 5 章にある著者向けの注記（黄色の枠）を削除します。

**`site.json` から `ingress_drops` を消した場合**: `make_values.py` はエラーで止まります。
`main.tex` の 5.1 節の文 "The ingress filters of the two upstream networks discarded … packets." と、
`response_to_reviewers.tex` の同じ内容の句を削除し、`make_values.py` の該当の検査を外してください。

---

## 5. 原稿の記述と実機の構成を合わせる点

v2 の本文は、次の構成を前提に書いています。実機が違う場合は、本文のほうを実機に合わせて直します。

| 本文の記述 | 確認すること |
|---|---|
| 「両方の上流網から到達できる web サーバが、観測した送信元アドレスを返す」 | 応答の本文に接続元の IPv6 アドレスが含まれるか。含まれない宛先では、`ISP observed by the server` の列が `n/a` になる |
| 「D1 の中のアドレスと 2001:db8::1 に要求を送る」 | 実際に使った宛先 |
| 「上流網は ingress filtering を行う」 | 実機の上流網がそうなっているか |
| 「エコー要求は 100 ms 間隔」 | `--interval 0.1` のままか |
| 「ルータ(b) の監視プロセスが上流インターフェースのキャリアを監視し、失われたら RA デーモンを止める」 | `watchdog.py` を既定（`--mode link`）で使ったか |

---

## 6. うまくいかないとき

| 症状 | 原因と対処 |
|---|---|
| `hostprobe.py` が何も返さない、`Cannot open network namespace` | `PX_NS_FORMAT` が実際の名前空間名と合っていない。`ip netns list` で確認 |
| `curl_*.json` で `exit` が 0 でないホストがある | 宛先が応答していない、または名前空間に IPv6 のグローバルアドレスがまだない（RA 状態を消した直後は 10 秒待つ） |
| forwarding の集計で「left through Router(a+b)」 | 測定中に G2 の `pref high` が baseline RA で戻された。2.3 の注意を参照してやり直す |
| failover の集計で「no RA with Router Lifetime 0 … was captured」 | `gorad` が停止時の RA を送っていない（版が古い）、または `site.json` の `ll` がルータ(b) の RA 送信元と違う |
| failover の集計で「no host was sending through Router(b) at t_fail」 | `wan_b.txt` にエコー要求がない。`$WAN_B` の指定、または `t_fail.txt` の時刻（ルータ(b) の時計）を確認 |
| T_withdraw や T_host が負になる | ルータ(b) とエミュレータの時計がずれている（1.3） |
| 2 回目以降の試行でホストがルータ(b) を使わない | 3.3 の復旧が不完全。上流のアドレスと経路、`gorad` の再起動、ポリシーの再適用を確認 |
