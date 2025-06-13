return {
    'petertriho/nvim-scrollbar',
    lazy = true,
    config = function()
        require("scrollbar").setup({
            handle = {
                blend = 10
            },
            marks = {
                Cursor = {
                    text = "█",
                    color = "#c4a7e7"
                }
            }
        })
        require("scrollbar.handlers.gitsigns").setup()
    end
}
