# Alloyによる形式検証の実習

## 検証対象のOSS
- 名称: sony/gobreaker
- URL: https://github.com/sony/gobreaker
- 概要: gobreakerは、システムにおけるCircuitBreakerパターンのGo言語による実装である。外部サービスへの呼び出し失敗が連続した場合に、自動的にリクエストを遮断(Open状態)し、一定時間後に試験的にリクエストを許可(Half-Open状態)して、システムの過負荷や連鎖的な障害を防ぐ機能を提供する。

## 検証すべき性質
CircuitBreakerでの状態遷移の整合性を検証する。具体的には以下の性質が常に満たされることを検証対象とする。
1. 不当な遮断の禁止: エラー発生等のトリガーがない限り、Closed(定常)状態からOpen(遮断)状態へ遷移しないこと。
2. 復帰プロセスの順序: Open状態から再びClose状態に戻るには、必ずHalf-Open(試験)状態を経由しなければならないこと(Openから直接Closedへ遷移しない)。
3. 試験中の挙動: Half-Open状態でリクエストが成功した場合のみClosed状態へ遷移し、失敗した場合には即座にOpen状態に戻ること

性質の判断材料: CircuitBreakerにおいて、状態遷移ロジックの誤りは正常な通信を妨げ、サーバーへの過剰アクセスに繋がるため、以上の性質は当該OSSのしようとして重要である。(gobreakerのREADME,gobreaker.go内の定義,以下記載の文献より)   
参考にした文献: https://martinfowler.com/bliki/CircuitBreaker.html

## モデル化

`sony/gobreaker` の中核ロジックである `gobreaker.go` を解析し、Alloy による形式検証に適した抽象度でモデル化した。本モデルでは、具体的なカウンタ数値や時刻そのものではなく、**「状態遷移の因果関係（State Transition Semantics）」**に焦点を当てている。

### 1. 状態（State）の定義
Go 言語の実装における `const` 定数定義を、Alloy の `sig` (Signature) として定義した。各状態は互いに排他的であるため、`extends` を用いて表現している。

| Go (gobreaker.go) | Alloy (Model) | 説明 |
| :--- | :--- | :--- |
| `StateClosed` | `sig Closed` | 定常状態。リクエストは通過する。 |
| `StateHalfOpen` | `sig HalfOpen` | 試験状態。リクエストを一つだけ通し、結果を待つ。 |
| `StateOpen` | `sig Open` | 遮断状態。リクエストは即座にエラーとなる。 |

### 2. イベントとトリガーの抽象化
`gobreaker` は内部に `Counts` 構造体を持ち、リクエスト数や失敗数をカウントして遷移を判定するが、モデル検証において無限の整数空間を扱うことは探索空間の爆発を招く。
そのため、本モデルでは**「遷移条件が満たされた」という事実をイベントとして抽象化**した。

* **`Failure` (失敗閾値到達):**
    * **Go:** `onFailure` メソッド内で呼び出される `cb.readyToTrip(cb.counts)` が `true` を返す状況に対応。単なる1回のエラーではなく、「遮断を引き起こすに足るエラーの蓄積」を表現する。
* **`TimeoutOp` (タイムアウト経過):**
    * **Go:** `beforeRequest` メソッド内での `cb.expiry.Before(now)` が `true` となる（Open期間が終了した）状況に対応。
* **`SuccessOp` (試験成功):**
    * **Go:** `onSuccess` メソッドの呼び出しに対応。特に Half-Open 状態での成功は状態遷移のトリガーとなる。

### 3. ステートマシンの記述
`util/ordering` モジュールを用いて時間の経過（ステップ）を表現し、ある時点 $t$ から次の時点 $nextT$ への変化を述語 `pred transition` として記述した。

Goのソースコード上のロジックと、Alloyモデルの対応関係は以下の通りである。

| 遷移元 | 遷移先 | トリガー (Alloy) | 対応する Go 実装ロジック |
| :--- | :--- | :--- | :--- |
| **Closed** | **Open** | `Failure` | `onFailure` メソッド: `readyToTrip` が真の場合、`setState(StateOpen)` を実行。 |
| **Open** | **HalfOpen** | `TimeoutOp` | `beforeRequest` メソッド: 現在時刻が `expiry` を過ぎている場合、`setState(StateHalfOpen)` を実行。 |
| **HalfOpen** | **Closed** | `SuccessOp` | `onSuccess` メソッド: 無条件で `setState(StateClosed)` を実行し、カウンタをリセット。 |
| **HalfOpen** | **Open** | `Failure` | `onFailure` メソッド: 無条件で `setState(StateOpen)` を実行（試験失敗）。 |

