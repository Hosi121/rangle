package sports

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/publisher"
)

func fixture(t *testing.T) *Context {
	t.Helper()
	data, err := os.ReadFile("../../testdata/sports/context.json")
	if err != nil {
		t.Fatal(err)
	}
	var c Context
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}
func TestReviewedContextAndIdentityConsistency(t *testing.T) {
	c := fixture(t)
	if err := ValidateCollection([]*Context{nil, c, fixture(t)}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Example Sports", "サンプルカップ決勝", "サンプル選手", "例示体育館", "東京", "サンプルA"} {
		if !strings.Contains(c.SearchText(), name) {
			t.Fatalf("search omits %s", name)
		}
	}
	other := fixture(t)
	other.Athletes[0].Name = "Another name"
	if ValidateCollection([]*Context{c, other}) == nil {
		t.Fatal("conflicting athlete identity accepted")
	}
}
func TestInvalidContextDoesNotInventDefaults(t *testing.T) {
	for name, mutate := range map[string]func(*Context){
		"personal publisher":      func(c *Context) { c.Publisher.Kind = "person" },
		"missing organization id": func(c *Context) { c.Publisher.ID = "" },
		"path id":                 func(c *Context) { c.Event.Venue.ID = "../private" },
		"unknown kind":            func(c *Context) { c.Event.Kind = "other" },
		"duplicate athlete":       func(c *Context) { c.Athletes = append(c.Athletes, c.Athletes[0]) },
		"duplicate team":          func(c *Context) { c.Event.Teams = append(c.Event.Teams, c.Event.Teams[0]) },
		"control label":           func(c *Context) { c.Event.Name = "Match\nExtra" },
		"empty context":           func(c *Context) { *c = Context{} },
	} {
		t.Run(name, func(t *testing.T) {
			c := fixture(t)
			mutate(c)
			if c.Validate() == nil {
				t.Fatal("accepted invalid context")
			}
		})
	}
	if (&Context{Publisher: &publisher.Account{ID: "org", Name: "団体", Kind: "organization"}}).Validate() != nil {
		t.Fatal("partial known metadata rejected")
	}
}
