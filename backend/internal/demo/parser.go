package demo

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	demoinfocs "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/msg"
)

// ErrInvalidDemo is returned when the reader doesn't hold a CS demo at all.
var ErrInvalidDemo = errors.New("invalid demo file")

// ErrUnknownPlayer is returned when the requested SteamID never played a round
// in the demo.
var ErrUnknownPlayer = errors.New("player not found in demo")

// Analyze reads a full demo from r and returns the match summary together with
// the statistics of the single player identified by steamID. Scoping the
// collection to one player is what makes the two-pass flow worthwhile: callers
// pick from ScanRoster's cheap output before paying for this.
//
// A truncated demo is not an error: whatever was parsed up to the cut is
// returned with Summary.Truncated set.
func Analyze(r io.Reader, fileName, steamID string) (a *Analysis, err error) {
	target, parseErr := strconv.ParseUint(steamID, 10, 64)
	if parseErr != nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPlayer, steamID)
	}

	// The library documents that it may panic on corrupt demos; surface that as
	// a normal error so the caller can answer with a 4xx.
	defer func() {
		if v := recover(); v != nil {
			a, err = nil, fmt.Errorf("%w: corrupt demo (%v)", ErrInvalidDemo, v)
		}
	}()

	// FACEIT and Valve serve demos compressed; unwrap before parsing.
	stream, closeStream, err := decompress(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidDemo, err)
	}
	defer closeStream()

	p := demoinfocs.NewParser(stream)
	defer p.Close()

	s := &Summary{
		FileName: fileName,
		ParsedAt: time.Now().UTC(),
	}

	// v5 no longer exposes the demo header, so the map name is taken from the
	// server info message instead.
	p.RegisterNetMessageHandler(func(m *msg.CSVCMsg_ServerInfo) {
		s.Map = m.GetMapName()
	})

	c := newCollector(p, target)
	c.register()

	switch err := p.ParseToEnd(); {
	case err == nil:
	case errors.Is(err, demoinfocs.ErrUnexpectedEndOfDemo):
		s.Truncated = true
	case errors.Is(err, demoinfocs.ErrInvalidFileType):
		return nil, fmt.Errorf("%w: %s", ErrInvalidDemo, err)
	default:
		return nil, fmt.Errorf("parsing demo: %w", err)
	}

	gs := p.GameState()

	s.TickRate = p.TickRate()
	s.DurationSeconds = p.CurrentTime().Seconds()

	s.RoundHistory = c.history

	s.Rounds = gs.TotalRoundsPlayed()
	if s.Rounds == 0 {
		s.Rounds = len(s.RoundHistory)
	}

	s.Score = [2]TeamScore{
		teamScore(gs.TeamCounterTerrorists()),
		teamScore(gs.TeamTerrorists()),
	}

	player := c.stats()
	if player.RoundsPlayed == 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnknownPlayer, steamID)
	}

	return &Analysis{Summary: *s, Player: player}, nil
}
