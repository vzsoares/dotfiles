return {
  -- LSP Support
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
    config = function()
      local lsp = require("lsp-zero")

      lsp.preset("recommended")
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
            vim_item.kind = string.sub(vim_item.kind, 0, 4)
            vim_item.menu = (string.sub(vim_item.menu or "", 0, 20) or '') .. ' '
            vim_item.word = (string.sub(vim_item.word or "", 0, 20) or '') .. ' '
            vim_item.abbr = (string.sub(vim_item.abbr, 0, 20) or '') .. ' '
            return vim_item
          end
        },
        sources = cmp.config.sources({
          { name = "nvim_lua" },
          { name = "nvim_lsp" },
          { name = "luasnip" },
          { name = "copilot" },
          { name = "eruby" },
        }, {
          { name = "path" },
          { name = "buffer", keyword_length = 5 },
        }, {
          { name = "gh_issues" },
        }),
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
        vim.keymap.set("n", "<leader>vd", function() vim.diagnostic.open_float({ focusable = true }) end, opts)
        vim.keymap.set("n", "[d", function() vim.diagnostic.goto_next() end, opts)
        vim.keymap.set("n", "]d", function() vim.diagnostic.goto_prev() end, opts)
        vim.keymap.set("n", "<leader>vca", function() vim.lsp.buf.code_action() end, opts)
        vim.keymap.set("n", "<leader>vrr", function() vim.lsp.buf.references() end, opts)
        vim.keymap.set("n", "<leader>vrn", function() vim.lsp.buf.rename() end, opts)
        vim.keymap.set("i", "<C-h>", function() vim.lsp.buf.signature_help() end, opts)
      end)

      require('mason').setup({})
      require('mason-lspconfig').setup({
        ensure_installed = {},
        handlers = {
          function(server_name)
            require('lspconfig')[server_name].setup({})
          end,
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
    end
  },

  -- Treesitter
  {
    'nvim-treesitter/nvim-treesitter',
    build = ':TSUpdate',
    config = function()
      require 'nvim-treesitter.configs'.setup {
        ensure_installed = { "vimdoc", "javascript", "typescript", "c", "lua", "rust", "go" },
        sync_install = false,
        auto_install = true,
        highlight = {
          enable = true,
          additional_vim_regex_highlighting = false,
        },
      }
    end
  },

  -- Auto-tag
  {
    'windwp/nvim-ts-autotag',
    config = function()
      require('nvim-ts-autotag').setup({
        opts = {
          enable_close = true,
          enable_rename = true,
          enable_close_on_slash = false
        },
        per_filetype = {
          ["html"] = {
            enable_close = false
          }
        }
      })
    end
  },

  -- Linting
  {
    'mfussenegger/nvim-lint',
    config = function()
      require('lint').linters_by_ft = {
        markdown = { 'markdownlint', },
        python = { 'ruff', 'flake8' }
      }

      local markdownlint = require('lint').linters.markdownlint
      markdownlint.args = {
        '--disable=MD030',
        '-'
      }

      vim.api.nvim_create_autocmd({ "BufWritePost" }, {
        callback = function()
          require("lint").try_lint()
        end,
      })
    end
  },

  -- Formatting
  {
    "stevearc/conform.nvim",
    config = function()
      local builtin = require("conform")

      builtin.setup({
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
      })

      builtin.formatters.prettier = {
        prepend_args = { "--tab-width", "4" },
      }

      vim.keymap.set("n", "<leader>f", function()
        builtin.format { async = true, lsp_fallback = true, timeout_ms = 2500 }
      end)
    end
  },
} 