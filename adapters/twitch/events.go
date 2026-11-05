package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/luks3r/lukky/core"
)

// Topics published by EventSub. Lua modules subscribe by name, for example
// host.on("twitch.follow", fn).
var (
	Follow        = core.Topic[FollowEvent]{Name: "twitch.follow"}
	Subscribe     = core.Topic[SubscribeEvent]{Name: "twitch.subscribe"}
	GiftSubs      = core.Topic[GiftEvent]{Name: "twitch.gift"}
	Resubscribe   = core.Topic[ResubEvent]{Name: "twitch.resub"}
	Cheer         = core.Topic[CheerEvent]{Name: "twitch.cheer"}
	Raid          = core.Topic[RaidEvent]{Name: "twitch.raid"}
	Redemption    = core.Topic[RedemptionEvent]{Name: "twitch.redemption"}
	StreamOnline  = core.Topic[StreamOnlineEvent]{Name: "twitch.stream_online"}
	StreamOffline = core.Topic[StreamOfflineEvent]{Name: "twitch.stream_offline"}
)

// User identifies a Twitch account. It is empty for anonymous gifts and cheers.
type User struct {
	ID    string `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

type FollowEvent struct {
	Channel    string    `json:"channel"`
	User       User      `json:"user"`
	FollowedAt time.Time `json:"followed_at"`
}

// SubscribeEvent is a new subscription. Tier is "1000", "2000", or "3000".
type SubscribeEvent struct {
	Channel string `json:"channel"`
	User    User   `json:"user"`
	Tier    string `json:"tier"`
	IsGift  bool   `json:"is_gift"`
}

type GiftEvent struct {
	Channel         string `json:"channel"`
	User            User   `json:"user"`
	Anonymous       bool   `json:"anonymous"`
	Total           int    `json:"total"`
	Tier            string `json:"tier"`
	CumulativeTotal int    `json:"cumulative_total"` // 0 when anonymous or not shared
}

// ResubEvent is a resubscription the user shared in chat.
type ResubEvent struct {
	Channel          string `json:"channel"`
	User             User   `json:"user"`
	Tier             string `json:"tier"`
	Message          string `json:"message"`
	CumulativeMonths int    `json:"cumulative_months"`
	StreakMonths     int    `json:"streak_months"` // 0 when not shared
	DurationMonths   int    `json:"duration_months"`
}

type CheerEvent struct {
	Channel   string `json:"channel"`
	User      User   `json:"user"`
	Anonymous bool   `json:"anonymous"`
	Bits      int    `json:"bits"`
	Message   string `json:"message"`
}

type RaidEvent struct {
	Channel string `json:"channel"`
	From    User   `json:"from"`
	Viewers int    `json:"viewers"`
}

type RedemptionEvent struct {
	Channel     string `json:"channel"`
	User        User   `json:"user"`
	RewardID    string `json:"reward_id"`
	RewardTitle string `json:"reward_title"`
	Cost        int    `json:"cost"`
	Input       string `json:"input"`
	Status      string `json:"status"`
}

type StreamOnlineEvent struct {
	Channel   string    `json:"channel"`
	Type      string    `json:"type"`
	StartedAt time.Time `json:"started_at"`
}

type StreamOfflineEvent struct {
	Channel string `json:"channel"`
}

// Wire formats, as Twitch sends them.

type wireUser struct {
	UserID    string `json:"user_id"`
	UserLogin string `json:"user_login"`
	UserName  string `json:"user_name"`
}

func (u wireUser) user() User { return User{ID: u.UserID, Login: u.UserLogin, Name: u.UserName} }

type wireBroadcaster struct {
	BroadcasterUserLogin string `json:"broadcaster_user_login"`
}

// subscription describes one EventSub type: what to request and how to
// turn its event into a topic event.
type subscription struct {
	kind, version string
	scope         string // required token scope; "" if none
	condition     func(broadcasterID string) map[string]string
	publish       func(ctx context.Context, events core.Publisher, raw json.RawMessage) error
}

// decodeAs decodes Twitch's event JSON as W, converts it, and emits it on topic.
func decodeAs[W, E any](topic core.Topic[E], convert func(W) E) func(context.Context, core.Publisher, json.RawMessage) error {
	return func(ctx context.Context, events core.Publisher, raw json.RawMessage) error {
		var wire W
		if err := json.Unmarshal(raw, &wire); err != nil {
			return fmt.Errorf("decode %s event: %w", topic.Name, err)
		}
		return core.Emit(ctx, events, topic, convert(wire))
	}
}

func broadcaster(id string) map[string]string { return map[string]string{"broadcaster_user_id": id} }

var subscriptions = []subscription{
	{
		kind: "channel.follow", version: "2", scope: "moderator:read:followers",
		condition: func(id string) map[string]string {
			return map[string]string{"broadcaster_user_id": id, "moderator_user_id": id}
		},
		publish: decodeAs(Follow, func(w struct {
			wireUser
			wireBroadcaster
			FollowedAt time.Time `json:"followed_at"`
		},
		) FollowEvent {
			return FollowEvent{Channel: w.BroadcasterUserLogin, User: w.user(), FollowedAt: w.FollowedAt}
		}),
	},
	{
		kind: "channel.subscribe", version: "1", scope: "channel:read:subscriptions", condition: broadcaster,
		publish: decodeAs(Subscribe, func(w struct {
			wireUser
			wireBroadcaster
			Tier   string `json:"tier"`
			IsGift bool   `json:"is_gift"`
		},
		) SubscribeEvent {
			return SubscribeEvent{Channel: w.BroadcasterUserLogin, User: w.user(), Tier: w.Tier, IsGift: w.IsGift}
		}),
	},
	{
		kind: "channel.subscription.gift", version: "1", scope: "channel:read:subscriptions", condition: broadcaster,
		publish: decodeAs(GiftSubs, func(w struct {
			wireUser
			wireBroadcaster
			Total           int    `json:"total"`
			Tier            string `json:"tier"`
			CumulativeTotal *int   `json:"cumulative_total"`
			IsAnonymous     bool   `json:"is_anonymous"`
		},
		) GiftEvent {
			e := GiftEvent{Channel: w.BroadcasterUserLogin, User: w.user(), Anonymous: w.IsAnonymous, Total: w.Total, Tier: w.Tier}
			if w.CumulativeTotal != nil {
				e.CumulativeTotal = *w.CumulativeTotal
			}
			return e
		}),
	},
	{
		kind: "channel.subscription.message", version: "1", scope: "channel:read:subscriptions", condition: broadcaster,
		publish: decodeAs(Resubscribe, func(w struct {
			wireUser
			wireBroadcaster
			Tier    string `json:"tier"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			CumulativeMonths int  `json:"cumulative_months"`
			StreakMonths     *int `json:"streak_months"`
			DurationMonths   int  `json:"duration_months"`
		},
		) ResubEvent {
			e := ResubEvent{
				Channel: w.BroadcasterUserLogin, User: w.user(), Tier: w.Tier, Message: w.Message.Text,
				CumulativeMonths: w.CumulativeMonths, DurationMonths: w.DurationMonths,
			}
			if w.StreakMonths != nil {
				e.StreakMonths = *w.StreakMonths
			}
			return e
		}),
	},
	{
		kind: "channel.cheer", version: "1", scope: "bits:read", condition: broadcaster,
		publish: decodeAs(Cheer, func(w struct {
			wireUser
			wireBroadcaster
			IsAnonymous bool   `json:"is_anonymous"`
			Bits        int    `json:"bits"`
			Message     string `json:"message"`
		},
		) CheerEvent {
			return CheerEvent{Channel: w.BroadcasterUserLogin, User: w.user(), Anonymous: w.IsAnonymous, Bits: w.Bits, Message: w.Message}
		}),
	},
	{
		kind: "channel.raid", version: "1",
		condition: func(id string) map[string]string { return map[string]string{"to_broadcaster_user_id": id} },
		publish: decodeAs(Raid, func(w struct {
			FromID    string `json:"from_broadcaster_user_id"`
			FromLogin string `json:"from_broadcaster_user_login"`
			FromName  string `json:"from_broadcaster_user_name"`
			ToLogin   string `json:"to_broadcaster_user_login"`
			Viewers   int    `json:"viewers"`
		},
		) RaidEvent {
			return RaidEvent{Channel: w.ToLogin, From: User{ID: w.FromID, Login: w.FromLogin, Name: w.FromName}, Viewers: w.Viewers}
		}),
	},
	{
		kind: "channel.channel_points_custom_reward_redemption.add", version: "1", scope: "channel:read:redemptions",
		condition: broadcaster,
		publish: decodeAs(Redemption, func(w struct {
			wireUser
			wireBroadcaster
			UserInput string `json:"user_input"`
			Status    string `json:"status"`
			Reward    struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Cost  int    `json:"cost"`
			} `json:"reward"`
		},
		) RedemptionEvent {
			return RedemptionEvent{
				Channel: w.BroadcasterUserLogin, User: w.user(), RewardID: w.Reward.ID, RewardTitle: w.Reward.Title,
				Cost: w.Reward.Cost, Input: w.UserInput, Status: w.Status,
			}
		}),
	},
	{
		kind: "stream.online", version: "1", condition: broadcaster,
		publish: decodeAs(StreamOnline, func(w struct {
			wireBroadcaster
			Type      string    `json:"type"`
			StartedAt time.Time `json:"started_at"`
		},
		) StreamOnlineEvent {
			return StreamOnlineEvent{Channel: w.BroadcasterUserLogin, Type: w.Type, StartedAt: w.StartedAt}
		}),
	},
	{
		kind: "stream.offline", version: "1", condition: broadcaster,
		publish: decodeAs(StreamOffline, func(w wireBroadcaster) StreamOfflineEvent {
			return StreamOfflineEvent{Channel: w.BroadcasterUserLogin}
		}),
	},
}
