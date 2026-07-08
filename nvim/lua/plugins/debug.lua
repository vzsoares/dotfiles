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

			dapui.setup()
			require("nvim-dap-virtual-text").setup()

			-- Go: wraps delve, adds debug-test helpers
			require("dap-go").setup()

			-- Build/run from the current file's directory, not nvim's cwd. dap-go
			-- defaults "Debug" to ${workspaceFolder}; that breaks for katas where
			-- each dir is its own `package main` and nvim was launched elsewhere.
			for _, cfg in ipairs(dap.configurations.go or {}) do
				if cfg.program == "${workspaceFolder}" then
					cfg.program = "${fileDirname}"
					cfg.cwd = "${fileDirname}"
					-- pause on the first line of main instead of running straight
					-- through (katas finish in microseconds without a breakpoint)
					cfg.stopOnEntry = true
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
				p = p:gsub("${fileDirname}", vim.fn.expand("%:p:h"))
				p = p:gsub("${workspaceFolder}", vim.fn.getcwd())
				-- if it points at a file, use its directory
				if vim.fn.isdirectory(p) == 0 and vim.fn.filereadable(p) == 1 then
					p = vim.fn.fnamemodify(p, ":h")
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

			-- Auto open/close the UI with the debug session
			dap.listeners.after.event_initialized["dapui_config"] = function()
				dapui.open()
			end
			dap.listeners.before.event_terminated["dapui_config"] = function()
				dapui.close()
			end
			dap.listeners.before.event_exited["dapui_config"] = function()
				dapui.close()
			end

			-- Breakpoint signs
			vim.fn.sign_define("DapBreakpoint", { text = "●", texthl = "DiagnosticError", linehl = "", numhl = "" })
			vim.fn.sign_define("DapStopped", { text = "▶", texthl = "DiagnosticWarn", linehl = "Visual", numhl = "" })

			-- Keymaps
			local map = vim.keymap.set
			map("n", "<F5>", dap.continue, { desc = "Debug: Start/Continue" })
			map("n", "<F10>", dap.step_over, { desc = "Debug: Step Over" })
			map("n", "<F11>", dap.step_into, { desc = "Debug: Step Into" })
			map("n", "<F12>", dap.step_out, { desc = "Debug: Step Out" })
			map("n", "<F6>", dap.terminate, { desc = "Debug: Terminate session" })
			map("n", "<leader>b", dap.toggle_breakpoint, { desc = "Debug: Toggle Breakpoint" })
			map("n", "<leader>B", function()
				dap.set_breakpoint(vim.fn.input("Breakpoint condition: "))
			end, { desc = "Debug: Conditional Breakpoint" })
			map("n", "<leader>dr", dap.repl.open, { desc = "Debug: Open REPL" })
			map("n", "<leader>du", dapui.toggle, { desc = "Debug: Toggle UI" })

			-- Go: debug the test nearest the cursor
			map("n", "<leader>dt", function()
				require("dap-go").debug_test()
			end, { desc = "Debug: Go test nearest cursor" })
		end,
	},
}
