return {
	{
		-- https://github.com/Davidyz/VectorCode/blob/main/docs/neovim.md#installation
		"Davidyz/VectorCode",
		version = "*",
		lazy = "Lazy",
		build = "uv tool upgrade vectorcode", -- This helps keeping the CLI up-to-date
		dependencies = { "nvim-lua/plenary.nvim" },
	},
	{
		"olimorris/codecompanion.nvim", -- The KING of AI programming
		-- enabled = false,
		lazy = "Lazy",
		cmd = { "CodeCompanion", "CodeCompanionChat", "CodeCompanionActions" },
		dependencies = {
			"j-hui/fidget.nvim", -- Display status
			"Davidyz/VectorCode",
			"ravitemer/codecompanion-history.nvim", -- Save and load conversation history
			{
				"ravitemer/mcphub.nvim", -- Manage MCP servers
				cmd = "MCPHub",
				build = "npm install -g mcp-hub@latest",
				config = true,
			},
			{
				"HakonHarnes/img-clip.nvim", -- Share images with the chat buffer
				event = "VeryLazy",
				cmd = "PasteImage",
				opts = {
					filetypes = {
						codecompanion = {
							prompt_for_file_name = false,
							template = "[Image]($FILE_PATH)",
							use_absolute_path = true,
						},
					},
				},
			},
		},
		opts = {
			extensions = {
				history = {
					enabled = true,
					opts = {
						keymap = "gh",
						save_chat_keymap = "sc",
						auto_save = true,
						auto_generate_title = true,
						continue_last_chat = true,
						chat_filter = function(chat_data)
							return chat_data.cwd == vim.fn.getcwd()
						end,
						delete_on_clearing_chat = false,
						expiration_days = 7,
						picker = "snacks",
						enable_logging = false,
						dir_to_save = vim.fn.stdpath("data") .. "/codecompanion-history",
					},
				},
				mcphub = {
					callback = "mcphub.extensions.codecompanion",
					opts = {
						make_vars = true,
						make_slash_commands = true,
						show_result_in_chat = true,
					},
				},
				vectorcode = {
					opts = {
						tool_group = {
							enabled = true,
							collapse = true,
						},
					},
				},
			},
			strategies = {
				chat = {
					adapter = {
						name = "copilot",
						model = "claude-sonnet-4",
					},
					roles = {
						user = "zenha",
					},
					tools = {
						opts = {
							default_tools = {
								"full_stack_dev",
							},
							wait_timeout = 3600000,
						},
						["insert_edit_into_file"] = {
							opts = {
								timeout = 3600000,
							},
						},
					},
				},
			},
			display = {
				action_palette = {
					provider = "default",
				},
				chat = {
					-- show_references = true,
					-- show_header_separator = false,
					show_settings = true,
				},
				diff = {
					enabled = true,
					layout = "horizontal",
					close_chat_at = 80,
				},
			},
			opts = {
				log_level = "DEBUG",
			},
		},
		keys = {
			{
				"<C-a>",
				"<cmd>CodeCompanionActions<CR>",
				desc = "Open the action palette",
				mode = { "n", "v" },
			},
			{
				"<Leader>aa",
				"<cmd>CodeCompanionChat Toggle<CR>",
				desc = "Toggle a chat buffer",
				mode = { "n" },
			},
			{
				"<Leader>aa",
				"<cmd>CodeCompanionChat Add<CR>",
				desc = "Add code to a chat buffer",
				mode = { "v" },
			},
		},
		init = function()
			vim.cmd([[cab cc CodeCompanion]])
		end,
	},
	-- Copilot
	{
		"zbirenbaum/copilot.lua",
		lazy = false,
		-- event = "VeryLazy",
		build = ":Copilot auth",
		opts = {
			suggestion = {
				enabled = true,
				auto_trigger = true,
				hide_during_completion = true,
				debounce = 75,
				keymap = {
					accept = "<M-Tab>",
					accept_word = false,
					accept_line = false,
					next = "<M-]>",
					prev = "<M-[>",
					dismiss = "<C-]>",
				},
			},
		},
	},

	-- Render Markdown
	{ "MeanderingProgrammer/render-markdown.nvim", lazy = false },
	-- Avante
	{
		"yetone/avante.nvim",
		enabled = false,
		lazy = false,
		build = "make",
		dependencies = {
			"nvim-treesitter/nvim-treesitter",
			"nvim-lua/plenary.nvim",
			"MunifTanjim/nui.nvim",
		},
		opts = {
			provider = "copilot",
			providers = {
				copilot = {
					endpoint = "https://api.githubcopilot.com",
					-- proxy = nil,
					-- allow_insecure = false,
					-- timeout = 10 * 60 * 1000,
					-- max_completion_tokens = 1000000,
					-- reasoning_effort = "high",
					model = "claude-sonnet-4",
				},
			},
			behaviour = {
				enable_cursor_planning_mode = true,
			},
			windows = {
				width = 40,
				ask = {
					start_insert = false,
				},
			},
			mappings = {
				diff = {
					ours = "to",
					theirs = "tt",
					all_theirs = "ta",
					both = "tb",
					cursor = "tt",
					next = "]x",
					prev = "[x",
				},
				suggestion = {
					accept = "<M-l>",
					next = "<M-]>",
					prev = "<M-[>",
					dismiss = "<C-]>",
				},
				jump = {
					next = "]]",
					prev = "[[",
				},
				submit = {
					normal = "<CR>",
					insert = "<C-s>",
				},
				sidebar = {
					apply_all = "A",
					apply_cursor = "a",
					switch_windows = "<Tab>",
					reverse_switch_windows = "<S-Tab>",
				},
			},
		},
	},
}
