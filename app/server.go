package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"searchan/platform"
	"searchan/reader"
	"searchan/research"
	"searchan/task"
)

type Server struct {
	Runtime  *Runtime
	Store    *task.Store
	wake     chan struct{}
	mu       sync.Mutex
	cancel   map[string]context.CancelFunc
	last     map[string]time.Time
	draining atomic.Bool
	probeMu  sync.Mutex
	probeAt  time.Time
	probeErr error
	claimMu  sync.Mutex
	active   atomic.Int64
}

func NewServer(r *Runtime) (*Server, error) {
	if len(r.Senders) == 0 {
		return nil, errors.New("configure a Telegram or QQ bot before starting the server")
	}
	if r.Telegram != nil {
		if err := r.Telegram.ValidateSecret(); err != nil {
			return nil, err
		}
	}
	if r.OneBot != nil {
		if err := r.OneBot.Validate(); err != nil {
			return nil, err
		}
	}
	if err := r.validateAccess(); err != nil {
		return nil, err
	}
	store, err := task.Open(r.Config.DataDir+"/tasks", r.Config.QueueSize)
	if err != nil {
		return nil, err
	}
	return &Server{Runtime: r, Store: store, wake: make(chan struct{}, 1), cancel: map[string]context.CancelFunc{}, last: map[string]time.Time{}}, nil
}
func (s *Server) Close() error { return s.Store.Close() }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.draining.Load() || s.probe() != nil {
			http.Error(w, "not ready", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready", "pending": s.Store.Pending(), "running": s.active.Load(), "capacity": s.Runtime.Config.QueueSize})
	})
	if s.Runtime.Telegram != nil {
		mux.HandleFunc("POST /webhooks/telegram", func(w http.ResponseWriter, r *http.Request) {
			event, err := s.Runtime.Telegram.DecodeWebhook(r)
			if err != nil {
				http.Error(w, "invalid webhook", 401)
				return
			}
			if err := s.Accept(r.Context(), event); err != nil {
				w.Header().Set("Retry-After", "2")
				http.Error(w, "unable to accept event", 503)
				return
			}
			w.WriteHeader(200)
		})
	}
	if s.Runtime.OneBot != nil {
		mux.HandleFunc("POST /webhooks/onebot", func(w http.ResponseWriter, r *http.Request) {
			event, err := s.Runtime.OneBot.DecodeWebhook(r)
			if err != nil {
				http.Error(w, "invalid webhook", 401)
				return
			}
			if err := s.Accept(r.Context(), event); err != nil {
				w.Header().Set("Retry-After", "2")
				http.Error(w, "unable to accept event", 503)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
	if s.Runtime.QQ != nil {
		mux.HandleFunc("POST /webhooks/qq", func(w http.ResponseWriter, r *http.Request) {
			event, ack, err := s.Runtime.QQ.DecodeWebhook(r)
			if err != nil {
				http.Error(w, "invalid webhook", 401)
				return
			}
			if event.ID != "" {
				if err := s.Accept(r.Context(), event); err != nil {
					w.Header().Set("Retry-After", "2")
					http.Error(w, "unable to accept event", 503)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ack)
		})
	}
	slots := make(chan struct{}, 32)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			mux.ServeHTTP(w, r)
			return
		}
		if s.draining.Load() {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "draining", 503)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			mux.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "2")
			http.Error(w, "server busy", http.StatusServiceUnavailable)
		}
	})
}

