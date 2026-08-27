package coach

import (
	"context"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// defaultAnthropicModel is the model used unless overridden.
const defaultAnthropicModel = "claude-opus-5"

type anthropicClient struct {
	api     anthropic.Client
	modelID string
}

func newAnthropic(apiKey, model string) *anthropicClient {
	if model == "" {
		model = defaultAnthropicModel
	}

	return &anthropicClient{
		api:     anthropic.NewClient(option.WithAPIKey(apiKey)),
		modelID: model,
	}
}

func (c *anthropicClient) provider() string { return ProviderAnthropic }
func (c *anthropicClient) model() string    { return c.modelID }

func (c *anthropicClient) complete(
	ctx context.Context,
	system, user string,
	schema map[string]any,
) (*completion, error) {
	// Adaptive thinking is the default on this model; naming it documents that
	// the reasoning depth is meant to be the model's call, not a fixed budget.
	adaptive := anthropic.ThinkingConfigAdaptiveParam{}

	resp, err := c.api.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     c.modelID,
		MaxTokens: maxTokens,
		Thinking:  anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive},
		System: []anthropic.TextBlockParam{{
			Text: system,
			// The rubric is identical on every call, so it is marked cacheable.
			// Note that it currently sits under the ~1024-token minimum for a
			// cacheable prefix, so this is a no-op until the rubric grows —
			// harmless, and it starts paying off the moment it does. Confirm
			// with Result.CachedTokens rather than assuming.
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{Schema: schema},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	})
	if err != nil {
		return nil, classifyAnthropic(err)
	}

	if resp.StopReason == anthropic.StopReasonRefusal {
		return nil, fmt.Errorf("the model declined to answer (%s)", resp.StopDetails.Category)
	}

	var text string

	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text += t.Text
		}
	}

	return &completion{
		Text:         text,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CachedTokens: resp.Usage.CacheReadInputTokens,
	}, nil
}

// classifyAnthropic turns SDK errors into something a handler can map to a
// status code, most specific first so a rate limit isn't reported as a generic
// failure.
func classifyAnthropic(err error) error {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("calling Anthropic: %w", err)
	}

	switch apiErr.StatusCode {
	case 401, 403:
		return fmt.Errorf("%w: the Anthropic API key was rejected", ErrNotConfigured)
	case 429:
		return fmt.Errorf("rate limited by Anthropic, try again shortly: %w", err)
	default:
		return fmt.Errorf("Anthropic returned %d: %w", apiErr.StatusCode, err)
	}
}
