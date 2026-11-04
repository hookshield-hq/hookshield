package queue

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Scheduled retries live in a sorted set (score = due time in unix ms).
// Members are "log_id|endpoint_id|attempt", so scheduling the same attempt
// twice is a no-op.

func scheduleMember(m DispatchMessage) string {
	return m.LogID + "|" + m.EndpointID + "|" + strconv.Itoa(m.Attempt)
}

// ScheduleDispatch schedules m to be pushed to the dispatch stream at `at`.
func (p *Producer) ScheduleDispatch(ctx context.Context, m DispatchMessage, at time.Time) error {
	if strings.Contains(m.LogID+m.EndpointID, "|") {
		return fmt.Errorf("%w: id contains '|'", ErrMalformed)
	}
	return p.rdb.ZAdd(ctx, p.keys.RetrySchedule(), redis.Z{Score: float64(at.UnixMilli()), Member: scheduleMember(m)}).Err()
}

// moveDue atomically moves due members to the dispatch stream: no retry can
// be lost or duplicated even with several workers running the scheduler.
var moveDue = redis.NewScript(`
local due = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, tonumber(ARGV[2]))
for _, m in ipairs(due) do
  local parts = {}
  for part in string.gmatch(m, '([^|]+)') do parts[#parts + 1] = part end
  if #parts == 3 then
    redis.call('XADD', KEYS[2], '*', 'log_id', parts[1], 'endpoint_id', parts[2], 'attempt', parts[3], 'persist_us', '0')
  end
  redis.call('ZREM', KEYS[1], m)
end
return #due
`)

// MoveDueRetries pushes up to limit retries due at or before now to the
// dispatch stream and returns how many were moved.
func (p *Producer) MoveDueRetries(ctx context.Context, now time.Time, limit int) (int, error) {
	n, err := moveDue.Run(ctx, p.rdb,
		[]string{p.keys.RetrySchedule(), p.keys.DispatchStream()},
		now.UnixMilli(), limit).Int()
	return n, err
}

// ScheduledCount returns how many retries are waiting.
func (p *Producer) ScheduledCount(ctx context.Context) (int64, error) {
	return p.rdb.ZCard(ctx, p.keys.RetrySchedule()).Result()
}
