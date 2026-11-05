---@meta lukky
-- Type definitions for Lukky Lua modules, for lua-language-server.
-- Point `workspace.library` at this directory (see .luarc.json).

---@class lukky.Target
---@field platform string
---@field channel string

---@alias lukky.Role "subscriber"|"vip"|"moderator"|"broadcaster"

---@class lukky.User
---@field id string
---@field name string
---@field display_name string
---@field roles lukky.Role[]

---@class lukky.Message
---@field id string
---@field source lukky.Target
---@field user lukky.User
---@field text string

---@class lukky.CommandOptions
---@field role? lukky.Role Ignore senders ranked below this role.

---@alias lukky.CommandHandler fun(msg: lukky.Message, args: string[])

---@class lukky.Store
---@field get fun(key: string): string?
---@field put fun(key: string, value: string)
---@field delete fun(key: string): boolean True if the key existed.
---@field list fun(): table<string, string>

---@class lukky.Log
---@field debug fun(msg: string, fields?: table)
---@field info fun(msg: string, fields?: table)
---@field warn fun(msg: string, fields?: table)
---@field error fun(msg: string, fields?: table)

---@class lukky.Overlay
---@field push fun(event: string, data: any) Send a one-time event, such as an alert, to the module's overlay page.
---@field set fun(key: string, data: any) Store and send a value; pages that connect later receive it first.

---@class lukky.Json
---@field encode fun(value: any): string
---@field decode fun(text: string): any

--- Host services for one module. Every function raises an error when the host call fails.
---@class lukky.Host
---@field command fun(name: string, handler: lukky.CommandHandler, opts?: lukky.CommandOptions) Run handler when the first word is `name`.
---@field exact fun(text: string, handler: lukky.CommandHandler, opts?: lukky.CommandOptions) Run handler when the message equals `text`.
---@field regex fun(pattern: string, handler: lukky.CommandHandler, opts?: lukky.CommandOptions) Go RE2 pattern; capture groups become args.
---@field on fun(topic: string, handler: fun(payload: any)) Subscribe to an event topic.
---@field emit fun(topic: string, payload: any) Publish an event.
---@field every fun(seconds: number, fn: fun()) Run fn repeatedly.
---@field after fun(seconds: number, fn: fun()) Run fn once.
---@field own fun(fn: fun()) Run fn when the module unloads.
---@field send fun(to: lukky.Target, text: string)
---@field reply fun(msg: lukky.Message, text: string) Send to the channel msg came from.
---@field store lukky.Store Storage private to this module.
---@field overlay lukky.Overlay Live data for the page in the module's overlay/ directory.
---@field log lukky.Log
---@field json lukky.Json

---@class lukky.Module
---@field name string
---@field requires? string[] Modules that must be loaded first.
---@field start fun(host: lukky.Host)
---@field stop? fun()

-- Twitch events, received with host.on("<topic>", fn). They need the
-- broadcaster token (see README). Users are empty for anonymous gifts and cheers.

---@class lukky.twitch.User
---@field id string
---@field login string
---@field name string

---@class lukky.twitch.Follow             # "twitch.follow"
---@field channel string
---@field user lukky.twitch.User
---@field followed_at string RFC 3339 time

---@class lukky.twitch.Subscribe          # "twitch.subscribe"
---@field channel string
---@field user lukky.twitch.User
---@field tier "1000"|"2000"|"3000"
---@field is_gift boolean

---@class lukky.twitch.Gift               # "twitch.gift"
---@field channel string
---@field user lukky.twitch.User
---@field anonymous boolean
---@field total integer
---@field tier string
---@field cumulative_total integer

---@class lukky.twitch.Resub              # "twitch.resub"
---@field channel string
---@field user lukky.twitch.User
---@field tier string
---@field message string
---@field cumulative_months integer
---@field streak_months integer
---@field duration_months integer

---@class lukky.twitch.Cheer              # "twitch.cheer"
---@field channel string
---@field user lukky.twitch.User
---@field anonymous boolean
---@field bits integer
---@field message string

---@class lukky.twitch.Raid               # "twitch.raid"
---@field channel string
---@field from lukky.twitch.User
---@field viewers integer

---@class lukky.twitch.Redemption         # "twitch.redemption"
---@field channel string
---@field user lukky.twitch.User
---@field reward_id string
---@field reward_title string
---@field cost integer
---@field input string
---@field status string

---@class lukky.twitch.StreamOnline       # "twitch.stream_online"
---@field channel string
---@field type string
---@field started_at string RFC 3339 time

---@class lukky.twitch.StreamOffline      # "twitch.stream_offline"
---@field channel string
