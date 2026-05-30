return {
    {
        "ThePrimeagen/harpoon",
        branch = "harpoon2",
        dependencies = { "nvim-lua/plenary.nvim", "nvim-telescope/telescope.nvim" },
        event = "VeryLazy",
        opts = {
            settings = {
                save_on_toggle = true,
                sync_on_ui_close = true,
            }
        },
        config = function(_, opts)
            local harpoon = require("harpoon")
            harpoon:setup(opts)

            -- ── Tabbed lists ────────────────────────────────────────
            -- Each tab is a separate harpoon named list (scoped per cwd).
            local TABS = { "alpha", "bravo", "charlie" }
            local ACTIVE_KEY = "__active_tab" -- reserved list name, holds the active tab per cwd
            local state = { active = TABS[1] }

            local function cwd() return harpoon.config.settings.key() end

            -- Stash / restore the active tab inside harpoon's own per-cwd data file.
            local function save_active()
                pcall(function()
                    harpoon.data:update(cwd(), ACTIVE_KEY, { state.active })
                    harpoon.data:sync()
                end)
            end
            local function load_active()
                pcall(function()
                    local vals = harpoon.data:data(cwd(), ACTIVE_KEY)
                    if vals and vals[1] and vim.tbl_contains(TABS, vals[1]) then
                        state.active = vals[1]
                    end
                end)
            end
            load_active()

            local function active_list() return harpoon:list(state.active) end

            local function tabline()
                local parts = {}
                for _, name in ipairs(TABS) do
                    parts[#parts + 1] = (name == state.active) and ("[" .. name .. "]") or (" " .. name .. " ")
                end
                return "Harpoon  " .. table.concat(parts, " ") .. "   (<Tab>/<S-Tab> switch)"
            end

            -- ── Telescope picker (UI, replaces the quick menu) ──────
            local pickers = require("telescope.pickers")
            local finders = require("telescope.finders")
            local conf = require("telescope.config").values
            local actions = require("telescope.actions")
            local action_state = require("telescope.actions.state")
            local entry_display = require("telescope.pickers.entry_display")

            local function make_finder()
                local results = {}
                for idx, item in ipairs(active_list().items) do
                    if item.value ~= "" then
                        results[#results + 1] = { idx = idx, item = item }
                    end
                end
                local displayer = entry_display.create({
                    separator = " ",
                    items = { { width = 2 }, { remaining = true } },
                })
                return finders.new_table({
                    results = results,
                    entry_maker = function(e)
                        local ctx = e.item.context or {}
                        return {
                            value = e,
                            ordinal = e.item.value,
                            display = function() return displayer({ tostring(e.idx), e.item.value }) end,
                            filename = e.item.value,
                            lnum = ctx.row or 1,
                            col = ctx.col or 0,
                        }
                    end,
                })
            end

            local open_picker -- forward decl

            local function refresh(prompt_bufnr)
                action_state.get_current_picker(prompt_bufnr):refresh(make_finder(), { reset_prompt = false })
            end

            local function switch_tab(step)
                return function(prompt_bufnr)
                    local i = 1
                    for idx, name in ipairs(TABS) do
                        if name == state.active then i = idx break end
                    end
                    state.active = TABS[(i - 1 + step) % #TABS + 1]
                    save_active()
                    actions.close(prompt_bufnr)      -- reopen so the title reflects the new tab
                    vim.schedule(function() open_picker() end)
                end
            end

            local function delete_mark(prompt_bufnr)
                local sel = action_state.get_selected_entry()
                if sel then
                    active_list():remove(sel.value.item) -- emits REMOVE → auto-syncs
                    refresh(prompt_bufnr)
                end
            end

            local function move(step)
                return function(prompt_bufnr)
                    local sel = action_state.get_selected_entry()
                    if not sel then return end
                    local items = active_list().items
                    local from, to = sel.value.idx, sel.value.idx + step
                    if to < 1 or to > #items then return end
                    table.insert(items, to, table.remove(items, from))
                    pcall(function() harpoon:sync() end) -- raw reorder doesn't emit, sync manually
                    refresh(prompt_bufnr)
                end
            end

            open_picker = function(picker_opts)
                picker_opts = picker_opts or {}
                pickers.new(picker_opts, {
                    prompt_title = tabline(),
                    initial_mode = "normal",
                    finder = make_finder(),
                    sorter = conf.generic_sorter(picker_opts),
                    previewer = conf.grep_previewer(picker_opts),
                    attach_mappings = function(_, map)
                        map("i", "<Tab>", switch_tab(1))
                        map("n", "<Tab>", switch_tab(1))
                        map("i", "<S-Tab>", switch_tab(-1))
                        map("n", "<S-Tab>", switch_tab(-1))
                        map("i", "<C-d>", delete_mark)
                        map("n", "<C-d>", delete_mark)
                        map("n", "dd", delete_mark)
                        map("i", "<C-k>", move(-1))
                        map("n", "<C-k>", move(-1))
                        map("i", "<C-j>", move(1))
                        map("n", "<C-j>", move(1))
                        return true
                    end,
                }):find()
            end

            -- ── Keymaps (lhs unchanged — now operate on the active tab) ──
            vim.keymap.set("n", "<C-e>", function() open_picker() end)

            vim.keymap.set("n", "<leader>a", function() active_list():add() end)

            -- vim.keymap.set("n", "<C-h>", function() active_list():select(1) end)
            vim.keymap.set("n", "<C-t>", function() active_list():select(2) end)
            vim.keymap.set("n", "<C-n>", function() active_list():select(3) end)
            vim.keymap.set("n", "<C-s>", function() active_list():select(4) end)

            -- Toggle previous & next buffers stored within Harpoon list
            vim.keymap.set("n", "<C-S-P>", function() active_list():prev() end)
            vim.keymap.set("n", "<C-S-N>", function() active_list():next() end)
        end
    },
    { 'nvim-lua/plenary.nvim',     lazy = false },
    { 'MunifTanjim/nui.nvim',      lazy = false },
    { 'HakonHarnes/img-clip.nvim', lazy = false },
    {
        'laytan/cloak.nvim',
        event = "VeryLazy",
        opts = {
            enabled = false,
            cloak_character = "*",
            -- The applied highlight group (colors) on the cloaking, see `:h highlight`.
            highlight_group = "Comment",
            patterns = {
                {
                    -- Match any file starting with ".env".
                    -- This can be a table to match multiple file patterns.
                    file_pattern = {
                        ".env*",
                        "wrangler.toml",
                        ".dev.vars",
                    },
                    -- Match an equals sign and any character after it.
                    -- This can also be a table of patterns to cloak,
                    -- example: cloak_pattern = { ":.+", "-.+" } for yaml files.
                    cloak_pattern = "=.+"
                },
            },
        },
    },
    {
        'mbbill/undotree',
        event = "VeryLazy",
        keys = {
            { "<leader>u", vim.cmd.UndotreeToggle, { desc = "Toggle Undotree" } },
        },
        config = function()
            vim.g.undotree_SetFocusWhenToggle = 1
        end
    },
    {
        "folke/trouble.nvim",
        version = 'v2.10.0',
        event = "VeryLazy",
        keys = {
            { "<leader>xq", "<cmd>TroubleToggle quickfix<cr>",             { desc = "Toggle Quickfix List" } },
            { "<leader>xx", "<cmd>TroubleToggle document_diagnostics<cr>", { desc = "Toggle Document Diagnostics" } },
        },
        opts = {
            -- your configuration options here
        },
        config = function(_, opts)
            require("trouble").setup(opts)
        end
    }
}
