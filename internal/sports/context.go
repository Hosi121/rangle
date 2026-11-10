// Package sports describes reviewed sporting context independently of users,
// media reconstruction and delivery. IDs identify entities; names are labels.
package sports

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hosi121/rangle/internal/publisher"
)

type Entity struct {
	ID   string `json:"id" firestore:"id"`
	Name string `json:"name" firestore:"name"`
}

type Venue struct {
	ID   string `json:"id" firestore:"id"`
	Name string `json:"name" firestore:"name"`
	City string `json:"city,omitempty" firestore:"city,omitempty"`
}

type Event struct {
	ID          string     `json:"id" firestore:"id"`
	Name        string     `json:"name" firestore:"name"`
	Kind        string     `json:"kind" firestore:"kind"` // match, training or demonstration
	Competition *Entity    `json:"competition,omitempty" firestore:"competition,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty" firestore:"starts_at,omitempty"`
	Venue       *Venue     `json:"venue,omitempty" firestore:"venue,omitempty"`
	Teams       []Entity   `json:"teams,omitempty" firestore:"teams,omitempty"`
}

// Context is a publication snapshot, not a user's claim to publishing rights.
// Teams describe the event; athletes do not assume a permanent affiliation.
// Unknown provenance must remain absent instead of becoming invented metadata.
type Context struct {
	Publisher *publisher.Account `json:"publisher,omitempty" firestore:"publisher,omitempty"`
	Event     *Event             `json:"event,omitempty" firestore:"event,omitempty"`
	Athletes  []Entity           `json:"athletes,omitempty" firestore:"athletes,omitempty"`
}

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func label(s string, max int) bool {
	if strings.TrimSpace(s) == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}
func entity(e Entity) bool { return identifier.MatchString(e.ID) && label(e.Name, 120) }
func entities(rows []Entity, max int) bool {
	if len(rows) > max {
		return false
	}
	seen := map[string]bool{}
	for _, e := range rows {
		if !entity(e) || seen[e.ID] {
			return false
		}
		seen[e.ID] = true
	}
	return true
}
func (c *Context) Validate() error {
	if c == nil {
		return nil
	}
	if c.Publisher == nil && c.Event == nil && len(c.Athletes) == 0 {
		return fmt.Errorf("empty sports context")
	}
	if p := c.Publisher; p != nil {
		if err := p.Validate(); err != nil {
			return err
		}
	}
	if e := c.Event; e != nil {
		if !entity(Entity{e.ID, e.Name}) || (e.Kind != "match" && e.Kind != "training" && e.Kind != "demonstration") || !entities(e.Teams, 16) {
			return fmt.Errorf("invalid sports event")
		}
		if e.Competition != nil && !entity(*e.Competition) {
			return fmt.Errorf("invalid competition")
		}
		if e.StartsAt != nil && (e.StartsAt.Year() < 1900 || e.StartsAt.Year() > 2200) {
			return fmt.Errorf("invalid event date")
		}
		if v := e.Venue; v != nil && (!entity(Entity{v.ID, v.Name}) || v.City != "" && !label(v.City, 120)) {
			return fmt.Errorf("invalid venue")
		}
	}
	if !entities(c.Athletes, 64) {
		return fmt.Errorf("invalid athletes")
	}
	return nil
}

// ValidateCollection rejects contradictory labels/relationships for the same ID
// in one reviewed snapshot. Separate entity kinds may use the same local ID.
func ValidateCollection(contexts []*Context) error {
	seen := map[string]string{}
	add := func(kind, id string, value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		key := kind + ":" + id
		if old, ok := seen[key]; ok && old != string(data) {
			return fmt.Errorf("conflicting %s %q", kind, id)
		}
		seen[key] = string(data)
		return nil
	}
	for _, c := range contexts {
		if err := c.Validate(); err != nil {
			return err
		}
		if c == nil {
			continue
		}
		if p := c.Publisher; p != nil {
			if err := add("publisher", string(p.ID), p); err != nil {
				return err
			}
		}
		if e := c.Event; e != nil {
			if err := add("event", e.ID, e); err != nil {
				return err
			}
			if e.Competition != nil {
				if err := add("competition", e.Competition.ID, e.Competition); err != nil {
					return err
				}
			}
			if e.Venue != nil {
				if err := add("venue", e.Venue.ID, e.Venue); err != nil {
					return err
				}
			}
			for _, team := range e.Teams {
				if err := add("team", team.ID, team); err != nil {
					return err
				}
			}
		}
		for _, a := range c.Athletes {
			if err := add("athlete", a.ID, a); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Context) SearchText() string {
	if c == nil {
		return ""
	}
	var parts []string
	if p := c.Publisher; p != nil {
		parts = append(parts, p.Name)
	}
	if e := c.Event; e != nil {
		parts = append(parts, e.Name)
		if e.Competition != nil {
			parts = append(parts, e.Competition.Name)
		}
		if e.Venue != nil {
			parts = append(parts, e.Venue.Name, e.Venue.City)
		}
		for _, t := range e.Teams {
			parts = append(parts, t.Name)
		}
	}
	for _, a := range c.Athletes {
		parts = append(parts, a.Name)
	}
	return strings.Join(parts, " ")
}

// Clone keeps immutable job/publication snapshots independent of configuration.
func (c *Context) Clone() *Context {
	if c == nil {
		return nil
	}
	result := *c
	if c.Publisher != nil {
		p := *c.Publisher
		result.Publisher = &p
	}
	result.Athletes = append([]Entity(nil), c.Athletes...)
	if c.Event != nil {
		e := *c.Event
		if e.Competition != nil {
			v := *e.Competition
			e.Competition = &v
		}
		if e.Venue != nil {
			v := *e.Venue
			e.Venue = &v
		}
		if e.StartsAt != nil {
			v := *e.StartsAt
			e.StartsAt = &v
		}
		e.Teams = append([]Entity(nil), e.Teams...)
		result.Event = &e
	}
	return &result
}
