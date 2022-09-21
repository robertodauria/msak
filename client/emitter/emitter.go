package emitter

import (
	"github.com/robertodauria/msak/pkg/ndtm/results"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
	"go.uber.org/zap"
)

type Emitter interface {
	OnMeasurement(spec.SubtestKind, int, results.Measurement)
	OnError(spec.SubtestKind, error)
	OnStart(spec.SubtestKind, int)
	OnComplete(spec.SubtestKind, int)
}

type LogEmitter struct{}

func (e *LogEmitter) OnMeasurement(kind spec.SubtestKind, n int, m results.Measurement) {
	if m.Origin == "receiver" {
		tp := float64(m.AppInfo.NumBytes) / float64(m.AppInfo.ElapsedTime) * 8
		zap.L().Sugar().Infof("%s #%d: throughput: %f Mb/s", kind, n, tp)
	}
}

func (e *LogEmitter) OnError(kind spec.SubtestKind, err error) {
	zap.L().Sugar().Errorf("%s: error (%v)", kind, err)
}
func (e *LogEmitter) OnStart(kind spec.SubtestKind, n int) {
	zap.L().Sugar().Infof("%s: starting stream #%d", kind, n)
}
func (e *LogEmitter) OnComplete(kind spec.SubtestKind, n int) {
	zap.L().Sugar().Infof("%s: completed stream #%d", kind, n)
}
