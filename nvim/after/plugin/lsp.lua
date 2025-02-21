local lsp = require("lsp-zero")

lsp.preset("recommended")

-- Fix Undefined global 'vim'
lsp.nvim_workspace()

local cmp = require('cmp')
local cmp_select = { behavior = cmp.SelectBehavior.Select }
local cmp_mappings = lsp.defaults.cmp_mappings({
    ['<C-p>'] = cmp.mapping.select_prev_item(cmp_select),
    ['<C-n>'] = cmp.mapping.select_next_item(cmp_select),
    ['<C-y>'] = cmp.mapping.confirm({ select = true }),
    ["<C-Space>"] = cmp.mapping.complete(),
})

cmp_mappings['<Tab>'] = nil
cmp_mappings['<S-Tab>'] = nil

lsp.setup_nvim_cmp({
    mapping = cmp_mappings,
    view = {
        entries = {
            name = 'custom', selection_order = 'near_cursor'
        }
    },
    formatting = {
        format = function(entry, vim_item)
            -- Kind icons
            vim_item.kind = string.sub(vim_item.kind, 0, 4)
            -- Source
            vim_item.menu = (string.sub(vim_item.menu or "", 0, 20) or '') .. ' '
            vim_item.word = (string.sub(vim_item.word or "", 0, 20) or '') .. ' '
            -- Content
            vim_item.abbr = (string.sub(vim_item.abbr, 0, 20) or '') .. ' '
            return vim_item
        end
    }
})

lsp.set_preferences({
    suggest_lsp_servers = false,
    sign_icons = {
        error = 'E',
        warn = 'W',
        hint = 'H',
        info = 'I'
    },
    autoformat = false
})

lsp.on_attach(function(client, bufnr)
    local opts = { buffer = bufnr, remap = false }

    vim.keymap.set("n", "gd", function() vim.lsp.buf.definition() end, opts)
    vim.keymap.set("n", "K", function() vim.lsp.buf.hover() end, opts)
    vim.keymap.set("n", "<leader>vws", function() vim.lsp.buf.workspace_symbol() end, opts)
    vim.keymap.set("n", "<leader>vd", function() vim.diagnostic.open_float() end, opts)
    vim.keymap.set("n", "[d", function() vim.diagnostic.goto_next() end, opts)
    vim.keymap.set("n", "]d", function() vim.diagnostic.goto_prev() end, opts)
    vim.keymap.set("n", "<leader>vca", function() vim.lsp.buf.code_action() end, opts)
    vim.keymap.set("n", "<leader>vrr", function() vim.lsp.buf.references() end, opts)
    vim.keymap.set("n", "<leader>vrn", function() vim.lsp.buf.rename() end, opts)
    vim.keymap.set("i", "<C-h>", function() vim.lsp.buf.signature_help() end, opts)
end)


require('mason').setup({})
require('mason-lspconfig').setup({
    ensure_installed = {
        "biome",
        "prettier",
        "ts_ls",
        "eslint",
        "jsonls",
        "emmet_ls",
        "goimports",
        "gopls",
        "lua_ls",
    },
    handlers = {
        -- this first function is the "default handler"
        -- it applies to every language server without a "custom handler"
        function(server_name)
            require('lspconfig')[server_name].setup({})
        end,

        -- this is the "custom handler" for `tsserver`
        jsonls = function()
            require('lspconfig').jsonls.setup {
                settings = {
                    json = {
                        schemas = require('schemastore').json.schemas(),
                        validate = { enable = true },
                    },
                },
            }
        end,
        ansiblels = function()
            require('lspconfig').ansiblels.setup {
                filetypes = { "yaml.ansible", ".ansible", "ansible.yaml" }
            }
        end,
        yamlls = function()
            require('lspconfig').yamlls.setup {
                settings = {
                    yaml = {
                        schemaStore = {
                            enable = false,
                            url = "",
                        },
                        schemas = require('schemastore').yaml.schemas(),
                    },
                },
            }
        end,
        gopls = function()
            require('lspconfig').gopls.setup {
                filetypes = { "go", "gomod", "gotmpl" }
            }
        end
    }
})

lsp.setup()

vim.diagnostic.config({
    virtual_text = true
})
