package ratelimiter

import (
	"context"
	"time"
)

// Request defines a request that needs to be evaluated against the ratelimiter.
// The `Key` is the identifier you're using for the client making calls. This can be the user UID signed into the application.
// The `Key` should be the same for multiple calls of the same client so the user can be identified and request limit be enforced across all services.
// `Limit` is the number of requests the client is allowed to make over the `Duration` period.
type Request struct {
	Key      string
	Limit    uint64
	Duration time.Duration
}

// State is the result of evaluating the rate limit, either `Deny` or `Allow` a request.
type State int16

const (
	Deny  State = 0
	Allow State = 1
)

// Result represents the response to a check if a client should be rate limited or not.
// The `State` will be either `Allow` or `Deny`, `TotalRequests` holds the number of specific caller
// has made over the current period of time and `ExpiresAt` defines when the rate limit will expire/roll over
// for clients that have gone overt the limit
type Result struct {
	State         State
	TotalRequests uint64
	ExpiresAt     time.Time
}

// Strategy is the interface the rate limit implementations must implement to be used,
// it takes a `Request` and returns a `Result` and an `error`, any errors the rate-limiter finds should be
// bubbled up so the code can make a decision about what it wants to do with the request.
type Strategy interface {
	Run(ctx context.Context, r *Request) (*Result, error)
}
