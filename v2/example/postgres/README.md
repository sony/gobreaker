# A breaker in front of a slow Postgres

A ticket shop sends 400 purchases per second to a real Postgres.
Each purchase locks its event row, checks the customer's ticket limit in `orders` and inserts an order.
After 10 seconds a migration drops the index the limit check uses.
Postgres still answers single purchases in tens of milliseconds, but serves only about a third of the load,
and the rest times out. After 60 seconds the index is built again.

Without `Recovery`, the half-open probes succeed and the breaker closes with the full load;
about a hundred purchases are waiting for an answer when the timeouts trip it again, and every cycle repeats it.
With `Recovery`, the breaker closes in steps (5%, 10%, 25%, 50%, 75%, one second each)
and trips on the first timeout, at the first step Postgres cannot serve.

## Run it

```sh
docker run --rm -d --name gobreaker-pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:18

go run .                # the breaker as before
go run . -recovery      # with Settings.Recovery
```

Each run takes 90 seconds and prints one line per second, then a summary of the 60 seconds without the index.
`go run . -h` lists the flags for the load, the incident and the breaker settings.
The example works in its own schema, `gobreaker_example`, which it creates and drops; it never drops anything else.
Run it next to Postgres, not through a port-forward, which delays every new connection.
