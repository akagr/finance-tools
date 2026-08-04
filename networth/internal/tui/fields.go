package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// field is one editable value in a form.
//
// Three kinds, chosen by what the value is allowed to be rather than by how it
// looks. Free text where only a handful of values are valid is a trap: it turns
// a decision into a spelling test, and the error arrives after the work instead
// of during it. So a fixed set of options is a choice you cycle, and a reference
// to something that already exists completes as you type.
type field struct {
	label string
	help  string
	kind  fieldKind

	input textinput.Model

	// choices holds every valid value for fieldChoice, all of them shown at
	// once so the vocabulary needs no explaining.
	choices []string
	idx     int

	// options is the full candidate list for fieldSuggest, matches is what the
	// current text narrows it to.
	options []string
	matches []string
	sel     int
	open    bool
	// strict refuses a value that is not one of the options.
	strict bool
}

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldChoice
	fieldSuggest
)

func newTextField(label, help string) *field {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 200
	return &field{label: label, help: help, kind: fieldText, input: in}
}

func newChoiceField(label, help string, choices []string) *field {
	return &field{label: label, help: help, kind: fieldChoice, choices: choices}
}

// newSuggestField completes against options. When strict, only one of them is
// accepted, which is what stops a rule pointing at an account that does not
// exist.
func newSuggestField(label, help string, options []string, strict bool) *field {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 200
	f := &field{label: label, help: help, kind: fieldSuggest,
		input: in, options: options, strict: strict}
	f.refilter()
	return f
}

// Value returns what the field currently holds.
func (f *field) Value() string {
	if f.kind == fieldChoice {
		if f.idx < len(f.choices) {
			return f.choices[f.idx]
		}
		return ""
	}
	return f.input.Value()
}

// SetValue loads a value, snapping a choice field to the matching option.
func (f *field) SetValue(v string) {
	if f.kind == fieldChoice {
		for i, c := range f.choices {
			if strings.EqualFold(c, v) {
				f.idx = i
				return
			}
		}
		f.idx = 0
		return
	}
	f.input.SetValue(v)
	f.refilter()
	f.open = false
}

// SetOptions replaces the completion candidates, for a list that changes while
// the form is open.
func (f *field) SetOptions(opts []string) {
	f.options = opts
	f.refilter()
}

func (f *field) Focus() {
	if f.kind != fieldChoice {
		f.input.Focus()
		f.input.CursorEnd()
	}
}

func (f *field) Blur() {
	f.input.Blur()
	f.open = false
}

// Valid reports whether the value is acceptable, and why not if it is not.
func (f *field) Valid() (bool, string) {
	if f.kind != fieldSuggest || !f.strict {
		return true, ""
	}
	v := strings.TrimSpace(f.input.Value())
	if v == "" {
		return false, "pick one of " + strings.Join(firstFew(f.options, 4), ", ")
	}
	for _, o := range f.options {
		if strings.EqualFold(o, v) {
			return true, ""
		}
	}
	return false, "no such " + strings.ToLower(f.label) + " — " +
		"start typing and pick from the list"
}

