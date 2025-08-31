package ui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"logsense/internal/config"
	"logsense/internal/filter"
	"logsense/internal/ingest"
	"logsense/internal/model"
	"logsense/internal/parse"
)

type tab int

const (
	tabStream tab = iota
	tabFilters
	tabInspector
	tabHelp
)

type state int

const (
	stateRunning state = iota
	statePaused
)

type modalKind int

const (
	modalNone modalKind = iota
	modalHelp
	modalStats
	modalStatsTime
	modalInspector
	modalSearch
	modalFilter
	modalRaw
	modalLogs
	modalExplain
)

type inlineMode int

const (
	inlineNone inlineMode = iota
	inlineSearch
	inlineFilter
	inlineBuffer
)

type Model struct {
	ctx context.Context
	cfg *config.Config
	// cancel function for current ingest pipeline; allows restarting (e.g., when toggling follow)
	ingestCancel context.CancelFunc
	// fileSizeAtLoad stores file size after completing non-follow drain; used to pick up missing lines when enabling follow
	fileSizeAtLoad int64
	// tailStartOffset allows passing a specific start offset to tail when (re)starting ingest in follow mode
	tailStartOffset int64

	// Pipeline
	lines  <-chan ingest.Line
	errs   <-chan error
	parser parse.Parser
	schema model.Schema

	// Data
	ring     *model.Ring
	filtered []model.LogEntry
	total    uint64
	dropped  uint64

	// UI
	tab        tab
	state      state
	tbl        table.Model
	help       help.Model
	styles     Styles
	search     textinput.Model
	viewport   viewport.Model
	spin       spinner.Model
	keymap     KeyMap
	cols       []string
	colOffset  int
	maxCols    int
	selColIdx  int // index in full column list
	termWidth  int
	termHeight int

	scanBufSize int

	// Dirty flags to minimize rebuilds
	rowsDirty    bool
	columnsDirty bool

	// Filter
	criteria filter.Criteria
	eval     *filter.Evaluator

	// Column sizing adjustments (by column name)
	colWidthAdj map[string]int

	// Discovered columns in order of first appearance across logs
	discovered    []string
	discoveredSet map[string]bool

	// status
	source     string
	follow     bool
	lastMsg    string
	lastSearch string
	showHelp   bool
	showStats  bool
	statsField string
	// Stats modal state
	statsItems   []statItem
	statsSel     int
	netBusy      bool
	failStreak   int
	prevDropped  uint64
	invalidCount int

	// Entry rate (lines/sec), EWMA-smoothed
	rateEWMA float64
	rateLast time.Time

	// Modal popup
	modalActive bool
	modalKind   modalKind
	modalVP     viewport.Model
	modalTitle  string
	modalBody   string

	// Help menu state
	helpItems []helpItem
	helpSel   int

	// Search state (navigation)
	searchActive  bool
	searchPattern string
	searchRegex   bool

	// Inline input mode (instead of modal for search/filter)
	inlineMode    inlineMode
	searchEditing bool
}

type helpItem struct {
	group string
	text  string
	key   tea.Key
}

// statItem represents one row in the stats list. It can be either a
// categorical value or a numeric bin range.
type statItem struct {
	label string
	count int
	// Categorical selection
	svalue string
	// Numeric selection
	hasRange bool
	low      float64
	high     float64
	hasExact bool
	fvalue   float64
}

func keyCmd(k tea.Key) tea.Cmd {
	return func() tea.Msg {
		if k.Type == tea.KeyRunes {
			return tea.KeyMsg{Type: k.Type, Runes: k.Runes}
		}
		return tea.KeyMsg{Type: k.Type}
	}
}

func keyLabel(k tea.Key) string {
	switch k.Type {
	case tea.KeyRunes:
		if len(k.Runes) == 1 {
			r := k.Runes[0]
			if r == ' ' {
				return "space"
			}
			return string(r)
		}
		return strings.ToLower(string(k.Runes))
	case tea.KeyEnter:
		return "enter"
	case tea.KeyEsc:
		return "esc"
	case tea.KeyTab:
		return "tab"
	case tea.KeyShiftTab:
		return "shift-tab"
	case tea.KeyLeft:
		return "left"
	case tea.KeyRight:
		return "right"
	case tea.KeyUp:
		return "up"
	case tea.KeyDown:
		return "down"
	case tea.KeyPgUp:
		return "pgup"
	case tea.KeyPgDown:
		return "pgdown"
	default:
		return strings.ToLower(k.String())
	}
}
