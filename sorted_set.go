package ratelimiter

import (
	"context"
	"strconv"
	"time"

	"github.com/go-redis/redis/v9"
	"github.com/google/uuid"
	"github.com/pkg/errors"
)

var (
	_ Strategy = &counterStrategy{}
)

const (
	keyWithoutExpire = -1
	sortedSetMin     = "-inf"
	sortedSetMax     = "+inf"
)

type counterStrategy struct {
	client *redis.ClusterClient
	now    func() time.Time
}

func NewCounterStrategy(client *redis.ClusterClient, now func() time.Time) *counterStrategy {
	return &counterStrategy{
		client: client,
		now:    now,
	}
}

// This `Run` implementation uses a sorted set in redis which holds a UUID for every request with a score
// that is the time the request has happened. This allows us to delete events from *before* the current window,
// if the window is 5 minutes, we want to remove all events from before 5 minutes ago, this way we make sure
// we roll old requests off the counters creating a rolling window for the rate limiter. So, if your window is
// 100 requests in 5 minutes and you spread the load evenly across the minutes, once you hit 6 minutes the requests
// you made on the first minute have now expired but the other 4 minmutes of requests are still valid.
// A rolling window counter is usually never 0 if traffic is consistent so it is very effective at preventing
// bursts of traffic as the counter won't ever expire.
func (s *counterStrategy) Run(ctx context.Context, r *Request) (*Result, error) {
	now := s.now()
	expiresAt := now.Add(r.Duration)
	minimum := now.Add(-r.Duration)

	// count how many requests over the period we're tracking on this rolling window so check whether
	// we're already over the limit or not. This prevents new requests from being added if a client is
	// already rate limited, not allowin it to add an infinite amount of requests to the system overloading redis.
	// If the client continues to send requests it also means that the memory for this specific key will not be reclaimed
	// (as we're not writing data here) so make sure there is an eviction policy that will clear up the
	// memory if the redis starts to get close to it's memory limit.
	result, err := s.client.ZCount(ctx, r.Key, strconv.FormatInt(minimum.UnixMilli(), 10), sortedSetMax).Uint64()
	if err != nil && result >= r.Limit {
		return &Result{
			State:         Deny,
			TotalRequests: result,
			ExpiresAt:     expiresAt,
		}, nil
	}

	// every request needs a UUID
	item := uuid.New()

	// We're using a pipeline to speed up process by packing operations to batches and sending them at once to redis and read
	// replies in a single step.
	p := s.client.Pipeline()

	// We can remove all requests that have already expired on this set
	removeByScore := p.ZRemRangeByScore(ctx, r.Key, "0", strconv.FormatInt(minimum.UnixMilli(), 10))

	// We add the current request
	add := p.ZAdd(ctx, r.Key, redis.Z{
		Score:  float64(now.UnixMilli()),
		Member: item.String(),
	})

	// count how many non-expired requests we have on the sorted set
	count := p.ZCount(ctx, r.Key, sortedSetMin, sortedSetMax)

	if _, err := p.Exec(ctx); err != nil {
		return nil, errors.Wrapf(err, "failed to execute sorted set pipeline for key: %v", r.Key)
	}

	if err := removeByScore.Err(); err != nil {
		return nil, errors.Wrapf(err, "failed to remove items from key %v", r.Key)
	}

	if err := add.Err(); err != nil {
		return nil, errors.Wrapf(err, "failed to add item to key %v", r.Key)
	}

	totalRequests, err := count.Result()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to count items for key %v", r.Key)
	}

	requests := uint64(totalRequests)

	if requests > r.Limit {
		return &Result{
			State:         Deny,
			TotalRequests: requests,
			ExpiresAt:     expiresAt,
		}, nil
	}

	return &Result{
		State:         Allow,
		TotalRequests: requests,
		ExpiresAt:     expiresAt,
	}, nil
}
