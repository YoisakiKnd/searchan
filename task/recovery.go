package task

import (
	"context"
	"os"

	"searchan/internal/fsutil"
)

// VisitRecoveryNotices streams only newly interrupted research. No queue slot
// or resident task index is needed, and callbacks may write to the store.
func (s *Store) VisitRecoveryNotices(ctx context.Context, visit func(Record) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fsutil.WalkDirs(s.Dir, func(id string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validID.MatchString(id) {
			return nil
		}
		r, err := s.Get(id)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if r.State == "failed" && r.Output == nil && r.RecoveryNotice && r.Error == InterruptedResearch {
			return visit(r)
		}
		return nil
	})
}
