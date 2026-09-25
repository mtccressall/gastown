package daemon

import "log"

// NewForTest and CheckpointWorktreeForTest exist so the END-TO-END path can be
// tested from internal/cmd: the dog writes the ref, and `gt prime` must tell a
// fresh session it is there. internal/cmd imports internal/daemon, so the test
// can only live on that side, and it needs a way in.
//
// A unit test on either half alone cannot catch the failure that matters here —
// a checkpoint written correctly and announced nowhere is indistinguishable
// from work that was lost.
func NewForTest(logger *log.Logger) *Daemon { return &Daemon{logger: logger} }

func (d *Daemon) CheckpointWorktreeForTest(workDir, rigName, polecatName string) bool {
	return d.checkpointWorktree(workDir, rigName, polecatName)
}