func (s *Server) Accept(ctx context.Context, event platform.Event) error {
	if s.draining.Load() {
		return task.ErrBusy
	}
	if event.ID == "" {
		return nil
	}
	cmd, ok := platform.ParseCommand(event.Text)
	if !ok {
		return nil
	}
	if event.Target.UserID == "" || event.Target.ChatID == "" || !s.Runtime.Config.Allowed(event.Target) {
		return nil
	}
	sender := s.Runtime.Senders[event.Target.Platform]
	if sender == nil {
		return errors.New("unknown platform")
	}
	switch cmd.Name {
	case "search", "ask", "deep":
		if cmd.Argument == "" {
			return s.reply(ctx, event, "请在命令后写上问题，例如：\n/search Spring Data 简介\n/search Go context 取消后 goroutine 会自动退出吗？")
		}
		if len(cmd.Argument) > 4096 {
			return s.reply(ctx, event, "问题过长，请缩短后重新发送（最多 4096 字节）。")
		}
		// Bound the limiter map too. Updates are serialized with submissions so the
		// queue's memory bound cannot be bypassed by concurrent webhook requests.
		key := event.Target.Platform + ":" + event.Target.ChatID + ":" + event.Target.UserID
		s.mu.Lock()
		now := time.Now()
		if previous := s.last[key]; now.Sub(previous) < 2*time.Second {
			reply, fresh, err := s.beginReplyLocked(event)
			s.mu.Unlock()
			if err != nil || !fresh {
				return err
			}
			return s.deliverReply(ctx, event, reply, "请求过快，请稍等两秒后重新发送问题。")
		}
		if len(s.last) >= 2048 {
			for k, t := range s.last {
				if now.Sub(t) > time.Minute {
					delete(s.last, k)
				}
			}
			if len(s.last) >= 2048 {
				s.mu.Unlock()
				return task.ErrBusy
			}
		}
		_, created, err := s.Store.SubmitLimited(event, cmd.Argument, s.Runtime.Config.MaxUserTasks, s.Runtime.Config.MaxChatTasks)
		if errors.Is(err, task.ErrQuota) || errors.Is(err, task.ErrBusy) {
			text := "已有较多搜索任务正在处理，请等待完成后重新发送。"
			if errors.Is(err, task.ErrBusy) {
				text = "搜索队列已满，请稍后重新发送。"
			}
			// Persist the rejected event before releasing submission serialization.
			// A later webhook retry must not turn this rejection into paid work.
			reply, fresh, replyErr := s.beginReplyLocked(event)
			s.mu.Unlock()
			if replyErr != nil || !fresh {
				return replyErr
			}
			return s.deliverReply(ctx, event, reply, text)
		}
		if err == nil && created {
			s.last[key] = now
		}
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if created {
			select {
			case s.wake <- struct{}{}:
			default:
			}
		}
		return nil
	case "cancel", "status", "sources":
		if cmd.Argument == "" {
			return s.reply(ctx, event, "请附上任务 ID，例如 /"+cmd.Name+" 任务ID。任务 ID 在搜索开始和结果消息中。")
		}
		rec, err := s.Store.Get(cmd.Argument)
		if err != nil || !task.SameTarget(rec.Target, event.Target) {
			return s.reply(ctx, event, "未找到本会话中的该任务，请核对任务 ID。")
		}
		text := "任务 " + rec.ID + "：" + taskStateText(rec.State)
		priority := false
		if cmd.Name == "cancel" {
			if changed, err := s.Store.CancelOnce(rec.ID, event.Target); err != nil {
				text = "无法取消该任务"
			} else {
				priority = changed
				s.mu.Lock()
				cancel := s.cancel[rec.ID]
				s.mu.Unlock()
				if cancel != nil {
					cancel()
				}
				text = "已取消任务 " + rec.ID
			}
		}
		if cmd.Name == "sources" {
			if rec.Output != nil {
				text = sourcesText(*rec.Output, rec.ID)
			} else {
				text += "\n来源尚未准备好。"
			}
		}
		return s.replyWithPriority(ctx, event, text, priority)
	case "start", "help":
		text := "发送 /search 问题 开始搜索，/deep 问题 增加检索额度。\n例：/search Spring Data 和 Spring Framework 有什么区别？\n/status 任务ID 查看状态，/sources 任务ID 查看来源，/cancel 任务ID 取消任务。\n搜索结果优先以图片发送，并附来源链接。"
		if event.Target.Platform == "telegram" && s.Runtime.Telegram != nil && s.Runtime.Telegram.Username != "" {
			text += "\n群里可发送 /search@" + s.Runtime.Telegram.Username + " 问题，明确指定本机器人。"
			text += "\n也可在消息开头发送 @" + s.Runtime.Telegram.Username + " 自然语言问题，例如：@" + s.Runtime.Telegram.Username + " Spring Data 是什么？"
			text += "\n若群里的 @提问没有回应，请改用 /search@" + s.Runtime.Telegram.Username + " 问题，并请维护者检查群消息接收设置。"
		}
		if event.Target.Platform == "qq" && s.Runtime.OneBot != nil {
			text += "\n群里也可在消息开头 @本机器人 后输入自然语言问题；只 @机器人 会显示帮助。结果以合并聊天记录发送。"
		}
		return s.reply(ctx, event, text)
	}
	return nil
}

