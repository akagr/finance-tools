package tui

import "github.com/charmbracelet/lipgloss"

// The palette is deliberately small. Colour carries meaning here - gain, loss,
// warning, "this figure is not trustworthy" - and a screen that uses colour
// decoratively has none left to say anything with.
//
// Every colour is adaptive so the interface stays readable on a light terminal.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "#0550ae", Dark: "#6cb6ff"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#57606a", Dark: "#8b949e"}
	colFaint  = lipgloss.AdaptiveColor{Light: "#8c959f", Dark: "#6e7681"}
	colGain   = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	colLoss   = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	colText   = lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#e6edf3"}
)

var (
	styTitle   = lipgloss.NewStyle().Bold(true).Foreground(colText)
	styHeading = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styMuted   = lipgloss.NewStyle().Foreground(colMuted)
	styFaint   = lipgloss.NewStyle().Foreground(colFaint)
	styGain    = lipgloss.NewStyle().Foreground(colGain)
	styLoss    = lipgloss.NewStyle().Foreground(colLoss)
	styWarn    = lipgloss.NewStyle().Foreground(colWarn)
	styBig     = lipgloss.NewStyle().Bold(true).Foreground(colText)

	// styKey and styKeyDesc render the footer hints. Keeping them visually quiet
	// but always present is the main discoverability mechanism: nothing in this
	// interface should require remembering a key that is not on screen.
	styKey     = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styKeyDesc = lipgloss.NewStyle().Foreground(colMuted)

	styTab       = lipgloss.NewStyle().Padding(0, 2).Foreground(colMuted)
	styTabActive = lipgloss.NewStyle().Padding(0, 2).Bold(true).Foreground(colAccent).
			Underline(true)
	// styTabMuted marks the tabs you cannot jump to right now, because the
	// screen has the keyboard. Dimming is the cheapest honest way to say so.
	styTabMuted = lipgloss.NewStyle().Padding(0, 2).Foreground(colFaint)

	styTableHead = lipgloss.NewStyle().Bold(true).Foreground(colMuted)
	stySelected  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)

	styFieldLabel  = lipgloss.NewStyle().Foreground(colMuted)
	styFieldActive = lipgloss.NewStyle().Bold(true).Foreground(colAccent)

	styBanner = lipgloss.NewStyle().Foreground(colWarn)
	styError  = lipgloss.NewStyle().Foreground(colLoss)
)

// signStyle picks the colour that matches the direction of a change, so a
// glance is enough to read a column of numbers.
func signStyle(v int64) lipgloss.Style {
	switch {
	case v > 0:
		return styGain
	case v < 0:
		return styLoss
	}
	return styMuted
}
