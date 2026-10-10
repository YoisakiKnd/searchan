// Package demo supplies explicitly labelled offline data for rendering checks.
package demo

import (
	"searchan/research"
	"time"
)

func Output() research.Output {
	ev := []research.Evidence{
		{ID: "e1", URL: "https://pkg.go.dev/context", Title: "Go 标准库 · context", Kind: "body", Paragraph: 1, Text: "Context carries a deadline, cancellation signal, and request-scoped values. Call the cancellation function to release associated resources."},
		{ID: "e2", URL: "https://go.dev/blog/pipelines", Title: "Go Blog · Pipelines and cancellation", Kind: "body", Paragraph: 1, Text: "A cancellation signal can let the pipeline's goroutines stop early. Each stage must cooperate and select on the cancellation signal."},
	}
	answer := research.Answer{
		Title: "演示：Go 并发任务如何取消？",
		Lead:  research.Claim{Text: "为同一请求的并发任务传递同一个 context；各任务主动监听取消信号，并在调用外部服务时继续传递它。", EvidenceIDs: []string{"e1", "e2"}},
		Sections: []research.Section{
			{Heading: "让取消沿调用链传递", Claims: []research.Claim{{Text: "在入口创建带超时或可取消的 context，把它传入搜索、读取和模型调用。请求结束时调用 cancel，释放相关资源。", EvidenceIDs: []string{"e1"}}}},
			{Heading: "任务需要主动响应", Claims: []research.Claim{{Text: "取消不会强行终止 goroutine。任务应在等待和工作循环中检查 ctx.Done()，停止后释放自己持有的资源。", EvidenceIDs: []string{"e2"}}}},
		},
		Table: &research.Table{Columns: []string{"动作", "目的"}, Rows: []research.TableRow{
			{Cells: []string{"传递 context", "让下游收到同一取消信号"}, EvidenceIDs: []string{"e1"}},
			{Cells: []string{"监听 Done", "让并发任务能够提前退出"}, EvidenceIDs: []string{"e2"}},
		}},
		Uncertainties: []string{"此图使用内置演示资料，未发起实时搜索；用于检查排版、中文字体、表格和引用。"},
	}
	return research.Output{Query: "Go context 如何取消并发任务？", Answer: answer, Evidence: ev, Sources: research.Sources(answer, ev), GeneratedAt: time.Now().UTC(), Stats: research.Stats{StageMS: map[string]int64{}}}
}
