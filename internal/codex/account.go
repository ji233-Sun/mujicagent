package codex

import (
	"context"
	"errors"
	"strings"
	"time"
)

type AccountStatus struct {
	Account *struct {
		Type  string `json:"type"`
		Email string `json:"email"`
	} `json:"account"`
	RequiresOpenAIAuth *bool `json:"requiresOpenaiAuth"`
}

func (a AccountStatus) Label() string {
	if a.Account != nil {
		if a.Account.Email != "" {
			return a.Account.Type + " · " + a.Account.Email
		}
		return a.Account.Type
	}
	if a.RequiresOpenAIAuth != nil && !*a.RequiresOpenAIAuth {
		return "Configured provider (no OpenAI login required)"
	}
	return "Sign in with codex login"
}

// Only retry this transient, read-only discovery failure. Never replay turns or auth writes.
func readAccount(ctx context.Context, call func(context.Context, string, any, any) error, delay time.Duration) (AccountStatus, error) {
	var result AccountStatus
	for attempt := 0; attempt < 3; attempt++ {
		result = AccountStatus{}
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := call(attemptCtx, "account/read", map[string]bool{"refreshToken": false}, &result)
		cancel()
		var rpc *RPCError
		if err == nil || attempt == 2 || !errors.As(err, &rpc) || rpc.Code != -32603 || !strings.Contains(strings.ToLower(rpc.Message), "workspace routing discovery timed out") {
			return result, err
		}
		timer := time.NewTimer(delay << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
	return result, nil
}

func (c *Client) ReadAccount(ctx context.Context) (AccountStatus, error) {
	return readAccount(ctx, c.Call, 500*time.Millisecond)
}
