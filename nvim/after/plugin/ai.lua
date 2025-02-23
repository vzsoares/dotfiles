require('copilot').setup({
    -- use recommended settings from above
    suggestion = {
        enabled = true,
        auto_trigger = true,
        hide_during_completion = true,
        debounce = 75,
        keymap = {
            accept = "<Tab>",
            accept_word = false,
            accept_line = false,
            next = "<M-]>",
            prev = "<M-[>",
            dismiss = "<C-]>",
        },
    },
})
require('render-markdown').setup({
    -- use recommended settings from above
})
require('avante_lib').load()
require('avante').setup({
    -- Your config here!
    provider = "copilot",
    behaviour = {
        enable_cursor_planning_mode = true, -- enable cursor planning mode!
    },
    windows = {
        width = 40,               -- default % based on available width
        ask = {
            start_insert = false, -- Start insert mode when opening the ask window
        },
    },
})
