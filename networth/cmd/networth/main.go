// Command networth tracks net worth over time in a single SQLite file, and
// separates the money you added from the money your investments earned - the
// question a balance-only spreadsheet cannot answer.
//
// Running it with no arguments opens the interactive interface, which is the
// normal way to use it. The subcommands exist for scripting, for exporting, and
// for the occasional check that is easier to read in a terminal.
//
// The database is the only source of truth: financial data, preferences and the
// interface's own state all live in it. Point the tool at any file and it works;
// if the file does not exist yet, it is created.
//
// Nothing here is financial advice. Every figure is a draft to check, and the
// audit trail exists so it can be checked.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akagr/finance-tools/networth/internal/db"
	"github.com/akagr/finance-tools/networth/internal/report"
	"github.com/akagr/finance-tools/networth/internal/store"
	"github.com/akagr/finance-tools/networth/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.1.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		os.Exit(cmdUI(nil))
	}
	// Flags before any subcommand belong to the interface, which is the default
	// command: `networth --db mine.db` is the obvious way to open a file and
	// must not be an error.
	if strings.HasPrefix(args[0], "-") && args[0] != "-h" && args[0] != "--help" &&
		args[0] != "--version" {
		os.Exit(cmdUI(args))
	}
	switch args[0] {
	case "ui":
		os.Exit(cmdUI(args[1:]))
	case "report":
		os.Exit(cmdReport(args[1:]))
	case "query", "q":
		os.Exit(cmdQuery(args[1:]))
	case "accounts":
		os.Exit(cmdAccounts(args[1:]))
	case "check":
		os.Exit(cmdCheck(args[1:]))
	case "doctor":
		os.Exit(cmdDoctor(args[1:]))
	case "backup":
		os.Exit(cmdBackup(args[1:]))
	case "version", "--version":
		fmt.Println("networth " + version)
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		// A bare path is a natural way to reach for a file, so open it rather
		// than rejecting it.
		if looksLikeDatabase(args[0]) {
			os.Exit(cmdUI(append([]string{"--db", args[0]}, args[1:]...)))
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

// looksLikeDatabase reports whether an argument is plausibly a path to open.
func looksLikeDatabase(arg string) bool {
	if !strings.HasSuffix(arg, ".db") && !strings.HasSuffix(arg, ".sqlite") &&
		!strings.HasSuffix(arg, ".sqlite3") {
		return false
	}
	// An existing file is unambiguous; so is a path into a directory that
	// exists, which is how a new database gets created somewhere specific.
	if _, err := os.Stat(arg); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Dir(arg))
	return err == nil
}

func usage(w *os.File) {
	fmt.Fprintf(w, `networth — track net worth, and tell contributions apart from returns

Usage:
  networth                       open the interactive interface
  networth <command> [flags]

Commands:
  report      print a summary (text, markdown, csv or json)
  query       run a saved query, or any SQL, against the database
  accounts    list accounts
  check       report anything that makes the numbers less trustworthy
  doctor      check the database file itself
  backup      write a timestamped copy of the database
  version     print the version

Common flags:
  --db PATH   the database to use. Defaults to $NETWORTH_DB, then ./networth.db
              if it exists, then ~/.local/share/networth/networth.db.
              It is created if it does not exist.

The database is the only source of truth: your data, your settings and the
interface's own state are all in that one file. Copy it to move everything;
open someone else's and it just works.

Output is not financial advice. Check every figure before relying on it.
`)
}

// openDB resolves the database path and opens it, creating it when absent.
func openDB(path string) (*db.DB, *store.Store, bool, error) {
	resolved := db.Resolve(path)
	d, created, err := db.Open(resolved)
	if err != nil {
		return nil, nil, false, err
	}
	return d, store.New(d), created, nil
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "networth:", err)
	return 1
}

func cmdUI(args []string) int {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file to open (created if missing)")
	fs.Parse(args)

	d, s, created, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer d.Close()

	model, err := tui.New(s, d.Path, created)
	if err != nil {
		return fail(err)
	}
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		return fail(err)
	}
	return 0
}

func cmdReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	format := fs.String("format", "text", "text, md, csv or json")
	since := fs.String("since", "", "compare against this date (YYYY-MM-DD); defaults to the whole history")
	out := fs.String("out", "", "write to a file instead of standard output")
	fs.Parse(args)

	d, s, _, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer d.Close()

	data, err := buildReport(d, s, *since)
	if err != nil {
		return fail(err)
	}

	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		w = f
	}

	switch *format {
	case "text", "txt":
		err = report.Text(w, data)
	case "md", "markdown":
		err = report.Markdown(w, data)
	case "csv":
		err = report.CSV(w, data)
	case "json":
		err = report.JSON(w, data)
	default:
		return fail(fmt.Errorf("unknown format %q: want text, md, csv or json", *format))
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

func buildReport(d *db.DB, s *store.Store, since string) (report.Data, error) {
	var data report.Data
	settings, err := d.Settings()
	if err != nil {
		return data, err
	}
	sum, err := s.Summarise(since)
	if err != nil {
		return data, err
	}
	data = report.Data{Summary: sum, Settings: settings, DBPath: d.Path}
	if sum.AsOf != "" {
		if data.Balances, err = s.BalancesAt(sum.AsOf); err != nil {
			return data, err
		}
	}
	data.Attribution, err = s.Attribution()
	return data, err
}

func cmdQuery(args []string) int {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	list := fs.Bool("list", false, "list the saved queries")
	limit := fs.Int("limit", 200, "maximum rows to print")
	fs.Parse(args)

	d, _, _, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer d.Close()

	saved, err := d.SavedQueries()
	if err != nil {
		return fail(err)
	}
	rest := fs.Args()
	if *list || len(rest) == 0 {
		fmt.Println("Saved queries:")
		for _, q := range saved {
			fmt.Printf("  %-14s %s\n", q.Name, q.Description)
		}
		fmt.Println("\nRun one with `networth query <name>`, or pass SQL directly.")
		fmt.Println("The same file opens in sqlite3 if you would rather explore there.")
		return 0
	}

	sqlText := strings.Join(rest, " ")
	for _, q := range saved {
		if q.Name == rest[0] {
			sqlText = q.SQL
			break
		}
	}
	res, err := d.RunQuery(sqlText, *limit)
	if err != nil {
		return fail(err)
	}
	printResult(res)
	return 0
}

// printResult writes a query result as aligned columns, which is what a person
// reading it in a terminal wants.
func printResult(res *db.Result) {
	widths := make([]int, len(res.Columns))
	for i, c := range res.Columns {
		widths[i] = len(c)
	}
	cells := make([][]string, len(res.Rows))
	for i, row := range res.Rows {
		cells[i] = make([]string, len(row))
		for j, v := range row {
			cells[i][j] = cellString(v)
			if len(cells[i][j]) > widths[j] {
				widths[j] = len(cells[i][j])
			}
		}
	}
	var head []string
	for i, c := range res.Columns {
		head = append(head, fmt.Sprintf("%-*s", widths[i], c))
	}
	fmt.Println(strings.Join(head, "  "))
	for _, row := range cells {
		var out []string
		for j, c := range row {
			out = append(out, fmt.Sprintf("%-*s", widths[j], c))
		}
		fmt.Println(strings.Join(out, "  "))
	}
}

func cellString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

func cmdAccounts(args []string) int {
	fs := flag.NewFlagSet("accounts", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	all := fs.Bool("all", false, "include closed accounts")
	fs.Parse(args)

	d, s, _, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer d.Close()

	accts, err := s.Accounts(*all)
	if err != nil {
		return fail(err)
	}
	if len(accts) == 0 {
		fmt.Println("No accounts yet. Run `networth` to add some.")
		return 0
	}
	last, _ := s.LastSnapshots()
	settings, _ := d.Settings()
	fmt.Printf("%-30s %-10s %-4s %-12s %-16s %s\n",
		"ACCOUNT", "KIND", "CUR", "IN NETWORTH", "LATEST", "VALUED")
	for _, a := range accts {
		inNW := "no"
		if a.InNetworth {
			inNW = "yes"
		}
		latest, valued := "-", "never"
		if ls, ok := last[a.ID]; ok {
			latest, valued = ls.Balance.Format(settings.Decimals, settings.Grouping), ls.AsOf
		}
		fmt.Printf("%-30s %-10s %-4s %-12s %16s %s\n",
			a.Name, a.Kind, a.Currency, inNW, latest, valued)
	}
	return 0
}

func cmdCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	fs.Parse(args)

	d, _, _, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer d.Close()

	issues, err := d.Check()
	if err != nil {
		return fail(err)
	}
	if len(issues) == 0 {
		fmt.Println("No problems found.")
		return 0
	}
	errCount := 0
	for _, i := range issues {
		fmt.Printf("%-8s %-14s %s\n", i.Severity, i.Check, i.Detail)
		if i.Severity == "error" {
			errCount++
		}
	}
	// Errors mean a reported figure is wrong, not merely uncertain, so the exit
	// status reflects that for anything running this on a schedule.
	if errCount > 0 {
		return 1
	}
	return 0
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	fs.Parse(args)

	d, s, created, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer d.Close()

	fmt.Printf("file            %s\n", d.Path)
	if fi, err := os.Stat(d.Path); err == nil {
		fmt.Printf("size            %.1f KB\n", float64(fi.Size())/1024)
	}
	if created {
		fmt.Println("state           newly created")
	}
	fmt.Printf("format          %s\n", d.Meta("format"))
	fmt.Printf("schema version  %s\n", d.Meta("schema_version"))
	fmt.Printf("written by      networth %s\n", d.Meta("app_version"))
	fmt.Printf("needs at least  networth %s\n", d.Meta("min_app_version"))
	fmt.Printf("money scale     %s (%s decimal places)\n", d.Meta("scale"), d.Meta("scale_places"))

	if integrity, err := d.Integrity(); err == nil {
		fmt.Printf("integrity       %s\n", integrity)
	}

	accts, _ := s.Accounts(true)
	vals, _ := s.ValuationDates()
	fmt.Printf("accounts        %d\n", len(accts))
	fmt.Printf("valuations      %d\n", len(vals))
	if len(vals) > 0 {
		fmt.Printf("history         %s to %s\n", vals[0], vals[len(vals)-1])
	}

	issues, _ := d.Check()
	fmt.Printf("issues          %d\n", len(issues))
	return 0
}

func cmdBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	out := fs.String("out", "", "where to write the copy; defaults to a timestamped file in backups/")
	keep := fs.Int("keep", 10, "how many automatic backups to retain")
	fs.Parse(args)

	d, _, _, err := openDB(*dbPath)
	if err != nil {
		return fail(err)
	}
	defer d.Close()

	if *out != "" {
		if err := d.Backup(*out); err != nil {
			return fail(err)
		}
		fmt.Println("wrote", *out)
		return 0
	}
	path, err := d.AutoBackup(*keep)
	if err != nil {
		return fail(err)
	}
	fmt.Println("wrote", path)
	fmt.Printf("keeping the %d most recent in %s\n", *keep, filepath.Dir(path))
	return 0
}
