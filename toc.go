// Package toc is a collage plugin that gives a page's headings ids, and puts a
// table of contents and a reading time where the templates ask for them.
//
//	app, err := collage.New(&collage.Config{
//		Plugins: []collage.Plugin{toc.New(toc.Options{})},
//	})
//
// A template places them:
//
//	<aside>{{toc}}</aside>
//	<p>{{readingTime}}</p>
//	<main>…</main>
//
// Neither function knows the page when it runs — a layout calls {{toc}} before
// its content has rendered — so each writes a placeholder, and once the page is
// rendered the plugin reads it: every h2 to h4 inside <main> (or <body>, without
// one) gets an id if it has none, the table of contents is made from them, and
// the reading time from the words around them. The placeholders are random per
// process, so text a page shows cannot be mistaken for one.
package toc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/Elagoht/collage/pkg/collage"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Name is the plugin's name, and the key its configuration is found under.
const Name = "elagoht/toc"

// Options configures the plugin.
type Options struct {
	// IDs gives headings ids on every page, not only on those with a
	// placeholder, so any heading can be linked to. On by default.
	IDs *bool `json:"ids"`
	// MinLevel and MaxLevel are the heading levels read. Default 2 and 4: the
	// h1 is the page's title, and below h4 a table of contents is a list of
	// everything.
	MinLevel int `json:"minLevel"`
	MaxLevel int `json:"maxLevel"`
	// WPM is the reading speed, in words a minute. Default 225.
	WPM int `json:"wpm"`
	// Text is what the table of contents and the reading time say.
	Text
	// Locales replaces Text for a locale: {"tr": {Label: "İçindekiler",
	// ReadingTime: "{n} dk okuma"}}. A field left empty keeps Text's.
	Locales map[string]Text `json:"locales"`
}

// Text is what the plugin writes on a page.
type Text struct {
	// Label names the table of contents to assistive technology: the <nav>'s
	// aria-label. Default "Table of contents".
	Label string `json:"label"`
	// ReadingTime is the reading time, "{n}" standing for the minutes. Default
	// "{n} min read".
	ReadingTime string `json:"readingTime"`
}

// Plugin reads the headings.
type Plugin struct {
	opts       Options
	ids        bool
	tocMarker  []byte
	timeMarker []byte
}

// The hooks the plugin means to implement: a misspelt method would otherwise
// be a hook that silently never fires.
var (
	_ collage.Plugin          = (*Plugin)(nil)
	_ collage.Configurer      = (*Plugin)(nil)
	_ collage.AfterRenderHook = (*Plugin)(nil)
)

// New returns a plugin with opts as its starting point, which the application's
// own configuration is then decoded over.
func New(opts Options) *Plugin { return &Plugin{opts: opts} }

func (p *Plugin) Name() string                   { return Name }
func (p *Plugin) Version() string                { return "0.1.5" }
func (p *Plugin) Shutdown(context.Context) error { return nil }

// Configure reads the configuration and adds {{toc}} and {{readingTime}}.
func (p *Plugin) Configure(_ context.Context, host collage.ConfigHost) error {
	var err error
	if p.opts, err = collage.PluginConfig(host, p.opts); err != nil {
		return err
	}
	o := &p.opts
	p.ids = o.IDs == nil || *o.IDs
	if o.MinLevel == 0 {
		o.MinLevel = 2
	}
	if o.MaxLevel == 0 {
		o.MaxLevel = 4
	}
	if o.MinLevel < 1 || o.MaxLevel > 6 || o.MinLevel > o.MaxLevel {
		return fmt.Errorf("toc: levels %d to %d: they must be 1 to 6, the first no greater than the second", o.MinLevel, o.MaxLevel)
	}
	if o.WPM == 0 {
		o.WPM = 225
	}
	if o.WPM < 0 {
		return fmt.Errorf("toc: wpm %d: a reading speed is positive", o.WPM)
	}
	if o.Label == "" {
		o.Label = "Table of contents"
	}
	if o.ReadingTime == "" {
		o.ReadingTime = "{n} min read"
	}
	if !strings.Contains(o.ReadingTime, "{n}") {
		return fmt.Errorf("toc: readingTime %q has no {n} for the minutes", o.ReadingTime)
	}
	for locale, text := range o.Locales {
		if text.ReadingTime != "" && !strings.Contains(text.ReadingTime, "{n}") {
			return fmt.Errorf("toc: locales.%s.readingTime %q has no {n} for the minutes", locale, text.ReadingTime)
		}
	}

	// Random per process, like collage's own forgery marker: a page showing text
	// a visitor wrote cannot contain it, and so cannot be handed a table of
	// contents in the middle of a comment.
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Errorf("toc: %w", err)
	}
	token := hex.EncodeToString(raw[:])
	p.tocMarker = []byte("collage-toc-" + token)
	p.timeMarker = []byte("collage-reading-time-" + token)
	toc, readingTime := string(p.tocMarker), string(p.timeMarker)
	if err := host.AddTemplateFunc("toc", func() string { return toc }); err != nil {
		return err
	}
	return host.AddTemplateFunc("readingTime", func() string { return readingTime })
}

