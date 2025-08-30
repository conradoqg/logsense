package ui

import "github.com/charmbracelet/lipgloss"

type Styles struct {
	Base        lipgloss.Style
	Status      lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	Level       map[string]lipgloss.Style
	Help        lipgloss.Style
	Inspector   lipgloss.Style
	TableStyles TableStyles
	PopupBox    lipgloss.Style
	PopupTitle  lipgloss.Style
	Muted       lipgloss.Style
	// JSON coloring
	JSONKey    lipgloss.Style
	JSONString lipgloss.Style
	JSONNumber lipgloss.Style
	JSONBool   lipgloss.Style
	JSONNull   lipgloss.Style
	JSONPunct  lipgloss.Style
}

type TableStyles struct {
	Header         lipgloss.Style
	Cell           lipgloss.Style
	Selected       lipgloss.Style
	HeaderSelected lipgloss.Style
}

func NewStyles(dark bool) Styles {
	s := Styles{}
	if dark {
		s.Base = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("0"))
		s.Status = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		s.TabActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
		s.TabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		s.Help = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
		s.Inspector = lipgloss.NewStyle().Padding(1)
		s.PopupBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("60")).Padding(1, 2)
		s.PopupTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
		s.Muted = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		s.JSONKey = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
		s.JSONString = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
		s.JSONNumber = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
		s.JSONBool = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
		s.JSONNull = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
		s.JSONPunct = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	} else {
		s.Base = lipgloss.NewStyle()
		s.Status = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.TabActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("27"))
		s.TabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.Help = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.Inspector = lipgloss.NewStyle().Padding(1)
		s.PopupBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("12")).Padding(1, 2)
		s.PopupTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("27"))
		s.Muted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.JSONKey = lipgloss.NewStyle().Foreground(lipgloss.Color("27"))
		s.JSONString = lipgloss.NewStyle().Foreground(lipgloss.Color("28"))
		s.JSONNumber = lipgloss.NewStyle().Foreground(lipgloss.Color("130"))
		s.JSONBool = lipgloss.NewStyle().Foreground(lipgloss.Color("25"))
		s.JSONNull = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.JSONPunct = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	}
	s.Level = map[string]lipgloss.Style{
		"TRACE": lipgloss.NewStyle().Foreground(lipgloss.Color("242")),
		"DEBUG": lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		"INFO":  lipgloss.NewStyle().Foreground(lipgloss.Color("45")),
		"WARN":  lipgloss.NewStyle().Foreground(lipgloss.Color("220")),
		"ERROR": lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		"FATAL": lipgloss.NewStyle().Foreground(lipgloss.Color("201")).Bold(true),
	}
	s.TableStyles = TableStyles{
		Header:         lipgloss.NewStyle().Bold(true),
		Cell:           lipgloss.NewStyle(),
		Selected:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("220")),
		HeaderSelected: lipgloss.NewStyle().Underline(true),
	}
	return s
}

func (s Styles) Table() lipgloss.Style { return s.Base }
