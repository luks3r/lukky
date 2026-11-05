package core

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Target identifies a chat destination: a channel on a platform.
type Target struct {
	Platform string `json:"platform"`
	Channel  string `json:"channel"`
}

// Role is a set of normalized chat roles. The zero value is a plain viewer.
// Higher flags rank higher, so AtLeast compares by the highest role held.
type Role uint8

const (
	RoleSubscriber Role = 1 << iota
	RoleVIP
	RoleModerator
	RoleBroadcaster
)

type roleName struct {
	role Role
	name string
}

var roleNames = []roleName{
	{RoleSubscriber, "subscriber"},
	{RoleVIP, "vip"},
	{RoleModerator, "moderator"},
	{RoleBroadcaster, "broadcaster"},
}

// Has reports whether every role in want is held.
func (r Role) Has(want Role) bool { return r&want == want }

// AtLeast reports whether the highest held role ranks at or above minRole.
func (r Role) AtLeast(minRole Role) bool { return r >= minRole }

// Names lists the held roles from lowest to highest rank.
func (r Role) Names() []string {
	names := []string{}
	for _, rn := range roleNames {
		if r.Has(rn.role) {
			names = append(names, rn.name)
		}
	}
	return names
}

// ParseRole returns the role with the given name, such as "moderator".
func ParseRole(name string) (Role, error) {
	i := slices.IndexFunc(roleNames, func(rn roleName) bool { return rn.name == name })
	if i < 0 {
		return 0, fmt.Errorf("unknown role %q", name)
	}
	return roleNames[i].role, nil
}

// MarshalJSON encodes roles as a list of names, for example ["subscriber","moderator"].
func (r Role) MarshalJSON() ([]byte, error) { return json.Marshal(r.Names()) }

func (r *Role) UnmarshalJSON(data []byte) error {
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return err
	}
	var roles Role
	for _, name := range names {
		role, err := ParseRole(name)
		if err != nil {
			return err
		}
		roles |= role
	}
	*r = roles
	return nil
}

type User struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Roles       Role   `json:"roles"`
}

// Message is a platform-neutral chat message produced by an adapter.
type Message struct {
	ID     string `json:"id"`
	Source Target `json:"source"` // where the message came from; replies go here
	User   User   `json:"user"`
	Text   string `json:"text"`
	Raw    any    `json:"-"` // adapter-specific data; modules should not depend on it
}