// Init checks the plugin was configured.
func (p *Plugin) Init(context.Context, collage.Host) error {
	if p.tocMarker == nil {
		return errors.New("toc: register the plugin in Config.Plugins, where Configure runs; {{toc}} needs it")
	}
	return nil
}

// text is what the plugin writes on a page in locale.
func (p *Plugin) text(locale string) Text {
	t := p.opts.Text
	if l, ok := p.opts.Locales[locale]; ok {
		if l.Label != "" {
			t.Label = l.Label
		}
		if l.ReadingTime != "" {
			t.ReadingTime = l.ReadingTime
		}
	}
	return t
}

// OnAfterRender gives the page's headings ids and fills its placeholders.
//
// It runs once per render; a cached page keeps what it wrote.
func (p *Plugin) OnAfterRender(_ context.Context, ev *collage.AfterRenderEvent) error {
	hasToc := bytes.Contains(ev.HTML, p.tocMarker)
	hasTime := bytes.Contains(ev.HTML, p.timeMarker)
	if !hasToc && !hasTime && !p.ids {
		return nil
	}
	page := p.read(ev.HTML, ev.Locale)

	var edits []edit
	for i := range page.headings {
		h := &page.headings[i]
		if h.newID {
			edits = append(edits, edit{at: h.idAt, insert: ` id="` + html.EscapeString(h.id) + `"`})
		}
	}
	text := p.text(ev.Locale)
	if hasToc {
		nav := p.nav(page.headings, text.Label)
		edits = append(edits, markers(ev.HTML, p.tocMarker, nav)...)
	}
	if hasTime {
		minutes := (page.words + p.opts.WPM - 1) / p.opts.WPM
		if minutes < 1 {
			minutes = 1
		}
		label := html.EscapeString(strings.ReplaceAll(text.ReadingTime, "{n}", strconv.Itoa(minutes)))
		edits = append(edits, markers(ev.HTML, p.timeMarker, label)...)
	}
	if len(edits) == 0 {
		return nil
	}
	ev.HTML = apply(ev.HTML, edits)
	return nil
}

// heading is one heading of the page.
type heading struct {
	level int
	id    string
	hasID bool // an id attribute, even an empty one: never given a second
	newID bool
	idAt  int // where an id attribute goes: just after the tag's name
	text  string
}

type page struct {
	headings []heading
	words    int
}

// read tokenises the page — rather than parsing it and writing it back, so that
// only what the plugin adds changes — and finds its headings and counts its
// words, inside <main>, or anywhere when there is none.
func (p *Plugin) read(src []byte, locale string) page {
	used := map[string]bool{}
	hasMain := false
	z := html.NewTokenizer(bytes.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, hasAttr := z.TagName()
		if atom.Lookup(name) == atom.Main {
			hasMain = true
		}
		for hasAttr {
			var key, val []byte
			key, val, hasAttr = z.TagAttr()
			if string(key) == "id" {
				used[string(val)] = true
			}
		}
	}

	var (
		out     page
		offset  int
		inMain  int
		skip    int // depth inside elements whose text is not read
		current *heading
		label   strings.Builder
	)
	z = html.NewTokenizer(bytes.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		start := offset
		raw := z.Raw()
		offset += len(raw)
		tok := z.Token()
		switch tt {
		case html.StartTagToken:
			switch tok.DataAtom {
			case atom.Main:
				inMain++
			case atom.Head, atom.Script, atom.Style, atom.Template, atom.Noscript, atom.Svg, atom.Textarea:
				skip++
			}
			if level := headingLevel(tok.DataAtom); level >= p.opts.MinLevel && level <= p.opts.MaxLevel &&
				current == nil && skip == 0 && (inMain > 0 || !hasMain) {
				id, hasID := attr(tok, "id")
				out.headings = append(out.headings, heading{level: level, id: id, hasID: hasID, idAt: start + 1 + len(tok.Data)})
				current = &out.headings[len(out.headings)-1]
				label.Reset()
			}
		case html.EndTagToken:
			switch tok.DataAtom {
			case atom.Main:
				if inMain > 0 {
					inMain--
				}
			case atom.Head, atom.Script, atom.Style, atom.Template, atom.Noscript, atom.Svg, atom.Textarea:
				if skip > 0 {
					skip--
				}
			}
			if current != nil && headingLevel(tok.DataAtom) == current.level {
				current.text = strings.Join(strings.Fields(label.String()), " ")
				current = nil
			}
		case html.TextToken:
			if skip > 0 || (hasMain && inMain == 0) {
				continue
			}
			text := strings.ReplaceAll(strings.ReplaceAll(tok.Data, string(p.tocMarker), " "), string(p.timeMarker), " ")
			out.words += len(strings.Fields(text))
			if current != nil {
				label.WriteString(text)
			}
		}
	}

	for i := range out.headings {
		h := &out.headings[i]
		if h.hasID {
			continue
		}
		base := slugify(h.text, locale)
		if base == "" {
			base = "section"
		}
		id := base
		for n := 1; used[id]; n++ {
			id = base + "-" + strconv.Itoa(n)
		}
		used[id] = true
		h.id, h.newID = id, true
	}
	return out
}

