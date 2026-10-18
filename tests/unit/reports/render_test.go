package reports_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/pdf"
	"github.com/pmaxis/helpic/internal/reports"
)

func day(s string) time.Time {
	d, _ := time.Parse(time.DateOnly, s)
	return d
}

func sample() reports.Report {
	return reports.Report{
		Title:      "Website — Oct 2026",
		From:       day("2026-10-01"),
		To:         day("2026-10-31"),
		Status:     reports.StatusDraft,
		Freelancer: reports.Party{Name: "Olena Petrenko", Email: "olena@example.com"},
		Client:     &reports.Party{Name: "Acme Inc", Email: "hi@acme.example"},
		Summary:    "- Launched the landing page\n- Fixed the <contact> form",
		CreatedAt:  day("2026-10-31"),
		Items: []reports.Item{
			{Title: "Hero block", ProjectName: "Website", CompletedOn: day("2026-10-03")},
			{Title: "Pricing", ProjectName: "", CompletedOn: day("2026-10-12")},
		},
	}
}

func TestRender(t *testing.T) {
	html, err := reports.Render(sample(), reports.ModePDF, i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		"Website — Oct 2026", "1 – 31 Oct 2026", "Olena Petrenko",
		"Hero block", "3 Oct 2026", ">Draft<", "<th class=\"project\">Project</th>",
		"Fixed the &lt;contact&gt; form",                // user text is escaped
		"default-src 'none'",                            // the PDF loads nothing from outside
		`content: "Olena Petrenko (olena@example.com)"`, // the sender, in every page's footer
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered report lacks %q", want)
		}
	}
	if strings.Contains(s, "fonts.googleapis.com") {
		t.Error("the PDF document must not load web fonts")
	}
	if strings.Contains(s, ">From<") || strings.Contains(s, ">To<") || strings.Contains(s, "Acme Inc") {
		t.Error("the report has no From or To block")
	}

	preview, _ := reports.Render(sample(), reports.ModePreview, i18n.EN)
	if !strings.Contains(string(preview), "<span>Olena Petrenko (olena@example.com)</span>") {
		t.Error("the preview's footer lacks the sender")
	}
}

func TestRender_PersonalAndSent(t *testing.T) {
	r := sample()
	r.Client = nil
	r.Projects = []reports.ProjectRef{{ID: "p1", Name: "Portfolio"}}
	html, _ := reports.Render(r, reports.ModePreview, i18n.EN)
	s := string(html)
	if strings.Contains(s, ">To<") || strings.Contains(s, ">Draft<") {
		t.Error("a personal report has no recipient and no draft mark")
	}
	if strings.Contains(s, "<th class=\"project\">") {
		t.Error("a one-project report needs no project column")
	}

	r = sample()
	r.Status = reports.StatusSent
	sent := day("2026-11-01")
	r.SentAt = &sent
	html, _ = reports.Render(r, reports.ModePDF, i18n.EN)
	if s := string(html); strings.Contains(s, ">Draft<") || !strings.Contains(s, "sent 1 Nov 2026") {
		t.Error("a sent report shows when it was sent and no draft mark")
	}
}

func TestRender_SeveralProjects(t *testing.T) {
	r := sample()
	r.Projects = []reports.ProjectRef{{ID: "p1", Name: "Website"}, {ID: "p2", Name: "Branding"}}
	r.Items[1].ProjectName = "Branding"
	html, _ := reports.Render(r, reports.ModePDF, i18n.EN)
	s := string(html)
	if !strings.Contains(s, "<span>Website, Branding</span>") || !strings.Contains(s, `<th class="project">Project</th>`) {
		t.Error("a report on several projects names them and has a project column")
	}
}

func TestRender_Prices(t *testing.T) {
	r := sample()
	html, _ := reports.Render(r, reports.ModePDF, i18n.EN)
	if s := string(html); strings.Contains(s, ">Price<") || strings.Contains(s, ">Total<") {
		t.Error("a report without prices has no price column and no total")
	}

	price := int64(120050)
	r.Currency = "EUR"
	r.Items[0].PriceCents = &price
	html, _ = reports.Render(r, reports.ModePDF, i18n.EN)
	s := string(html)
	for _, want := range []string{">Price<", "€1,200.50", ">Total<", `<td class="price none">—</td>`} {
		if !strings.Contains(s, want) {
			t.Errorf("priced report lacks %q", want)
		}
	}
}

func TestRender_Detailed(t *testing.T) {
	r := sample()
	r.Items[0].Notes = "Built the hero block"
	r.Items[1].Notes = "- Monthly and yearly plans\n- A <b>comparison</b> table"

	html, _ := reports.Render(r, reports.ModePDF, i18n.EN)
	if strings.Contains(string(html), "Built the hero block") {
		t.Error("a basic report shows no notes")
	}

	r.Kind = reports.KindDetailed
	html, _ = reports.Render(r, reports.ModePDF, i18n.EN)
	s := string(html)
	for _, want := range []string{
		`<p class="notes">Built the hero block</p>`,
		`<ul class="notes"><li>Monthly and yearly plans</li><li>A &lt;b&gt;comparison&lt;/b&gt; table</li></ul>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("detailed report lacks %q", want)
		}
	}
}

func TestPeriodLabel(t *testing.T) {
	cases := map[[2]string]string{
		{"2026-10-01", "2026-10-31"}: "1 – 31 Oct 2026",
		{"2026-09-28", "2026-10-03"}: "28 Sep – 3 Oct 2026",
		{"2025-12-20", "2026-01-05"}: "20 Dec 2025 – 5 Jan 2026",
		{"2026-10-16", "2026-10-16"}: "16 Oct 2026",
	}
	for in, want := range cases {
		if got := reports.PeriodLabel(reports.Report{From: day(in[0]), To: day(in[1])}, i18n.EN); got != want {
			t.Errorf("periodLabel(%v) = %q, want %q", in, got, want)
		}
	}
}

// TestPDF prints a real PDF. It needs Chrome, so it's opt-in:
// HELPIC_PDF_TEST=1 go test ./tests/unit/reports/ (CHROME_PATH picks the browser).
func TestPDF(t *testing.T) {
	if os.Getenv("HELPIC_PDF_TEST") == "" {
		t.Skip("set HELPIC_PDF_TEST=1 to print a real PDF")
	}
	r := pdf.NewChrome(os.Getenv("CHROME_PATH"))
	if !r.Available() {
		t.Skip("no Chrome found")
	}
	html, err := reports.Render(sample(), reports.ModePDF, i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	file, err := r.Render(context.Background(), html)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(file, []byte("%PDF")) {
		t.Fatal("not a PDF")
	}
}

func TestRender_Ukrainian(t *testing.T) {
	html, err := reports.Render(sample(), reports.ModePreview, i18n.UK)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`lang="uk"`, "Звіт про роботу", "Виконані задачі", ">Задача<"} {
		if !bytes.Contains(html, []byte(want)) {
			t.Errorf("Ukrainian report lacks %q", want)
		}
	}
	if got := reports.PeriodLabel(reports.Report{From: day("2026-10-01"), To: day("2026-10-31")}, i18n.UK); got != "1 – 31 жовтня 2026" {
		t.Errorf("Ukrainian period = %q", got)
	}
}
