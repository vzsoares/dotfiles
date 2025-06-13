return {
    -- Lazy.nvim manages itself
    { 'folke/lazy.nvim', version = '*', lazy = false },

    { 'nvim-telescope/telescope.nvim', version = '0.1.5', dependencies = { 'nvim-lua/plenary.nvim' }, lazy = true },

    {
        'rose-pine/neovim',
        name = 'rose-pine',
        lazy = false,
        config = function()
            vim.cmd('colorscheme rose-pine')
        end
    },

    {
        "folke/trouble.nvim",
        version = 'v2.10.0',
        lazy = true,
        config = function()
            require("trouble").setup { icons = false }
        end
    },

    {
        "folke/ts-comments.nvim",
        lazy = true,
        config = function()
            require('ts-comments').setup()
        end
    },

    {
        'nvim-treesitter/nvim-treesitter',
        build = ':TSUpdate',
        lazy = true
    },

    { 'nvim-treesitter/playground', lazy = true },
    { 'nvim-lua/plenary.nvim', lazy = true },

    {
        "ThePrimeagen/harpoon",
        branch = "harpoon2",
        dependencies = { "nvim-lua/plenary.nvim" },
        lazy = true
    },

    { 'mbbill/undotree', lazy = true },
    { 'tpope/vim-fugitive', lazy = true },
    { 'nvim-treesitter/nvim-treesitter-context', lazy = true },

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
        lazy = true
    },

    {
        "stevearc/conform.nvim",
        lazy = true,
        config = function()
            require("conform").setup()
        end
    },

    { 'b0o/schemastore.nvim', lazy = true },
    { 'laytan/cloak.nvim', lazy = true },

    {
        'nvim-tree/nvim-tree.lua',
        dependencies = { 'nvim-tree/nvim-web-devicons' },
        lazy = true
    },

    { 'mfussenegger/nvim-lint', lazy = true },
    { 'petertriho/nvim-scrollbar', lazy = true },
    { 'lewis6991/gitsigns.nvim', lazy = true },
    { 'windwp/nvim-ts-autotag', lazy = true },

    -- AI / UX
    { 'stevearc/dressing.nvim', lazy = true },
    { 'MunifTanjim/nui.nvim', lazy = true },
    { 'MeanderingProgrammer/render-markdown.nvim', lazy = true },
    { 'HakonHarnes/img-clip.nvim', lazy = true },
    { 'zbirenbaum/copilot.lua', lazy = true },

    {
        'yetone/avante.nvim',
        branch = 'main',
        build = 'make',
        lazy = true,
        config = function()
            require('avante_lib').load()
            require('avante').setup()
        end
    }
}