func (s *Server) reply(ctx context.Context, event platform.Event, text string) error {
	return s.replyWithPriority(ctx, event, text, false)
}

func (s *Server) replyWithPriority(ctx context.Context, event platform.Event, text string, priority bool) error {
	// Persist command reply intent too: webhook retries must not repeat
	// external sends after a timeout or an unknown delivery outcome.
	reply, created, err := s.beginReply(event, priority)
	if err != nil || !created {
		return err
	}
	return s.deliverReply(ctx, event, reply, text)
}

func taskStateText(state string) string {
	switch state {
	case "queued":
		return "排队中"
	case "running":
		return "正在搜索和整理"
	case "ready":
		return "资料已整理，等待生成图片"
	case "sending":
		return "正在发送"
	case "completed":
		return "已完成"
	case "cancelled":
		return "已取消"
	case "failed":
		return "未能完成"
	case "uncertain":
		return "发送结果待核对，请先检查聊天记录，避免重复请求"
	default:
		return "状态待核对"
	}
}

func (s *Server) deliverReply(ctx context.Context, event platform.Event, reply task.Record, text string) error {
	sender := s.Runtime.Senders[event.Target.Platform]
	replyCtx, stop := context.WithTimeout(ctx, 4*time.Second)
	defer stop()
	for i, chunk := range textChunks(text, 3000) {
		if i > 0 {
			reply.Deliveries = append(reply.Deliveries, task.Delivery{Kind: "control", Page: i + 1, State: "sending"})
			if err := s.Store.Save(reply); err != nil {
				return err
			}
		}
		receipt, sendErr := sender.SendText(replyCtx, event.Target, chunk, i+1)
		reply.Deliveries[i].State = "sent"
		reply.Deliveries[i].Receipt = receipt.MessageID
		if sendErr != nil {
			reply.State, reply.Deliveries[i].State = "uncertain", "uncertain"
			if platform.Definite(sendErr) {
				reply.State, reply.Deliveries[i].State = "failed", "failed"
			}
			reply.Error = reader.Clip(sendErr.Error(), 512)
			slog.Warn("command delivery ended", "task", reply.ID, "state", reply.State)
			return s.Store.Save(reply)
		}
		if err := s.Store.Save(reply); err != nil {
			return err
		}
	}
	reply.State = "completed"
	return s.Store.Save(reply)
}

func (s *Server) beginReply(event platform.Event, priority bool) (task.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beginReplyWithPriorityLocked(event, priority)
}

func (s *Server) beginReplyLocked(event platform.Event) (task.Record, bool, error) {
	return s.beginReplyWithPriorityLocked(event, false)
}

