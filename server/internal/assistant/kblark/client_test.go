package kblark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/larkcli"
)

// stubCLI writes a fake lark-cli that answers each command with the given JSON and exits 0; fail, when set, is what it
// prints to stderr and exits 1 with.
func stubCLI(t *testing.T, search, detail, inspect, docs string, fail string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\n"
	if fail != "" {
		script += "echo '" + fail + "' >&2\nexit 1\n"
	} else {
		script += `case "$1 $2" in
"minutes +search") cat <<'JSON'
` + search + `
JSON
;;
"minutes +detail") cat <<'JSON'
` + detail + `
JSON
;;
"docs +search") cat <<'JSON'
` + docs + `
JSON
;;
"drive +inspect") cat <<'JSON'
` + inspect + `
JSON
;;
*) echo "unexpected: $@" >&2; exit 2 ;;
esac
`
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func testClient(t *testing.T, bin string) *Client {
	t.Helper()
	return NewClient(larkcli.New(larkcli.Config{Bin: bin, Timeout: 5 * time.Second}), time.FixedZone("CST", 8*3600))
}

func TestClientSearchMinutesAndDetail(t *testing.T) {
	bin := stubCLI(t,
		`{"ok":true,"data":{"items":[
{"token":"t1","display_info":"标题一\n&lt;b&gt;关键词:&lt;/b&gt; 甲，乙\n所有者: 我 开始时间: 2026.11.17 20:01:31 时长: 47 分","meta_data":{"app_link":"https://x.larkoffice.com/minutes/t1","description":"所有者: 我 开始时间: 2026.11.17 20:01:31 时长: 47 分"}},
{"token":"t2","display_info":"标题二","meta_data":{"app_link":"https://x.larkoffice.com/minutes/t2","description":"开始时间: 2026.11.18 09:05:00"}}
],"has_more":false,"page_token":""}}`,
		`{"ok":true,"data":{"minutes":[{"title":"标题一","artifacts":{"summary":"一句摘要","todos":[{"content":"做甲"},{"content":"做乙"}],"chapters":[{"title":"开场"},{"title":"讨论"}]}}]}}`,
		`{"ok":true,"data":{"title":"文档标题","token":"d1","url":"https://x.larkoffice.com/docx/d1","type":"docx"}}`,
		`{"ok":true,"data":{"results":[{"result_meta":{"token":"d2","url":"https://x.larkoffice.com/docx/d2","update_time_iso":"2026-11-20T09:52:58Z"},"title_highlighted":"<em>表情</em>分析"}]}}`,
		"")
	c := testClient(t, bin)
	ctx := context.Background()

	minutes, err := c.SearchMinutes(ctx, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(minutes) != 2 {
		t.Fatalf("minutes %+v", minutes)
	}
	want := time.Date(2026, 11, 17, 20, 1, 31, 0, time.FixedZone("CST", 8*3600))
	if minutes[0].Title != "标题一" || minutes[0].URL != "https://x.larkoffice.com/minutes/t1" || !minutes[0].Start.Equal(want) {
		t.Fatalf("minute %+v", minutes[0])
	}
	if minutes[1].Title != "标题二" || minutes[1].Start.Hour() != 9 {
		t.Fatalf("minute %+v", minutes[1])
	}

	d, err := c.MinuteDetail(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Summary != "一句摘要" || len(d.Todos) != 2 || len(d.Chapters) != 2 || d.Title != "标题一" {
		t.Fatalf("detail %+v", d)
	}

	title, err := c.InspectDoc(ctx, "https://x.larkoffice.com/docx/d1")
	if err != nil || title != "文档标题" {
		t.Fatalf("inspect %q %v", title, err)
	}

	docs, err := c.SearchDocs(ctx, "表情")
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs %+v %v", docs, err)
	}
	if docs[0].Token != "d2" || docs[0].Title != "表情分析" || docs[0].At.IsZero() {
		t.Fatalf("doc %+v", docs[0])
	}
}

func TestClientFailsWhenNotLoggedIn(t *testing.T) {
	bin := stubCLI(t, "", "", "", "", `{"error":{"type":"authentication","subtype":"not_logged_in"}}`)
	c := testClient(t, bin)
	_, err := c.SearchMinutes(context.Background(), time.Now().AddDate(0, 0, -1), time.Now())
	if err == nil {
		t.Fatal("no error")
	}
	if kind := larkcli.KindOf(err); kind != larkcli.KindNotLoggedIn {
		t.Fatalf("kind %q (%v)", kind, err)
	}
	var adapterErr *larkcli.Error
	if !errors.As(err, &adapterErr) {
		t.Fatalf("not a larkcli error: %T", err)
	}
}
