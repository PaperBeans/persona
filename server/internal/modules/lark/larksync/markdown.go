package larksync

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Docx block types (docx/v1 block_type) the renderer knows.
const (
	blockPage           = 1
	blockText           = 2
	blockHeading1       = 3 // … blockHeading1+8 is heading9
	blockBullet         = 12
	blockOrdered        = 13
	blockCode           = 14
	blockQuote          = 15
	blockTodo           = 17
	blockCallout        = 19
	blockDivider        = 22
	blockFile           = 23
	blockImage          = 27
	blockTable          = 31
	blockTableCell      = 32
	blockQuoteContainer = 34
)

// textKeys are the JSON fields that hold a text-like block's elements, by block type.
var textKeys = map[int]string{
	blockPage: "page", blockText: "text", blockBullet: "bullet", blockOrdered: "ordered", blockCode: "code",
	blockQuote: "quote", blockTodo: "todo",
	blockHeading1: "heading1", blockHeading1 + 1: "heading2", blockHeading1 + 2: "heading3", blockHeading1 + 3: "heading4",
	blockHeading1 + 4: "heading5", blockHeading1 + 5: "heading6", blockHeading1 + 6: "heading7", blockHeading1 + 7: "heading8",
	blockHeading1 + 8: "heading9",
}

// docxBlock is one block of GET /open-apis/docx/v1/documents/{id}/blocks; fields holds the rest of its JSON, where
// the payload sits under a key named for its type.
type docxBlock struct {
	BlockID   string   `json:"block_id"`
	ParentID  string   `json:"parent_id"`
	Children  []string `json:"children"`
	BlockType int      `json:"block_type"`
	fields    map[string]json.RawMessage
}

func (b *docxBlock) UnmarshalJSON(raw []byte) error {
	type plain docxBlock
	var p plain
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	*b = docxBlock(p)
	return json.Unmarshal(raw, &b.fields)
}