// Update offers the key to the field first. It reports whether it consumed it,
// so the form only sees keys the field did not want: arrows mean "move within
// the list" while a list is open, and "move between fields" otherwise.
func (f *field) Update(key tea.KeyMsg) (bool, tea.Cmd) {
	switch f.kind {
	case fieldChoice:
		// Tab always means "next field", never "next option", so that moving
		// through a form works the same way regardless of what a field holds.
		switch key.String() {
		case "left":
			f.idx = (f.idx + len(f.choices) - 1) % len(f.choices)
			return true, nil
		case "right", " ":
			f.idx = (f.idx + 1) % len(f.choices)
			return true, nil
		}
		// Typing the first letter of an option jumps to it.
		if len(key.Runes) == 1 {
			r := strings.ToLower(string(key.Runes))
			for i, c := range f.choices {
				if strings.HasPrefix(strings.ToLower(c), r) {
					f.idx = i
					return true, nil
				}
			}
		}
		return false, nil

	case fieldSuggest:
		switch key.String() {
		case "down":
			if !f.open {
				f.open = true
				f.refilter()
				return true, nil
			}
			if f.sel < len(f.matches)-1 {
				f.sel++
			}
			return true, nil
		case "up":
			if f.open {
				if f.sel > 0 {
					f.sel--
					return true, nil
				}
				f.open = false
				return true, nil
			}
			return false, nil
		case "tab":
			// Tab accepts a suggestion only while the list is open. With it
			// closed the value is already settled, so tab means "next field".
			if f.open && len(f.matches) > 0 {
				f.accept()
				return true, nil
			}
			return false, nil
		case "shift+tab":
			// Never a text key, and there is nothing to go back to within the
			// field, so it always means "previous field". Falling through to the
			// input below would swallow it and strand the cursor.
			return false, nil
		case "enter":
			if f.open && len(f.matches) > 0 {
				f.accept()
				return true, nil
			}
			return false, nil
		case "esc":
			if f.open {
				f.open = false
				return true, nil
			}
			return false, nil
		}
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(key)
		f.refilter()
		f.open = len(f.matches) > 0 && f.input.Value() != ""
		return true, cmd
	}

	// Keys that move between fields must never be swallowed by a text input, or
	// the form traps the cursor on its first field.
	switch key.String() {
	case "tab", "shift+tab", "up", "down", "enter", "esc":
		return false, nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(key)
	return true, cmd
}

func (f *field) accept() {
	if f.sel < len(f.matches) {
		f.input.SetValue(f.matches[f.sel])
		f.input.CursorEnd()
	}
	f.open = false
	f.refilter()
}

// refilter narrows the options to those matching what has been typed. Matching
// anywhere in the string, not just at the start, is what makes "card" find
// "HDFC Card".
func (f *field) refilter() {
	needle := strings.ToLower(strings.TrimSpace(f.input.Value()))
	var prefix, contains []string
	for _, o := range f.options {
		lo := strings.ToLower(o)
		switch {
		case needle == "" || strings.HasPrefix(lo, needle):
			prefix = append(prefix, o)
		case strings.Contains(lo, needle):
			contains = append(contains, o)
		}
	}
	sort.Strings(prefix)
	sort.Strings(contains)
	f.matches = append(prefix, contains...)
	if f.sel >= len(f.matches) {
		f.sel = 0
	}
}

// View renders the field. A choice shows every option at once, because the point
// of a choice field is that the valid values need no looking up.
func (f *field) View(labelWidth int, focused bool) string {
	style, marker := styFieldLabel, "  "
	if focused {
		style, marker = styFieldActive, styKey.Render("▸ ")
	}
	line := marker + style.Render(pad(f.label, labelWidth, false))

	if f.kind == fieldChoice {
		var parts []string
		for i, c := range f.choices {
			if i == f.idx {
				parts = append(parts, stySelected.Render("["+c+"]"))
			} else {
				parts = append(parts, styFaint.Render(" "+c+" "))
			}
		}
		return line + strings.Join(parts, " ") + "\n"
	}
	return line + f.input.View() + "\n"
}

// Extra renders the completion list, aligned under the value it completes.
func (f *field) Extra(valueCol, limit int) string {
	if f.kind != fieldSuggest || !f.open || len(f.matches) == 0 {
		return ""
	}
	indent := strings.Repeat(" ", valueCol)
	var b strings.Builder
	shown := f.matches
	if len(shown) > limit {
		shown = shown[:limit]
	}
	for i, m := range shown {
		if i == f.sel {
			b.WriteString(indent + stySelected.Render("▸ "+m) + "\n")
			continue
		}
		b.WriteString(indent + "  " + styMuted.Render(m) + "\n")
	}
	if len(f.matches) > len(shown) {
		b.WriteString(indent + "  " + styFaint.Render("…and more") + "\n")
	}
	return b.String()
}

// Help renders the explanation under a focused field, indented to line up with
// the value rather than falling back to the left margin when it wraps.
func (f *field) Help(valueCol, width int) string {
	if f.help == "" {
		return ""
	}
	indent := strings.Repeat(" ", valueCol+2)
	return styFaint.Render(wrapIndent(f.help, max(20, width-valueCol-6), indent)) + "\n"
}

func firstFew(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	return append(append([]string{}, ss[:n]...), "…")
}
