package ui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"logsense/internal/config"
	"logsense/internal/model"
)

func initialModel(ctx context.Context, cfg *config.Config) *Model {
	m := &Model{
		ctx:             ctx,
		cfg:             cfg,
		ring:            model.NewRing(cfg.MaxBuffer),
		tab:             tabStream,
		state:           stateRunning,
		help:            help.New(),
		styles:          NewStyles(cfg.Theme == config.ThemeDark),
		keymap:          DefaultKeyMap(),
		search:          textinput.New(),
		spin:            spinner.New(),
		follow:          cfg.Follow,
		scanBufSize:     1024 * 1024,
		rowsDirty:       true,
		columnsDirty:    true,
		tailStartOffset: -1,
	}
	m.spin.Spinner = spinner.Dot
	m.search.Placeholder = "search... (text or /regex/)"
	m.search.CharLimit = 256
	m.search.Prompt = "/"
	m.viewport = viewport.New(80, 20)

	m.tbl = table.New(table.WithFocused(true), table.WithHeight(20))
	m.tbl.SetColumns([]table.Column{{Title: "ts", Width: 19}, {Title: "level", Width: 6}, {Title: "source", Width: 12}, {Title: "message", Width: 80}})
	// Remove default padding to make width math exact
	ts := table.DefaultStyles()
	ts.Header = lipgloss.NewStyle().PaddingRight(1)
	ts.Cell = lipgloss.NewStyle().PaddingRight(1)
	ts.Selected = m.styles.TableStyles.Selected
	m.tbl.SetStyles(ts)
	m.maxCols = 6
	m.selColIdx = 0
	m.colWidthAdj = map[string]int{}
	m.discoveredSet = map[string]bool{}
	// Initialize columns so a column is visibly selected before detection
	m.applyColumns(m.visibleColumns(m.deriveColumns()))
	return m
}

func Run(ctx context.Context, cfg *config.Config) error {
	m := initialModel(ctx, cfg)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(setupPipeline(m), tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} }))
}
