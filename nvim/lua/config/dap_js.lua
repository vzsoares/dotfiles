-- DAP setup for JavaScript / TypeScript / React / Next.js.
--
-- Backed by vscode-js-debug (Mason package "js-debug-adapter"). Mason ships a
-- shim that execs `node .../js-debug/src/dapDebugServer.js "$@"`, and that
-- server takes `<port> [host]` as positional args -- hence the arg shape below.
-- One server binary serves every `pwa-*` type, so the adapters differ only in
-- name.

local M = {}

-- vue/svelte are here because their SFCs compile to the same pwa-node/pwa-chrome
-- sessions; drop them if you never touch those.
local FILETYPES = {
	"javascript",
	"javascriptreact",
	"typescript",
	"typescriptreact",
}

-- Stepping through node's own C++-backed JS or a dependency's transpiled output
-- is never what you want from a breakpoint in your own source.
local SKIP = { "<node_internals>/**", "**/node_modules/**" }

local uv = vim.uv or vim.loop

local function exists(path)
	return path and uv.fs_stat(path) ~= nil
end

-- Directory of the current buffer, or cwd for unnamed buffers (the REPL, a
-- picker) -- every upward search below starts here.
local function buf_dir()
	local name = vim.api.nvim_buf_get_name(0)
	if name == "" then
		return vim.fn.getcwd()
	end
	return vim.fn.fnamemodify(name, ":h")
end

-- The nearest enclosing package, not nvim's cwd: in a monorepo (or with nvim
-- launched from ~) the two are different, and `cwd` is what resolves relative
-- imports, .env files and next.config.js.
local function project_root()
	local marker = vim.fs.find({ "package.json", "tsconfig.json", "jsconfig.json", ".git" }, {
		path = buf_dir(),
		upward = true,
	})[1]
	return marker and vim.fn.fnamemodify(marker, ":h") or vim.fn.getcwd()
end

-- Walk up looking for node_modules/.bin/<name>. A single check under
-- project_root() misses the hoisted root bin dir of a pnpm/npm workspace.
local function local_bin(name)
	local dir = buf_dir()
	while dir and dir ~= "" do
		local candidate = dir .. "/node_modules/.bin/" .. name
		if exists(candidate) then
			return candidate
		end
		local parent = vim.fs.dirname(dir)
		if parent == dir then
			break
		end
		dir = parent
	end
	local global = vim.fn.exepath(name)
	return global ~= "" and global or nil
end

local function has_module(name)
	local dir = buf_dir()
	while dir and dir ~= "" do
		if exists(dir .. "/node_modules/" .. name) then
			return true
		end
		local parent = vim.fs.dirname(dir)
		if parent == dir then
			break
		end
		dir = parent
	end
	return false
end

-- node >= 23.6 strips TypeScript types on its own, so a bare `node foo.ts`
-- usually just works. tsx is still preferred when the project has it: it honours
-- tsconfig `paths`, which native stripping ignores.
local function ts_runtime_args()
	if has_module("tsx") then
		return { "--import", "tsx" }
	end
	if has_module("ts-node") then
		return { "--require", "ts-node/register" }
	end
	return {}
end

-- bun runs package scripts with the bun runtime, which does not speak the V8
-- inspector protocol vscode-js-debug drives -- so a bun project still gets npm
-- here. The script it launches (`next dev`) runs under node either way.
local function pkg_manager()
	local root = project_root()
	if exists(root .. "/pnpm-lock.yaml") then
		return "pnpm"
	end
	if exists(root .. "/yarn.lock") then
		return "yarn"
	end
	return "npm"
end

local function browser()
	for _, name in ipairs({
		"google-chrome-stable",
		"google-chrome",
		"chromium",
		"brave",
		"microsoft-edge-stable",
	}) do
		local path = vim.fn.exepath(name)
		if path ~= "" then
			return path
		end
	end
	return nil
end

local function input(prompt, default)
	return function()
		return vim.fn.input(prompt, default or "")
	end
end

-- vim.fn.input returns one string; node wants argv.
local function input_args(prompt)
	return function()
		return vim.split(vim.fn.input(prompt), " +", { trimempty = true })
	end
end

