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

## 検証手法

## 補足事項