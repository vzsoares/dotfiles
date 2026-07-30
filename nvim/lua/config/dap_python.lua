-- DAP setup for Python, including uv-managed projects.
--
-- Two different interpreters are in play and conflating them is the usual
-- source of "ModuleNotFoundError: debugpy":
--   * the ADAPTER runs from Mason's own debugpy venv, always;
--   * the DEBUGGEE runs under the project's interpreter, which needs no debugpy
--     of its own -- the adapter injects its launcher onto that process's path.
-- So the project venv never has to install debugpy.

local M = {}

local uv = vim.uv or vim.loop

local function exists(path)
	return path and uv.fs_stat(path) ~= nil
end

local function buf_dir()
	local name = vim.api.nvim_buf_get_name(0)
	if name == "" then
		return vim.fn.getcwd()
	end
	return vim.fn.fnamemodify(name, ":h")
end

-- Nearest enclosing project, not nvim's cwd -- `cwd` is what makes `import
-- mypkg` resolve and what relative data paths are read against.
local function project_root()
	local marker = vim.fs.find({
		"pyproject.toml",
		"uv.lock",
		"setup.py",
		"setup.cfg",
		"requirements.txt",
		".venv",
		".git",
	}, { path = buf_dir(), upward = true })[1]
	return marker and vim.fn.fnamemodify(marker, ":h") or vim.fn.getcwd()
end

-- `uv run` can take seconds (it may resolve and sync the environment), and the
-- interpreter path does not change between launches, so ask once per root.
local uv_cache = {}

local function uv_python(root)
	if uv_cache[root] ~= nil then
		return uv_cache[root] or nil
	end
	if vim.fn.executable("uv") == 0 or not exists(root .. "/pyproject.toml") then
		uv_cache[root] = false
		return nil
	end
	vim.notify("Debug: resolving uv environment...", vim.log.levels.INFO)
	local out = vim.fn.system({
		"uv",
		"run",
		"--project",
		root,
		"python",
		"-c",
		"import sys; print(sys.executable)",
	})
	local path = vim.trim(out or "")
	if vim.v.shell_error ~= 0 or not exists(path) then
		vim.notify("Debug: `uv run` could not resolve an interpreter\n" .. path, vim.log.levels.WARN)
		uv_cache[root] = false
		return nil
	end
	uv_cache[root] = path
	return path
end

-- Preference order: an activated venv beats everything (it is what the shell
-- that launched nvim was using), then the project's own venv dir, then uv's
-- managed environment, then whatever python is on PATH.
local function python_path()
	if vim.env.VIRTUAL_ENV and exists(vim.env.VIRTUAL_ENV .. "/bin/python") then
		return vim.env.VIRTUAL_ENV .. "/bin/python"
	end
	local root = project_root()
	for _, dir in ipairs({ ".venv", "venv", "env" }) do
		local candidate = root .. "/" .. dir .. "/bin/python"
		if exists(candidate) then
			return candidate
		end
	end
	local from_uv = uv_python(root)
	if from_uv then
		return from_uv
	end
	local system = vim.fn.exepath("python3")
	return system ~= "" and system or "python"
end

local function input_args(prompt)
	return function()
		return vim.split(vim.fn.input(prompt), " +", { trimempty = true })
	end
end

function M.setup(dap)
	local mason = vim.fn.stdpath("data") .. "/mason"
	local adapter_python = mason .. "/packages/debugpy/venv/bin/python"
	if not exists(adapter_python) then
		adapter_python = vim.fn.exepath("python3")
	end

	-- "executable" and not "server": debugpy's adapter speaks DAP on stdio.
	dap.adapters.debugpy = {
		type = "executable",
		command = adapter_python,
		args = { "-m", "debugpy.adapter" },
		options = { detach = false },
	}
	-- debugpy's own launch.json snippets still say type "python"
	dap.adapters.python = dap.adapters.debugpy

	dap.configurations.python = {
		{
			type = "debugpy",
			request = "launch",
			name = "Launch file",
			program = "${file}",
			cwd = project_root,
			pythonPath = python_path,
			-- default true skips every frame outside your own source, which also
			-- hides the frame that actually raised inside a library
			justMyCode = false,
			console = "integratedTerminal",
		},
		{
			type = "debugpy",
			request = "launch",
			name = "Launch file (with arguments)",
			program = "${file}",
			args = input_args("Arguments: "),
			cwd = project_root,
			pythonPath = python_path,
			justMyCode = false,
			console = "integratedTerminal",
		},
		{
			type = "debugpy",
			request = "launch",
			name = "Launch module...",
			module = function()
				return vim.fn.input("Module: ")
			end,
			args = input_args("Arguments: "),
			cwd = project_root,
			pythonPath = python_path,
			justMyCode = false,
			console = "integratedTerminal",
		},
		{
			type = "debugpy",
			request = "launch",
			name = "pytest: current file",
			module = "pytest",
			-- -s keeps stdout unswallowed; without it print/logging vanish into
			-- pytest's capture and the terminal looks dead while you step
			args = { "${file}", "-s", "-q" },
			cwd = project_root,
			pythonPath = python_path,
			justMyCode = false,
			console = "integratedTerminal",
		},
		{
			type = "debugpy",
			request = "launch",
			name = "pytest: whole suite",
			module = "pytest",
			args = { "-s", "-q" },
			cwd = project_root,
			pythonPath = python_path,
			justMyCode = false,
			console = "integratedTerminal",
		},
		{
			type = "debugpy",
			request = "launch",
			name = "uvicorn: run app...",
			module = "uvicorn",
			-- deliberately no --reload: the reloader re-execs the server in a
			-- child process the adapter is not attached to, so breakpoints stop
			-- firing after the first edit
			args = function()
				return { vim.fn.input("App (module:attr): ", "main:app"), "--port", "8000" }
			end,
			cwd = project_root,
			pythonPath = python_path,
			justMyCode = false,
			console = "integratedTerminal",
		},
		{
			type = "debugpy",
			request = "attach",
			name = "Attach to port 5678",
			-- pair with: python -m debugpy --listen 5678 --wait-for-client app.py
			connect = { host = "127.0.0.1", port = 5678 },
			justMyCode = false,
			pathMappings = {
				{
					localRoot = project_root,
					remoteRoot = ".",
				},
			},
		},
	}
end

M.filetypes = { "python" }
M.test_config = "pytest: current file"
M.python_path = python_path

return M
