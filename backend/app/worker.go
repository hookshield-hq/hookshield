package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/alert"
	"github.com/hookshield-hq/hookshield/backend/internal/config"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/dispatcher"
	"github.com/hookshield-hq/hookshield/backend/internal/httpx"
	"github.com/hookshield-hq/hookshield/backend/internal/live"
	"github.com/hookshield-hq/hookshield/backend/internal/mailer"
	"github.com/hookshield-hq/hookshield/backend/internal/outbound"
	"github.com/hookshield-hq/hookshield/backend/internal/processor"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/usage"
)

// Worker runs the async pipeline until ctx is done: processors (ingest stream →
// transform → encrypt → PostgreSQL), dispatchers (delivery, retries, DLQ,
// alerts), the retry scheduler and the alert sender. Several workers can run
// side by side (Redis consumer groups).
func Worker(ctx context.Context, opts Options) error {
	cfg, err := config.Load(config.Requirement{Database: true, Redis: true, EncryptionKey: true})
	if err != nil {
		return err
	}
	log := cfg.Logger("worker")
	plans := core.OrSelfHosted(opts.Plans, cfg.Retention)

	ctx, stop := context.WithCancel(ctx)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Processors, dispatchers and the alert sender wait on blocking stream reads.
	rdb, err := redisx.OpenFor(ctx, cfg.RedisURL, cfg.ProcessorConcurrency+cfg.DispatcherConcurrency+1)
	if err != nil {
		return err
	}
	defer rdb.Close()

	if err := migrateOnBoot(ctx, cfg, pool, log); err != nil {
		return err
	}
	cipher, err := cipherFor(ctx, cfg, pool, log)
	if err != nil {
		return err
	}

	keys := redisx.NewKeys(cfg.RedisPrefix)
	st := store.New(pool)
	producer := queue.NewProducer(rdb, keys)
	events := live.NewPublisher(rdb, keys)

	host, _ := os.Hostname()
	var wg sync.WaitGroup
	errCh := make(chan error, cfg.ProcessorConcurrency+cfg.DispatcherConcurrency+2)
	for i := 0; i < cfg.ProcessorConcurrency; i++ {
		name := fmt.Sprintf("%s-%d-%d", host, os.Getpid(), i)
		p := processor.New(
			queue.NewConsumer(rdb, keys.IngestStream(), processor.Group, name),
			producer, st, cipher,
			processor.Options{Logger: log.With("component", "processor", "consumer", name), Plans: plans, Events: events},
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Run(ctx); err != nil {
				errCh <- fmt.Errorf("processor %s: %w", name, err)
			}
		}()
	}
	log.Info("processors started", "count", cfg.ProcessorConcurrency)

	client := outbound.NewClient(outbound.Options{Timeout: cfg.DispatchTimeout, AllowPrivate: cfg.AllowPrivateTargets})
	if cfg.AllowPrivateTargets {
		log.Warn("HS_ALLOW_PRIVATE_TARGETS=true: deliveries to private/loopback addresses are allowed (do not use on a public multi-tenant install)")
	}
	notifier := alert.NewService(rdb, keys, cfg.AlertCooldown)
	var schedulerOwner *dispatcher.Dispatcher
	// One per-endpoint limiter for the whole process: a slow destination holds
	// at most DefaultMaxPerEndpoint delivery slots of this worker.
	endpointSlots := dispatcher.NewEndpointSlots(dispatcher.DefaultMaxPerEndpoint)
	for i := 0; i < cfg.DispatcherConcurrency; i++ {
		name := fmt.Sprintf("%s-%d-d%d", host, os.Getpid(), i)
		d := dispatcher.New(
			queue.NewConsumer(rdb, keys.DispatchStream(), dispatcher.Group, name),
			producer, st, cipher, client, notifier,
			dispatcher.Options{Logger: log.With("component", "dispatcher", "consumer", name), Plans: plans, Events: events, Endpoints: endpointSlots},
		)
		if schedulerOwner == nil {
			schedulerOwner = d
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.Run(ctx); err != nil {
				errCh <- fmt.Errorf("dispatcher %s: %w", name, err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		schedulerOwner.RunScheduler(ctx, time.Second, time.Minute)
	}()
	log.Info("dispatchers started", "count", cfg.DispatcherConcurrency, "timeout", cfg.DispatchTimeout)

	wg.Add(1)
	go func() {
		defer wg.Done()
		runRetention(ctx, st, log.With("component", "retention"))
	}()

	// Durable copy of the monthly usage counters, then the extra jobs.
	jobs := append([]Job{
		{Name: "usage-flush", Every: time.Minute, Run: func(ctx context.Context, jc JobContext) error {
			_, err := usage.Flush(ctx, rdb, keys, st, time.Now())
			return err
		}},
		{Name: "auth-cleanup", Every: time.Hour, Run: func(ctx context.Context, jc JobContext) error {
			sessions, tokens, err := st.PurgeAuthRecords(ctx, time.Now(), cfg.SessionMaxAge)
			if sessions+tokens > 0 {
				jc.Logger.Info("stale sessions and email tokens deleted", "sessions", sessions, "tokens", tokens)
			}
			return err
		}},
	}, opts.Jobs...)
	jc := JobContext{DB: pool, Redis: rdb, Logger: log.With("component", "jobs"), Usage: usage.Store{St: st}}
	for _, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runJob(ctx, j, rdb, keys, jc)
		}()
	}
	log.Info("jobs started", "count", len(jobs))

	mail, mailKind, err := mailer.FromConfig(cfg.ResendAPIKey, cfg.ResendAPIURL, cfg.SMTPURL, cfg.EmailFrom)
	if err != nil {
		return err
	}
	if mail == nil {
		log.Info("no mailer (set HS_RESEND_API_KEY or HS_SMTP_URL): email alert channels are disabled")
	} else {
		log.Info("email alerts enabled", "mailer", mailKind)
	}
	sender := alert.NewSender(
		queue.NewConsumer(rdb, keys.AlertStream(), alert.Group, fmt.Sprintf("%s-%d-alerts", host, os.Getpid())),
		st, cipher, client,
		alert.SenderOptions{AppURL: cfg.AppURL, TelegramAPI: cfg.TelegramAPI, Mailer: mail,
			Logger: log.With("component", "alerts"),
			AllowEmail: func(ctx context.Context, userID string) bool {
				return redisx.AllowAlertEmail(ctx, rdb, keys, userID, time.Now())
			}},
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := sender.Run(ctx); err != nil {
			errCh <- fmt.Errorf("alert sender: %w", err)
		}
	}()
	log.Info("alert sender started", "cooldown", cfg.AlertCooldown, "email", mail != nil)

	mux := http.NewServeMux()
	httpx.Health(mux, "worker", map[string]httpx.ReadyCheck{
		"database": db.ReadyCheck(pool),
		"redis":    redisx.ReadyCheck(rdb),
	})
	go func() {
		if err := httpx.Serve(ctx, httpx.NewServer(cfg.WorkerHealthAddr, mux), log); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		stop()
		wg.Wait()
		return err
	}
	wg.Wait()
	log.Info("worker stopped")
	return nil
}

// runRetention deletes payload logs past their retention deadline (expires_at,
// set per account at insert time) every minute, in batches.
func runRetention(ctx context.Context, st *store.Store, log *slog.Logger) {
	const batch = 1000
	for {
		var total int64
		for ctx.Err() == nil {
			n, err := st.PurgeExpiredLogs(ctx, time.Now(), batch)
			if err != nil {
				if ctx.Err() == nil {
					log.Warn("purge expired logs failed", "err", err)
				}
				break
			}
			total += n
			if n < batch {
				break
			}
		}
		if total > 0 {
			log.Info("expired payload logs deleted", "count", total)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}
