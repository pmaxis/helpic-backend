package i18n_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/i18n"
)

// texts are the patterns user-facing English takes in the code.
var texts = []*regexp.Regexp{
	regexp.MustCompile(`fields\["[a-z_]+"\] = "((?:[^"\\]|\\.)+)"`),
	regexp.MustCompile(`response\.Error\(w, [^,]+, [^,]+, "((?:[^"\\]|\\.)+)"`),
	regexp.MustCompile(`map\[string\]string\{"[a-z_]+": "((?:[^"\\]|\\.)+)"\}`),
	regexp.MustCompile(`i18n\.T\([^,]+,\s*"((?:[^"\\]|\\.)+)"`),
	regexp.MustCompile(`\bt\("((?:[^"\\]|\\.)+)"`),
	regexp.MustCompile(`\{\{t "([^"]+)"\}\}`),
	regexp.MustCompile(`(?m)^\s+return "([A-Z][^"]+)"$`),
}

// collect finds every user-facing text in the backend's code and templates.
func collect(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	root := filepath.Join("..", "..", "..", "internal")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".txt") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.Contains(path, string(filepath.Separator)+"i18n"+string(filepath.Separator)) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, re := range texts {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				text := strings.NewReplacer(`\n`, "\n", `\"`, `"`).Replace(m[1])
				if text != "ok" { // GET /health, for machines
					seen[text] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for text := range seen {
		out = append(out, text)
	}
	sort.Strings(out)
	return out
}

var verbs = regexp.MustCompile(`%[sdv]`)

func TestEveryTextIsTranslated(t *testing.T) {
	list := collect(t)
	if len(list) < 100 {
		t.Fatalf("found only %d texts: the patterns have gone stale", len(list))
	}
	var missing []string
	for _, text := range list {
		if !i18n.Has(text) {
			missing = append(missing, text)
			continue
		}
		en, uk := verbs.FindAllString(text, -1), verbs.FindAllString(i18n.T(i18n.UK, text), -1)
		if strings.Join(en, "") != strings.Join(uk, "") {
			t.Errorf("%q: the translation has %v where the English has %v", text, uk, en)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d texts have no Ukrainian translation:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}

func TestDefaultColumnsAreTranslated(t *testing.T) {
	for _, name := range []string{"New", "In progress", "Done", "Paid"} {
		if !i18n.Has(name) {
			t.Errorf("the default column %q has no Ukrainian name", name)
		}
	}
}

func TestLocale(t *testing.T) {
	for in, want := range map[string]string{"uk": "uk", "uk-UA": "uk", "en-GB": "en", "de": "en", "": "en"} {
		if got := i18n.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"uk-UA,uk;q=0.9,en;q=0.8": "uk", "en-US,en;q=0.9": "en", "de-DE,uk;q=0.5": "uk", "": "en", "fr": "en",
	} {
		if got := i18n.FromHeader(in); got != want {
			t.Errorf("FromHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCountsAndDates(t *testing.T) {
	for n, want := range map[int]string{1: "1 хвилина", 2: "2 хвилини", 4: "4 хвилини", 5: "5 хвилин", 11: "11 хвилин", 12: "12 хвилин", 21: "21 хвилина", 22: "22 хвилини", 25: "25 хвилин", 111: "111 хвилин"} {
		if got := i18n.Count(i18n.UK, n, "minute"); got != want {
			t.Errorf("Count(uk, %d) = %q, want %q", n, got, want)
		}
	}
	if got := i18n.Count(i18n.EN, 1, "minute") + ", " + i18n.Count(i18n.EN, 3, "minute"); got != "1 minute, 3 minutes" {
		t.Errorf("English counts = %q", got)
	}
	if got := i18n.Duration(i18n.UK, 150*time.Second); got != "3 хвилини" {
		t.Errorf("Duration(uk, 150s) = %q", got)
	}
	d := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	if got := i18n.Date(i18n.UK, d) + " / " + i18n.Date(i18n.EN, d) + " / " + i18n.Month(i18n.UK, d); got != "17 жовтня 2026 / 17 Oct 2026 / Жовтень 2026" {
		t.Errorf("dates = %q", got)
	}
}
