package coach

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
)

// TestProviderSelection covers every way a backend can be chosen or refused.
// The refusals matter as much as the successes: a misconfigured provider has to
// disable one feature, never crash the server.
func TestProviderSelection(t *testing.T) {
	const (
		anthropicKey = "sk-ant-test"
		openaiKey    = "sk-openai-test"
	)

	tests := []struct {
		name     string
		opts     Options
		provider string
		model    string
		wantErr  bool
	}{
		{
			name:     "auto with only an anthropic key",
			opts:     Options{AnthropicKey: anthropicKey},
			provider: ProviderAnthropic,
			model:    defaultAnthropicModel,
		},
		{
			name:     "auto with only an openai key",
			opts:     Options{OpenAIKey: openaiKey},
			provider: ProviderOpenAI,
			model:    defaultOpenAIModel,
		},
		{
			// Documented tie-break, so a leftover key can't silently redirect
			// the billing.
			name:     "auto with both keys prefers anthropic",
			opts:     Options{AnthropicKey: anthropicKey, OpenAIKey: openaiKey},
			provider: ProviderAnthropic,
			model:    defaultAnthropicModel,
		},
		{
			name:     "explicit openai wins over a present anthropic key",
			opts:     Options{Provider: ProviderOpenAI, AnthropicKey: anthropicKey, OpenAIKey: openaiKey},
			provider: ProviderOpenAI,
			model:    defaultOpenAIModel,
		},
		{
			name:     "provider name is case and space insensitive",
			opts:     Options{Provider: "  OpenAI ", OpenAIKey: openaiKey},
			provider: ProviderOpenAI,
			model:    defaultOpenAIModel,
		},
		{
			name:     "model override",
			opts:     Options{Provider: ProviderOpenAI, OpenAIKey: openaiKey, Model: "gpt-5.4-mini"},
			provider: ProviderOpenAI,
			model:    "gpt-5.4-mini",
		},
		{
			name:    "no keys at all",
			opts:    Options{},
			wantErr: true,
		},
		{
			name:    "openai requested without its key",
			opts:    Options{Provider: ProviderOpenAI, AnthropicKey: anthropicKey},
			wantErr: true,
		},
		{
			name:    "anthropic requested without its key",
			opts:    Options{Provider: ProviderAnthropic, OpenAIKey: openaiKey},
			wantErr: true,
		},
		{
			name:    "unknown provider",
			opts:    Options{Provider: "gemini", OpenAIKey: openaiKey},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.opts)

			if tt.wantErr {
				if !errors.Is(err, ErrNotConfigured) {
					t.Fatalf("got %v, want ErrNotConfigured", err)
				}

				if c != nil {
					t.Error("a coach was returned alongside the error")
				}

				return
			}

			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if got := c.Provider(); got != tt.provider {
				t.Errorf("provider = %q, want %q", got, tt.provider)
			}

			if got := c.Model(); got != tt.model {
				t.Errorf("model = %q, want %q", got, tt.model)
			}
		})
	}
}

// TestProviderOnNilCoach keeps the accessors safe on the disabled path.
func TestProviderOnNilCoach(t *testing.T) {
	var c *Coach

	if c.Provider() != "" || c.Model() != "" {
		t.Error("a nil coach reported a provider")
	}
}

// TestAnalyzeOnNilCoach makes sure the disabled path can't dereference nil, so
// a handler that forgets to check still fails cleanly.
func TestAnalyzeOnNilCoach(t *testing.T) {
	var c *Coach

	if _, err := c.Analyze(t.Context(), &demo.Analysis{}, nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v, want ErrNotConfigured", err)
	}
}

