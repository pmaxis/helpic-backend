package reports

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/pmaxis/helpic/internal/money"
)

//go:embed templates/report.html
var templateFS embed.FS

var reportTmpl = template.Must(template.New("report.html").
	Funcs(template.FuncMap{"inc": func(i int) int { return i + 1 }}).
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
	Count     int
	Prepared  string
	SentOn    string
	PaidOn    string
}

const printDate = "2 Jan 2006"

func party(p Party) viewParty {
	if p.Name == "" {
		return viewParty{Name: p.Email, Email: p.Email}
	}
	return viewParty{Name: p.Name, Email: p.Email}
}

// PeriodLabel is "1 – 31 Oct 2026", "28 Sep – 3 Oct 2026" or one day.
func PeriodLabel(r Report) string {
	from, to := r.From, r.To
	switch {
	case from.Equal(to):
		return from.Format(printDate)
	case from.Year() == to.Year() && from.Month() == to.Month():
		return fmt.Sprintf("%d – %s", from.Day(), to.Format(printDate))
	case from.Year() == to.Year():
		return from.Format("2 Jan") + " – " + to.Format(printDate)
	}
	return from.Format(printDate) + " – " + to.Format(printDate)
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

func buildView(r Report, mode RenderMode) view {
	v := view{
		ForPDF:   mode == ModePDF,
		Draft:    r.Status == StatusDraft && r.Client != nil,
		Detailed: r.Kind == KindDetailed,
		Title:    r.Title,
		Period:   PeriodLabel(r),
		Project:  r.ProjectNames(),
		From:     party(r.Freelancer),
		Count:    len(r.Items),
		Prepared: r.CreatedAt.Format(printDate),
	}
	if r.SentAt != nil {
		v.SentOn = r.SentAt.Format(printDate)
	}
	if r.PaidAt != nil {
		v.PaidOn = r.PaidAt.Format(printDate)
	}
	v.SummaryText, v.SummaryBullets = splitText(r.Summary)
	for _, it := range r.Items {
		if len(r.Projects) != 1 && it.ProjectName != "" {
			v.ShowProject = true
		}
		item := viewItem{Title: it.Title, ProjectName: it.ProjectName, Completed: it.CompletedOn.Format(printDate)}
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

// Render produces the report's HTML document.
func Render(r Report, mode RenderMode) ([]byte, error) {
	var buf bytes.Buffer
	if err := reportTmpl.Execute(&buf, buildView(r, mode)); err != nil {
		return nil, fmt.Errorf("render report: %w", err)
	}
	return buf.Bytes(), nil
}
