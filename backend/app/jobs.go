package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
)

// Job is a periodic task of the worker (e.g. HookShield Cloud's usage
// reporting). With several worker replicas, each run happens on one of them
// only: a Redis lock held for the job's interval.
type Job struct {
	Name  string        // unique, used for the lock and in logs
	Every time.Duration // interval between runs (minimum 1 s)
	Run   func(ctx context.Context, jc JobContext) error
}

// JobContext is what a job may use.
type JobContext struct {
	DB     *pgxpool.Pool
	Redis  *redis.Client
	Logger *slog.Logger
	// Usage is the durable monthly usage (copied from Redis every minute).
	Usage core.UsageStore
}

// runJob runs j every j.Every until ctx is done. The first run happens
// immediately; errors are logged and the job keeps its schedule.
func runJob(ctx context.Context, j Job, rdb *redis.Client, keys redisx.Keys, jc JobContext) {
	every := max(j.Every, time.Second)
	log := jc.Logger.With("job", j.Name)
	for {
		// Hold the lock a bit less than the interval so the next tick of any
		// replica can take it.
		lockTTL := every - every/10
		ok, err := rdb.SetNX(ctx, keys.JobLock(j.Name), "1", lockTTL).Result()
		if err != nil && ctx.Err() == nil {
			log.Warn("job lock failed", "err", err)
		}
		if ok {
			// A run ends before its lock expires, so runs never overlap
			// (another replica takes the next one only after the lock is gone).
			start := time.Now()
			runCtx, cancel := context.WithTimeout(ctx, lockTTL-lockTTL/10)
			if err := j.Run(runCtx, jc); err != nil && ctx.Err() == nil {
				log.Warn("job failed", "err", err, "took", time.Since(start))
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
