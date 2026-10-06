// SPDX-License-Identifier: AGPL-3.0-only

package streaming

import (
	"context"
	"fmt"
	"log/slog"

	golibrespot "github.com/devgianlu/go-librespot"
)

// slogLogger sends go-librespot's logs to slog. Its info and lower levels
// are chatty protocol detail, so they go to debug.
type slogLogger struct{ l *slog.Logger }

var _ golibrespot.Logger = slogLogger{}

func (s slogLogger) logf(level slog.Level, format string, args ...any) {
	if s.l.Enabled(context.Background(), level) {
		s.l.Log(context.Background(), level, fmt.Sprintf(format, args...))
	}
}

func (s slogLogger) Tracef(format string, args ...any) { s.logf(slog.LevelDebug-4, format, args...) }
func (s slogLogger) Debugf(format string, args ...any) { s.logf(slog.LevelDebug, format, args...) }
func (s slogLogger) Infof(format string, args ...any)  { s.logf(slog.LevelDebug, format, args...) }
func (s slogLogger) Warnf(format string, args ...any)  { s.logf(slog.LevelWarn, format, args...) }
func (s slogLogger) Errorf(format string, args ...any) { s.logf(slog.LevelError, format, args...) }

func (s slogLogger) Trace(args ...any) { s.logf(slog.LevelDebug-4, "%s", fmt.Sprint(args...)) }
func (s slogLogger) Debug(args ...any) { s.logf(slog.LevelDebug, "%s", fmt.Sprint(args...)) }
func (s slogLogger) Info(args ...any)  { s.logf(slog.LevelDebug, "%s", fmt.Sprint(args...)) }
func (s slogLogger) Warn(args ...any)  { s.logf(slog.LevelWarn, "%s", fmt.Sprint(args...)) }
func (s slogLogger) Error(args ...any) { s.logf(slog.LevelError, "%s", fmt.Sprint(args...)) }

func (s slogLogger) WithField(key string, value any) golibrespot.Logger {
	return slogLogger{s.l.With(key, value)}
}

func (s slogLogger) WithError(err error) golibrespot.Logger { return slogLogger{s.l.With("err", err)} }