// docxText is a text-like block's payload.
type docxText struct {
	Elements []struct {
		TextRun *struct {
			Content string `json:"content"`
			Style   struct {
				Bold          bool `json:"bold"`
				Italic        bool `json:"italic"`
				Strikethrough bool `json:"strikethrough"`
				InlineCode    bool `json:"inline_code"`
				Link          *struct {
					URL string `json:"url"`
				} `json:"link"`
			} `json:"text_element_style"`
		} `json:"text_run"`
		MentionUser *struct {
			UserID string `json:"user_id"`
		} `json:"mention_user"`
		MentionDoc *struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"mention_doc"`
		Equation *struct {
			Content string `json:"content"`
		} `json:"equation"`
	} `json:"elements"`
	Style struct {
		Done bool `json:"done"`
	} `json:"style"`
}

// docxRender renders one document's blocks.
type docxRender struct {
	byID map[string]*docxBlock
	seen map[string]bool // a block rendered once even if the tree lists it twice
}

// renderDocx turns a document's blocks into Markdown: headings, paragraphs, bulleted, numbered and to-do lists, code,
// quotes and callouts, dividers and tables (each cell as plain text). Images and files are a placeholder; other
// blocks render their children.
func renderDocx(docID string, blocks []docxBlock) string {
	d := &docxRender{byID: map[string]*docxBlock{}, seen: map[string]bool{}}
	var roots []string
	for i := range blocks {
		b := &blocks[i]
		if b.BlockID != "" {
			d.byID[b.BlockID] = b
		}
	}
	var page *docxBlock
	if p := d.byID[docID]; p != nil && p.BlockType == blockPage {
		page = p
	}
	for i := range blocks {
		b := &blocks[i]
		if page == nil && b.BlockType == blockPage {
			page = b
		}
		if _, ok := d.byID[b.ParentID]; !ok && b.BlockType != blockPage {
			roots = append(roots, b.BlockID)
		}
	}
	if page != nil {
		d.seen[page.BlockID] = true
		roots = append(append([]string(nil), page.Children...), roots...)
	}
	var w mdWriter
	d.children(&w, roots, 0)
	return w.String()
}

func (d *docxRender) children(w *mdWriter, ids []string, depth int) {
	ordinal := 0
	for _, id := range ids {
		b := d.byID[id]
		if b == nil || d.seen[id] {
			continue
		}
		if b.BlockType == blockOrdered {
			ordinal++
		} else {
			ordinal = 0
		}
		d.block(w, b, depth, ordinal)
	}
}

func (d *docxRender) block(w *mdWriter, b *docxBlock, depth, ordinal int) {
	d.seen[b.BlockID] = true
	indent := strings.Repeat("  ", depth)
	text := d.text(b)
	switch t := b.BlockType; {
	case t >= blockHeading1 && t <= blockHeading1+8:
		if text != "" {
			w.block(strings.Repeat("#", min(t-blockHeading1+1, 6))+" "+text, false)
		}
		d.children(w, b.Children, depth)
	case t == blockBullet, t == blockOrdered, t == blockTodo:
		marker := "- "
		switch t {
		case blockOrdered:
			marker = strconv.Itoa(max(ordinal, 1)) + ". "
		case blockTodo:
			marker = "- [ ] "
			if d.payload(b).Style.Done {
				marker = "- [x] "
			}
		}
		w.block(indent+marker+text, true)
		d.children(w, b.Children, depth+1)
	case t == blockText:
		if text != "" {
			w.block(indent+text, depth > 0)
		}
		d.children(w, b.Children, depth)
	case t == blockCode:
		w.block("```\n"+d.plain(b)+"\n```", false)
	case t == blockQuote:
		if text != "" {
			w.block(quote(text), false)
		}
	case t == blockCallout, t == blockQuoteContainer:
		var inner mdWriter
		d.children(&inner, b.Children, 0)
		if s := inner.String(); s != "" {
			w.block(quote(s), false)
		}
	case t == blockDivider:
		w.block("---", false)
	case t == blockImage:
		w.block(indent+"[image]", depth > 0)
	case t == blockFile:
		w.block(indent+"[file]", depth > 0)
	case t == blockTable:
		if tbl := d.table(b); tbl != "" {
			w.block(strings.TrimRight(tbl, "\n"), false)
		}
	default:
		d.children(w, b.Children, depth)
	}
}

// table renders a table block's cells (row by row) as a Markdown table, each cell's content as one line.
func (d *docxRender) table(b *docxBlock) string {
	var tbl struct {
		Cells    []string `json:"cells"`
		Property struct {
			ColumnSize int `json:"column_size"`
		} `json:"property"`
	}
	if raw, ok := b.fields["table"]; ok {
		_ = json.Unmarshal(raw, &tbl)
	}
	cells := tbl.Cells
	if len(cells) == 0 {
		cells = b.Children
	}
	cols := tbl.Property.ColumnSize
	if cols <= 0 || len(cells) == 0 {
		return ""
	}
	var rows [][]any
	for i, id := range cells {
		if i%cols == 0 {
			rows = append(rows, make([]any, 0, cols))
		}
		text := ""
		if cell := d.byID[id]; cell != nil {
			d.seen[id] = true
			var inner mdWriter
			d.children(&inner, cell.Children, 0)
			text = strings.Join(strings.Fields(inner.String()), " ")
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], text)
	}
	for _, id := range b.Children {
		d.seen[id] = true
	}
	return markdownTable(rows)
}

func (d *docxRender) payload(b *docxBlock) docxText {
	var t docxText
	if raw, ok := b.fields[textKeys[b.BlockType]]; ok {
		_ = json.Unmarshal(raw, &t)
	}
	return t
}

// text is a text-like block's inline Markdown.
func (d *docxRender) text(b *docxBlock) string {
	var s strings.Builder
	for _, e := range d.payload(b).Elements {
		switch {
		case e.TextRun != nil:
			c := e.TextRun.Content
			st := e.TextRun.Style
			// Markers wrap the run's words; the spaces around them stay outside ("**now** ", not "**now **").
			lead, trail := c[:len(c)-len(strings.TrimLeftFunc(c, unicode.IsSpace))], c[len(strings.TrimRightFunc(c, unicode.IsSpace)):]
			if core := strings.TrimSpace(c); core != "" {
				c = core
				switch {
				case st.InlineCode:
					c = "`" + c + "`"
				case st.Bold:
					c = "**" + c + "**"
				case st.Italic:
					c = "*" + c + "*"
				case st.Strikethrough:
					c = "~~" + c + "~~"
				}
				if st.Link != nil && st.Link.URL != "" {
					u, err := url.QueryUnescape(st.Link.URL)
					if err != nil {
						u = st.Link.URL
					}
					c = "[" + c + "](" + u + ")"
				}
				c = lead + c + trail
			}
			s.WriteString(c)
		case e.MentionDoc != nil:
			u, err := url.QueryUnescape(e.MentionDoc.URL)
			if err != nil {
				u = e.MentionDoc.URL
			}
			s.WriteString("[" + cmpString(e.MentionDoc.Title, u) + "](" + u + ")")
		case e.MentionUser != nil:
			s.WriteString("@user")
		case e.Equation != nil:
			s.WriteString("$" + strings.TrimSpace(e.Equation.Content) + "$")
		}
	}
	return strings.TrimSpace(s.String())
}

// plain is a block's text without inline Markdown (a code block's lines).
func (d *docxRender) plain(b *docxBlock) string {
	var s strings.Builder
	for _, e := range d.payload(b).Elements {
		if e.TextRun != nil {
			s.WriteString(e.TextRun.Content)
		}
	}
	return strings.TrimRight(s.String(), "\n")
}

// mdWriter joins blocks with a blank line, and consecutive list items with a line break.
type mdWriter struct {
	b        strings.Builder
	lastList bool
}

func (w *mdWriter) block(s string, list bool) {
	if w.b.Len() > 0 {
		if list && w.lastList {
			w.b.WriteString("\n")
		} else {
			w.b.WriteString("\n\n")
		}
	}
	w.b.WriteString(s)
	w.lastList = list
}

func (w *mdWriter) String() string { return w.b.String() }

func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

// markdownTable renders rows (the first is the header) as a Markdown table, without trailing empty rows and
// columns. Nothing when every cell is empty.
func markdownTable(rows [][]any) string {
	var cells [][]string
	width := 0
	for _, row := range rows {
		line := make([]string, len(row))
		for i, v := range row {
			line[i] = cellText(v)
		}
		cells = append(cells, line)
		for i := len(line) - 1; i >= 0; i-- {
			if line[i] != "" {
				width = max(width, i+1)
				break
			}
		}
	}
	for len(cells) > 0 && allEmpty(cells[len(cells)-1]) {
		cells = cells[:len(cells)-1]
	}
	if width == 0 || len(cells) == 0 {
		return ""
	}
	var b strings.Builder
	for i, row := range cells {
		b.WriteString("|")
		for c := range width {
			v := ""
			if c < len(row) {
				v = row[c]
			}
			b.WriteString(" " + escapeCell(v) + " |")
		}
		b.WriteString("\n")
		if i == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
		}
	}
	return b.String()
}

// recordsTable renders bitable records as a Markdown table, the fields' names (sorted) as the header.
func recordsTable(rows []map[string]any) string {
	names := map[string]bool{}
	for _, r := range rows {
		for k := range r {
			names[k] = true
		}
	}
	if len(names) == 0 {
		return ""
	}
	cols := sortedKeys(names)
	header := make([]any, len(cols))
	for i, c := range cols {
		header[i] = c
	}
	table := [][]any{header}
	for _, r := range rows {
		line := make([]any, len(cols))
		for i, c := range cols {
			line[i] = r[c]
		}
		table = append(table, line)
	}
	return markdownTable(table)
}

// cellText is a sheet cell's or a bitable field's value as plain text: rich-text segments joined, options and people
// listed, links by their text.
func cellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return formatNumber(x)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		segments := len(x) > 0
		var parts []string
		for _, e := range x {
			m, ok := e.(map[string]any)
			if _, hasText := m["text"]; !ok || !hasText {
				segments = false
			}
			if s := cellText(e); s != "" {
				parts = append(parts, s)
			}
		}
		if segments {
			return strings.Join(parts, "") // one rich text in segments
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		for _, k := range []string{"text", "name", "full_address", "link", "en_name", "value"} {
			if s := cellText(x[k]); s != "" {
				return s
			}
		}
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	return strings.ReplaceAll(s, "\n", "<br>")
}

func allEmpty(row []string) bool {
	for _, c := range row {
		if c != "" {
			return false
		}
	}
	return true
}

// truncatedMark ends a body cut at its bound.
const truncatedMark = "\n\n…"

// bound cuts s to at most n bytes on a character boundary, marking the cut.
func bound(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len(truncatedMark)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncatedMark
}
