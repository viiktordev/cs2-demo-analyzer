package coach

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/v1sscardoso/pro-coach-cs2/backend/internal/demo"
)

// systemPrompt is the coaching rubric, shared by every provider. It is
// deliberately stable — it is marked cacheable, so any change here costs a cache
// miss on the next request.
//
// The emphasis on not recomputing is the important part: every number in the
// payload was measured exactly from the demo, and a model that second-guesses
// the arithmetic produces confident nonsense.
const systemPrompt = `You are a Counter-Strike 2 coach reviewing one player's match from parsed demo data.

The numbers you receive were measured exactly from the demo. Treat them as
ground truth: never recompute, re-derive, or contradict them. Your job is to
explain what they mean and what to do about it.

What good coaching looks like here:

- Tie every claim to specific evidence — a stat, a round number, a death cluster.
- Prefer causes over restatements. "ADR 51 with 70% headshots" is not a aiming
  problem, it is a problem of being in the wrong duels; say that.
- Contrast the rounds the player won against the ones they lost, and the sides
  they played. Sides swap at halftime, so read each round's own side field.
- Rank ruthlessly. Two or three things worth practising beat ten observations.
- Be direct about weaknesses without being harsh. This is for someone trying to
  improve, not an audience.

Reading the data:

- Death clusters are groups of deaths that happened in nearly the same place.
  The coordinates are raw world units with no map calibration, so they are only
  meaningful relative to each other — never guess a callout or area name from
  them. "You died in the same spot in rounds 2, 5 and 6" is the claim to make.
- buyType is the player's own economy that round: pistol, eco, force or full.
- moneyLeftover is cash still unspent once the buy period closed.
- Accuracy is hits per shot fired. Shotgun blasts register several hits from one
  shot, so heavy-class accuracy reads high; do not treat it as comparable.
- utilityUnused counts grenades still held at death — utility that was bought
  and never used.
- teammatesFlashed counts the player's own team blinded by their flashes.
- mvps is 0 on demos whose server does not emit the event, which is common on
  third-party platforms. Never read a 0 there as a finding.
- clutchesPlayed counts every round entered as the last player alive, including
  hopeless ones, so a low win rate there is normal and rarely worth a finding.

Write in the same language the player's data suggests, defaulting to Brazilian
Portuguese. Keep each field short: a finding is one or two sentences.

Respect the list limits in the schema descriptions. Leave trend as an empty
string when no previous matches were supplied.`

// reportSchema constrains the response so the frontend can render fixed
// sections rather than parse prose.
//
// It is written to the intersection of what both providers accept, which means
// OpenAI's strict subset: every property listed in "required",
// "additionalProperties" false on each object, and no maxItems. The list
// lengths therefore live in the descriptions instead of the schema — a limit
// the model is asked to respect rather than one the API enforces.
var reportSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"summary", "strengths", "mistakes", "drills", "trend"},
	"properties": map[string]any{
		"summary": map[string]any{
			"type":        "string",
			"description": "Two or three sentences reading the match as a whole.",
		},
		"strengths": map[string]any{
			"type":        "array",
			"description": "What the player did well and should keep doing. At most 3.",
			"items":       findingSchema("A strength, with the numbers behind it."),
		},
		"mistakes": map[string]any{
			"type":        "array",
			"description": "Recurring mistakes, most costly first. At most 4.",
			"items":       findingSchema("A recurring mistake, with the numbers behind it."),
		},
		"drills": map[string]any{
			"type":        "array",
			"description": "Concrete things to practise, most important first. At most 3.",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"title", "why"},
				"properties": map[string]any{
					"title": map[string]any{"type": "string", "description": "What to practise."},
					"why":   map[string]any{"type": "string", "description": "Which finding this addresses."},
				},
			},
		},
		"trend": map[string]any{
			"type": "string",
			"description": "How the player changed across the supplied previous matches. " +
				"An empty string when no previous matches were supplied.",
		},
	},
}

func findingSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"description":          description,
		"required":             []string{"title", "detail", "evidence"},
		"properties": map[string]any{
			"title":    map[string]any{"type": "string", "description": "A short label."},
			"detail":   map[string]any{"type": "string", "description": "One or two sentences explaining it."},
			"evidence": map[string]any{"type": "string", "description": "The specific stats or rounds behind it."},
		},
	}
}

// pastMatch is the compact view of an earlier match. Only aggregates go in:
// carrying full round breakdowns for every past match would grow the prompt
// without adding much to a trend.
type pastMatch struct {
	Map     string  `json:"map"`
	Rounds  int     `json:"rounds"`
	Kills   int     `json:"kills"`
	Deaths  int     `json:"deaths"`
	ADR     float64 `json:"adr"`
	KAST    float64 `json:"kast"`
	Rating  float64 `json:"rating"`
	HSPct   float64 `json:"headshotPct"`
	Opening string  `json:"openingDuels"`
}

// buildPrompt renders the payload the model reads.
func buildPrompt(current *demo.Analysis, previous []*demo.Analysis) (string, error) {
	if current == nil {
		return "", fmt.Errorf("no analysis to coach")
	}

	payload := map[string]any{
		"match": map[string]any{
			"map":      current.Map,
			"rounds":   current.Rounds,
			"score":    current.Score,
			"tickRate": current.TickRate,
		},
		"player": current.Player,
	}

	if len(previous) > 0 {
		past := make([]pastMatch, 0, len(previous))

		for _, p := range previous {
			if p == nil {
				continue
			}

			past = append(past, pastMatch{
				Map:     p.Map,
				Rounds:  p.Rounds,
				Kills:   p.Player.Kills,
				Deaths:  p.Player.Deaths,
				ADR:     p.Player.ADR,
				KAST:    p.Player.KAST,
				Rating:  p.Player.Rating,
				HSPct:   p.Player.HeadshotPct,
				Opening: fmt.Sprintf("%d/%d", p.Player.OpeningKills, p.Player.OpeningDeaths),
			})
		}

		if len(past) > 0 {
			payload["previousMatches"] = past
		}
	}

	buf, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding prompt payload: %w", err)
	}

	var b strings.Builder

	b.WriteString("Review this match for ")
	b.WriteString(current.Player.Name)
	b.WriteString(".\n\n")

	if len(previous) > 0 {
		b.WriteString("previousMatches holds the player's earlier matches, newest first. ")
		b.WriteString("Use them for the trend field; the findings should still be about this match.\n\n")
	}

	b.WriteString("```json\n")
	b.Write(buf)
	b.WriteString("\n```")

	return b.String(), nil
}
