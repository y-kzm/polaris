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
| `$DST` | D1 に含まれない web サーバのアドレス。ping に応答し、HTTP で接続元アドレスを返す | `ifconfig.net` の AAAA の 1 つ（1.0 で調べる） |
| `$DST_NAME` | `$DST` のホスト名 | `ifconfig.net` |
| `d1_targets.txt` | D1 の各 prefix の中の web サーバを 1 行に 1 つ書いたファイル（7 行）。1.0 で作る | 下記 |

上流が実際の ISP（インターネット）の場合を前提にしています。D1 は Microsoft の prefix なので、
D1 の宛先は Microsoft の実際のサーバ、D1 外の宛先は接続元アドレスを返す公開サービスです。
論文は、両ルータの上流でのキャプチャと応答の受信だけを根拠にしています。サーバが返す接続元アドレスは、論文では使いません。
返すサーバ（D1 外の宛先）については、集計のときに、キャプチャが示す ISP と一致するかを追加で確かめます。

---

## 1. 準備（1 回だけ）

### 1.0 宛先の web サーバを決める

エミュレータのホスト 1 台（例: `pxh001`）から調べます。どのコマンドも数回の接続だけです。

**(1) D1 外の宛先（`$DST`）**

```sh
DST_NAME=ifconfig.net
DST=$(dig +short AAAA $DST_NAME | grep : | head -1); echo $DST
sudo ip netns exec pxh001 curl -6 -sS --resolve "$DST_NAME:443:[$DST]" https://$DST_NAME/   # 自分の IPv6 アドレスが返る
sudo ip netns exec pxh001 ping -6 -c 3 $DST                                                # 応答がある
```

返ったアドレスが、どちらかの ISP の prefix に入っていることを確認します。
`$DST` が D1 のどの prefix にも入っていないことも確認します。
ping に応答しない場合、failover（3 章）の宛先には ping に応答する別のアドレスを使います。

**(2) D1 の 7 つの prefix の中の web サーバ（`d1_targets.txt`）**

各 prefix について、その中にあって HTTPS に応答するアドレスを 1 つ探します。
候補のホスト名を引き、どの prefix に入るかを表示するには次のとおりです。

```sh
python3 - <<'EOF'
import ipaddress, socket
D1 = ["2603:1027::/48", "2603:1037::/48", "2603:1047::/48", "2603:1057::/48",
      "2603:1063::/38", "2620:1ec:6::/48", "2620:1ec:40::/42"]          # 論文の Table と同じ順にする
NAMES = ["teams.microsoft.com", "login.microsoftonline.com", "teams.microsoft.us", "api.teams.skype.com",
         "go.trouter.teams.microsoft.com", "worldaz.tr.teams.microsoft.com", "s-0005.dual-s-msedge.net"]
for name in NAMES:
    try:
        addrs = sorted({a[4][0] for a in socket.getaddrinfo(name, 443, socket.AF_INET6)})
    except OSError:
        continue
    for addr in addrs:
        hit = [p for p in D1 if ipaddress.ip_address(addr) in ipaddress.ip_network(p)]
        print(f"{hit[0] if hit else '(D1 の外)':18} {name} {addr}")
EOF
```

2026-11-21 に日本から引いた結果では、`teams.microsoft.com` が `2603:1063::/38`、`login.microsoftonline.com` が
`2603:1047::/48`、`teams.microsoft.us` が `2620:1ec:40::/42` に入りました（場所と時期で変わります）。
残りの prefix（`2603:1027::/48`、`2603:1037::/48`、`2603:1057::/48`、`2620:1ec:6::/48`）は、この方法では見つかっていません。
`2603:10x7::/48` は地域ごとのサインインのサーバと考えられるので、見つかったアドレスの 2 番目の 16 ビットだけを変えたアドレス
（例: `2603:1047:1:a0::14` に対して `2603:1027:1:a0::14`）を候補として試してください（推測です。応答しなければ使えません）。

候補のアドレスは、ホスト名を付けて次のように試します。HTTP の状態コードが 200〜399 なら使えます。

```sh
sudo ip netns exec pxh001 curl -6 -sS -o /dev/null -m 8 -w '%{http_code} %{remote_ip}\n' \
     --resolve 'login.microsoftonline.com:443:[2603:1047:1:a0::14]' https://login.microsoftonline.com/
```