func (s *Server) beginReplyWithPriorityLocked(event platform.Event, priority bool) (task.Record, bool, error) {
	key := "control:" + event.Target.Platform + ":" + event.Target.ChatID + ":" + event.Target.UserID
	now := time.Now()
	// Only a newly persisted cancellation gets priority, at most once per
	// active task. Repeated commands still share the ordinary reply limiter.
	if !priority && now.Sub(s.last[key]) < 2*time.Second {
		return task.Record{}, false, s.Store.IgnoreEvent(event)
	}
	if len(s.last) >= 2048 {
		for k, at := range s.last {
			if now.Sub(at) > time.Minute {
				delete(s.last, k)
			}
		}
		if len(s.last) >= 2048 {
			return task.Record{}, false, task.ErrBusy
		}
	}
	record, created, err := s.Store.BeginReply(event)
	if err == nil && created {
		s.last[key] = now
	}
	return record, created, err
}

func (s *Server) probe() error {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if time.Since(s.probeAt) >= 5*time.Second {
		s.probeErr = s.Store.Probe()
		s.probeAt = time.Now()
	}
	return s.probeErr
}

func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.Runtime.Config.Listen)
	if err != nil {
		return err
	}
	return s.serve(ctx, listener)
}

func (s *Server) serve(ctx context.Context, listener net.Listener) error {
	defer listener.Close()
	_ = s.Store.Prune(7 * 24 * time.Hour)
	if s.Runtime.Cache != nil {
		_ = s.Runtime.Cache.Prune()
	}
	workerCtx, stopWorkers := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWorkers()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				_ = s.Store.Prune(7 * 24 * time.Hour)
				if s.Runtime.Cache != nil {
					_ = s.Runtime.Cache.Prune()
				}
			}
		}
	}()
	for i := 0; i < s.Runtime.Config.Workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.worker(workerCtx) }()
	}
	httpServer := &http.Server{Addr: s.Runtime.Config.Listen, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(listener) }()
	slog.Info("bot server started", "listen", listener.Addr().String(), "workers", s.Runtime.Config.Workers)
	var err error
	select {
	case <-ctx.Done():
	case err = <-errCh:
	}
	s.claimMu.Lock()
	s.draining.Store(true)
	s.claimMu.Unlock()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if shutdownErr := httpServer.Shutdown(shutdown); shutdownErr != nil {
		_ = httpServer.Close()
	}
	grace := s.Runtime.Config.ShutdownTimeout
	if grace <= 0 {
		grace = 30 * time.Second
	}
	deadline := time.NewTimer(grace)
	tick := time.NewTicker(20 * time.Millisecond)
drain:
	for {
		if s.active.Load() == 0 {
			break
		}
		select {
		case <-deadline.C:
			break drain
		case <-tick.C:
		}
	}
	deadline.Stop()
	tick.Stop()
	stopWorkers()
	wg.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
