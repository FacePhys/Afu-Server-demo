package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
)

// Open 按「GORM使用约定」初始化数据库连接。
func Open(cfg config.DBConfig, log *slog.Logger) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{
		TranslateError:         true, // 1062 → gorm.ErrDuplicatedKey
		SkipDefaultTransaction: true, // 单条写入不自动包事务，需要事务的地方显式写
		Logger: ignoreDuplicateKey{logger.New(slogWriter{log}, logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			IgnoreRecordNotFoundError: true, // 查不到安装记录是正常分支，不打错误日志
			ParameterizedQueries:      true, // 日志里不带参数值
			LogLevel:                  logger.Warn,
		})},
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	return db, nil
}

// ignoreDuplicateKey 不把唯一键冲突当错误记日志：注册时编号已存在是换号的正常分支。
type ignoreDuplicateKey struct {
	logger.Interface
}

func (l ignoreDuplicateKey) LogMode(level logger.LogLevel) logger.Interface {
	return ignoreDuplicateKey{l.Interface.LogMode(level)}
}

func (l ignoreDuplicateKey) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		err = nil // 仍按慢查询规则记录
	}
	l.Interface.Trace(ctx, begin, fc, err)
}

// slogWriter 把GORM的日志转到slog。
type slogWriter struct {
	log *slog.Logger
}

func (w slogWriter) Printf(format string, args ...any) {
	w.log.Warn(fmt.Sprintf(format, args...), "component", "gorm")
}
