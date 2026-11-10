package publisher

import (
	"encoding/json"
	"testing"
)

func TestOrganizationOwnsItsStaffIdentity(t *testing.T) {
	a := Account{ID: "club-a", Name: "Club A", Kind: "organization", Status: Active}
	b := Account{ID: "club-b", Name: "Club B", Kind: "organization", Status: Active}
	// IDs and usernames can be identical under different organizations.
	staffA := StaffAccount{ID: "staff-1", PublisherAccountID: a.ID, Username: "editor", Role: Editor}
	staffB := staffA
	staffB.PublisherAccountID = b.ID
	actorA := StaffPrincipal{PublisherAccountID: a.ID, StaffAccountID: staffA.ID}
	actorB := StaffPrincipal{PublisherAccountID: b.ID, StaffAccountID: staffB.ID}
	if !a.Allows(actorA, staffA, PublishClip) || !b.Allows(actorB, staffB, PublishClip) {
		t.Fatal("local staff denied")
	}
	for name, allowed := range map[string]bool{
		"wrong resource account":      b.Allows(actorA, staffA, PublishClip),
		"substituted staff record":    a.Allows(actorA, staffB, PublishClip),
		"substituted actor":           a.Allows(actorB, staffA, PublishClip),
		"unscoped actor":              a.Allows(StaffPrincipal{StaffAccountID: staffA.ID}, staffA, PublishClip),
		"username is not a principal": a.Allows(StaffPrincipal{PublisherAccountID: a.ID, StaffAccountID: StaffID(staffA.Username)}, staffA, PublishClip),
	} {
		if allowed {
			t.Errorf("authorization crossed organization boundary: %s", name)
		}
	}
}

func TestOrganizationRBACAndOffboarding(t *testing.T) {
	account := Account{ID: "club", Name: "Club", Kind: "organization", Status: Active}
	staff := StaffAccount{ID: "staff-1", PublisherAccountID: account.ID, Username: "editor", Role: Editor}
	actor := StaffPrincipal{PublisherAccountID: account.ID, StaffAccountID: staff.ID}
	for _, permission := range []Permission{ReadPresets, CreateGeneration, ReadGeneration, CancelGeneration, PublishClip, WithdrawClip} {
		if !account.Allows(actor, staff, permission) {
			t.Errorf("editor denied %s", permission)
		}
	}
	for _, permission := range []Permission{ManageStaff, RequestDeletion, Permission("*"), Permission("platform.admin")} {
		if account.Allows(actor, staff, permission) {
			t.Errorf("editor allowed %s", permission)
		}
	}
	admin := staff
	admin.Role = Administrator
	if !account.Allows(actor, admin, ManageStaff) || !account.Allows(actor, admin, RequestDeletion) {
		t.Fatal("organization administrator denied")
	}
	if account.Allows(actor, admin, Permission("platform.admin")) {
		t.Fatal("organization admin became platform operator")
	}
	admin.Disabled = true
	if account.Allows(actor, admin, PublishClip) {
		t.Fatal("offboarded staff kept publishing permission")
	}
	if account.Validate() != nil {
		t.Fatal("staff offboarding invalidated the organization")
	}
	staff.Role = "unknown"
	if account.Allows(actor, staff, PublishClip) {
		t.Fatal("unknown role granted access")
	}
	if account.Allows(actor, StaffAccount{}, PublishClip) {
		t.Fatal("missing staff record granted access")
	}
}

func TestAccountLifecycleAndStaffAbsenceAreIndependent(t *testing.T) {
	account := Account{ID: "club", Name: "Club", Kind: "organization", Status: Active}
	staff := StaffAccount{ID: "staff-1", PublisherAccountID: account.ID, Username: "editor", Role: Editor}
	actor := StaffPrincipal{PublisherAccountID: account.ID, StaffAccountID: staff.ID}
	for _, status := range []Status{"", Suspended, Deleting, Deleted, "unknown"} {
		account.Status = status
		if account.Allows(actor, staff, PublishClip) {
			t.Fatalf("publishing accepted with status %q", status)
		}
	}
	account.Status = Active
	if account.Allows(actor, StaffAccount{}, PublishClip) || account.Validate() != nil {
		t.Fatal("staff absence changed organization existence")
	}
	data, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	var public Account
	if err := json.Unmarshal(data, &public); err != nil {
		t.Fatal(err)
	}
	if public.Status != "" || public.Allows(actor, staff, PublishClip) {
		t.Fatal("public metadata became account authorization")
	}
}

func TestStaffCredentialsAndPrivilegesAreNotPublicClipMetadata(t *testing.T) {
	staff := StaffAccount{ID: "staff-1", PublisherAccountID: "club", Username: "editor", Role: Administrator}
	data, err := json.Marshal(staff)
	if err != nil || string(data) != "{}" {
		t.Fatal("private staff profile leaked", string(data), err)
	}
	account := Account{ID: "club", Name: "Club", Kind: "person"}
	actor := StaffPrincipal{PublisherAccountID: account.ID, StaffAccountID: staff.ID}
	if account.Allows(actor, staff, PublishClip) {
		t.Fatal("personal publishing account accepted")
	}
}