### 4. 前提条件と制約 (Assumptions)
モデル化にあたり、以下の仕様を前提とした。
* **並行性の捨象:** 対象の `gobreaker` は `sync.Mutex` を用いてスレッドセーフに実装されているが、本検証の目的は並行処理における競合状態の検出ではなく、Circuit Breaker パターンとしての状態遷移ロジックの正当性検証にある。   
したがって、Alloy モデル上では並行実行に伴う競合状態は考慮せず、各イベントが一つの処理のみで実行され、システムの状態が離散的に遷移するモデルとして記述した。
* **No-Op の許容:** 実際のシステムではリクエストが発生しない期間が存在するため、モデル上でも「状態も変化せず、意味のあるイベントも発生しない（NoOp）」ステップを許容した。
## 検証手法
設計したモデルに対し、Alloy Analyzer のモデル検査機能（SATソルバ）を用いて、OSSの仕様が論理的に整合しているかを確認した。検証は `assert`記述による反例探索を行い、以下の2つの観点から実施した。

### 安全性の検証1
**目的:**
「不正な遷移が決して発生しないこと」を検証する。具体的には、Circuit Breaker パターンにおいて最も危険な「Open（遮断）状態から、試験期間（Half-Open）を経ずにいきなり Closed（復旧）してしまう」という遷移の不在を証明する。

**論理式:**
任意の時刻 $t$ において、「状態が `Open` であり、かつ直後の時刻の状態が `Closed` である」という事象は成立しないことを主張する。

```alloy
assert NoJumpFromOpenToClosed {
    // 最後の時刻を除くすべての時刻 t について
    all t: Time - last | 
        // OpenからClosedへの直接遷移は存在しない
        not (t.state = Open and t.next.state = Closed)
}
```
### 安全性の検証2
**目的:**
「特定の条件下で期待される遷移が必ず発生すること」を検証する。`gobreaker` の仕様において、Half-Open 状態でのリクエスト失敗は、サーバーがまだ復旧していないことを意味するため、即座に Open 状態へ戻らなければならない。

**論理式:**
任意の時刻 $t$ において、状態が `HalfOpen` かつイベントが `Failure` であるならば、直後の時刻 $nextT$ の状態は必ず `Open` であることを主張する。

```alloy
assert HalfOpenFailureTripsBreaker {
    all t: Time - last |
        (t.state = HalfOpen and t.event = Failure) implies t.next.state = Open
}
```

### 検証範囲の設定（Scope）
Alloy は有限の探索空間内で反例を探す「有界モデル検査」を行う。本検証では以下のコマンドを用いた。

```alloy
check NoJumpFromOpenToClosed for 10 Time
check HalfOpenFailureTripsBreaker for 10 Time
```

**スコープ設定の根拠（Small Scope Hypothesis）**: 形式手法における「小スコープ仮説」に基づき、探索範囲を 10 Timeとした。 Circuit Breaker の基本的なサイクル（Closed → Open → HalfOpen → Closed/Open）は最短でも3〜4ステップで一周する。10ステップあれば、このサイクルを2周以上繰り返すシナリオを網羅できるため、論理的な欠陥が存在すれば検出可能であると判断した。

### 結果の考察
Alloy Analyzer 4.2 にて上記 check コマンドを実行した結果、No counterexample found. という結果を得た。

これにより、以下の結論が得られる。

- **仕様の堅牢性**: gobreaker の状態遷移ロジックは、モデル化された抽象度において矛盾を含んでいない。
- **安全性の担保**: 復旧手順（Half-Open）をスキップするような不正な遷移は論理的に発生し得ない。
- **意図通りの挙動**: 失敗時の遮断ロジックが仕様通りに機能している。

以上の結果より、sony/gobreaker のステートマシン設計は、Circuit Breaker パターンとして要求される基本的な安全性を満たしていると結論付ける。