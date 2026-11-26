package writers

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

// The day recap's merge (kb/day_recap_merge), rendered directly: the known meetings of every kind, a long day left
// partial, a chunk with no summary, entries with and without points' speakers, and actions with and without deadlines.
func TestGoldenDayRecapMerge(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 11, h, m, 0, 0, goldenZone) }
	day := prompts.KBDayRecapDay{Pinned: "示例用户 在示例公司做发布", Title: "出差", Day: "2026-11-11", Weekday: "周三",
		Zone: "Asia/Shanghai", Language: "zh-CN", Part: 2}
	in := prompts.KBDayRecapMergeInput{Day: day, Partial: true, Meetings: []prompts.KBDayMeeting{
		{N: 1, Kind: KindRecording, Title: "设计评审", From: at(9, 0), To: at(9, 45)},
		{N: 2, Kind: KindMeeting, Title: "周会（视频）", From: at(14, 0)},
		{N: 3, Kind: KindEvent, Title: "午饭约", From: at(12, 0)},
	}, Chunks: []prompts.KBDayRecapChunk{
		{From: "09:01", To: "09:44", Summary: "评审导出的上限", Entries: []prompts.KBDayRecapEntry{
			{FromLine: 12, ToLine: 40, Kind: "meeting", Title: "导出评审", Points: []prompts.KBMinutesNote{
				{Line: 15, Text: "导出超过 4 MB 的条目会被丢掉", Who: "Ada"}, {Line: 31, Text: "这一版先保留上限"}}},
		}, Actions: []prompts.KBMinutesNote{{Line: 38, Text: "补上回归测试", Who: "Bo", By: "周五前", DueDate: "2026-11-13"}}},
		{From: "12:03", To: "12:40", Entries: []prompts.KBDayRecapEntry{
			{FromLine: 90, ToLine: 120, Kind: "outside", Title: "午饭", Points: []prompts.KBMinutesNote{{Line: 95, Text: "聊到下周出差"}}},
		}, Actions: []prompts.KBMinutesNote{{Line: 101, Text: "订周一的机票"}}},
	}}
	golden(t, "day_recap_merge.system", prompts.RenderKBDayRecapMerge(prompts.KBOwner{Owner: "示例用户"}))
	golden(t, "day_recap_merge.user", prompts.RenderKBDayRecapMergeInput(in))
	in.Meetings, in.Partial, in.Day = nil, false, prompts.KBDayRecapDay{Day: "2026-11-11", Weekday: "周三", Zone: "Asia/Shanghai"}
	golden(t, "day_recap_merge.no_meetings.user", prompts.RenderKBDayRecapMergeInput(in))
}
