# elagoht/toc

A collage plugin that gives a page's headings ids, and puts a table of contents and
a reading time where the templates ask for them.

```go
app, err := collage.New(&collage.Config{
	Plugins: []collage.Plugin{toc.New(toc.Options{})},
})
```

Requires collage v0.49.0 or later. Register it in `Config.Plugins`: it adds template
functions, which only a plugin registered there can.

## In a template

```html
<aside>{{toc}}</aside>
<p>{{readingTime}}</p>
<main>
  {{slot "content"}}
</main>
```

`{{toc}}` becomes a nested list of the page's headings:

```html
<nav class="toc" aria-label="Table of contents">
  <ol>
    <li><a href="#getting-started">Getting started</a>
      <ol><li><a href="#install">Install</a></li></ol>
    </li>
    <li><a href="#configuration">Configuration</a></li>
  </ol>
</nav>
```

and `{{readingTime}}` becomes `4 min read`. Style the list with `.toc`; it carries no
styling of its own. A page with no headings gets no table of contents at all rather
than an empty box.

## How it reads the page

Neither function knows the page when it runs — a layout calls `{{toc}}` before its
content has rendered — so each writes a placeholder, and once the page is rendered
the plugin reads it, in the `AfterRender` hook:

- **Headings** are every `h2` to `h4` inside `<main>`, or inside the whole page when
  it has no `<main>` — so a site header's heading is not in an article's contents.
  A heading's text is its text content, markup taken away.
- **Ids.** A heading without an `id` gets one made from its text: lower case,
  letters of any alphabet kept, everything between them a hyphen, and a number
  added when the page already has it — `Getting started` is `getting-started`, and
  the second one `getting-started-1`. On a Turkish page `Işık` is `ışık` and
  `İstanbul` is `istanbul`, as Turkish lowers them. A heading that has an id keeps
  it.
- **Words** are counted in the same place, leaving out scripts, styles, templates
  and SVG, and divided by `wpm`, rounded up: a page is at least a minute.

The page is tokenised with `golang.org/x/net/html` rather than parsed and written
back, so what the plugin adds is the only change: the rest of the page is served
byte for byte as the templates wrote it.

A page without placeholders still has its headings given ids, so any heading can
be linked to; `ids: false` leaves such a page alone. A page with a placeholder
always gets them, since its table of contents links to them.

### The placeholders cannot be forged

They are random for each process, like collage's own form-token marker, so text a
page shows — a comment, a search query — cannot contain one, and cannot be handed a
table of contents or have its words replaced.

### Cost

The pass runs once per render. A cached page is served as the plugin left it, so a
static or incremental page is read once, not once per reader.

## Options

| Option | Default | |
| --- | --- | --- |
| `ids` | `true` | give headings ids on pages without placeholders too |
| `minLevel`, `maxLevel` | `2`, `4` | the heading levels read; 1 to 6 |
| `wpm` | `225` | reading speed, in words a minute |
| `label` | `Table of contents` | the `<nav>`'s `aria-label` |
| `readingTime` | `{n} min read` | the reading time; `{n}` is the minutes |
| `locales` | | `label` and `readingTime` for a locale, by the page's locale |

```go
toc.New(toc.Options{
	Locales: map[string]toc.Text{
		"tr": {Label: "İçindekiler", ReadingTime: "{n} dk okuma"},
	},
})
```

## Configuration

```json
{
  "elagoht/toc": {
    "ids": true,
    "minLevel": 2,
    "maxLevel": 4,
    "wpm": 225,
    "label": "Table of contents",
    "readingTime": "{n} min read",
    "locales": {
      "tr": { "label": "İçindekiler", "readingTime": "{n} dk okuma" }
    }
  }
}
```

Levels outside 1 to 6 or reversed, a negative speed, and a `readingTime` without
`{n}` stop `collage.New`.

## Limitations

- Place `{{toc}}` where an element can go — inside `<aside>`, `<div>` — not in an
  attribute. `{{readingTime}}` is text and can go in either.
- Words are what spaces separate, so Chinese, Japanese and Thai text, written
  without spaces between words, reads as fewer, longer words than it is.
- A fragment served on its own — a fragment path, a pushed update — does not run
  the page hooks, so a placeholder in it is not replaced. Put `{{toc}}` in the page,
  not in a fragment refreshed on its own.
- Headings a script adds after the page loads are not in the table of contents.
