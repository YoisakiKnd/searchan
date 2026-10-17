package app

import (
	"context"
	"errors"
	"log/slog"

	"searchan/platform"
	"searchan/task"
)

func recoveryEvent(r task.Record) platform.Event {
	return platform.Event{ID: "recovery:" + r.ID, Target: r.Target}
}

func (s *Server) notifyRecoveryNotices(ctx context.Context) error {
	return s.Store.VisitRecoveryNotices(ctx, func(r task.Record) error {
		if s.draining.Load() {
			return context.Canceled
		}
		eligible := s.Runtime.Senders[r.Target.Platform] != nil && s.Runtime.Config.Allowed(r.Target)
		for _, delivery := range r.Deliveries {
			if delivery.Kind == "failure" {
				eligible = false
			}
		}
		if !eligible {
			r.RecoveryNotice = false
			return s.Store.Save(r)
		}
		event := recoveryEvent(r)
		reply, created, err := s.Store.BeginReply(event)
		if err != nil {
			return err
		}
		if created {
			// QQ's reply sequence shares the original incoming message. Do not
			// reuse the sequence of the already accepted notification.
			reply.Deliveries[0].Page = len(r.Deliveries) + 1
			if err := s.Store.Save(reply); err != nil {
				return err
			}
		}
		// The deterministic reply intent must be durable before clearing the
		// original marker. Either crash window is safe from duplicate sends.
		r.RecoveryNotice = false
		if err := s.Store.Save(r); err != nil {
			return err
		}
		if !created {
			return nil
		}
		return s.deliverReply(ctx, event, reply, "服务中断，本次搜索未能完成，请重新发送问题。\n任务："+r.ID+"\n/status "+r.ID)
	})
}

// Use one existing worker for the one-time startup scan. Other workers may
// resume queued tasks; recovery adds neither a worker nor a resident queue.
func (s *Server) recoverNotifications(ctx context.Context) {
	if !s.recoveryStarted.CompareAndSwap(false, true) {
		return
	}
	s.claimMu.Lock()
	if ctx.Err() != nil || s.draining.Load() {
		s.claimMu.Unlock()
		return
	}
	s.active.Add(1)
	s.claimMu.Unlock()
	defer s.active.Add(-1)
	if err := s.notifyRecoveryNotices(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("interrupted task notification failed", "error", err)
	}
}
