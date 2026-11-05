package core

import (
	"encoding/json"
	"testing"
)

func TestRoles(t *testing.T) {
	mod := RoleModerator | RoleSubscriber
	if !mod.Has(RoleSubscriber) || mod.Has(RoleVIP) {
		t.Fatal("Has")
	}
	if !mod.AtLeast(RoleVIP) || !mod.AtLeast(RoleModerator) || mod.AtLeast(RoleBroadcaster) {
		t.Fatal("AtLeast")
	}
	if Role(0).AtLeast(RoleSubscriber) || !Role(0).AtLeast(0) {
		t.Fatal("viewer")
	}
}

func TestMessageJSONContract(t *testing.T) {
	msg := Message{
		ID:     "1",
		Source: Target{Platform: "twitch", Channel: "luks3r"},
		User:   User{ID: "42", Name: "ann", DisplayName: "Ann", Roles: RoleSubscriber | RoleModerator},
		Text:   "!ping",
		Raw:    struct{ Secret string }{"adapter data"},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"1","source":{"platform":"twitch","channel":"luks3r"},"user":{"id":"42","name":"ann","display_name":"Ann","roles":["subscriber","moderator"]},"text":"!ping"}`
	if string(data) != want {
		t.Fatalf("got  %s\nwant %s", data, want)
	}
	var back Message
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	msg.Raw = nil
	if back != msg {
		t.Fatalf("round trip: %+v", back)
	}
}

func TestRoleJSON(t *testing.T) {
	var r Role
	if err := json.Unmarshal([]byte(`[]`), &r); err != nil || r != 0 {
		t.Fatalf("empty: %v %v", r, err)
	}
	if data, _ := json.Marshal(Role(0)); string(data) != "[]" {
		t.Fatalf("viewer: %s", data)
	}
	if err := json.Unmarshal([]byte(`["owner"]`), &r); err == nil {
		t.Fatal("unknown role accepted")
	}
}
