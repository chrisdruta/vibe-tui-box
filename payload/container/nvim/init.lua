-- vibe review-stack nvim config (docs/tui-layout.md "Editor
-- surfaces"). Read-only payload bytes: no state and no plugins live
-- here — the plugin packpath and the compiled parsers are baked into
-- the tools image at pinned SHAs (internal/builder/install.go), and
-- edit.sh points the XDG dirs here and at scratch. Opinionated and
-- sharp on purpose: a review/read surface, not an IDE — no LSP, no
-- completion, no nerd-font glyphs.

-- Image-baked plugin packpath + compiled parser site dir.
vim.opt.packpath:prepend('/opt/vibe/nvim')
vim.opt.runtimepath:append('/opt/vibe/nvim-data/nvim/site')

vim.g.mapleader = ' '
vim.g.maplocalleader = ' '

local o = vim.opt
o.number = true
o.signcolumn = 'yes'
o.termguicolors = true
o.mouse = 'a'
o.scrolloff = 6
o.wrap = false
o.ignorecase = true
o.smartcase = true
o.splitright = true
o.splitbelow = true
-- Scratch XDG dirs: nothing here is durable, so keep nvim from trying.
o.swapfile = false
o.undofile = false
o.updatetime = 250
o.timeoutlen = 500
-- Yanks escape the container as OSC 52 through the popup TTY (the
-- host conf's allow-passthrough carries them to the real terminal);
-- paste stays terminal-native, so the '+' register is copy-only.
vim.g.clipboard = 'osc52'

-- tokyonight's highlight coverage under the engine palette; theme.lua
-- is GENERATED from internal/tmuxui/theme.go — the accent hues are
-- already tokyonight's own, so only the surfaces move.
local thm = require('vibe.theme')
require('tokyonight').setup({
  style = 'night',
  on_colors = function(c)
    c.bg = thm.bg
    c.bg_dark = thm.bg
    c.bg_float = thm.surface
    c.bg_popup = thm.surface
    c.bg_sidebar = thm.bg
    c.bg_statusline = thm.surface
    c.border = thm.border
    c.fg = thm.fg
    c.comment = thm.dim
  end,
})
vim.cmd.colorscheme('tokyonight')

-- Treesitter (main branch): parsers are precompiled into the image;
-- start per buffer, silently falling back to regex syntax where no
-- parser shipped.
vim.api.nvim_create_autocmd('FileType', {
  callback = function()
    pcall(vim.treesitter.start)
  end,
})

-- mini.nvim: picker, statusline, keymap hints, ascii icon marks.
require('mini.icons').setup({ style = 'ascii' })
require('mini.statusline').setup({ use_icons = false })
require('mini.pick').setup()
local clue = require('mini.clue')
clue.setup({
  triggers = {
    { mode = 'n', keys = '<Leader>' },
    { mode = 'x', keys = '<Leader>' },
    { mode = 'n', keys = 'g' },
    { mode = 'n', keys = ']' },
    { mode = 'n', keys = '[' },
    { mode = 'n', keys = 'z' },
    { mode = 'n', keys = '<C-w>' },
  },
  clues = {
    clue.gen_clues.g(),
    clue.gen_clues.windows(),
    clue.gen_clues.z(),
    { mode = 'n', keys = '<Leader>g', desc = '+git' },
  },
})

-- gitsigns: hunks without ceremony.
require('gitsigns').setup({
  current_line_blame = false,
  on_attach = function(buf)
    local gs = require('gitsigns')
    local function map(l, r, desc)
      vim.keymap.set('n', l, r, { buffer = buf, desc = desc })
    end
    map(']h', function() gs.nav_hunk('next') end, 'next hunk')
    map('[h', function() gs.nav_hunk('prev') end, 'previous hunk')
    map('<Leader>gp', gs.preview_hunk, 'preview hunk')
    map('<Leader>gs', gs.stage_hunk, 'stage hunk')
    map('<Leader>gr', gs.reset_hunk, 'reset hunk')
    map('<Leader>gb', gs.blame_line, 'blame line')
  end,
})

-- oil: the files surface — the directory IS the window (`nvim .`
-- lands in it via default_file_explorer), `-` climbs, editing the
-- listing edits the filesystem. Diff review is lazygit's job
-- (prefix+g); gitsigns covers hunks while reading here.
require('oil').setup({
  default_file_explorer = true,
  view_options = { show_hidden = true },
  keymaps = {
    ['q'] = { 'actions.close', mode = 'n' },
  },
})

local map = vim.keymap.set
map('n', '-', '<Cmd>Oil<CR>', { desc = 'browse parent dir' })
map('n', '<Leader>f', function() require('mini.pick').builtin.files() end, { desc = 'find file' })
map('n', '<Leader>/', function() require('mini.pick').builtin.grep_live() end, { desc = 'grep' })
map('n', '<Leader>b', function() require('mini.pick').builtin.buffers() end, { desc = 'buffers' })
map({ 'n', 'x' }, '<Leader>y', '"+y', { desc = 'copy to host clipboard' })
map('n', '<Leader>q', '<Cmd>quitall<CR>', { desc = 'quit' })
map('n', '<Esc>', '<Cmd>nohlsearch<CR>')
