// 服务入口：加载配置、初始化依赖、启动http(主接口 + 可选的内部后台)、优雅退出。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/billing"
	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/console"
	"github.com/FacePhys/Afu-Server-demo/internal/handler"
	"github.com/FacePhys/Afu-Server-demo/internal/keystore"
	"github.com/FacePhys/Afu-Server-demo/internal/service"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
	"github.com/FacePhys/Afu-Server-demo/web"
)

const shutdownTimeout = 10 * time.Second

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "配置文件路径")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	log, err := newLogger(cfg.Log.Level)
	if err != nil {
		return err
	}
	keys, err := keystore.Load(cfg.Keys)
	if err != nil {
		return err
	}

	db, err := store.Open(cfg.DB, log) // gorm.Open 会先ping一次，数据库不可用时直接退出
	if err != nil {
		return fmt.Errorf("连接数据库: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	svc := service.New(db, billing.Noop{}, billing.LogNotifier{Log: log}, cfg.Meter, log)

	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	servers := []*http.Server{{
		Addr:              cfg.Server.Addr,
		Handler:           handler.New(svc, keys, cfg.Server.MaxBodyBytes, log).Router(),
		ReadHeaderTimeout: cfg.Server.ReadTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       60 * time.Second,
	}}
	if cfg.Admin.Addr != "" {
		adminSrv, closeAdminDB, err := newAdminServer(cfg, db, log)
		if err != nil {
			return err
		}
		defer closeAdminDB()
		servers = append(servers, adminSrv)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, len(servers))
	for _, srv := range servers {
		go func() {
			log.Info("listening", "addr", srv.Addr)
			errCh <- srv.ListenAndServe()
		}()
	}

	var runErr error
	select {
	case err := <-errCh: // 任何一个端口起不来或异常退出，整个进程一起退出
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = err
		}
	}
	return runErr
}

// newAdminServer 创建内部数据后台；配置了 admin.db_dsn 时用单独的(只读)连接。
func newAdminServer(cfg *config.Config, mainDB *gorm.DB, log *slog.Logger) (*http.Server, func(), error) {
	db, closeDB := mainDB, func() {}
	if cfg.Admin.DBDSN != "" {
		adminDB, err := store.Open(config.DBConfig{DSN: cfg.Admin.DBDSN, MaxOpenConns: 10, MaxIdleConns: 2}, log)
		if err != nil {
			return nil, nil, fmt.Errorf("连接后台数据库: %w", err)
		}
		sqlDB, err := adminDB.DB()
		if err != nil {
			return nil, nil, err
		}
		db, closeDB = adminDB, func() { sqlDB.Close() }
	}

	var kids []int
	for _, k := range cfg.Keys {
		if k.Status == config.KeyActive {
			kids = append(kids, k.Kid)
		}
	}
	cs, err := console.New(db, cfg.Admin, cfg.Meter, kids, web.Dist(), log.With("component", "console"))
	if err != nil {
		closeDB()
		return nil, nil, err
	}
	return &http.Server{
		Addr:              cfg.Admin.Addr,
		Handler:           cs.Router(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}, closeDB, nil
}

func newLogger(level string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log.level: %w", err)
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})), nil
}
