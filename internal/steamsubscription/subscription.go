package steamsubscription

import (
	"context"
	"fmt"
	"time"
)

var SkeetoItems = [...]uint64{3782512354, 3787239937}

type Client interface {
	State(uint64) uint32
	Subscribe(uint64) bool
	Unsubscribe(uint64) bool
	RunCallbacks()
}

// Subscribed is distinct from downloaded/installed. Never report the latter.
func Subscribe(ctx context.Context, c Client, ids []uint64, poll time.Duration) error {
	return Set(ctx, c, ids, true, poll)
}

func Set(ctx context.Context, c Client, ids []uint64, subscribed bool, poll time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if poll <= 0 {
		return fmt.Errorf("poll duration must be positive")
	}
	for _, id := range ids {
		if (c.State(id)&1 != 0) != subscribed {
			ok := false
			if subscribed {
				ok = c.Subscribe(id)
			} else {
				ok = c.Unsubscribe(id)
			}
			if !ok {
				return fmt.Errorf("Steam rejected subscription=%t request: %d", subscribed, id)
			}
		}
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		c.RunCallbacks()
		all := true
		for _, id := range ids {
			if (c.State(id)&1 != 0) != subscribed {
				all = false
			}
		}
		if all {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("subscription not confirmed (login/network/interface): %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
