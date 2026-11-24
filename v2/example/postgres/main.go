// Command postgres shows a CircuitBreaker in front of a Postgres that becomes slow:
// the half-open probes pass, but the full load times out. Compare:
//
//	go run .
//	go run . -recovery
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sony/gobreaker/v2"
)

const appName = "gobreaker-example"

type phase struct {
	name  string
	until time.Duration
}

var phases = []phase{
	{"healthy", 10 * time.Second},
	{"no index", 70 * time.Second},
	{"index back", 90 * time.Second},
}

func phaseAt(at time.Duration) string {
	for _, p := range phases {
		if at < p.until {
			return p.name
		}
	}
	return phases[len(phases)-1].name
}

type config struct {
	dsn          string
	rate         int
	timeout      time.Duration
	orders       int
	probes       uint32
	tripAfter    uint32
	openFor      time.Duration
	recovery     bool
	steps        string
	stepDuration time.Duration
	// recoveryTripAfter, if not 0, sets Recovery.ReadyToTrip to trip after that many consecutive failures.
	recoveryTripAfter uint32
	jsonFile          string
}

func main() {
	var cfg config
	flag.StringVar(&cfg.dsn, "dsn", "postgres://postgres:postgres@localhost:5432/postgres",
		"Postgres to run against; the example creates its own schema "+schema+" and drops it at the end")
	flag.IntVar(&cfg.rate, "rate", 400, "purchases per second; they keep coming whatever happens to earlier ones")
	flag.DurationVar(&cfg.timeout, "timeout", 300*time.Millisecond, "how long a purchase may take")
	flag.IntVar(&cfg.orders, "orders", 1500000, "past orders; more make the limit check without the index slower")
	cfg.probes, cfg.tripAfter = 3, 5
	flag.Func("probes", "Settings.MaxRequests: purchases let through in the half-open state (default 3)", parseUint32(&cfg.probes))
	flag.Func("trip-after", "Settings.ReadyToTrip: consecutive failures that trip the breaker (default 5)", parseUint32(&cfg.tripAfter))
	flag.DurationVar(&cfg.openFor, "open-for", 3*time.Second, "Settings.Timeout: how long the breaker stays open")
	flag.BoolVar(&cfg.recovery, "recovery", false, "set Settings.Recovery")
	flag.StringVar(&cfg.steps, "steps", "0.05,0.1,0.25,0.5,0.75", "Recovery.Steps: shares of purchases let through")
	flag.DurationVar(&cfg.stepDuration, "step-duration", time.Second, "Recovery.StepDuration: how long each share lasts")
	flag.Func("recovery-trip-after", "Recovery.ReadyToTrip: consecutive failures that trip the breaker during recovery (default: the first failure)", parseUint32(&cfg.recoveryTripAfter))
	flag.StringVar(&cfg.jsonFile, "json", "", "also write the results to this JSON file")
	flag.Parse()

	if err := run(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}

func parseUint32(v *uint32) func(string) error {
	return func(s string) error {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return err
		}
		*v = uint32(n)
		return nil
	}
}

