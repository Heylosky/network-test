package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"os"
)

const (
	mode        = "pro"              //模式
	filename    = "/onett_logs"      //日志存放路径
	level       = zapcore.DebugLevel //日志级别
	max_size    = 200                //最大存储大小，MB
	max_age     = 30                 //最大存储时间
	max_backups = 7                  //备份数量
)

func getLogWriter(filename string, maxSize, maxBackup, maxAge int) zapcore.WriteSyncer {
	//使用lumberjack分割归档日志
	lumberLackLogger := &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    maxSize,
		MaxAge:     maxAge,
		MaxBackups: maxBackup,
	}
	return zapcore.AddSync(lumberLackLogger)
}

func getEncoder() zapcore.Encoder {
	//使用一份官方预定义的production的配置，然后更改
	encoderConfig := zap.NewProductionEncoderConfig()
	//默认时间格式是这样的: "ts":1670214777.9225469 | EpochTimeEncoder serializes a time.Time to a floating-point number of seconds
	//重新设置时间格式
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	//重新设置时间字段的key
	encoderConfig.TimeKey = "time"
	//默认的level是小写的zapcore.LowercaseLevelEncoder ｜ "level":"info" 可以改成大写
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	//"caller":"zap/zap.go:90" 也可以改成Full的更加详细
	encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder
	return zapcore.NewJSONEncoder(encoderConfig)
}

func InitLogger() (err error) {
	//创建核心三大件，进行初始化
	//NewCore(enc Encoder, ws WriteSyncer, enab LevelEnabler)
	writerSyncer := getLogWriter(filename, max_size, max_backups, max_age)
	encoder := getEncoder()

	//创建核心
	var core zapcore.Core
	//如果是dev模式，同时要在前端打印；如果是其他模式，就只输出到文件
	if mode == "dev" {
		//使用默认的encoder配置就行了
		//NewConsoleEncoder里面实际上就是一个NewJSONEncoder，需要输入配置
		consoleEncoder := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
		//Tee方法将全部日志条目复制到两个或多个底层核心中
		core = zapcore.NewTee(
			zapcore.NewCore(encoder, writerSyncer, level),                   //写入到文件的核心
			zapcore.NewCore(consoleEncoder, zapcore.Lock(os.Stdout), level), //写到前台的核心
		)
	} else {
		core = zapcore.NewCore(encoder, writerSyncer, level)
	}

	//创建logger对象
	//New方法返回logger，非自定义的情况下就是NewProduction, NewDevelopment,NewExample或者config就可以了。
	//zap.AddCaller是个option，会添加上调用者的文件名和行数，到日志里
	logger := zap.New(core, zap.AddCaller())
	zap.ReplaceGlobals(logger)

	// return logger 如果return了logger就可以使用之前的ginzap.Ginzap和ginzap.RecoveryWithZap。
	return
}
