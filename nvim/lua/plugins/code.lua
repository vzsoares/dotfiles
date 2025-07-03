return {
    {
        "stevearc/conform.nvim",
        layz = false,
        opts = {
            formatters_by_ft = {
                lua = { "stylua" },
                python = { "black", "isort", "ruff" },
                javascript = { "prettier" },
                javascriptreact = { "prettier" },
                typescript = { "prettier" },
                typescriptreact = { "prettier" },
                markdown = { "prettier" },
                mdx = { "prettier" },
                json = { "prettier" },
                css = { "prettier" },
                html = { "prettier" },
                yaml = { "prettier" },
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
                builtin.format { async = true, lsp_fallback = true, timeout_ms = 2500 }
            end)
        end
    },
    {
        'VonHeikemen/lsp-zero.nvim',
        branch = 'v1.x',
        dependencies = {
            'neovim/nvim-lspconfig',
            'williamboman/mason.nvim',
            'williamboman/mason-lspconfig.nvim',
            'hrsh7th/nvim-cmp',
            'hrsh7th/cmp-buffer',
            'hrsh7th/cmp-path',
            'saadparwaiz1/cmp_luasnip',
            'hrsh7th/cmp-nvim-lsp',
            'hrsh7th/cmp-nvim-lua',
            'L3MON4D3/LuaSnip',
            'rafamadriz/friendly-snippets',
        },
        lazy = false
    },
    {
        'neovim/nvim-lspconfig',
        -- event = { "BufReadPre", "BufNewFile" },
        lazy = false,
        dependencies = {
            'williamboman/mason.nvim',
            'williamboman/mason-lspconfig.nvim',
            'hrsh7th/nvim-cmp',
            'hrsh7th/cmp-buffer',
            'hrsh7th/cmp-path',
            'hrsh7th/cmp-nvim-lsp',
            'hrsh7th/cmp-nvim-lua',
            'L3MON4D3/LuaSnip',
            'rafamadriz/friendly-snippets',
            'b0o/schemastore.nvim',
        },
        config = function()
            -- Setup nvim-cmp
            local cmp = require('cmp')
            local luasnip = require('luasnip')
            require('luasnip.loaders.from_vscode').lazy_load()

            cmp.setup({
                snippet = {
                    expand = function(args)
                        luasnip.lsp_expand(args.body)
                    end,
                },
                mapping = cmp.mapping.preset.insert({
                    ['<C-b>'] = cmp.mapping.scroll_docs(-4),
                    ['<C-f>'] = cmp.mapping.scroll_docs(4),
                    ['<C-Space>'] = cmp.mapping.complete(),
                    ['<C-e>'] = cmp.mapping.abort(),
                    ['<CR>'] = cmp.mapping.confirm({ select = true }),
                    ['<Tab>'] = cmp.mapping(function(fallback)
                        if cmp.visible() then
                            cmp.select_next_item()
                        elseif luasnip.expand_or_jumpable() then
                            luasnip.expand_or_jump()
                        else
                            fallback()
                        end
                    end, { 'i', 's' }),
                    ['<S-Tab>'] = cmp.mapping(function(fallback)
                        if cmp.visible() then
                            cmp.select_prev_item()
                        elseif luasnip.jumpable(-1) then
                            luasnip.jump(-1)
                        else
                            fallback()
                        end
                    end, { 'i', 's' }),
                }),
                sources = cmp.config.sources({
                    { name = 'nvim_lsp' },
                    { name = 'luasnip' },
                    { name = 'buffer' },
                    { name = 'path' },
                    { name = 'nvim_lua' },
                }),
                formatting = {
                    format = function(entry, vim_item)
                        vim_item.kind = string.sub(vim_item.kind, 0, 4)
                        vim_item.menu = (string.sub(vim_item.menu or "", 0, 20) or '') .. ' '
                        vim_item.word = (string.sub(vim_item.word or "", 0, 20) or '') .. ' '
                        vim_item.abbr = (string.sub(vim_item.abbr, 0, 20) or '') .. ' '
                        return vim_item
                    end
                },
            })

            -- Setup LSP servers
            local lspconfig = require('lspconfig')
            local capabilities = require('cmp_nvim_lsp').default_capabilities()

            -- Common LSP settings
            local common_settings = {
                capabilities = capabilities,
                flags = {
                    debounce_text_changes = 150,
                },
            }

            -- Setup each LSP server
            lspconfig.lua_ls.setup(vim.tbl_deep_extend('force', common_settings, {
                settings = {
                    Lua = {
                        workspace = { checkThirdParty = false },
                        telemetry = { enable = false },
                        diagnostics = {
                            globals = { "vim" }
                        }
                    },
                },
            }))

            -- Load schemastore after ensuring it's installed
            local schemastore = require('schemastore')

            lspconfig.jsonls.setup(vim.tbl_deep_extend('force', common_settings, {
                settings = {
                    json = {
                        schemas = schemastore.json.schemas(),
                        validate = { enable = true },
                    },
                },
            }))

            lspconfig.yamlls.setup(vim.tbl_deep_extend('force', common_settings, {
                settings = {
                    yaml = {
                        schemaStore = {
                            enable = false,
                            url = "",
                        },
                        schemas = schemastore.yaml.schemas(),
                    },
                },
            }))

            lspconfig.gopls.setup(vim.tbl_deep_extend('force', common_settings, {
                filetypes = { "go", "gomod", "gotmpl" }
            }))

            lspconfig.ansiblels.setup(vim.tbl_deep_extend('force', common_settings, {
                filetypes = { "yaml.ansible", ".ansible", "ansible.yaml" }
            }))

            -- Global LSP keymaps
            vim.keymap.set('n', 'gd', vim.lsp.buf.definition, { desc = 'Go to Definition' })
            vim.keymap.set('n', 'gr', vim.lsp.buf.references, { desc = 'Go to References' })
            vim.keymap.set('n', 'K', vim.lsp.buf.hover, { desc = 'Hover Documentation' })
            vim.keymap.set('n', '<leader>rn', vim.lsp.buf.rename, { desc = 'Rename Symbol' })
            vim.keymap.set("n", "<leader>vd", function() vim.diagnostic.open_float({ focusable = true }) end, opts)
            vim.keymap.set('n', '<leader>ca', vim.lsp.buf.code_action, { desc = 'Code Action' })
            vim.keymap.set('n', '<leader>d[', vim.diagnostic.goto_prev, { desc = 'Previous Diagnostic' })
            vim.keymap.set('n', '<leader>d]', vim.diagnostic.goto_next, { desc = 'Next Diagnostic' })
            vim.keymap.set('n', '<leader>dl', vim.diagnostic.setloclist, { desc = 'Diagnostic List' })

            -- Diagnostic configuration
            vim.diagnostic.config({
                virtual_text = true,
                signs = {
                    text = {
                        [vim.diagnostic.severity.ERROR] = 'E',
                        [vim.diagnostic.severity.WARN] = 'W',
                        [vim.diagnostic.severity.INFO] = 'I',
                        [vim.diagnostic.severity.HINT] = 'H',
                    },
                },
                underline = true,
                update_in_insert = false,
                severity_sort = true,
            })
        end
    },
    {
        "mason-org/mason.nvim",
        event = "VeryLazy",
        opts = {
            ui = {
                border = "rounded",
                icons = {
                    package_installed = "✓",
                    package_pending = "➜",
                    package_uninstalled = "✗"
                }
            }
        }
    },
    {
        "mason-org/mason-lspconfig.nvim",
        event = { "BufReadPre", "BufNewFile" },
        opts = {
            ensure_installed = {
                "lua_ls",
                "jsonls",
                "yamlls",
                "gopls",
                "ansiblels",
            },
            automatic_installation = true,
        },
        dependencies = {
            { "mason-org/mason.nvim" },
            "neovim/nvim-lspconfig",
        },
    },
    {
        "folke/ts-comments.nvim",
        event = "VeryLazy",
        config = function()
            require('ts-comments').setup()
        end
    },
    {
        'mfussenegger/nvim-lint',
        lazy = false,
        event = { "BufWritePost", "BufReadPost", "InsertLeave" },
        opts = {
            linters_by_ft = {
                markdown = { 'markdownlint' },
                python = { 'ruff', 'flake8' }
            },
            linters = {
                markdownlint = {
                    args = {
                        '--disable=MD030',
                        '-'
                    }
                }
            }
        },
        config = function(_, opts)
            require('lint').linters_by_ft = opts.linters_by_ft
            require('lint').linters.markdownlint.args = opts.linters.markdownlint.args

            vim.api.nvim_create_autocmd({ "BufWritePost" }, {
                callback = function()
                    require("lint").try_lint()
                end,
            })
        end
    }
}
