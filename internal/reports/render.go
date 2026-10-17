package reports

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/money"
)

//go:embed templates/report.html
var templateFS embed.FS

// reportTmpl is parsed once; each render clones it with "t" speaking the
// reader's language.
var reportTmpl = template.Must(template.New("report.html").
	Funcs(template.FuncMap{"inc": func(i int) int { return i + 1 }, "t": func(s string) string { return s }}).
	ParseFS(templateFS, "templates/report.html"))

// RenderMode selects what the HTML is for.
type RenderMode int

const (
	// ModePreview renders an on-screen A4 sheet and may load web fonts.
	ModePreview RenderMode = iota
	// ModePDF renders bare pages for Chromium's print-to-PDF: no external
	// resources at all (fonts come from the system).
	ModePDF
)

type viewParty struct {
	Name  string
	Email string
}

type viewItem struct {
	Title       string
	ProjectName string
	Completed   string
	// Price is "" for a task without a price.
	Price string
	// NotesText or NotesBullets say what was done, in a detailed report.
	NotesText    string
	NotesBullets []string
}

type view struct {
	// Locale is the reader's language.
	Locale string
	ForPDF bool
	Draft  bool
	// Detailed shows each task's notes under its title.
	Detailed bool
	Title    string
	Period   string
	// Project names the report's one project; empty for all projects.
	Project string
	// From is the freelancer, shown in every page's footer.
	From           viewParty
	SummaryText    string
	SummaryBullets []string
	// ShowProject adds a project column when the report spans projects.
	ShowProject bool
	// ShowPrice adds a price column and the total when any task has a price.
	ShowPrice bool
	Total     string
	Items     []viewItem
	// Completed says how many tasks: "3 tasks completed".
	Completed string
	// Footer says when it was prepared, sent and paid.
	Footer string
}

func party(p Party) viewParty {
	if p.Name == "" {
		return viewParty{Name: p.Email, Email: p.Email}
	}
	return viewParty{Name: p.Name, Email: p.Email}
}

// PeriodLabel is "1 – 31 Oct 2026", "28 Sep – 3 Oct 2026" or one day, in
// the locale ("1 – 31 жовтня 2026").
func PeriodLabel(r Report, locale string) string {
	from, to := r.From, r.To
	switch {
	case from.Equal(to):
		return i18n.Date(locale, from)
	case from.Year() == to.Year() && from.Month() == to.Month():
		return fmt.Sprintf("%d – %s", from.Day(), i18n.Date(locale, to))
	case from.Year() == to.Year():
		return i18n.DayMonth(locale, from) + " – " + i18n.Date(locale, to)
	}
	return i18n.Date(locale, from) + " – " + i18n.Date(locale, to)
}

var bulletPrefixes = []string{"- ", "* ", "• ", "– ", "— ", "-", "•"}

// splitText renders free text either as a paragraph or, when it has several
// lines or bullet markers, as a bullet list.
func splitText(s string) (paragraph string, bullets []string) {
	lines := []string{}
	marked := false
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, p := range bulletPrefixes {
			if strings.HasPrefix(line, p) {
				line = strings.TrimSpace(strings.TrimPrefix(line, p))
				marked = true
				break
			}
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "", nil
	}
	if len(lines) == 1 && !marked {
		return lines[0], nil
	}
	return "", lines
}

func buildView(r Report, mode RenderMode, locale string) view {
	t := func(text string, args ...any) string { return i18n.T(locale, text, args...) }
	v := view{
		Locale:    locale,
		ForPDF:    mode == ModePDF,
		Draft:     r.Status == StatusDraft && r.Client != nil,
		Detailed:  r.Kind == KindDetailed,
		Title:     r.Title,
		Period:    PeriodLabel(r, locale),
		Project:   r.ProjectNames(),
		From:      party(r.Freelancer),
		Completed: t("%s completed", i18n.Count(locale, len(r.Items), "task")),
		Footer:    t("Prepared %s", i18n.Date(locale, r.CreatedAt)),
	}
	if r.SentAt != nil {
		v.Footer += " · " + t("sent %s", i18n.Date(locale, *r.SentAt))
	}
	if r.PaidAt != nil {
		v.Footer += " · " + t("paid %s", i18n.Date(locale, *r.PaidAt))
	}
	v.SummaryText, v.SummaryBullets = splitText(r.Summary)
	for _, it := range r.Items {
		if len(r.Projects) != 1 && it.ProjectName != "" {
			v.ShowProject = true
		}
		item := viewItem{Title: it.Title, ProjectName: it.ProjectName, Completed: i18n.Date(locale, it.CompletedOn)}
		if it.PriceCents != nil {
			item.Price = money.Format(*it.PriceCents, r.Currency)
		}
		if v.Detailed {
			item.NotesText, item.NotesBullets = splitText(it.Notes)
		}
		v.Items = append(v.Items, item)
	}
	if total, ok := r.Total(); ok {
		v.ShowPrice = true
		v.Total = money.Format(total, r.Currency)
	}
	return v
}

// Render produces the report's HTML document in the reader's language.
func Render(r Report, mode RenderMode, locale string) ([]byte, error) {
	tmpl, err := reportTmpl.Clone()
	if err != nil {
		return nil, fmt.Errorf("clone report template: %w", err)
	}
	tmpl.Funcs(template.FuncMap{"t": func(s string) string { return i18n.T(locale, s) }})
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, buildView(r, mode, locale)); err != nil {
		return nil, fmt.Errorf("render report: %w", err)
	}
	return buf.Bytes(), nil
}
