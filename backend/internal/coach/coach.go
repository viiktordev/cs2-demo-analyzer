// Package coach turns the statistics extracted from a demo into written
// coaching, by handing the derived features to a language model.
//
// The split is deliberate: the demo package computes every number exactly, and
// the model only interprets. Feeding raw tick data instead would be both
// impossible — a match holds well over a million position samples — and worse,
// since a model asked to do arithmetic will get wrong what Go already has right.
//
// Two providers are supported. The prompt, the schema and the parsing are shared
// between them; only the API call differs, behind the client interface.
package coach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
)

// Supported providers.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	// ProviderAuto picks whichever provider has a key configured.
	ProviderAuto = "auto"
)

// maxTokens is generous: the report is a few thousand tokens, and hitting the
// cap would truncate it mid-sentence.
const maxTokens = 16000

// requestTimeout bounds one coaching call. Reasoning on a hard prompt can take a
// while, so this is well above a typical API call.
const requestTimeout = 5 * time.Minute

// ErrNotConfigured marks the coaching feature as unusable for a reason the
// operator has to fix — no key, or one the API rejected. The rest of the
// application keeps working either way. Callers wrap it with the specifics.
var ErrNotConfigured = errors.New("coaching is unavailable")

// completion is one model response, normalised across providers.
type completion struct {
	Text         string
	InputTokens  int64
	OutputTokens int64
	// CachedTokens is the part of the prompt served from the provider's cache.
	CachedTokens int64
}

// client is one model backend. Implemented by anthropicClient and openaiClient.
type client interface {
	provider() string
	model() string
	// complete sends the prompt and returns the raw JSON the schema describes.
	complete(ctx context.Context, system, user string, schema map[string]any) (*completion, error)
}

// Options configures which provider the coaching runs on.
type Options struct {
	// Provider is "anthropic", "openai", or "auto" (the default) to use
	// whichever key is present. With both keys set, auto picks Anthropic.
	Provider string
	// AnthropicKey and OpenAIKey are the API keys. Leaving one empty rules that
	// provider out.
	AnthropicKey string
	OpenAIKey    string
	// Model overrides the default model of the chosen provider.
	Model string
}

// Coach generates coaching reports. The zero value is unusable; build one with
// New.
type Coach struct {
	backend client
}

// New returns a Coach for the configured provider, or an error wrapping
// ErrNotConfigured when none can be built. Callers are expected to treat that as
// a disabled feature rather than a fatal error.
func New(opts Options) (*Coach, error) {
	switch provider := strings.ToLower(strings.TrimSpace(opts.Provider)); provider {
	case ProviderAnthropic:
		if opts.AnthropicKey == "" {
			return nil, fmt.Errorf("%w: provider is anthropic but ANTHROPIC_API_KEY is empty", ErrNotConfigured)
		}

		return &Coach{backend: newAnthropic(opts.AnthropicKey, opts.Model)}, nil

	case ProviderOpenAI:
		if opts.OpenAIKey == "" {
			return nil, fmt.Errorf("%w: provider is openai but OPENAI_API_KEY is empty", ErrNotConfigured)
		}

		return &Coach{backend: newOpenAI(opts.OpenAIKey, opts.Model)}, nil

	case "", ProviderAuto:
		// Anthropic first only because it was here first; with one key set the
		// order never comes up.
		if opts.AnthropicKey != "" {
			return &Coach{backend: newAnthropic(opts.AnthropicKey, opts.Model)}, nil
		}

		if opts.OpenAIKey != "" {
			return &Coach{backend: newOpenAI(opts.OpenAIKey, opts.Model)}, nil
		}

		return nil, fmt.Errorf("%w: set ANTHROPIC_API_KEY or OPENAI_API_KEY", ErrNotConfigured)

	default:
		return nil, fmt.Errorf("%w: unknown provider %q, want anthropic, openai or auto",
			ErrNotConfigured, provider)
	}
}

// Provider reports which backend is in use.
func (c *Coach) Provider() string {
	if c == nil || c.backend == nil {
		return ""
	}

	return c.backend.provider()
}

// Model reports the model the reports are generated with.
func (c *Coach) Model() string {
	if c == nil || c.backend == nil {
		return ""
	}

	return c.backend.model()
}

// Report is what the model returns, shaped so the frontend can render fixed
// sections instead of parsing prose.
type Report struct {
	// Summary is a two or three sentence read on the match.
	Summary string `json:"summary"`
	// Strengths and Mistakes are what to keep and what to fix.
	Strengths []Finding `json:"strengths"`
	Mistakes  []Finding `json:"mistakes"`
	// Drills are the concrete things to practise, most important first.
	Drills []Drill `json:"drills"`
	// Trend is filled only when previous matches were supplied.
	Trend string `json:"trend,omitempty"`
}

// Finding is one observation, tied back to the numbers that support it.
type Finding struct {
	Title string `json:"title"`
	// Detail explains the observation in one or two sentences.
	Detail string `json:"detail"`
	// Evidence names the specific stats or rounds behind it.
	Evidence string `json:"evidence"`
}

// Drill is a practice recommendation.
type Drill struct {
	Title string `json:"title"`
	// Why ties the drill to a finding, so it doesn't read as generic advice.
	Why string `json:"why"`
}

// Result carries the report plus what the call cost.
type Result struct {
	Report       *Report
	Raw          json.RawMessage
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
}

// Analyze asks the model to read one match. Previous matches for the same
// player, newest first, may be supplied to get a trend; pass nil for none.
func (c *Coach) Analyze(ctx context.Context, current *demo.Analysis, previous []*demo.Analysis) (*Result, error) {
	if c == nil || c.backend == nil {
		return nil, ErrNotConfigured
	}

	prompt, err := buildPrompt(current, previous)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	out, err := c.backend.complete(ctx, systemPrompt, prompt, reportSchema)
	if err != nil {
		return nil, err
	}

	if out.Text == "" {
		return nil, errors.New("the model returned no report")
	}

	var report Report
	if err := json.Unmarshal([]byte(out.Text), &report); err != nil {
		return nil, fmt.Errorf("decoding report: %w", err)
	}

	return &Result{
		Report:       &report,
		Raw:          json.RawMessage(out.Text),
		Provider:     c.backend.provider(),
		Model:        c.backend.model(),
		InputTokens:  out.InputTokens,
		OutputTokens: out.OutputTokens,
		CachedTokens: out.CachedTokens,
	}, nil
}
