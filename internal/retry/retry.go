package retry

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/farshidmousavii/netmon/internal/logger"
)

// Config - Retry settings
type Config struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64 //exponential backoff
}

// DefaultConfig - Defaullt settings
func DefaultConfig() Config {
	return Config{
		MaxAttempts:  3,
		InitialDelay: 2 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
	}
}

// SSHConfig - SSH-specific settings
func SSHConfig() Config {
	return Config{
		MaxAttempts:  3,
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
	}
}

// Do - execute function with retry
func Do(ctx context.Context, cfg Config, operation string, fn func() error) error {
	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		// Context check before each attempt
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled before attempt %d: %w", attempt, ctx.Err())
		default:
		}

		err := fn()
		if err == nil {
			if attempt > 1 {
				logger.Info("%s succeeded on attempt %d/%d", operation, attempt, cfg.MaxAttempts)
			}
			return nil
		}

		lastErr = err

		if !IsRetryable(err) {
			return fmt.Errorf("%s failed (non-retryable): %w", operation, err)
		}

		if attempt >= cfg.MaxAttempts {
			break
		}

		logger.Warning("%s failed (attempt %d/%d): %v - retrying in %v",
			operation, attempt, cfg.MaxAttempts, err, delay)

		//  Wait with context - we check every 100ms
		retryDeadline := time.Now().Add(delay)
		for time.Now().Before(retryDeadline) {
			select {
			case <-ctx.Done():
				return fmt.Errorf("operation cancelled during retry wait: %w", ctx.Err())
			case <-time.After(100 * time.Millisecond):
				//  wait
			}
		}

		delay = min(time.Duration(float64(delay)*cfg.Multiplier), cfg.MaxDelay)
	}

	return fmt.Errorf("%s failed after %d attempts: %w", operation, cfg.MaxAttempts, lastErr)
}

// IsRetryable - Checks whether the error is retryable or not
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())

	// Non-retryable errors
	nonRetryableErrors := []string{
		"authentication failed",
		"permission denied",
		"no route to host",
		"invalid credentials",
		"unable to authenticate",
		"access denied",
		"handshake failed",
		"ssh: handshake failed",
	}

	for _, nonRetryable := range nonRetryableErrors {
		if strings.Contains(errMsg, nonRetryable) {
			return false
		}
	}

	// Retryable errors
	retryableErrors := []string{
		"timeout",
		"connection refused",
		"connection reset",
		"network is unreachable",
		"temporary failure",
		"i/o timeout",
		"connection timed out",
		"dial tcp",
		"ssh: disconnect",
	}

	for _, retryable := range retryableErrors {
		if strings.Contains(errMsg, retryable) {
			return true
		}
	}

	// By default, retry
	return true
}

// WithJitter - Add jitter to prevent thundering herd
func WithJitter(delay time.Duration, jitterPercent float64) time.Duration {
	if jitterPercent <= 0 || jitterPercent > 1 {
		return delay
	}

	jitter := time.Duration(float64(delay) * jitterPercent * (2.0*rand.Float64() - 1.0))
	return delay + jitter
}
