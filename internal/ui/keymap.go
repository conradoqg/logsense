package ui

import tea "github.com/charmbracelet/bubbletea"

type KeyMap struct {
	Pause        tea.Key
	Follow       tea.Key
	Search       tea.Key
	Export       tea.Key
	Explain      tea.Key
	Redetect     tea.Key
	Top          tea.Key
	Bottom       tea.Key
	Help         tea.Key
	Quit         tea.Key
	StreamTab    tea.Key
	FilterTab    tea.Key
	InspectorTab tea.Key
	Stats        tea.Key
	NextStats    tea.Key
	Filter       tea.Key
	SearchNext   tea.Key
	SearchPrev   tea.Key
	CopyLine     tea.Key
	ClearFilter  tea.Key
	ViewRaw      tea.Key
	AppLogs      tea.Key
	Buffer       tea.Key
	IncColWidth  tea.Key
	DecColWidth  tea.Key
}

func DefaultKeyMap() KeyMap {
    return KeyMap{
		Pause:        tea.Key{Type: tea.KeyRunes, Runes: []rune{' '}},
		Follow:       tea.Key{Type: tea.KeyRunes, Runes: []rune{'t'}},
		Search:       tea.Key{Type: tea.KeyRunes, Runes: []rune{'/'}},
		Export:       tea.Key{Type: tea.KeyRunes, Runes: []rune{'e'}},
		Explain:      tea.Key{Type: tea.KeyRunes, Runes: []rune{'i'}},
		Redetect:     tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}},
		Top:          tea.Key{Type: tea.KeyRunes, Runes: []rune{'g'}},
		Bottom:       tea.Key{Type: tea.KeyRunes, Runes: []rune{'G'}},
		Help:         tea.Key{Type: tea.KeyRunes, Runes: []rune{'?'}},
		Quit:         tea.Key{Type: tea.KeyRunes, Runes: []rune{'q'}},
		StreamTab:    tea.Key{Type: tea.KeyTab},
		FilterTab:    tea.Key{Type: tea.KeyShiftTab},
		InspectorTab: tea.Key{Type: tea.KeyEnter},
		Stats:        tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}},
		NextStats:    tea.Key{Type: tea.KeyRunes, Runes: []rune{'X'}},
		Filter:       tea.Key{Type: tea.KeyRunes, Runes: []rune{'f'}},
		SearchNext:   tea.Key{Type: tea.KeyRunes, Runes: []rune{'n'}},
		SearchPrev:   tea.Key{Type: tea.KeyRunes, Runes: []rune{'N'}},
		CopyLine:     tea.Key{Type: tea.KeyRunes, Runes: []rune{'c'}},
		ClearFilter:  tea.Key{Type: tea.KeyRunes, Runes: []rune{'F'}},
		ViewRaw:      tea.Key{Type: tea.KeyRunes, Runes: []rune{'v'}},
		AppLogs:      tea.Key{Type: tea.KeyRunes, Runes: []rune{'L'}},
		Buffer:       tea.Key{Type: tea.KeyRunes, Runes: []rune{'B'}},
		IncColWidth:  tea.Key{Type: tea.KeyRunes, Runes: []rune{']'}},
		DecColWidth:  tea.Key{Type: tea.KeyRunes, Runes: []rune{'['}},
	}
}

func keyMatches(msg tea.KeyMsg, k tea.Key) bool {
	if k.Type != tea.KeyRunes {
		return msg.Type == k.Type
	}
	if len(k.Runes) > 0 {
		return msg.String() == string(k.Runes)
	}
	return false
}
