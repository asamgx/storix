package tui

import "charm.land/bubbles/v2/key"

// KeyMap is every binding the interface answers to. It satisfies
// help.KeyMap, so the footer hint and the help overlay are generated from it
// and cannot drift from what Update actually does.
type KeyMap struct {
	Up        key.Binding
	Down      key.Binding
	PageUp    key.Binding
	PageDown  key.Binding
	Top       key.Binding
	Bottom    key.Binding
	Open      key.Binding
	Parent    key.Binding
	SortSize  key.Binding
	SortName  key.Binding
	SortMtime key.Binding
	SortCount key.Binding
	Apparent  key.Binding
	Bundles   key.Binding
	Filter    key.Binding
	Escape    key.Binding
	Finder    key.Binding
	Copy      key.Binding
	Switch    key.Binding
	Browse    key.Binding
	Unacc     key.Binding
	Rescan    key.Binding
	Help      key.Binding
	Quit      key.Binding
}

// DefaultKeyMap is the keymap of docs/03-architecture.md § Browse.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		PageUp:    key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		PageDown:  key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
		Top:       key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "first")),
		Bottom:    key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "last")),
		Open:      key.NewBinding(key.WithKeys("enter", "l", "right"), key.WithHelp("enter/l", "open")),
		Parent:    key.NewBinding(key.WithKeys("backspace", "h", "left"), key.WithHelp("bksp/h", "parent")),
		SortSize:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort by size")),
		SortName:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "sort by name")),
		SortMtime: key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "sort by modified")),
		SortCount: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "sort by files")),
		Apparent:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "allocated/apparent")),
		Bundles:   key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "enter bundles")),
		Filter:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Escape:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
		Finder:    key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "reveal in Finder")),
		Copy:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy path")),
		Switch:    key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch view")),
		Browse:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "browse")),
		Unacc:     key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "unaccounted")),
		Rescan:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rescan")),
		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:      key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp is the one-line hint in the footer.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Open, k.Parent, k.SortSize, k.Filter, k.Switch, k.Help, k.Quit}
}

// FullHelp is the help overlay, in columns.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom},
		{k.Open, k.Parent, k.Bundles, k.Filter, k.Escape},
		{k.SortSize, k.SortName, k.SortMtime, k.SortCount, k.Apparent},
		{k.Finder, k.Copy, k.Switch, k.Browse, k.Unacc},
		{k.Rescan, k.Help, k.Quit},
	}
}