func run(ctx context.Context, cfg config) error {
	admin, err := pgx.Connect(ctx, cfg.dsn)
	if err != nil {
		return err
	}
	defer admin.Close(ctx)
	sampler, err := pgx.Connect(ctx, cfg.dsn)
	if err != nil {
		return err
	}
	defer sampler.Close(ctx)

	log.Printf("creating %d orders", cfg.orders)
	if err := setup(ctx, admin, cfg.orders); err != nil {
		return err
	}
	defer func() {
		if err := dropSchema(ctx, admin); err != nil {
			log.Printf("dropping schema %s: %v", schema, err)
		}
	}()

	pool, err := newPool(ctx, cfg.dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	rec := newRecorder()
	cb, err := newBreaker(cfg, rec)
	if err != nil {
		return err
	}
	end := phases[len(phases)-1].until

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runIncident(ctx, admin, rec)
	}()
	go func() {
		defer wg.Done()
		for rec.since() < end {
			a, err := samplePostgres(ctx, sampler)
			if err != nil {
				log.Printf("sampling Postgres: %v", err)
			} else {
				rec.addPostgres(a)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()

	sendPurchases(ctx, cfg, pool, cb, rec, end)
	wg.Wait()

	time.Sleep(2 * time.Second) // let Postgres finish what the callers gave up on
	if a, err := samplePostgres(ctx, sampler); err == nil {
		rec.addPostgres(a)
	}

	rec.printTimeline(os.Stdout)
	inc := rec.summarize(phases[0].until, phases[1].until)
	inc.print(os.Stdout)

	if cfg.jsonFile != "" {
		data, err := json.MarshalIndent(map[string]any{
			"settings": map[string]any{
				"rate": cfg.rate, "timeout": cfg.timeout.String(), "orders": cfg.orders, "probes": cfg.probes,
				"trip_after": cfg.tripAfter, "open_for": cfg.openFor.String(), "recovery": cfg.recovery,
				"steps": cfg.steps, "step_duration": cfg.stepDuration.String(), "recovery_trip_after": cfg.recoveryTripAfter,
			},
			"without_index": inc,
		}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(cfg.jsonFile, data, 0o600)
	}
	return nil
}

func newPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 50
	cfg.MinConns = 10
	cfg.ConnConfig.RuntimeParams["application_name"] = appName
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "1s"
	// One core per scan, so the example does not take over the machine.
	cfg.ConnConfig.RuntimeParams["max_parallel_workers_per_gather"] = "0"
	return pgxpool.NewWithConfig(ctx, cfg)
}

func newBreaker(cfg config, rec *recorder) (*gobreaker.CircuitBreaker[struct{}], error) {
	st := gobreaker.Settings{
		Name:        "postgres",
		MaxRequests: cfg.probes,
		Timeout:     cfg.openFor,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.tripAfter
		},
		IsSuccessful: func(err error) bool {
			return err == nil || errors.Is(err, errLimitReached)
		},
		OnStateChange: func(_ string, from, to gobreaker.State) {
			rec.addStateChange(from, to)
		},
	}

	if cfg.recovery {
		var steps []float64
		for _, s := range strings.Split(cfg.steps, ",") {
			share, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				return nil, fmt.Errorf("-steps: %w", err)
			}
			steps = append(steps, share)
		}
		st.Recovery = &gobreaker.Recovery{Steps: steps, StepDuration: cfg.stepDuration}
		if cfg.recoveryTripAfter > 0 {
			st.Recovery.ReadyToTrip = func(counts gobreaker.Counts) bool {
				return counts.ConsecutiveFailures >= cfg.recoveryTripAfter
			}
		}
	}

	return gobreaker.NewCircuitBreaker[struct{}](st), nil
}

// runIncident drops the index at the end of the first phase and builds it again at the end of the second.
func runIncident(ctx context.Context, conn *pgx.Conn, rec *recorder) {
	steps := []func(context.Context, *pgx.Conn) error{dropIndex, createIndex}
	for i, step := range steps {
		time.Sleep(time.Until(rec.start.Add(phases[i].until)))
		if err := step(ctx, conn); err != nil {
			log.Printf("%s: %v", phases[i+1].name, err)
			continue
		}
		log.Printf("%5.2fs %s", rec.since().Seconds(), phases[i+1].name)
	}
}

// sendPurchases sends purchases at a fixed rate until end, each through the breaker.
func sendPurchases(ctx context.Context, cfg config, pool *pgxpool.Pool, cb *gobreaker.CircuitBreaker[struct{}], rec *recorder, end time.Duration) {
	var wg sync.WaitGroup
	interval := time.Second / time.Duration(cfg.rate)
	for n := 0; time.Duration(n)*interval < end; n++ {
		time.Sleep(time.Until(rec.start.Add(time.Duration(n) * interval)))
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			arrived := time.Now()
			var finished time.Time
			_, err := cb.Execute(func() (struct{}, error) {
				ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
				defer cancel()
				err := purchase(ctx, pool, 1+n%hotEvents, 1+(n*7919)%customers)
				finished = time.Now()
				return struct{}{}, err
			})
			rec.addRequest(arrived, finished, err)
		}(n)
	}
	wg.Wait()
}
