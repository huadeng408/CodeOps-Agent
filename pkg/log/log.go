// Package log contains logging helpers.
package log

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Keep logging safe before application startup (and in small package tests).
// Init replaces this no-op logger with the configured logger during startup.
var sugar = zap.NewNop().Sugar()

// Init initializes the zap logger.
func Init(level, format, outputPath string) {
	var err error
	var logger *zap.Logger
	var zapConfig zap.Config

	logLevel := zap.NewAtomicLevel()
	if err := logLevel.UnmarshalText([]byte(level)); err != nil {
		logLevel.SetLevel(zap.InfoLevel)
	}

	encoding := "json"
	if format == "console" {
		encoding = "console"
	}

	if format == "console" {
		zapConfig = zap.NewDevelopmentConfig()
		zapConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		zapConfig = zap.NewProductionConfig()
	}

	zapConfig.Level = logLevel
	zapConfig.Encoding = encoding
	zapConfig.OutputPaths = []string{"stdout"}
	if outputPath != "" {
		_ = os.MkdirAll(outputPath, os.ModePerm)
		zapConfig.OutputPaths = append(zapConfig.OutputPaths, outputPath+"/app.log")
	}

	logger, err = zapConfig.Build()
	if err != nil {
		panic(err)
	}

	sugar = logger.Sugar()
}

func Info(msg string)                                { sugar.Info(msg) }
func Infof(template string, args ...interface{})     { sugar.Infof(template, args...) }
func Infow(msg string, keysAndValues ...interface{}) { sugar.Infow(msg, keysAndValues...) }
func Warnf(template string, args ...interface{})     { sugar.Warnf(template, args...) }
func Error(msg string, err error)                    { sugar.Errorw(msg, "error", err) }
func Errorf(template string, args ...interface{})    { sugar.Errorf(template, args...) }
func Fatal(msg string, err error)                    { sugar.Fatalw(msg, "error", err) }
func Fatalf(template string, args ...interface{})    { sugar.Fatalf(template, args...) }
func Sync()                                          { _ = sugar.Sync() }
