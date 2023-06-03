package log

import (
	"sync"

	"go.uber.org/zap"
)

func Logger() *zap.SugaredLogger {
	return zap.S()
}

var (
	//nolint:gochecknoglobals
	onceInitLoggerDefaults sync.Once
)

//nolint:gochecknoinits
func init() {
	onceInitLoggerDefaults.Do(func() {
		// Initialize default logger, can be overwritten later
		devLogger, err := zap.NewDevelopment()
		if err != nil {
			panic(err)
		}
		zap.ReplaceGlobals(devLogger)

		//runtime.SetFinalizer(logger, func(l *zap.Logger) {
		//	l.Sync()
		//})
	})
}