// TestReportSchemaIsValid guards the structured-output contract: the API rejects
// a malformed schema, and that failure would only surface on a paid call.
func TestReportSchemaIsValid(t *testing.T) {
	buf, err := json.Marshal(reportSchema)
	if err != nil {
		t.Fatalf("schema does not marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf, &parsed); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props, ok := parsed["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}

	// Every field the frontend renders must exist in the schema, or the model
	// is free to omit it.
	for _, field := range []string{"summary", "strengths", "mistakes", "drills", "trend"} {
		if _, ok := props[field]; !ok {
			t.Errorf("schema is missing %q", field)
		}
	}

	// OpenAI's strict mode is the tighter of the two providers, and breaking it
	// would only surface as a 400 on a real call.
	assertStrictCompatible(t, parsed, "root")

	// A Report has to be able to hold what the schema describes.
	sample := `{
		"summary": "resumo",
		"strengths": [{"title": "t", "detail": "d", "evidence": "e"}],
		"mistakes": [{"title": "t", "detail": "d", "evidence": "e"}],
		"drills": [{"title": "t", "why": "w"}],
		"trend": "melhorou"
	}`

	var report Report
	if err := json.Unmarshal([]byte(sample), &report); err != nil {
		t.Fatalf("Report cannot decode a schema-shaped response: %v", err)
	}

	if report.Summary != "resumo" || len(report.Drills) != 1 || report.Trend == "" {
		t.Errorf("decoded report lost fields: %+v", report)
	}
}

func sampleAnalysis(name string) *demo.Analysis {
	return &demo.Analysis{
		Summary: demo.Summary{Map: "de_mirage", Rounds: 22},
		Player: demo.PlayerStats{
			SteamID: "76561198410645260",
			Name:    name,
			Kills:   25,
			Deaths:  13,
			ADR:     112.18,
			Rating:  1.68,
		},
	}
}

func TestBuildPrompt(t *testing.T) {
	t.Run("without history", func(t *testing.T) {
		got, err := buildPrompt(sampleAnalysis("MuriloAS"), nil)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(got, "MuriloAS") {
			t.Error("prompt does not name the player")
		}

		if strings.Contains(got, "previousMatches") {
			t.Error("prompt mentions previous matches when there are none")
		}

		assertPayloadIsJSON(t, got)
	})

	t.Run("with history", func(t *testing.T) {
		previous := []*demo.Analysis{sampleAnalysis("MuriloAS"), sampleAnalysis("MuriloAS")}

		got, err := buildPrompt(sampleAnalysis("MuriloAS"), previous)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(got, "previousMatches") {
			t.Fatal("prompt does not carry the history")
		}

		payload := assertPayloadIsJSON(t, got)

		past, ok := payload["previousMatches"].([]any)
		if !ok {
			t.Fatal("previousMatches is not a list")
		}

		if len(past) != 2 {
			t.Errorf("got %d previous matches, want 2", len(past))
		}

		// Past matches carry aggregates only: shipping full round breakdowns
		// for every match would bloat the prompt for no gain.
		first, _ := past[0].(map[string]any)
		if _, hasRounds := first["rounds"]; !hasRounds {
			t.Error("past match lost its round count")
		}

		if _, hasDetail := first["coaching"]; hasDetail {
			t.Error("past match carries the full coaching detail")
		}
	})

	t.Run("nil analysis", func(t *testing.T) {
		if _, err := buildPrompt(nil, nil); err == nil {
			t.Error("a nil analysis was accepted")
		}
	})
}

// assertPayloadIsJSON pulls the fenced JSON block out of the prompt and checks
// it parses — a malformed payload would be a silently bad request.
func assertPayloadIsJSON(t *testing.T, prompt string) map[string]any {
	t.Helper()

	_, after, found := strings.Cut(prompt, "```json\n")
	if !found {
		t.Fatal("prompt has no JSON block")
	}

	body, _, found := strings.Cut(after, "\n```")
	if !found {
		t.Fatal("prompt's JSON block is not closed")
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}

	return payload
}

// assertStrictCompatible walks the schema checking OpenAI's strict-mode rules:
// every object lists all its properties as required and forbids extras, and no
// unsupported keyword sneaks in.
func assertStrictCompatible(t *testing.T, node map[string]any, path string) {
	t.Helper()

	for _, unsupported := range []string{"maxItems", "minItems", "pattern", "format"} {
		if _, found := node[unsupported]; found {
			t.Errorf("%s: %q is not allowed in OpenAI strict mode", path, unsupported)
		}
	}

	if node["type"] == "object" {
		if extra, ok := node["additionalProperties"].(bool); !ok || extra {
			t.Errorf("%s: objects must set additionalProperties to false", path)
		}

		props, _ := node["properties"].(map[string]any)

		required := map[string]bool{}
		if list, ok := node["required"].([]any); ok {
			for _, r := range list {
				name, _ := r.(string)
				required[name] = true
			}
		}

		for name, child := range props {
			if !required[name] {
				t.Errorf("%s: %q is a property but not required, which strict mode rejects", path, name)
			}

			if sub, ok := child.(map[string]any); ok {
				assertStrictCompatible(t, sub, path+"."+name)
			}
		}
	}

	if items, ok := node["items"].(map[string]any); ok {
		assertStrictCompatible(t, items, path+"[]")
	}
}