func headingLevel(a atom.Atom) int {
	switch a {
	case atom.H1:
		return 1
	case atom.H2:
		return 2
	case atom.H3:
		return 3
	case atom.H4:
		return 4
	case atom.H5:
		return 5
	case atom.H6:
		return 6
	}
	return 0
}

func attr(tok html.Token, key string) (string, bool) {
	for _, a := range tok.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// nav is the table of contents: a list nested as the headings are, one level of
// list per level of heading however many levels a heading skips. A heading with
// an empty id cannot be linked to and is left out; a page with no headings has no
// table of contents, rather than an empty box.
func (p *Plugin) nav(all []heading, label string) string {
	var headings []heading
	for _, h := range all {
		if h.id != "" {
			headings = append(headings, h)
		}
	}
	if len(headings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<nav class="toc" aria-label="` + html.EscapeString(label) + `">`)
	var levels []int
	for _, h := range headings {
		switch {
		case len(levels) == 0 || h.level > levels[len(levels)-1]:
			b.WriteString("<ol><li>")
			levels = append(levels, h.level)
		default:
			for len(levels) > 1 && h.level < levels[len(levels)-1] && h.level <= levels[len(levels)-2] {
				b.WriteString("</li></ol>")
				levels = levels[:len(levels)-1]
			}
			b.WriteString("</li><li>")
			levels[len(levels)-1] = h.level
		}
		b.WriteString(`<a href="#` + html.EscapeString(h.id) + `">` + html.EscapeString(h.text) + `</a>`)
	}
	for range levels {
		b.WriteString("</li></ol>")
	}
	b.WriteString("</nav>")
	return b.String()
}

// edit is one change to the page: at an offset, remove n bytes and insert text.
type edit struct {
	at, remove int
	insert     string
}

// markers is an edit replacing every occurrence of marker in src.
func markers(src, marker []byte, with string) []edit {
	var out []edit
	for i := 0; ; {
		j := bytes.Index(src[i:], marker)
		if j < 0 {
			return out
		}
		out = append(out, edit{at: i + j, remove: len(marker), insert: with})
		i += j + len(marker)
	}
}

func apply(src []byte, edits []edit) []byte {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].at < edits[j].at })
	var out bytes.Buffer
	out.Grow(len(src) + 256)
	last := 0
	for _, e := range edits {
		if e.at < last {
			continue // overlapping: cannot happen with markers and tag names, but never corrupt the page
		}
		out.Write(src[last:e.at])
		out.WriteString(e.insert)
		last = e.at + e.remove
	}
	out.Write(src[last:])
	return out.Bytes()
}

// slugify makes an id of text: lower case, letters and digits of any alphabet
// kept, everything between them a single hyphen. For Turkish and Azerbaijani, "I"
// is "ı" and "İ" is "i", as those languages lower them; in any other locale "İ"
// is "i" rather than an "i" with a combining dot above.
func slugify(text, locale string) string {
	turkic := locale == "tr" || locale == "az" || strings.HasPrefix(locale, "tr-") || strings.HasPrefix(locale, "az-")
	var b strings.Builder
	hyphen := false
	for _, r := range text {
		switch {
		case r == 'İ':
			r = 'i'
		case r == 'I' && turkic:
			r = 'ı'
		}
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(unicode.ToLower(r))
		case unicode.Is(unicode.Mn, r):
			// A combining mark belongs to the letter before it; an id without it
			// reads the same.
		default:
			hyphen = true
		}
	}
	return b.String()
}
