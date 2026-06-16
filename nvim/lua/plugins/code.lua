return {
	{
		"mason-org/mason.nvim",
		-- event = "VeryLazy",
		opts = {
			ui = {
				border = "rounded",
				icons = {
					package_installed = "✓",
					package_pending = "➜",
					package_uninstalled = "✗",
				},
			},
		},
	},
	{
		"mason-org/mason-lspconfig.nvim",
		-- event = { "BufReadPre", "BufNewFile" },
		opts = {
			ensure_installed = {
				"lua_ls",
				"jsonls",
				"yamlls",
				"gopls",
				"ansiblels",
				"eslint",
				"emmet_ls",
				"biome",
				"ts_ls",
				"ty",
				"ruff",
				"html",
				"cssls",
				"marksman",
			},
			automatic_installation = true,
		},
		dependencies = {
			{ "mason-org/mason.nvim" },
			"neovim/nvim-lspconfig",
		},
	},
	{
		"WhoIsSethDaniel/mason-tool-installer.nvim",
		requires = {
			"williamboman/mason.nvim",
		},
		config = function()
			require("mason-tool-installer").setup({
				ensure_installed = {
					"black",
					"stylua",
					"markdownlint",
				},
			})
		end,
	},
	{
		"stevearc/conform.nvim",
		layz = false,
		opts = {
			formatters_by_ft = {
				lua = { "stylua" },
				python = { "black", "isort", "ruff" },
				javascript = { "prettier", "biome" },
				javascriptreact = { "prettier", "biome" },
				typescript = { "prettier", "biome" },
				typescriptreact = { "prettier", "biome" },
				markdown = { "prettier", "biome" },
				mdx = { "prettier", "biome" },
				json = { "prettier", "biome" },
				css = { "prettier", "biome" },
				html = { "prettier", "biome" },
				yaml = { "prettier", "biome" },
				sh = { "shfmt" },
				go = { "goimports" },
			},
			formatters = {
				prettier = {
					prepend_args = { "--tab-width", "4" },
				},
			},
		},
		config = function(_, opts)
			local builtin = require("conform")
			builtin.setup(opts)

			vim.keymap.set("n", "<leader>f", function()
				builtin.format({ async = true, lsp_fallback = true, timeout_ms = 2500 })
			end)
		end,
	},
	{
		"neovim/nvim-lspconfig",
		-- event = { "BufReadPre", "BufNewFile" },
		lazy = false,
		dependencies = {
			"williamboman/mason.nvim",
			"williamboman/mason-lspconfig.nvim",
			"hrsh7th/nvim-cmp",
			"hrsh7th/cmp-buffer",
			"hrsh7th/cmp-path",
			"hrsh7th/cmp-nvim-lsp",
			"hrsh7th/cmp-nvim-lua",
			"L3MON4D3/LuaSnip",
			"saadparwaiz1/cmp_luasnip",
			"rafamadriz/friendly-snippets",
			"b0o/schemastore.nvim",
		},
		config = function()
			-- Setup nvim-cmp
			local cmp = require("cmp")
			local luasnip = require("luasnip")
			require("luasnip.loaders.from_vscode").lazy_load()

			cmp.setup({
				snippet = {
					expand = function(args)
						luasnip.lsp_expand(args.body)
					end,
				},
				mapping = cmp.mapping.preset.insert({
					["<C-b>"] = cmp.mapping.scroll_docs(-4),
					["<C-f>"] = cmp.mapping.scroll_docs(4),
					["<C-Space>"] = cmp.mapping.complete(),
					["<C-e>"] = cmp.mapping.abort(),
					["<CR>"] = cmp.mapping.confirm({ select = true }),
					["<Tab>"] = cmp.mapping(function(fallback)
						if cmp.visible() then
							cmp.select_next_item()
						elseif luasnip.expand_or_jumpable() then
							luasnip.expand_or_jump()
						else
							fallback()
						end
					end, { "i", "s" }),
					["<S-Tab>"] = cmp.mapping(function(fallback)
						if cmp.visible() then
							cmp.select_prev_item()
						elseif luasnip.jumpable(-1) then
							luasnip.jump(-1)
						else
							fallback()
						end
					end, { "i", "s" }),
				}),
				sources = cmp.config.sources({
					{ name = "nvim_lsp" },
					{ name = "luasnip" },
					{ name = "buffer" },
					{ name = "path" },
					{ name = "nvim_lua" },
				}),
				formatting = {
					format = function(entry, vim_item)
						vim_item.kind = string.sub(vim_item.kind, 0, 4)
						vim_item.menu = (string.sub(vim_item.menu or "", 0, 20) or "") .. " "
						vim_item.word = (string.sub(vim_item.word or "", 0, 20) or "") .. " "
						vim_item.abbr = (string.sub(vim_item.abbr, 0, 20) or "") .. " "
						return vim_item
					end,
				},
			})

			-- Setup LSP servers via the native vim.lsp.config API.
			-- Base configs (cmd, root markers, ...) are supplied by nvim-lspconfig on the rtp.
			local capabilities = require("cmp_nvim_lsp").default_capabilities()

			-- Defaults merged into every server config
			vim.lsp.config("*", {
				capabilities = capabilities,
				flags = {
					debounce_text_changes = 150,
				},
			})

			vim.lsp.config("lua_ls", {
				settings = {
					Lua = {
						workspace = { checkThirdParty = false },
						telemetry = { enable = false },
						diagnostics = {
							globals = { "vim" },
						},
					},
				},
			})

			-- Load schemastore after ensuring it's installed
			local schemastore = require("schemastore")

			vim.lsp.config("jsonls", {
				settings = {
					json = {
						schemas = schemastore.json.schemas(),
						validate = { enable = true },
					},
				},
			})

			vim.lsp.config("yamlls", {
				settings = {
					yaml = {
						schemaStore = {
							enable = false,
							url = "",
						},
						schemas = schemastore.yaml.schemas(),
					},
				},
			})

			vim.lsp.config("gopls", {
				filetypes = { "go", "gomod", "gotmpl" },
			})

			vim.lsp.config("ansiblels", {
				filetypes = { "yaml.ansible", ".ansible", "ansible.yaml" },
			})

			-- Global LSP keymaps
			vim.keymap.set("n", "gd", vim.lsp.buf.definition, { desc = "Go to Definition" })
			vim.keymap.set("n", "gr", vim.lsp.buf.references, { desc = "Go to References" })
			-- K (hover, and K-K-K to expand TS types) is owned by config.expand_type
			vim.keymap.set("n", "<leader>vd", function()
				vim.diagnostic.open_float({ focusable = true })
			end)
			vim.keymap.set("n", "<F2>", "<cmd>lua vim.lsp.buf.rename()<cr>")
			vim.keymap.set("n", "<F4>", "<cmd>lua vim.lsp.buf.code_action()<cr>")

			-- Diagnostic configuration
			vim.diagnostic.config({
				virtual_text = true,
				signs = {
					text = {
						[vim.diagnostic.severity.ERROR] = "E",
						[vim.diagnostic.severity.WARN] = "W",
						[vim.diagnostic.severity.INFO] = "I",
						[vim.diagnostic.severity.HINT] = "H",
					},
				},
				underline = true,
				update_in_insert = false,
				severity_sort = true,
			})

			vim.lsp.enable({
				"lua_ls",
				"jsonls",
				"yamlls",
				"gopls",
				"ansiblels",
				"biome",
				"ts_ls",
				"ty",
				"ruff",
				"html",
				"cssls",
				"marksman",
			})
		end,
	},
	{
		"folke/ts-comments.nvim",
		event = "VeryLazy",
		config = function()
			require("ts-comments").setup()
		end,
	},
	{
		"mfussenegger/nvim-lint",
		lazy = false,
		event = { "BufWritePost", "BufReadPost", "InsertLeave" },
		opts = {
			linters_by_ft = {
				markdown = { "markdownlint" },
				python = { "ruff", "flake8" },
			},
			linters = {
				markdownlint = {
					args = {
						"--disable=MD030",
						"-",
					},
				},
			},
		},
		config = function(_, opts)
			require("lint").linters_by_ft = opts.linters_by_ft
			require("lint").linters.markdownlint.args = opts.linters.markdownlint.args

			vim.api.nvim_create_autocmd({ "BufWritePost" }, {
				callback = function()
					-- require("lint").try_lint()
				end,
			})
		end,
	},
	{
		"jmbuhr/otter.nvim",
		dependencies = {
			"nvim-treesitter/nvim-treesitter",
		},
		opts = {
			lsp = {
				diagnostic_update_events = { "BufWritePost", "InsertLeave", "TextChanged" },
				root_dir = function(_, bufnr)
					return vim.fs.root(bufnr or 0, {
						".git",
						"package.json",
						"tsconfig.json",
					}) or vim.fn.getcwd(0)
				end,
			},
			buffers = {
				set_filetype = true,
				write_to_disk = false,
			},
			handle_leading_whitespace = true,
		},
	},
	{
		"antosha417/nvim-lsp-file-operations",
		dependencies = {
			"nvim-lua/plenary.nvim",
			"nvim-tree/nvim-tree.lua",
		},
		config = function()
			require("lsp-file-operations").setup()
		end,
	},
}