func (s *Server) worker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.claimMu.Lock()
		if ctx.Err() != nil || s.draining.Load() {
			s.claimMu.Unlock()
			return
		}
		rec, ok, err := s.Store.Claim()
		if ok {
			s.active.Add(1)
		}
		s.claimMu.Unlock()
		if err != nil {
			slog.Error("task claim failed", "error", err)
		}
		if ok {
			s.process(ctx, rec)
			s.active.Add(-1)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

func (s *Server) process(parent context.Context, rec task.Record) {
	ctx, cancel := context.WithTimeout(parent, s.Runtime.Config.Timeout)
	defer cancel()
	s.mu.Lock()
	s.cancel[rec.ID] = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.cancel, rec.ID); s.mu.Unlock() }()
	if current, err := s.Store.Get(rec.ID); err != nil || current.State == "cancelled" {
		return
	}
	// A crash after acknowledging a failure reply must not restart paid work.
	if rec.Output == nil && rec.Error != "" {
		rec.State = "failed"
		_ = s.Store.Save(rec)
		return
	}
	sender := s.Runtime.Senders[rec.Target.Platform]
	send := func(kind string, page int, fn func(int) (platform.Receipt, error)) bool {
		for _, delivery := range rec.Deliveries {
			if delivery.Kind == kind && delivery.Page == page && delivery.State == "sent" {
				return true
			}
		}
		rec.State = "sending"
		rec.Deliveries = append(rec.Deliveries, task.Delivery{Kind: kind, Page: page, State: "sending"})
		idx := len(rec.Deliveries) - 1
		if err := s.Store.Save(rec); err != nil {
			return false
		}
		receipt, err := fn(idx + 1)
		if err != nil {
			rec.Deliveries[idx].State = "uncertain"
			rec.State = "uncertain"
			if platform.Definite(err) {
				rec.Deliveries[idx].State = "failed"
				rec.State = "failed"
			}
			rec.Error = err.Error()
			slog.Warn("delivery ended", "task", rec.ID, "kind", kind, "page", page, "state", rec.State)
			_ = s.Store.Save(rec)
			return false
		}
		rec.Deliveries[idx].State = "sent"
		rec.Deliveries[idx].Receipt = receipt.MessageID
		rec.State = "ready"
		return s.Store.Save(rec) == nil
	}
	fail := func(err error, stats research.Stats) {
		rec.State = "failed"
		rec.Error = err.Error()
		rec.Failure = &task.FailureDiagnostic{SearchCalls: stats.SearchCalls, PagesRead: stats.PagesRead, ModelCalls: stats.ModelCalls}
		for _, call := range stats.Calls {
			if call.ErrorCode != "" {
				if rec.Failure.SearchFailures == nil {
					rec.Failure.SearchFailures = make(map[string]int)
				}
				rec.Failure.SearchFailures[call.ErrorCode]++
			}
		}
		if s.Store.Save(rec) != nil {
			return
		}
		slog.Error("task failed", "task", rec.ID, "error", err, "validation", research.AnswerValidationCode(err), "search_calls", stats.SearchCalls, "model_calls", stats.ModelCalls, "search_failures", rec.Failure.SearchFailures)
		if ctx.Err() == nil && send("failure", 0, func(seq int) (platform.Receipt, error) {
			return sender.SendText(ctx, rec.Target, research.FailureMessage(err)+"\n任务："+rec.ID+"\n/status "+rec.ID, seq)
		}) {
			rec.State = "failed"
			_ = s.Store.Save(rec)
		}
	}
	if !send("accepted", 0, func(seq int) (platform.Receipt, error) {
		return sender.SendText(ctx, rec.Target, "开始搜索。任务："+rec.ID+"\n/cancel "+rec.ID, seq)
	}) {
		return
	}
	rec.State = "running"
	if err := s.Store.Save(rec); err != nil {
		return
	}
	if rec.Output == nil {
		researchCtx, stop := context.WithTimeout(ctx, s.Runtime.Config.ResearchTimeout)
		var output research.Output
		var err error
		if rec.Deep {
			output, err = s.Runtime.Engine.RunDeep(researchCtx, rec.Query)
		} else {
			output, err = s.Runtime.Engine.Run(researchCtx, rec.Query)
		}
		stop()
		if err != nil {
			fail(err, output.Stats)
			return
		}
		rec.Output = &output
		if err := s.Store.Save(rec); err != nil {
			return
		}
	}
	if len(rec.Artifacts) == 0 {
		artifacts, err := s.Runtime.Renderer.Render(ctx, rec.ID, *rec.Output, s.Store.Path(rec.ID)+"/images")
		if err != nil {
			rec.Output.Warnings = append(rec.Output.Warnings, "图片渲染失败，已降级文字")
			slog.Warn("render failed", "task", rec.ID, "error", err)
		} else {
			rec.Artifacts = artifacts
		}
	}
	rec.State = "ready"
	if err := s.Store.Save(rec); err != nil {
		return
	}
	// Persist intent before each send. Any unknown remote outcome ends the task
	// in 'uncertain'; it must never silently trigger a duplicate send.
	fallbackText := func() bool {
		chunks := textChunks(rec.Output.Text()+"\n任务："+rec.ID, 3000)
		for i, text := range chunks {
			if !send("text", i+1, func(seq int) (platform.Receipt, error) { return sender.SendText(ctx, rec.Target, text, seq) }) {
				return false
			}
		}
		return true
	}
	useText := len(rec.Artifacts) == 0
	legacy := false
	for _, d := range rec.Deliveries {
		// Resume old per-page deliveries without duplicating them in an album.
		if d.Kind == "image" || d.Kind == "sources" || d.Kind == "text" {
			legacy = true
		}
		if d.Kind == "image" && d.Page == 1 && d.State == "failed" {
			useText = true
		}
		if d.Kind == "report" && d.State == "failed" {
			useText = true
		}
	}
	if reportSender, ok := sender.(platform.ReportSender); ok && reportSender.SupportsReport() && !useText && !legacy {
		if !send("report", 0, func(seq int) (platform.Receipt, error) {
			return reportSender.SendReport(ctx, rec.Target, platform.Report{Images: rec.Artifacts, Output: *rec.Output, TaskID: rec.ID}, seq)
		}) {
			if rec.State != "failed" {
				return
			}
			if !fallbackText() {
				return
			}
		}
	} else if useText {
		if !fallbackText() {
			return
		}
	} else {
		imageRejected := false
		for i, image := range rec.Artifacts {
			caption := fmt.Sprintf("%s · %d/%d · %s", rec.Output.Answer.Title, i+1, len(rec.Artifacts), rec.ID)
			if !send("image", i+1, func(seq int) (platform.Receipt, error) { return sender.SendImage(ctx, rec.Target, image, caption, seq) }) {
				if rec.State == "failed" && i == 0 {
					imageRejected = true
					break
				}
				return
			}
		}
		if imageRejected {
			if !fallbackText() {
				return
			}
		} else {
			for i, text := range textChunks(sourcesText(*rec.Output, rec.ID), 3000) {
				if !send("sources", i+1, func(seq int) (platform.Receipt, error) { return sender.SendText(ctx, rec.Target, text, seq) }) {
					return
				}
			}
		}
	}
	rec.State = "completed"
	rec.Error = ""
	if err := s.Store.Save(rec); err != nil {
		slog.Error("task commit failed", "task", rec.ID, "error", err)
	} else {
		slog.Info("task completed", "task", rec.ID, "elapsed_ms", rec.Output.Stats.ElapsedMS, "search_calls", rec.Output.Stats.SearchCalls)
	}
}

