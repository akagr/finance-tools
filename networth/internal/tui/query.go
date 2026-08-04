package tui

import (
	"fmt"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// queryScreen is the deliberate replacement for a spreadsheet's formula bar.
//
// The reason for keeping everything in a real database is that any question can
// be asked of it later, including questions this interface was never designed
// for. Hiding that behind a separate command would make it a last resort; here
// it is one keystroke away, with the built-in queries doubling as worked
// examples of how the schema fits together.
type queryScreen struct {
	m *Model

	queries []db.SavedQuery
	cursor  int

	editing  bool
	editor   textarea.Model
	result   *db.Result
	resErr   error
	ranName  string
	rowStart int
}

func newQueryScreen(m *Model) *queryScreen {
	ta := textarea.New()
	ta.Placeholder = "SELECT * FROM v_networth ORDER BY as_of DESC"
	ta.ShowLineNumbers = false
	ta.SetHeight(6)
	return &queryScreen{m: m, editor: ta}
}

func (q *queryScreen) Refresh() error {
	qs, err := q.m.store.DB.SavedQueries()
	if err != nil {
		return err
	}
	q.queries = qs
	if q.cursor >= len(q.queries) {
		q.cursor = max(0, len(q.queries)-1)
	}
	return nil
}

func (q *queryScreen) Capturing() bool { return q.editing }

func (q *queryScreen) Hints() []hint {
	if q.editing {
		return []hint{{"ctrl+r", "run"}, {"esc", "back to the list"}}
	}
	return []hint{
		{"↵", "run"}, {"e", "edit sql"}, {"n", "new"},
		{"↑↓", "select"}, {"pgup/pgdn", "scroll results"},
	}
}

func (q *queryScreen) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if q.editing {
		switch key.String() {
		case "esc":
			q.editing = false
			q.editor.Blur()
			return nil
		case "ctrl+r":
			q.run(q.editor.Value(), "(edited)")
			return nil
		}
		var cmd tea.Cmd
		q.editor, cmd = q.editor.Update(key)
		return cmd
	}

	switch key.String() {
	case "up", "k":
		if q.cursor > 0 {
			q.cursor--
		}
	case "down", "j":
		if q.cursor < len(q.queries)-1 {
			q.cursor++
		}
	case "enter":
		if q.cursor < len(q.queries) {
			sel := q.queries[q.cursor]
			q.run(sel.SQL, sel.Name)
		}
	case "e":
		if q.cursor < len(q.queries) {
			q.editor.SetValue(q.queries[q.cursor].SQL)
			q.editing = true
			q.editor.Focus()
		}
	case "n":
		q.editor.SetValue("")
		q.editing = true
		q.editor.Focus()
	case "pgdown":
		q.rowStart += 10
	case "pgup":
		q.rowStart = max(0, q.rowStart-10)
	}
	return nil
}

func (q *queryScreen) run(sql, name string) {
	q.result, q.resErr = q.m.store.DB.RunQuery(sql, 500)
	q.ranName, q.rowStart = name, 0
}

func (q *queryScreen) View(width, height int) string {
	var b strings.Builder
	if q.editing {
		b.WriteString("  " + styHeading.Render("SQL") + "\n")
		b.WriteString(q.editor.View() + "\n")
	} else {
		b.WriteString("  " + styHeading.Render("Saved queries") + "\n")
		listHeight := min(len(q.queries), max(4, height/3))
		start := 0
		if q.cursor >= listHeight {
			start = q.cursor - listHeight + 1
		}
		for i := start; i < min(len(q.queries), start+listHeight); i++ {
			item := q.queries[i]
			line := pad(item.Name, 16, false) + styFaint.Render(item.Description)
			if i == q.cursor {
				b.WriteString("  " + stySelected.Render("▸ ") + stySelected.Render(pad(item.Name, 16, false)) +
					styMuted.Render(item.Description) + "\n")
			} else {
				b.WriteString("    " + line + "\n")
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(q.resultView(width, height))
	return b.String()
}

func (q *queryScreen) resultView(width, height int) string {
	if q.resErr != nil {
		return "  " + styError.Render(q.resErr.Error()) + "\n"
	}
	if q.result == nil {
		return styMuted.Render(wrapIndent(
			"Pick a query and press enter, or press n to write your own.\n\n"+
				"Everything is queryable: v_networth, v_balances_base, v_flows, v_attribution "+
				"and the tables underneath them. The same file opens in sqlite3 or any GUI if you "+
				"would rather explore there.", min(width-4, 76), "  ")) + "\n"
	}
	if len(q.result.Rows) == 0 {
		return "  " + styMuted.Render("no rows") + "\n"
	}

	cols := q.result.Columns
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	cells := make([][]string, 0, len(q.result.Rows))
	for _, row := range q.result.Rows {
		out := make([]string, len(cols))
		for i, v := range row {
			out[i] = renderCell(v)
			widths[i] = max(widths[i], len(out[i]))
		}
		cells = append(cells, out)
	}

	// Fit to the terminal by trimming the widest columns rather than wrapping,
	// which keeps rows scannable.
	avail := width - 4 - 2*len(cols)
	total := 0
	for _, w := range widths {
		total += w
	}
	for total > avail && avail > 0 {
		widest := 0
		for i, w := range widths {
			if w > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= 6 {
			break
		}
		widths[widest]--
		total--
	}

	tcols := make([]column, len(cols))
	for i, c := range cols {
		tcols[i] = column{title: c, width: widths[i], right: isNumericCol(cells, i)}
	}

	visible := max(3, height-16)
	if q.rowStart >= len(cells) {
		q.rowStart = max(0, len(cells)-visible)
	}
	end := min(len(cells), q.rowStart+visible)

	var b strings.Builder
	head := "  " + styHeading.Render("Result")
	if q.ranName != "" {
		head += styFaint.Render("  ·  "+q.ranName) +
			styFaint.Render(fmt.Sprintf("  ·  rows %d–%d of %d", q.rowStart+1, end, len(cells)))
	}
	b.WriteString(head + "\n")
	b.WriteString(renderTable(tcols, cells[q.rowStart:end], nil, -1, false))
	return b.String()
}

// renderCell formats a raw SQL value. Scaled integers are the one case worth
// special handling: printing 12000000000000 where 1,20,000 was meant would make
// the query screen useless for money, which is most of the schema.
func renderCell(v any) string {
	switch x := v.(type) {
	case nil:
		return "—"
	case int64:
		return fmt.Sprintf("%d", x)
	case float64:
		return fmt.Sprintf("%.4g", x)
	case []byte:
		return string(x)
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func isNumericCol(rows [][]string, i int) bool {
	for _, r := range rows {
		if i >= len(r) || r[i] == "—" {
			continue
		}
		for _, c := range r[i] {
			if (c < '0' || c > '9') && c != '.' && c != '-' && c != '+' && c != 'e' {
				return false
			}
		}
	}
	return true
}
