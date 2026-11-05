-- Copy this directory into scripts/ to load it.

---@type lukky.Module
return {
	name = "shoutout",

	start = function(host)
		host.command("!so", function(msg, args)
			local who = args[1]
			if not who then
				host.reply(msg, "Usage: !so <channel>")
				return
			end
			who = who:gsub("^@", "")
			local count = tonumber(host.store.get(who) or "0") + 1
			host.store.put(who, tostring(count))
			host.reply(msg, "Go follow " .. who .. " at https://twitch.tv/" .. who .. " (shoutout #" .. count .. ")")
		end, { role = "moderator" })
	end,
}