func textChunks(text string, maxRunes int) []string {
	// Answers are already capped at 12 KiB. Rune chunking here avoids cutting UTF-8.
	runes := []rune(text)
	var chunks []string
	for len(runes) > 0 {
		n := min(maxRunes, len(runes))
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
}
func sourcesText(o research.Output, id string) string {
	var b strings.Builder
	b.WriteString(reader.Clip(o.Answer.Lead.Text, 600) + "\n\n")
	for _, source := range o.Sources {
		fmt.Fprintf(&b, "[%d] %s\n%s\n", source.ID, source.Title, source.URL)
	}
	fmt.Fprintf(&b, "\n任务：%s\n/status %s\n/sources %s", id, id, id)
	return b.String()
}

// Diagnostic remains local and never prints keys.
func (r *Runtime) Diagnostic() map[string]any {
	result := map[string]any{"providers": r.Config.Providers, "workers": r.Config.Workers, "queue_size": r.Config.QueueSize, "search_concurrency": r.Config.SearchConcurrency, "read_concurrency": r.Config.ReadConcurrency, "task_timeout": r.Config.Timeout.String(), "typst_binary": r.Typst.Binary, "platforms": []string{}}
	_, err := exec.LookPath(r.Typst.Binary)
	result["typst_available"] = err == nil
	if r.Telegram != nil {
		result["platforms"] = append(result["platforms"].([]string), "telegram")
	}
	if r.QQ != nil || r.OneBot != nil {
		result["platforms"] = append(result["platforms"].([]string), "qq")
	}
	return result
}
