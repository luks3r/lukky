-- Alerts for Twitch events, shown on an OBS overlay.
-- Copy this directory into scripts/, then add http://127.0.0.1:7878/overlays/alerts/
-- as a Browser Source (1920x1080). Moderators can test with !testalert <kind>.

local tiers = { ["1000"] = "Tier 1", ["2000"] = "Tier 2", ["3000"] = "Tier 3" }

local function who(user, anonymous)
	if anonymous or not user or user.name == "" then
		return "Anonymous"
	end
	return user.name
end

-- Each handler turns an event into the text the overlay shows.
local alerts = {
	["twitch.follow"] = function(e)
		return { kind = "follow", title = e.user.name, text = "just followed!" }
	end,
	["twitch.subscribe"] = function(e)
		if e.is_gift then
			return nil -- the gift alert covers it
		end
		return { kind = "sub", title = e.user.name, text = "subscribed with " .. (tiers[e.tier] or e.tier) .. "!" }
	end,
	["twitch.resub"] = function(e)
		return {
			kind = "sub",
			title = e.user.name,
			text = "resubscribed for " .. e.cumulative_months .. " months!",
			message = e.message,
		}
	end,
	["twitch.gift"] = function(e)
		return { kind = "gift", title = who(e.user, e.anonymous), text = "gifted " .. e.total .. " subs!" }
	end,
	["twitch.cheer"] = function(e)
		return { kind = "cheer", title = who(e.user, e.anonymous), text = "cheered " .. e.bits .. " bits!", message = e.message }
	end,
	["twitch.raid"] = function(e)
		return { kind = "raid", title = e.from.name, text = "is raiding with " .. e.viewers .. " viewers!" }
	end,
	["twitch.redemption"] = function(e)
		return { kind = "points", title = e.user.name, text = "redeemed " .. e.reward_title, message = e.input }
	end,
}

local samples = {
	follow = { "twitch.follow", { user = { name = "TestFollower" } } },
	sub = { "twitch.subscribe", { user = { name = "TestSub" }, tier = "1000", is_gift = false } },
	resub = { "twitch.resub", { user = { name = "TestResub" }, cumulative_months = 12, message = "Love the stream!" } },
	gift = { "twitch.gift", { user = { name = "TestGifter" }, total = 5, anonymous = false } },
	cheer = { "twitch.cheer", { user = { name = "TestCheer" }, bits = 500, message = "Cheer500 hype!", anonymous = false } },
	raid = { "twitch.raid", { from = { name = "TestRaider" }, viewers = 42 } },
	points = { "twitch.redemption", { user = { name = "TestViewer" }, reward_title = "Hydrate", input = "" } },
}

---@type lukky.Module
return {
	name = "alerts",

	start = function(host)
		local count = tonumber(host.store.get("count") or "0")
		host.overlay.set("count", count)

		local function show(alert)
			if not alert then
				return
			end
			host.overlay.push("alert", alert)
			count = count + 1
			host.store.put("count", tostring(count))
			host.overlay.set("count", count)
			host.log.info("alert", { kind = alert.kind, title = alert.title })
		end

		for topic, format in pairs(alerts) do
			host.on(topic, function(e)
				show(format(e))
			end)
		end

		host.command("!testalert", function(msg, args)
			local sample = samples[args[1] or "follow"]
			if not sample then
				host.reply(msg, "Usage: !testalert follow|sub|resub|gift|cheer|raid|points")
				return
			end
			show(alerts[sample[1]](sample[2]))
		end, { role = "moderator" })
	end,
}
