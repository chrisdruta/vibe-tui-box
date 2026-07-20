-- vibe.yazi — harness-owned review machinery for `vibe review` and the
-- tmux preview window. Keybindings live in ../..​/keymap.toml: A = approve,
-- R = reject with an optional note (A/R are unbound in yazi's defaults, so
-- a/r keep their create/rename meaning — no collisions). Verdicts append
-- via the baked vibe-verdict helper; feedback is a toast plus a per-file
-- ✓/✗ badge column (the "verdict" linemode, enabled in yazi.toml).

local M = {}

-- Sync-side state: absolute path -> "approve" | "reject". Read by the
-- linemode renderer every frame, so it must be a plain table lookup; the
-- async entry point only touches it through the ya.sync updaters below.
local verdicts = {}
local loaded_dirs = {}

-- The repaint entry point moved across yazi versions (ya.render → ui.render,
-- sync context only); without one the badge still appears on the next UI event.
local function refresh()
	local fn = ya.render or ya.redraw or (ui and ui.render)
	if fn then
		fn()
	end
end

local set_verdict = ya.sync(function(_, path, verdict)
	verdicts[path] = verdict
	refresh()
end)

local hovered_and_cwd = ya.sync(function()
	local cur = cx.active.current
	local h = cur.hovered
	return { path = h and tostring(h.url) or nil, cwd = tostring(cur.cwd) }
end)

-- Seed the badge state from an existing decisions file, once per directory
-- per session. Parses only the fields vibe-verdict writes (its JSON string
-- escaping is trivial for the paths this flow sees); last line per path
-- wins — the same rule consuming agents apply. Plain sync-side function:
-- called from the cd subscription (sync context); entry goes through the
-- ya.sync wrapper below. The decisions file is small and read once per
-- directory, so blocking the main thread here is fine.
local function load_existing_sync(cwd)
	if loaded_dirs[cwd] then
		return
	end
	loaded_dirs[cwd] = true
	local target = os.getenv("VIBE_REVIEW_DECISIONS") or (cwd .. "/.review-decisions.jsonl")
	local f = io.open(target, "r")
	if not f then
		return
	end
	for line in f:lines() do
		local p = line:match('"path":"(.-)"')
		local v = line:match('"verdict":"(.-)"')
		if p and v then
			verdicts[p] = v
		end
	end
	f:close()
	refresh()
end

local load_existing = ya.sync(function(_, cwd)
	load_existing_sync(cwd)
end)

function M:setup()
	-- ✓/✗ badge column; yazi.toml selects it with `linemode = "verdict"`.
	function Linemode:verdict()
		local v = verdicts[tostring(self._file.url)]
		if v == "approve" then
			return ui.Line { ui.Span("✓ "):fg("green") }
		elseif v == "reject" then
			return ui.Line { ui.Span("✗ "):fg("red") }
		end
		return ui.Line("")
	end

	-- Badges for already-judged files appear as soon as a directory is
	-- entered (including the startup directory), not only after the first
	-- A/R press there.
	ps.sub("cd", function()
		pcall(load_existing_sync, tostring(cx.active.current.cwd))
	end)
end

function M:entry(job)
	local action = job.args[1]
	if action ~= "approve" and action ~= "reject" then
		return
	end

	local st = hovered_and_cwd()
	if not st.path then
		ya.notify { title = "vibe review", content = "No file hovered", level = "warn", timeout = 3 }
		return
	end
	load_existing(st.cwd) -- no-op when the cd subscription already ran here

	local args = { action, st.path }
	if action == "reject" then
		local note, event = ya.input {
			title = "Reject note (optional, Enter to skip):",
			pos = { "top-center", y = 3, w = 60 },
		}
		if event ~= 1 then
			ya.notify { title = "vibe review", content = "Reject cancelled", timeout = 2 }
			return
		end
		if note and note ~= "" then
			args[#args + 1] = note
		end
	end

	-- cwd matters: vibe-verdict's default target is ./.review-decisions.jsonl
	-- relative to the BROWSED directory, not yazi's own process cwd.
	local status, err = Command("vibe-verdict"):arg(args):cwd(st.cwd):status()
	local name = st.path:match("[^/]+$") or st.path
	if status and status.success then
		set_verdict(st.path, action)
		ya.notify {
			title = "vibe review",
			content = (action == "approve" and "✓ approved: " or "✗ rejected: ") .. name,
			timeout = 2,
		}
	else
		ya.notify {
			title = "vibe review",
			content = "vibe-verdict failed" .. (err and (": " .. tostring(err)) or ""),
			level = "error",
			timeout = 5,
		}
	end
end

return M
