package core

// Target identifies a chat destination: a channel on a platform.
type Target struct {
	Platform string
	Channel  string
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

// Has reports whether every role in want is held.
func (r Role) Has(want Role) bool { return r&want == want }

// AtLeast reports whether the highest held role ranks at or above minRole.
func (r Role) AtLeast(minRole Role) bool { return r >= minRole }

type User struct {
	ID          string
	Name        string
	DisplayName string
	Roles       Role
}

// Message is a platform-neutral chat message produced by an adapter.
type Message struct {
	ID     string
	Source Target // where the message came from; replies go here
	User   User
	Text   string
	Raw    any // adapter-specific data; modules should not depend on it
}