使えるものを、D1 の prefix の順（論文の Table と同じ順）に、`URL` と `--resolve の値` を空白で区切って 1 行ずつ書きます。

```
https://login.microsoftonline.com/ login.microsoftonline.com:443:[2603:1027:…]
https://login.microsoftonline.com/ login.microsoftonline.com:443:[2603:1037:…]
https://login.microsoftonline.com/ login.microsoftonline.com:443:[2603:1047:1:a0::14]
https://login.microsoftonline.com/ login.microsoftonline.com:443:[2603:1057:…]
https://teams.microsoft.com/ teams.microsoft.com:443:[2603:1063:27:1::14]
https://… …:443:[2620:1ec:6:…]
https://teams.microsoft.us/ teams.microsoft.us:443:[2620:1ec:42::131]
```

> **応答するサーバが見つからない prefix がある場合**: その prefix は測定から外し、見つかった prefix だけで行います
> （`d1_targets.txt` の行数と、2.4 の `ports.json` の `range(1, 8)` をその数に合わせる）。
> この場合、論文の「seven prefixes」の記述と Figure の列数を、測定した数に直す必要があります。

> **アクセスの制限**: 1 回の測定で、同じ /64 から各サーバへ 200 件の接続が短時間に届きます。
> 本番の前に `--hosts 1-10` で試し、すべて応答することを確認してください。応答が欠ける場合は `--parallel 4` などで同時接続数を下げます。

### 1.1 `site.json` を書く

手元の作業ディレクトリ（以降 `~/manual`）に作ります。

```json
{
  "hosts": 200,
  "g1_hosts": 50,
  "default_dst": "<$DST のアドレス>",
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
- `ingress_drops`: 上流網の ingress filter が破棄したパケット数。記録用で、原稿では使いません。
  読み取れない場合はこの行を消します。

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

「ポリシー適用前」「適用後」のそれぞれで、D1 の 7 つの prefix それぞれの中の宛先と、D1 外の宛先 1 つに、
全ホストから HTTP 要求を 1 回ずつ送ります。宛先は 8 個、測定は 2 回（適用前、適用後）です。
ホスト i は送信元ポート `port-base + i` を使います
（NPTv6 は送信元プレフィクスを書き換えるので、上流側ではポートでホストを見分けます）。
port-base は宛先ごとに変えるので、キャプチャは 1 回の測定につきルータごとに 1 本で足ります。

| 宛先 | タグ（適用前 / 適用後） | port-base（適用前 / 適用後） |
|---|---|---|
| `d1_targets.txt` の n 行目（n = 1〜7） | `before_D1-n` / `after_D1-n` | 10000 + n × 1000 = 11000〜17000 / 20000 + n × 1000 = 21000〜27000 |
| `$DST`（D1 外） | `before_default` / `after_default` | 18000 / 28000 |

port-base はすべて 32768 未満にしています。それ以上は Linux が他の通信の送信元ポートに自動で割り当てる範囲で、
上流インターフェースを通る別の通信（SSH など）がキャプチャに混ざり、ホストの通信として数えられるおそれがあるためです。

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

### 2.2 1 回の測定（適用前と適用後の 2 回）

以下は適用前（`PHASE=before`、port-base 11000〜18000）の例です。適用後は `PHASE=after`、`BASE=20000` にします。

**(1) 両ルータでキャプチャを開始**（それぞれの端末で）

```sh
# ルータ(a)  適用後は portrange 21001-28200、ファイル名は wan_a_after.txt
sudo tcpdump -i $WAN_A -nn -tt -l -B 16384 'ip6 and tcp and portrange 11001-18200' > wan_a_before.txt
# ルータ(b)  適用後は portrange 21001-28200、ファイル名は wan_b_after.txt
sudo tcpdump -i $WAN_B -nn -tt -l -B 16384 'ip6 and tcp and portrange 11001-18200' > wan_b_before.txt
```

`listening on …` と表示されるまで待ちます。

**(2) エミュレータで、8 個の宛先に順に要求を送る**

```sh
PHASE=before; BASE=10000          # 適用後は PHASE=after; BASE=20000
n=1
while read -r url resolve; do           # d1_targets.txt の 1 行 = D1 の 1 つの prefix
  sudo -E python3 ~/px/hostprobe.py curl --hosts 1-200 --url "$url" --resolve "$resolve" \
       --port-base $((BASE + n * 1000)) < /dev/null > curl_${PHASE}_D1-$n.json
  n=$((n + 1))
