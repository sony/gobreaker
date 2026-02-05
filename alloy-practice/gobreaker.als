module gobreaker

open util/ordering[Time]

/* * 1. 状態の定義
 */
abstract sig State {}
one sig Closed, HalfOpen, Open extends State {}

/*
 * 2. イベントの抽象化
 */
abstract sig Event {}
one sig SuccessOp, Failure, TimeoutOp, NoOp extends Event {}

/*
 * 3. 時間ステップの定義
 */
sig Time {
    state: one State,
    event: one Event
}

/*
 * 初期状態
 */
fact Init {
    first.state = Closed
}

/*
 * 遷移ロジック
 * 曖昧さを防ぐため、各状態のロジックを () で囲み、and で繋いでいます。
 */
pred transition [t, nextT: Time] {
    // --- State: Closed ---
    (t.state = Closed implies (
        t.event = Failure implies nextT.state = Open 
        else nextT.state = Closed
    ))
    and 

    // --- State: Open ---
    (t.state = Open implies (
        t.event = TimeoutOp implies nextT.state = HalfOpen 
        else nextT.state = Open
    ))
    and 

    // --- State: HalfOpen ---
    (t.state = HalfOpen implies (
        t.event = SuccessOp implies nextT.state = Closed 
        else t.event = Failure implies nextT.state = Open 
        else nextT.state = HalfOpen
    ))
}

// トレース: すべての隣接する時刻で遷移ルールを守る
fact Traces {
    all t: Time - last | let nextT = t.next | transition[t, nextT]
}

/*
 * 4. 検証
 */

// 検証1: OpenからClosedへ直接ジャンプするような時刻tは「存在しない(no)」
assert NoJumpFromOpenToClosed {
    no t: Time - last | 
        (t.state = Open and t.next.state = Closed)
}

// 検証2: HalfOpenで失敗したら、次の状態はOpenであることを検証
assert HalfOpenFailureTripsBreaker {
    all t: Time - last |
        (t.state = HalfOpen and t.event = Failure) implies t.next.state = Open
}

// 実行コマンド
--check NoJumpFromOpenToClosed for 10 Time
--check HalfOpenFailureTripsBreaker for 10 Time

// 動作確認用
pred showScenario {}
--run showScenario for 10 Time