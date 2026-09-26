package toc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	toc "github.com/Elagoht/collage-toc"
	"github.com/Elagoht/collage/pkg/collage"
)

func newSite(opts toc.Options, body string, data map[string]string) (*collage.App, error) {
	app, err := collage.New(&collage.Config{
		Server:   collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: fstest.MapFS{"t/p.html": {Data: []byte(body)}}, Root: "t"},
		Locale:   collage.LocaleConfig{Default: "en", Supported: []string{"en", "tr"}},
		Plugins:  []collage.Plugin{toc.New(opts)},
	})
	if err != nil {
		return nil, err
	}
	page := collage.NewPage("p").
		WithContent(collage.NewFragment("p", "p.html").WithData(data).Build()).
		WithPath("en", "/").
		WithPath("tr", "/").
		Build()
	return app, app.RegisterPage(page)
}

func site(t *testing.T, opts toc.Options, body string) *collage.App {
	t.Helper()
	app, err := newSite(opts, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func get(app *collage.App, path string) string {
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Body.String()
}

const article = `<html><head><title>Words in the title</title></head><body>
<header><h2>Site header</h2></header>
<aside>{{toc}}</aside>
<p class="rt">{{readingTime}}</p>
<main>
<h1>The title</h1>
<h2>Getting started</h2>
<p>one two three</p>
<h3 id="kept">Install &amp; run</h3>
<h3>Getting started</h3>
<h4>Deep <em>down</em></h4>
<h5>Too deep</h5>
<h2 class="x">A &lt;tag&gt;</h2>
<h4>Skipped a level</h4>
<script>var words = "these are not counted";</script>
</main>
</body></html>`

func TestTableOfContents(t *testing.T) {
	body := get(site(t, toc.Options{}, article), "/")
	want := `<nav class="toc" aria-label="Table of contents"><ol>` +
		`<li><a href="#getting-started">Getting started</a><ol>` +
		`<li><a href="#kept">Install &amp; run</a></li>` +
		`<li><a href="#getting-started-1">Getting started</a><ol>` +
		`<li><a href="#deep-down">Deep down</a></li></ol></li></ol></li>` +
		`<li><a href="#a-tag">A &lt;tag&gt;</a><ol>` +
		`<li><a href="#skipped-a-level">Skipped a level</a></li></ol></li></ol></nav>`
	if !strings.Contains(body, "<aside>"+want+"</aside>") {
		t.Errorf("table of contents:\n%s\nwant\n%s", body, want)
	}
	for _, want := range []string{
		`<h2 id="getting-started">Getting started</h2>`,
		`<h3 id="kept">Install &amp; run</h3>`,
		`<h3 id="getting-started-1">Getting started</h3>`,
		`<h4 id="deep-down">Deep <em>down</em></h4>`,
		`<h2 id="a-tag" class="x">A &lt;tag&gt;</h2>`,
		`<h1>The title</h1>`,
		`<h5>Too deep</h5>`,
		`<header><h2>Site header</h2></header>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s\n%s", want, body)
		}
	}
	if !strings.Contains(body, `<p class="rt">1 min read</p>`) {
		t.Errorf("reading time:\n%s", body)
	}
}

// Words are counted inside <main>, scripts left out, and rounded up to the
// minute at the configured speed.
func TestReadingTime(t *testing.T) {
	words := strings.Repeat("word ", 451)
	page := `<html><body><p>` + strings.Repeat("outside ", 1000) + `</p><main><p>{{readingTime}}</p><p>` + words + `</p><script>` + strings.Repeat("x ", 1000) + `</script></main></body></html>`
	if body := get(site(t, toc.Options{}, page), "/"); !strings.Contains(body, "<p>3 min read</p>") {
		t.Errorf("451 words at 225 a minute:\n%.300s", body)
	}
	if body := get(site(t, toc.Options{WPM: 500}, page), "/"); !strings.Contains(body, "<p>1 min read</p>") {
		t.Errorf("451 words at 500 a minute:\n%.300s", body)
	}
}

// Without <main>, the body is read.
func TestBodyFallback(t *testing.T) {
	body := get(site(t, toc.Options{}, `<html><head><title>T</title></head><body>{{toc}}<h2>Only</h2></body></html>`), "/")
	if !strings.Contains(body, `<a href="#only">Only</a>`) || !strings.Contains(body, `<h2 id="only">Only</h2>`) {
		t.Errorf("body fallback:\n%s", body)
	}
}

// Turkish headings keep their letters, and lower I and İ as Turkish does on a
// Turkish page; the texts follow the locale.
func TestLocales(t *testing.T) {
	opts := toc.Options{Locales: map[string]toc.Text{"tr": {Label: "İçindekiler", ReadingTime: "{n} dk okuma"}}}
	page := `<html><body>{{toc}} {{readingTime}}<main><h2>Işık ve İstanbul</h2><h2>Çiçek, ğ ü ş ö</h2></main></body></html>`
	app := site(t, opts, page)
	body := get(app, "/tr")
	for _, want := range []string{
		`<nav class="toc" aria-label="İçindekiler">`,
		`<h2 id="ışık-ve-istanbul">`,
		`<h2 id="çiçek-ğ-ü-ş-ö">`,
		`1 dk okuma`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Turkish page lacks %s\n%s", want, body)
		}
	}
	body = get(app, "/")
	if !strings.Contains(body, `<h2 id="işık-ve-istanbul">`) || !strings.Contains(body, `aria-label="Table of contents"`) || !strings.Contains(body, "1 min read") {
		t.Errorf("English page:\n%s", body)
	}
}

// Text a page shows — whatever a visitor wrote — is never taken for a
// placeholder: the placeholders are random per process.
func TestPlaceholdersAreUnguessable(t *testing.T) {
	page := `<html><body><p>{{.Comment}}</p><div id="a">{{toc}}</div><main><h2>Heading</h2></main></body></html>`
	marker := regexp.MustCompile(`<div id="a">([^<]*)</div>`)
	raw := func(opts toc.Options) string {
		// A page with no headings shows what the placeholder became: nothing.
		app, err := newSite(opts, `<p>{{toc}}|{{readingTime}}</p>`, nil)
		if err != nil {
			t.Fatal(err)
		}
		return get(app, "/")
	}
	if body := raw(toc.Options{}); !strings.Contains(body, "<p>|1 min read</p>") {
		t.Errorf("placeholders in a page without headings:\n%s", body)
	}

	app, err := newSite(toc.Options{}, page, map[string]string{"Comment": "collage-toc-000000000000000000000000 collage-toc-"})
	if err != nil {
		t.Fatal(err)
	}
	body := get(app, "/")
	if !strings.Contains(body, "<p>collage-toc-000000000000000000000000 collage-toc-</p>") {
		t.Errorf("a visitor's text was replaced:\n%s", body)
	}
	if m := marker.FindStringSubmatch(body); m != nil {
		t.Errorf("the placeholder was not replaced: %q", m[1])
	}

	// Two processes, two markers: nothing a page learns from one works in the
	// other.
	seen := map[string]bool{}
	for range 2 {
		var got string
		app, err := collage.New(&collage.Config{
			Server: collage.ServerConfig{Host: "localhost", Port: 3000},
			Template: collage.TemplateConfig{FS: fstest.MapFS{
				"t/p.html": {Data: []byte(`{{toc}}`)},
			}, Root: "t"},
			// capture runs first, so it sees the placeholder toc then replaces.
			Plugins: []collage.Plugin{&capture{out: &got}, toc.New(toc.Options{})},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = app.RegisterPage(collage.NewPage("p").WithContent(collage.NewFragment("p", "p.html").Build()).WithPath("en", "/").Build())
		get(app, "/")
		if got == "" || seen[got] {
			t.Errorf("marker %q repeated or empty", got)
		}
		seen[got] = true
	}
}

// capture records the rendered page as the plugins after it will receive it.
type capture struct{ out *string }

func (c *capture) Name() string                                 { return "capture" }
func (c *capture) Version() string                              { return "0" }
func (c *capture) Init(_ context.Context, _ collage.Host) error { return nil }
func (c *capture) Shutdown(context.Context) error               { return nil }
func (c *capture) OnAfterRender(_ context.Context, ev *collage.AfterRenderEvent) error {
	*c.out = string(ev.HTML)
	return nil
}

// Without placeholders a page is given heading ids, and nothing else changes;
// with IDs off it is not touched at all.
func TestIDsOnly(t *testing.T) {
	page := `<!DOCTYPE html><HTML><body><main><P CLASS=x>text &amp; more<br/></P><H2 data-x='1'>Title</H2></main></body></HTML>`
	want := `<!DOCTYPE html><HTML><body><main><P CLASS=x>text &amp; more<br/></P><H2 id="title" data-x='1'>Title</H2></main></body></HTML>`
	if body := get(site(t, toc.Options{}, page), "/"); body != want {
		t.Errorf("got\n%s\nwant\n%s", body, want)
	}
	off := false
	if body := get(site(t, toc.Options{IDs: &off}, page), "/"); body != page {
		t.Errorf("IDs off changed the page:\n%s", body)
	}
	// A placeholder still gets the headings their ids: a table of contents
	// links to them.
	if body := get(site(t, toc.Options{IDs: &off}, `<main>{{toc}}<h2>A</h2></main>`), "/"); !strings.Contains(body, `<h2 id="a">A</h2>`) {
		t.Errorf("IDs off with a placeholder:\n%s", body)
	}
}

func TestLevels(t *testing.T) {
	body := get(site(t, toc.Options{MinLevel: 1, MaxLevel: 2}, `<main>{{toc}}<h1>One</h1><h2>Two</h2><h3>Three</h3></main>`), "/")
	if !strings.Contains(body, `<h1 id="one">`) || !strings.Contains(body, `<h3>Three</h3>`) || strings.Contains(body, "#three") {
		t.Errorf("levels 1 to 2:\n%s", body)
	}
}

func TestMisconfigurationStopsNew(t *testing.T) {
	for name, opts := range map[string]toc.Options{
		"levels reversed":        {MinLevel: 4, MaxLevel: 2},
		"level 7":                {MaxLevel: 7},
		"negative speed":         {WPM: -1},
		"reading time without n": {Text: toc.Text{ReadingTime: "min read"}},
		"a locale's without n":   {Locales: map[string]toc.Text{"tr": {ReadingTime: "dk"}}},
	} {
		if _, err := newSite(opts, "x", nil); err == nil {
			t.Errorf("%s: collage.New succeeded", name)
		}
	}
}

// A cached page is served as the plugin left it: the placeholder never reaches a
// reader from the cache.
func TestCachedPage(t *testing.T) {
	app, err := collage.New(&collage.Config{
		Server:   collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: fstest.MapFS{"t/p.html": {Data: []byte(`<main>{{toc}}<h2>A</h2></main>`)}}, Root: "t"},
		Cache:    collage.CacheConfig{Enabled: true, Type: "memory", DefaultTTL: time.Hour},
		Plugins:  []collage.Plugin{toc.New(toc.Options{})},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = app.RegisterPage(collage.NewPage("p").WithContent(collage.NewFragment("p", "p.html").Build()).WithPath("en", "/").Static().Build())
	first, second := get(app, "/"), get(app, "/")
	if first != second || !strings.Contains(second, `<a href="#a">A</a>`) || strings.Contains(second, "collage-toc-") {
		t.Errorf("first\n%s\nsecond\n%s", first, second)
	}
}
