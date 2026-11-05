package core

import "testing"

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
