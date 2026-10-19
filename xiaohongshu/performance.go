package xiaohongshu

import (
	"time"

	"github.com/sirupsen/logrus"
)

// 只记录分段耗时，不记录凭据和笔记内容。
func logPhase(phase string, started time.Time) {
	logrus.WithFields(logrus.Fields{"phase": phase, "elapsed_ms": time.Since(started).Milliseconds()}).Info("performance")
}
