package coach

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// defaultOpenAIModel is the model used unless overridden.
const defaultOpenAIModel = openai.ChatModelGPT5_5

// schemaName labels the structured output. OpenAI requires one, and it only
// accepts letters, digits, underscores and dashes.
const schemaName = "coaching_report"

type openaiClient struct {
	api     openai.Client
	modelID string
}

func newOpenAI(apiKey, model string) *openaiClient {
	if model == "" {
		model = defaultOpenAIModel
	}

	return &openaiClient{
		api:     openai.NewClient(option.WithAPIKey(apiKey)),
		modelID: model,
	}
}

func (c *openaiClient) provider() string { return ProviderOpenAI }
func (c *openaiClient) model() string    { return c.modelID }

func (c *openaiClient) complete(
	ctx context.Context,
	system, user string,
	schema map[string]any,
) (*completion, error) {
	resp, err := c.api.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:               c.modelID,
		MaxCompletionTokens: openai.Int(maxTokens),
		// The report benefits from real reasoning; this is the analogue of the
		// adaptive thinking used on the Anthropic side.
		ReasoningEffort: shared.ReasoningEffortHigh,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(system),
			openai.UserMessage(user),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name: schemaName,
					// Strict mode guarantees the response matches the schema, at
					// the cost of a restricted subset of JSON Schema — which is
					// why reportSchema avoids maxItems and marks every property
					// required.
					Strict: openai.Bool(true),
					Schema: schema,
				},
			},
		},
	})
	if err != nil {
		return nil, classifyOpenAI(err)
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("OpenAI returned no choices")
	}

	choice := resp.Choices[0]

	// A refusal comes back in its own field rather than as an error.
	if choice.Message.Refusal != "" {
		return nil, fmt.Errorf("the model declined to answer: %s", choice.Message.Refusal)
	}

	return &completion{
		Text:         choice.Message.Content,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		CachedTokens: resp.Usage.PromptTokensDetails.CachedTokens,
	}, nil
}

// classifyOpenAI mirrors classifyAnthropic: turn SDK errors into something a
// handler can map to a status code, most specific first.
func classifyOpenAI(err error) error {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("calling OpenAI: %w", err)
	}

	switch apiErr.StatusCode {
	case 401, 403:
		return fmt.Errorf("%w: the OpenAI API key was rejected", ErrNotConfigured)
	case 429:
		// OpenAI uses 429 both for rate limits and for an exhausted quota, and
		// the two need different fixes, so the message stays open about it.
		return fmt.Errorf("OpenAI returned 429 — rate limit or exhausted quota: %w", err)
	default:
		return fmt.Errorf("OpenAI returned %d: %w", apiErr.StatusCode, err)
	}
}
