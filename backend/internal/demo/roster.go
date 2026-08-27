package demo

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"

	demoinfocs "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/events"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/msg"
)

// maxRosterFrames bounds the scan on demos that never announce a match start,
// so a broken file can't turn the cheap pass into a full parse.
const maxRosterFrames = 100_000

// ScanRoster reads just enough of a demo to list who played it: it parses frame
// by frame and stops at the match start, where the teams are settled. On a
// 200 MB demo that is around a tenth of a second and a couple of percent of the
// file, which is what makes choosing a player before the real parse worthwhile.
//
// Players who join after the match starts are not listed.
func ScanRoster(r io.Reader) (roster *Roster, err error) {
	// The library documents that it may panic on corrupt demos.
	defer func() {
		if v := recover(); v != nil {
			roster, err = nil, fmt.Errorf("%w: corrupt demo (%v)", ErrInvalidDemo, v)
		}
	}()

	stream, closeStream, err := decompress(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidDemo, err)
	}
	defer closeStream()

	p := demoinfocs.NewParser(stream)
	defer p.Close()

	roster = &Roster{}

	p.RegisterNetMessageHandler(func(m *msg.CSVCMsg_ServerInfo) {
		roster.Map = m.GetMapName()
	})

	matchStarted := false
	p.RegisterEventHandler(func(events.MatchStart) { matchStarted = true })

	found := make(map[uint64]PlayerIdentity)

	for frames := 0; frames < maxRosterFrames && !matchStarted; frames++ {
		more, err := p.ParseNextFrame()

		switch {
		case errors.Is(err, demoinfocs.ErrInvalidFileType):
			return nil, fmt.Errorf("%w: %s", ErrInvalidDemo, err)
		case errors.Is(err, demoinfocs.ErrUnexpectedEndOfDemo):
			// A truncated demo can still hold a usable roster.
			more = false
		case err != nil:
			return nil, fmt.Errorf("scanning demo: %w", err)
		}

		for _, pl := range p.GameState().Participants().Playing() {
			if pl.SteamID64 == 0 || !isPlayingSide(pl.Team) {
				continue
			}

			id := PlayerIdentity{
				SteamID: strconv.FormatUint(pl.SteamID64, 10),
				Name:    pl.Name,
				Side:    sideName(pl.Team),
			}

			if pl.TeamState != nil {
				id.Team = pl.TeamState.ClanName()
			}

			found[pl.SteamID64] = id
		}

		if !more {
			break
		}
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("%w: no players found", ErrInvalidDemo)
	}

	roster.Players = make([]PlayerIdentity, 0, len(found))
	for _, id := range found {
		roster.Players = append(roster.Players, id)
	}

	// Group teammates together, alphabetical within a team.
	sort.Slice(roster.Players, func(i, j int) bool {
		a, b := roster.Players[i], roster.Players[j]
		if a.Team != b.Team {
			return a.Team < b.Team
		}

		return a.Name < b.Name
	})

	return roster, nil
}