done < d1_targets.txt
sudo -E python3 ~/px/hostprobe.py curl --hosts 1-200 --url "https://$DST_NAME/" --resolve "$DST_NAME:443:[$DST]" \
     --port-base $((BASE + 8000)) > curl_${PHASE}_default.json
```

`--resolve` で宛先アドレスを固定しているので、どのホストも同じアドレスへ接続します。
各ファイルで、`"http"` が 200〜399、`"remote"` が指定したアドレスになっていることを確認します。

**(3) キャプチャを止める**: 両ルータで Ctrl-C。`0 packets dropped by kernel` を確認します。

### 2.3 ポリシーを適用する

1. controller で評価シナリオのポリシーを適用する（G1 = ホスト 1〜50 は D1 を Router(a)、G2 = 51〜200 はデフォルトを Router(b)）。
2. **適用の直後に** 2.2 の測定を `PHASE=after`、`BASE=20000` で行う。8 個の宛先で 40 秒を超える場合があるので、
   下の注意のとおり、測定の前後で `pref high` を確認する。

> **1 アドレス設計での注意**: G2 の `pref high` は、ルータ(b) の次の baseline RA で `medium` に戻ります。
> 測定の前後に次のコマンドで、ホスト 51 のデフォルト経路に `pref high` があることを確認してください。
> 測定中に `medium` に戻っていた場合は、次の policy RA（最長 40 秒）を待ってやり直します。
>
> ```sh
> sudo -E python3 ~/px/hostprobe.py snapshot --hosts 51 --dst $DST | python3 -m json.tool | grep -B2 -A2 pref
> ```

### 2.4 ファイルを集める

`~/manual/forwarding/` に次の 21 ファイルを置きます。

```
curl_before_D1-1.json … curl_before_D1-7.json   curl_before_default.json
curl_after_D1-1.json  … curl_after_D1-7.json    curl_after_default.json
wan_a_before.txt   wan_b_before.txt   wan_a_after.txt   wan_b_after.txt
ports.json
```

`ports.json` は次の内容です（表と同じ値）。次のコマンドで作れます。

```sh
python3 - > ports.json <<'EOF'
import json
ports = {}
for phase, base in (("before", 10000), ("after", 20000)):
    for n in range(1, 8):
        ports[f"{phase}_D1-{n}"] = base + n * 1000
    ports[f"{phase}_default"] = base + 8000
print(json.dumps(ports, indent=1))
EOF
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
| forwarding | 3,200 / 3,200 応答（200 台 × 8 宛先 × 2 回）。`after` の D1-1〜D1-7 は G1 が Router(a) のみ、G2 が Router(b) のみで、反対側のルータは `0 / 0` |
| failover | 試行 10、測定数は約 1,700〜1,800（G2 の 150 台と、G1 のうちルータ(b) を選んでいた台数の 10 回分）、`not recovered` が 0 |

`FAIL` の行が出た場合は、内容を確認して該当の測定をやり直します。

### 4.3 原稿へ

```sh
cd MDPI_Electronics_REVISEv2_20261120
python3 data/make_values.py ~/manual/results
latexmk -pdf main.tex && pdflatex response_to_reviewers.tex
```

黄色の値、Figure（各ホストが通った出口の図）、Table（切り替え時間）が実機の値に置き換わります。
その後、`main.tex` の 5 章にある著者向けの注記（黄色の枠）を削除します。

---

## 5. 原稿の記述と実機の構成を合わせる点

v2 の本文は、次の構成を前提に書いています。実機が違う場合は、本文のほうを実機に合わせて直します。

| 本文の記述 | 確認すること |
|---|---|
| 「D1 の 7 つの prefix それぞれの中の web サーバと、D1 外の web サーバに要求を送る」 | 実際に使った宛先と、その数（7 より少ない場合は本文と Figure を直す） |
| 「3,200 件すべてに応答があった」 | 公開サーバの制限で応答が欠けていないか |
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