function M.setup(dap)
	local server = vim.fn.exepath("js-debug-adapter")
	if server == "" then
		server = vim.fn.stdpath("data") .. "/mason/bin/js-debug-adapter"
	end

	for _, name in ipairs({ "pwa-node", "pwa-chrome", "node", "chrome" }) do
		dap.adapters[name] = {
			type = "server",
			host = "127.0.0.1",
			port = "${port}",
			executable = {
				command = server,
				args = { "${port}", "127.0.0.1" },
			},
		}
	end

	local configurations = {
		{
			type = "pwa-node",
			request = "launch",
			name = "Launch file",
			program = "${file}",
			cwd = project_root,
			runtimeArgs = ts_runtime_args,
			sourceMaps = true,
			skipFiles = SKIP,
			-- program stdout goes to a real terminal buffer; the dapui repl only
			-- ever gets DAP output events, which is not where node prints
			console = "integratedTerminal",
		},
		{
			type = "pwa-node",
			request = "launch",
			name = "Launch file (with arguments)",
			program = "${file}",
			args = input_args("Arguments: "),
			cwd = project_root,
			runtimeArgs = ts_runtime_args,
			sourceMaps = true,
			skipFiles = SKIP,
			console = "integratedTerminal",
		},
		{
			type = "pwa-node",
			request = "attach",
			name = "Attach to process...",
			processId = function()
				return require("dap.utils").pick_process()
			end,
			cwd = project_root,
			sourceMaps = true,
			skipFiles = SKIP,
		},
		{
			type = "pwa-node",
			request = "attach",
			name = "Attach to port 9229",
			address = "localhost",
			port = 9229,
			cwd = project_root,
			-- reconnect when nodemon/next restarts the process instead of dying
			-- with the old one
			restart = true,
			sourceMaps = true,
			skipFiles = SKIP,
		},

		-- Next.js -------------------------------------------------------------
		-- Server components, route handlers, middleware and getServerSideProps
		-- run in node; `next dev` forks a worker per route compile, and js-debug
		-- auto-attaches to those children, so breakpoints land in the fork too.
		{
			type = "pwa-node",
			request = "launch",
			name = "Next.js: server-side (dev)",
			runtimeExecutable = pkg_manager,
			runtimeArgs = { "run", "dev" },
			cwd = project_root,
			console = "integratedTerminal",
			sourceMaps = true,
			skipFiles = SKIP,
			-- next spawns its compiler/router workers as children; without this
			-- only the CLI wrapper process is debugged and nothing ever hits
			autoAttachChildProcesses = true,
		},
		{
			type = "pwa-node",
			request = "attach",
			name = "Next.js: attach to running dev server (9229)",
			address = "localhost",
			port = 9229,
			cwd = project_root,
			restart = true,
			sourceMaps = true,
			skipFiles = SKIP,
		},
		-- Client components / hooks / event handlers run in the browser, so they
		-- need a Chrome session -- the node configs above will never stop there.
		{
			type = "pwa-chrome",
			request = "launch",
			name = "Next.js: client-side (launch Chrome)",
			url = input("Dev server URL: ", "http://localhost:3000"),
			webRoot = project_root,
			runtimeExecutable = browser,
			-- a fresh profile: Chrome refuses to open a debug port against a
			-- profile an already-running instance holds
			userDataDir = vim.fn.stdpath("cache") .. "/dap-chrome",
			sourceMaps = true,
			sourceMapPathOverrides = {
				["webpack://_N_E/./*"] = "${webRoot}/*",
				["webpack://?:*/*"] = "${webRoot}/*",
				["turbopack://[project]/*"] = "${webRoot}/*",
			},
		},
		{
			type = "pwa-chrome",
			request = "attach",
			name = "Chrome: attach (port 9222)",
			port = 9222,
			webRoot = project_root,
			sourceMaps = true,
		},

		-- Tests ---------------------------------------------------------------
		{
			type = "pwa-node",
			request = "launch",
			name = "Vitest: current file",
			program = function()
				return local_bin("vitest")
			end,
			-- `run` disables watch mode; a watching runner never exits and the
			-- session hangs after the first pass
			args = { "run", "${file}" },
			cwd = project_root,
			console = "integratedTerminal",
			sourceMaps = true,
			skipFiles = SKIP,
			autoAttachChildProcesses = true,
		},
		{
			type = "pwa-node",
			request = "launch",
			name = "Jest: current file",
			program = function()
				return local_bin("jest")
			end,
			-- jest forks workers per test file and breakpoints do not survive the
			-- fork boundary reliably; --runInBand keeps everything in-process
			args = { "--runInBand", "--no-coverage", "${file}" },
			cwd = project_root,
			console = "integratedTerminal",
			sourceMaps = true,
			skipFiles = SKIP,
		},

		{
			type = "pwa-node",
			request = "launch",
			name = "Run package script...",
			runtimeExecutable = pkg_manager,
			runtimeArgs = function()
				return { "run", vim.fn.input("Script: ", "dev") }
			end,
			cwd = project_root,
			console = "integratedTerminal",
			sourceMaps = true,
			skipFiles = SKIP,
			autoAttachChildProcesses = true,
		},
	}

	for _, ft in ipairs(FILETYPES) do
		dap.configurations[ft] = configurations
	end
end

-- Which config `<leader>dt` should run for a JS/TS buffer.
function M.test_config()
	if has_module("vitest") then
		return "Vitest: current file"
	end
	if has_module("jest") then
		return "Jest: current file"
	end
	return nil
end

M.filetypes = FILETYPES

return M
