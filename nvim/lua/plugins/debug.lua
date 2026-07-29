return {
	{
		"mfussenegger/nvim-dap",
		dependencies = {
			-- UI
			"rcarriga/nvim-dap-ui",
			"nvim-neotest/nvim-nio",
			-- inline variable values
			"theHamsta/nvim-dap-virtual-text",
			-- installs the delve adapter via Mason
			"mason-org/mason.nvim",
			"jay-babu/mason-nvim-dap.nvim",
			-- Go / delve
			"leoluz/nvim-dap-go",
		},
		config = function()
			local dap = require("dap")
			local dapui = require("dapui")

			-- Ensure the delve adapter is installed via Mason
			require("mason-nvim-dap").setup({
				ensure_installed = { "delve" },
				automatic_installation = true,
				handlers = {},
			})

			-- the play/step/terminate toolbar defaults to the repl element, which is
			-- buried in the bottom tray; put it on the always-visible sidebar pane
			dapui.setup({
				controls = { enabled = true, element = "scopes" },
				layouts = {
					{
						position = "left",
						-- fraction of the screen rather than a fixed 40 columns, so
						-- values have room without eating the source on a small term
						size = 0.25,
						elements = {
							{ id = "repl", size = 0.2 },
							{ id = "watches", size = 0.3 },
							{ id = "stacks", size = 0.3 },
							{ id = "breakpoints", size = 0.2 },
						},
					},
					{
						position = "bottom",
						size = 10,
						-- no "console": it only ever shows nvim-dap's integrated
						-- terminal, and delve rejects any outputMode but "remote"
						-- (verified: 'unsupported outputMode attribute "terminal"'),
						-- so for Go it is permanently empty. Program stdout arrives
						-- as DAP output events, which land in the repl instead.
						elements = { "scopes" },
					},
				},
				render = {
					indent = 1,
					max_type_length = nil, -- never abbreviate the type
					max_value_lines = 200, -- long slices/maps stay readable
				},
			})

			-- wrap long values instead of running them off the edge of the pane
			vim.api.nvim_create_autocmd("FileType", {
				group = vim.api.nvim_create_augroup("DapUiWrap", { clear = true }),
				pattern = { "dapui_scopes", "dapui_watches", "dapui_hover" },
				callback = function()
					vim.opt_local.wrap = true
				end,
			})
			require("nvim-dap-virtual-text").setup()

			-- Go: wraps delve, adds debug-test helpers
			require("dap-go").setup()

			-- Trim dap-go's seven configs down to the ones that still differ once
			-- `program` is pinned below: "Debug", "Debug (Arguments & Build Flags)"
			-- and the file-scoped "Debug test" collapse into duplicates. Listed in
			-- the order they should appear in the F5 picker.
			local keep = { "Debug Package", "Debug (Arguments)", "Debug test (go.mod)", "Attach" }
			local by_name = {}
			for _, cfg in ipairs(dap.configurations.go or {}) do
				by_name[cfg.name] = cfg
			end
			local configs = {}
			for _, name in ipairs(keep) do
				if by_name[name] then
					table.insert(configs, by_name[name])
				end
			end
			-- if dap-go ever renames its configs, keep whatever it shipped
			if #configs > 0 then
				dap.configurations.go = configs
			end

			-- Build/run from the current file's directory, not nvim's cwd. dap-go
			-- points its launch configs at ${file} / ./${relativeFileDirname}; both
			-- break for katas where each dir is its own `package main` and nvim was
			-- launched elsewhere. Debug the whole package, from the file's own dir.
			-- NB: do NOT set stopOnEntry with delve. It halts at the binary's entry
			-- point, before the Go runtime has created goroutine 1, so delve answers
			-- stackTrace with "Unable to produce stack trace" and every subsequent
			-- step errors with "unknown goroutine 1". Stopping at a real breakpoint
			-- in main is handled by ensure_breakpoint() below instead.
			for _, cfg in ipairs(dap.configurations.go) do
				if cfg.request == "launch" then
					cfg.program = "${fileDirname}"
					cfg.cwd = "${fileDirname}"
				end
			end

			-- delve runs `go build` from its OWN process cwd, and Go resolves the
			-- module from that cwd — not from the package path argument. The default
			-- adapter spawns `dlv dap` in nvim's cwd, so the build fails to find
			-- go.mod when nvim was launched outside the module. Wrap the adapter in a
			-- function that spawns delve with cwd set to the debug target's directory.
			local dlv = vim.fn.exepath("dlv")
			if dlv == "" then
				dlv = vim.fn.stdpath("data") .. "/mason/bin/dlv"
			end
			local function resolve(p)
				if not p then
					return nil
				end
				-- ${fileDirname} before ${file} so the longer name wins
				p = p:gsub("${fileDirname}", vim.fn.expand("%:p:h"))
				p = p:gsub("${file}", vim.fn.expand("%:p"))
				p = p:gsub("${workspaceFolder}", vim.fn.getcwd())
				-- if it points at a file, use its directory
				if vim.fn.filereadable(p) == 1 then
					return vim.fn.fnamemodify(p, ":h")
				end
				-- unexpanded placeholder or bogus path: let the caller fall back
				-- rather than spawning delve with a cwd that doesn't exist
				if vim.fn.isdirectory(p) == 0 then
					return nil
				end
				return p
			end
			dap.adapters.delve = function(callback, config)
				local dir = resolve(config.cwd) or resolve(config.program) or vim.fn.expand("%:p:h")
				callback({
					type = "server",
					port = "${port}",
					executable = {
						command = dlv,
						args = { "dap", "-l", "127.0.0.1:${port}" },
						-- server adapters read `executable.cwd` directly (nvim-dap
						-- spawn_server_executable), not `executable.options.cwd`
						cwd = dir,
					},
				})
			end

			-- nvim-tree is a 100-column sidebar; dapui adds its own sidebar plus a
			-- bottom tray. Both open at once leaves no room for the source, and
			-- whichever closes second leaves the survivors at odd widths. Give the
			-- debug UI the screen, and re-even the splits when it goes away.
			local function close_file_tree()
				local ok, api = pcall(require, "nvim-tree.api")
				if ok and api.tree.is_visible() then
					api.tree.close()
				end
			end
			local function balance_windows()
				vim.schedule(function()
					vim.cmd("wincmd =")
				end)
			end
			local ui_open = false

			-- nvim-tree opening/closing/resizing perturbs every other window, and
			-- dapui has no automatic recovery. `reset = true` re-applies the
			-- configured layout sizes (dapui/init.lua: "Reset windows to original
			-- size"), so the debug panes snap back instead of drifting bigger.
			-- ask the windows themselves rather than trusting a flag: the UI can be
			-- opened by dapui directly, not only through our own toggle
			local function ui_is_open()
				for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
					local ft = vim.bo[vim.api.nvim_win_get_buf(win)].filetype
					if ft:match("^dapui_") or ft == "dap-repl" then
						return true
					end
				end
				return false
			end
			local function reset_ui_layout()
				if not ui_is_open() then
					return
				end
				vim.schedule(function()
					pcall(dapui.open, { reset = true })
				end)
			end
			-- requiring the api loads nvim-tree through lazy if it isn't up yet; fall
			-- back to VeryLazy only if that fails, rather than depending on it
			local function subscribe_tree_events()
				local ok, nvt = pcall(require, "nvim-tree.api")
				if not ok then
					return false
				end
				for _, ev in ipairs({
					nvt.events.Event.TreeOpen,
					nvt.events.Event.TreeClose,
					nvt.events.Event.Resize,
				}) do
					nvt.events.subscribe(ev, reset_ui_layout)
				end
				return true
			end
			if not subscribe_tree_events() then
				vim.api.nvim_create_autocmd("User", {
					pattern = "VeryLazy",
					once = true,
					callback = subscribe_tree_events,
				})
			end

			-- Open the UI with the session, but leave it up when the program ends so
			-- the final state is still readable. <leader>du dismisses it.
			dap.listeners.after.event_initialized["dapui_config"] = function()
				close_file_tree()
				dapui.open()
				ui_open = true
			end
			dap.listeners.before.event_terminated["dapui_config"] = function()
				vim.notify("Debug: program exited", vim.log.levels.INFO)
			end

			-- On a panic delve parks at runtime.fatalpanic in panic.go and carries the
			-- message in the stopped event -- nothing in the UI shows it, so you land
			-- in unfamiliar runtime source with no idea what blew up. Surface it.
			dap.listeners.after.event_stopped["panic_message"] = function(s, body)
				if body.reason ~= "exception" then
					return
				end
				local msg = body.text or body.description
				if msg then
					vim.notify("Debug panic: " .. msg, vim.log.levels.ERROR)
				elseif s.capabilities.supportsExceptionInfoRequest then
					s:request("exceptionInfo", { threadId = body.threadId }, function(_, resp)
						vim.notify("Debug panic: " .. ((resp and resp.description) or "?"), vim.log.levels.ERROR)
					end)
				end
			end

			-- Stepping past the end of main lands in Go's runtime (runtime.main in
			-- proc.go, then the exit path) -- unreadable frames that look like a
			-- crash. Nothing above main is yours, so end the session there instead.
			-- current_frame is only populated once the stackTrace response lands,
			-- which is what this hooks (see dap/session.lua get_top_frame).
			--
			-- Match these two names EXACTLY. A broad "^runtime%." also catches
			-- runtime.gopanic/sigpanic/fatalpanic, which is where delve parks you on
			-- a panic -- terminating there throws away the stack and the panic
			-- message, which is the one moment you most need them.
			local past_main = { ["runtime.main"] = true, ["runtime.goexit"] = true }
			local leaving_main = false
			dap.listeners.after.event_initialized["stop_at_runtime"] = function()
				leaving_main = false
			end
			dap.listeners.after.stackTrace["stop_at_runtime"] = function(s)
				if leaving_main then
					return -- several stackTrace responses can be in flight; act once
				end
				local frame = s.current_frame
				if frame and past_main[frame.name] then
					leaving_main = true
					vim.notify("Debug: returned from main, ending session", vim.log.levels.INFO)
					dap.terminate()
				end
			end

			-- Breakpoint signs
			vim.fn.sign_define("DapBreakpoint", { text = "●", texthl = "DiagnosticError", linehl = "", numhl = "" })
			vim.fn.sign_define("DapStopped", { text = "▶", texthl = "DiagnosticWarn", linehl = "Visual", numhl = "" })

			-- ${fileDirname} is re-expanded against whichever buffer is focused at
			-- launch. Stepping into a runtime frame focuses a file under GOROOT, so
			-- the next launch would try to debug $GOROOT/src/runtime ("not an
			-- executable file"). Remember the last buffer that was actually your own
			-- Go source and launch from there instead.
			local goroot
			local function under_goroot(path)
				if goroot == nil then
					local ok, res = pcall(vim.fn.system, { "go", "env", "GOROOT" })
					goroot = (ok and vim.trim(res)) or ""
				end
				return goroot ~= "" and vim.startswith(path, goroot)
			end
			local last_go_dir
			local function go_source_buf()
				local path = vim.api.nvim_buf_get_name(0)
				if path:match("%.go$") and not under_goroot(path) then
					last_go_dir = vim.fn.fnamemodify(path, ":h")
					return vim.api.nvim_get_current_buf(), last_go_dir
				end
				return nil, last_go_dir
			end

			-- nvim-dap holds breakpoints as buffer signs only, so they die with the
			-- session. Persist them per project (keyed by cwd) under stdpath("state")
			-- and restore each file's set when its buffer is read.
			local bps = require("dap.breakpoints")
			local bp_dir = vim.fn.stdpath("state") .. "/dap-breakpoints"
			local function bp_store()
				return bp_dir .. "/" .. vim.fn.getcwd():gsub("[^%w]", "_") .. ".json"
			end

			local function read_store()
				local fd = io.open(bp_store(), "r")
				if not fd then
					return {}
				end
				local raw = fd:read("*a")
				fd:close()
				local ok, decoded = pcall(vim.json.decode, raw)
				return (ok and type(decoded) == "table") and decoded or {}
			end

			local function save_breakpoints()
				local store = read_store()
				-- drop entries for every buffer currently loaded, then re-add from
				-- live state; untouched files keep whatever was already on disk
				for _, bufnr in ipairs(vim.api.nvim_list_bufs()) do
					if vim.api.nvim_buf_is_loaded(bufnr) then
						local path = vim.api.nvim_buf_get_name(bufnr)
						if path ~= "" then
							store[path] = nil
						end
					end
				end
				for bufnr, buf_bps in pairs(bps.get()) do
					local path = vim.api.nvim_buf_is_valid(bufnr) and vim.api.nvim_buf_get_name(bufnr) or ""
					if path ~= "" and #buf_bps > 0 then
						local list = {}
						for _, bp in ipairs(buf_bps) do
							table.insert(list, {
								line = bp.line,
								condition = bp.condition,
								hitCondition = bp.hitCondition,
								logMessage = bp.logMessage,
							})
						end
						store[path] = list
					end
				end
				vim.fn.mkdir(bp_dir, "p")
				local fd = io.open(bp_store(), "w")
				if fd then
					fd:write(vim.json.encode(store))
					fd:close()
				end
			end

			local restored = {}
			local function restore_breakpoints(bufnr)
				local path = vim.api.nvim_buf_get_name(bufnr)
				if path == "" or restored[path] then
					return
				end
				local list = read_store()[path]
				if not list then
					return
				end
				restored[path] = true
				for _, bp in ipairs(list) do
					bps.set({
						condition = bp.condition,
						hit_condition = bp.hitCondition,
						log_message = bp.logMessage,
					}, bufnr, bp.line)
				end
			end

			local bp_group = vim.api.nvim_create_augroup("DapBreakpointPersist", { clear = true })
			vim.api.nvim_create_autocmd("BufReadPost", {
				group = bp_group,
				callback = function(args)
					restore_breakpoints(args.buf)
				end,
			})
			vim.api.nvim_create_autocmd("VimLeavePre", {
				group = bp_group,
				callback = save_breakpoints,
			})
			-- buffers already read before this config ran get no BufReadPost
			for _, bufnr in ipairs(vim.api.nvim_list_bufs()) do
				if vim.api.nvim_buf_is_loaded(bufnr) then
					restore_breakpoints(bufnr)
				end
			end

			-- A kata `main` finishes in microseconds, so launching with no breakpoint
			-- just runs to completion and tears the UI straight back down. If nothing
			-- is set anywhere, park one on the first statement of the current file's
			-- `func main()`. Any breakpoint of your own disables this entirely.
			local function ensure_breakpoint()
				for _, bps in pairs(require("dap.breakpoints").get()) do
					if #bps > 0 then
						return
					end
				end
				local bufnr = go_source_buf()
				if not bufnr then
					return
				end
				local lines = vim.api.nvim_buf_get_lines(bufnr, 0, -1, false)
				for i, line in ipairs(lines) do
					if line:match("^func main%s*%(%s*%)") then
						for j = i + 1, #lines do
							local text = vim.trim(lines[j])
							if text ~= "" and not text:match("^//") then
								require("dap.breakpoints").set({}, bufnr, j)
								vim.notify("Debug: breakpoint set at main() line " .. j, vim.log.levels.INFO)
								return
							end
						end
					end
				end
			end

			-- Keymaps
			local map = vim.keymap.set
			-- every path that dismisses the UI has to re-even the splits, otherwise
			-- the windows dapui was sharing space with keep their squeezed sizes
			local function toggle_ui()
				dapui.toggle()
				-- only re-even the splits when the UI went AWAY. Doing it on open
				-- too makes `wincmd =` inflate dapui's panes to full equal shares,
				-- which is exactly the "debugger is giant" problem.
				ui_open = not ui_open
				if not ui_open then
					balance_windows()
				end
			end
			-- F5 is overloaded in nvim-dap: with no session it picks a config, when
			-- parked on a stopped thread it continues, and in every other state it
			-- pops an 8-entry menu with Terminate/Disconnect at the top. Collapse
			-- that third case to a no-op so the key can only start or continue.
			map("n", "<F5>", function()
				local s = dap.session()
				if s and not s.stopped_thread_id then
					local what = s.initialized and "running (not paused)" or "still initializing"
					vim.notify("Debug: " .. what, vim.log.levels.INFO)
					return
				end
				if not s then
					local _, dir = go_source_buf()
					if dir then
						for _, cfg in ipairs(dap.configurations.go) do
							if cfg.request == "launch" then
								cfg.program = dir
								cfg.cwd = dir
							end
						end
					end
					ensure_breakpoint()
				end
				dap.continue()
			end, { desc = "Debug: Start/Continue" })
			map("n", "<F10>", dap.step_over, { desc = "Debug: Step Over" })
			map("n", "<F11>", dap.step_into, { desc = "Debug: Step Into" })
			map("n", "<F12>", dap.step_out, { desc = "Debug: Step Out" })
			-- session menu: the destructive actions the F5 menu used to offer, behind
			-- their own key so they can't be hit by reflex while continuing
			map("n", "<F6>", function()
				local actions = {}
				-- terminate/restart only make sense with something to act on; the
				-- UI and REPL panes are useful either way
				if dap.session() then
					table.insert(actions, { label = "Terminate session", action = dap.terminate })
					table.insert(actions, { label = "Restart session", action = dap.restart })
				end
				table.insert(actions, { label = "Toggle UI", action = toggle_ui })
				table.insert(actions, { label = "Open REPL", action = dap.repl.open })
				vim.ui.select(actions, {
					prompt = "Debug session> ",
					format_item = function(item)
						return item.label
					end,
				}, function(choice)
					if choice then
						choice.action()
					end
				end)
			end, { desc = "Debug: Session menu" })
			-- direct shortcut, skips the menu
			map("n", "<leader>dR", dap.restart, { desc = "Debug: Restart session" })
			-- persist on every change, so a crash or :qa! can't lose them
			map("n", "<leader>b", function()
				dap.toggle_breakpoint()
				save_breakpoints()
			end, { desc = "Debug: Toggle Breakpoint" })
			map("n", "<leader>B", function()
				dap.set_breakpoint(vim.fn.input("Breakpoint condition: "))
				save_breakpoints()
			end, { desc = "Debug: Conditional Breakpoint" })
			map("n", "<leader>dr", dap.repl.open, { desc = "Debug: Open REPL" })
			map("n", "<leader>du", toggle_ui, { desc = "Debug: Toggle UI" })

			-- inspect the value under the cursor (or the visual selection) in a
			-- scrollable float -- the way to read values too big for the pane
			map({ "n", "v" }, "<leader>de", function()
				dapui.eval(nil, { enter = true })
			end, { desc = "Debug: Eval under cursor" })

			-- Go: debug the test nearest the cursor
			map("n", "<leader>dt", function()
				require("dap-go").debug_test()
			end, { desc = "Debug: Go test nearest cursor" })
		end,
	},
}
