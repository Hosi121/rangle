package publisher

import (
	"encoding/json"
	"testing"
)

func TestPublishingRequiresMembershipInTheActingOrganization(t *testing.T) {
	account := Account{ID: "club-a", Name: "Sample Club", Kind: "organization"}
	member := Membership{PublisherAccountID: account.ID, UserID: "viewer", Role: Editor}
	if !account.CanPublish("viewer", member) {
		t.Fatal("organization editor rejected")
	}
	for name, change := range map[string]func(*Account, *Membership){
		"unaffiliated person":             func(_ *Account, m *Membership) { *m = Membership{} },
		"another person":                  func(_ *Account, m *Membership) { m.UserID = "another" },
		"another organization":            func(_ *Account, m *Membership) { m.PublisherAccountID = "club-b" },
		"username equals organization id": func(_ *Account, m *Membership) { m.UserID = "club-a" },
		"unknown role":                    func(_ *Account, m *Membership) { m.Role = "admin" },
		"individual publisher":            func(a *Account, _ *Membership) { a.Kind = "person" },
		"empty organization id":           func(a *Account, _ *Membership) { a.ID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			a, m := account, member
			change(&a, &m)
			if a.CanPublish("viewer", m) {
				t.Fatal("publishing authority crossed account boundaries")
			}
		})
	}
	if account.CanPublish("club-a", Membership{}) {
		t.Fatal("matching a publisher ID granted a person publishing rights")
	}
	member.Role = Owner
	if !account.CanPublish("viewer", member) {
		t.Fatal("organization owner rejected")
	}
	b, err := json.Marshal(member)
	if err != nil || string(b) != "{}" {
		t.Fatal("membership leaked into public JSON", string(b), err)
	}
}
