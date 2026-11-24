package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// hotEvents is how many events are on sale; all purchases go to them.
const hotEvents = 4

// customers is how many customers the orders belong to.
const customers = 500000

// schema holds the shop's tables; the example touches nothing outside it.
const schema = "gobreaker_example"

// schemaComment marks the schema as created by the example, so that a later run may drop it.
const schemaComment = "created by the gobreaker Postgres example"

// errLimitReached is a business outcome, not a failure of Postgres.
var errLimitReached = errors.New("ticket limit reached")

// setup creates the shop's tables with the given number of past orders in a fresh schema.
func setup(ctx context.Context, conn *pgx.Conn, orders int) error {
	if err := dropSchema(ctx, conn); err != nil {
		return err
	}
	statements := []string{
		`CREATE SCHEMA ` + schema,
		`COMMENT ON SCHEMA ` + schema + ` IS '` + schemaComment + `'`,
		`SET search_path TO ` + schema,
		`CREATE TABLE events (
			id       int PRIMARY KEY,
			name     text NOT NULL,
			capacity int NOT NULL,
			sold     int NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE orders (
			id          bigserial PRIMARY KEY,
			event_id    int NOT NULL,
			customer_id int NOT NULL,
			quantity    int NOT NULL,
			created_at  timestamptz NOT NULL DEFAULT now()
		)`,
		`INSERT INTO events (id, name, capacity) SELECT g, 'event ' || g, 1000000000 FROM generate_series(1, 1000) g`,
		`SELECT setseed(0.42)`,
		fmt.Sprintf(`INSERT INTO orders (event_id, customer_id, quantity, created_at)
			SELECT 1 + (random() * 999)::int, 1 + (random() * %d)::int, 1 + (random() * 3)::int,
			       now() - random() * interval '365 days'
			FROM generate_series(1, %d)`, customers-1, orders),
		`CREATE INDEX orders_event_customer ON orders (event_id, customer_id)`,
		`VACUUM ANALYZE events, orders`,
	}
	for _, s := range statements {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("%.40s: %w", s, err)
		}
	}
	return nil
}

// dropSchema drops the schema if the example created it: first the example's tables, then the schema.
// Neither drop cascades, so if anything else is in the schema or depends on the tables, it fails
// instead of dropping that too. A schema of the same name that the example did not create is an error.
func dropSchema(ctx context.Context, conn *pgx.Conn) error {
	var comment *string
	err := conn.QueryRow(ctx,
		`SELECT obj_description(oid, 'pg_namespace') FROM pg_namespace WHERE nspname = $1`, schema).Scan(&comment)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if comment == nil || *comment != schemaComment {
		return fmt.Errorf("schema %s exists and was not created by this example; drop it or use another database", schema)
	}

	for _, s := range []string{
		`DROP TABLE IF EXISTS ` + schema + `.orders, ` + schema + `.events`,
		`DROP SCHEMA ` + schema,
	} {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

// purchase sells one ticket of an event to a customer.
func purchase(ctx context.Context, pool *pgxpool.Pool, event, customer int) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// Purchases of one event take turns on its row.
		if _, err := tx.Exec(ctx, `UPDATE events SET sold = sold + 1 WHERE id = $1`, event); err != nil {
			return err
		}

		// The limit check: an index lookup, or a scan of all orders without the index.
		var bought int
		err := tx.QueryRow(ctx,
			`SELECT coalesce(sum(quantity), 0) FROM orders WHERE event_id = $1 AND customer_id = $2`,
			event, customer).Scan(&bought)
		if err != nil {
			return err
		}
		if bought >= 10 {
			return errLimitReached
		}

		_, err = tx.Exec(ctx, `INSERT INTO orders (event_id, customer_id, quantity) VALUES ($1, $2, 1)`, event, customer)
		return err
	})
}

// dropIndex is the incident: a migration drops the index the limit check uses.
func dropIndex(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `DROP INDEX orders_event_customer`)
	return err
}

// createIndex is the fix: the index is built again without blocking writes.
func createIndex(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `CREATE INDEX CONCURRENTLY orders_event_customer ON orders (event_id, customer_id)`)
	return err
}

// pgActivity is what Postgres is busy with: purchases running, and those of them waiting for an event row.
type pgActivity struct {
	Busy      int
	LockWaits int
	Rollbacks int64
}

func samplePostgres(ctx context.Context, conn *pgx.Conn) (pgActivity, error) {
	var a pgActivity
	err := conn.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state = 'active'),
		       count(*) FILTER (WHERE state = 'active' AND wait_event_type = 'Lock'),
		       (SELECT xact_rollback FROM pg_stat_database WHERE datname = current_database())
		FROM pg_stat_activity
		WHERE datname = current_database() AND application_name = $1`, appName).Scan(&a.Busy, &a.LockWaits, &a.Rollbacks)
	return a, err
}
