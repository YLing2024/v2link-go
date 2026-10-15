// Command v2link-go 是临时 VLESS 链接管理控制面的 Go 重写版。
//
// 两种运行形态（对齐需求书 §5/§7）：
//
//	v2link-go                  启动控制面（HTTP + 后台调度 + access log 采集）
//	v2link-go --static-only    只托管嵌入的前端产物（本地预览用）
//
// 存储只用 MySQL（DB_HOST/DB_PORT/DB_NAME/DB_USER/DB_PASSWORD 或 DB_DSN）。
// 旧 SQLite 数据的一次性搬运由独立工具 tools/migrate-sqlite-to-mysql/ 完成，不在本程序内。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	v2link "v2link-go"
	"v2link-go/internal/auth"
	"v2link-go/internal/clash"
	"v2link-go/internal/config"
	"v2link-go/internal/httpapi"
	"v2link-go/internal/links"
	"v2link-go/internal/monitor"
	"v2link-go/internal/reconcile"
	"v2link-go/internal/regions"
	"v2link-go/internal/scheduler"
	"v2link-go/internal/store"
	"v2link-go/internal/xray"
)

func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败：%v\n", err)
		os.Exit(1)
	}
}

// run 解析子命令并分派；get 便于测试注入环境变量。
func run(args []string, get func(string) string) error {
	logger := newLogger()

	staticOnly := false
	for _, arg := range args {
		switch arg {
		case "", "serve":
			// 默认形态，显式写出也接受。
		case "--static-only", "-static-only":
			staticOnly = true
		default:
			return fmt.Errorf("未知参数 %q（可用：serve、--static-only）", arg)
		}
	}

	cfg, err := config.Load(get)
	if err != nil {
		return err
	}
	if staticOnly {
		return serveStaticOnly(cfg, logger)
	}
	return serve(cfg, logger)
}

// serve 起完整控制面：开库 → 装配各服务 → listen → 起后台任务 → 等退出信号。
func serve(cfg *config.Config, logger *slog.Logger) error {
	ctx := context.Background()
	db, err := store.Open(ctx, databaseDSN(cfg), maxOpenConns)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	acct := auth.NewAccounts(db, int64(cfg.SessionTtlMs))
	if cfg.AuthMode == config.AuthBuiltin {
		if _, err := acct.CleanupExpiredSessions(ctx); err != nil {
			return err
		}
		boot, err := acct.BootstrapAdmin(ctx, cfg.AdminUser, cfg.AdminPassword)
		if err != nil {
			return err
		}
		if boot.Created {
			logger.Info("[auth] 首次启动已创建管理员账号: " + cfg.AdminUser)
			if boot.Password != "" {
				printGeneratedPassword(cfg.AdminUser, boot.Password)
			}
		}
	} else {
		logger.Info("[auth] 自带口令已关闭，身份由 X-Auth-User 决定")
	}
	logger.Info("AUTH_MODE=" + string(cfg.AuthMode))

	authr := auth.NewAuthenticator(cfg.AuthMode, acct, cfg.TrustProxy)
	xrayClient := xray.New(xray.Options{
		APIAddr:    cfg.XrayAPI,
		Bin:        cfg.XrayBin,
		InboundTag: cfg.InboundTag,
		Timeout:    msDuration(cfg.XrayTimeoutMs),
		Retries:    cfg.XrayRetries,
		Logger:     logger,
	})

	linkSvc := links.New(links.Options{
		Repo:    db,
		Monitor: db,
		Xray:    xrayClient,
		Runner:  db,
		Limits:  links.Limits{DefaultHours: cfg.DefaultHours, MaxHours: cfg.MaxHours},
		Logger:  logger,
	})

	clashSvc := clash.New(clash.ServiceOptions{
		Links:         db,
		Subs:          db,
		Audit:         db,
		Q:             db.SQL,
		PublicBaseURL: cfg.PublicBaseURL,
		TTL:           msDuration(cfg.ClashSubTtlMs),
		DirectDomains: cfg.ClashDirectDomains,
		Host:          cfg.PublicHost,
		Sni:           cfg.PublicSni,
		Path:          cfg.PublicPath,
		Logger:        logger,
	})

	regionSvc := regions.New(regions.Options{
		Probes: toSpecs(config.ParseRegionProbes(cfg.RegionProbesRaw)),
		Logger: logger,
	})

	reconcileSvc := reconcile.New(reconcile.Options{Repo: db, Xray: xrayClient, Logger: logger})

	sched := scheduler.New(scheduler.Options{
		Repo:                  db,
		Monitor:               db,
		Xray:                  xrayClient,
		Tx:                    db,
		ExpireInterval:        msDuration(cfg.ExpireScanIntervalMs),
		LedgerInterval:        msDuration(cfg.LedgerIntervalMs),
		ConnCleanupInterval:   msDuration(cfg.ConnCleanupIntervalMs),
		SampleCleanupInterval: msDuration(cfg.SampleCleanupIntervalMs),
		ConnRetention:         msDuration(cfg.ConnRetentionMs),
		SampleRetention:       msDuration(cfg.SampleRetentionMs),
		RegionProbe:           regionSvc,
		RegionProbeInterval:   msDuration(cfg.RegionProbeIntervalMs),
		Reconcile:             reconcileSvc,
		ReconcileInterval:     msDuration(cfg.XrayReconcileIntervalMs),
		ClashSubs:             db,
		Logger:                logger,
	})

	tailer := monitor.NewTailer(monitor.TailerOptions{
		Path:     cfg.AccessLogPath,
		Interval: 2 * time.Second,
		ResolveLinkID: func(email string) *string {
			return resolveLinkID(ctx, db, logger, email)
		},
		Sink: func(rows []store.ConnectionInput) error {
			return db.Tx(ctx, func(q store.Querier) error {
				return db.InsertConnections(ctx, q, rows)
			})
		},
		Logger: logger,
	})

	handler := httpapi.New(httpapi.Options{
		Config:      cfg,
		Auth:        authr,
		Links:       linkSvc,
		Monitor:     db,
		RegionProbe: regionSvc,
		Reconcile:   reconcileSvc,
		Clash:       clashSvc,
		StaticFS:    v2link.DistFS(),
		Logger:      logger,
		StartedAt:   time.Now(),
	})

	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	logger.Info("v2link 控制面已启动: http://" + server.Addr + " | " + cfg.Describe())
	logger.Info("access log 采集: " + cfg.AccessLogPath)

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("服务器错误：" + err.Error())
		}
	}()

	// 后台任务在 listen 成功后启动（对齐旧实现 index.ts 的 listen 回调）。
	sched.Start()
	tailer.Start()

	return waitForShutdown(server, sched, tailer, db, logger)
}

// serveStaticOnly 只托管嵌入的前端产物（本地预览便利形态，与旧实现无关）。
func serveStaticOnly(cfg *config.Config, logger *slog.Logger) error {
	handler := httpapi.New(httpapi.Options{
		Config:     cfg,
		StaticFS:   v2link.DistFS(),
		StaticOnly: true,
		Logger:     logger,
		StartedAt:  time.Now(),
	})
	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	logger.Info("v2link 纯静态模式已启动（只托管前端产物）: http://" + server.Addr)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("服务器错误：" + err.Error())
		}
	}()
	return waitForShutdown(server, nil, nil, nil, logger)
}

// waitForShutdown 按旧实现顺序优雅退出：停调度 → 停采集 → 关 HTTP → 关库。
func waitForShutdown(server *http.Server, sched *scheduler.Scheduler, tailer *monitor.Tailer, db *store.DB, logger *slog.Logger) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	sig := <-signals
	logger.Info("收到 " + signalName(sig) + "，正在退出...")
	if sched != nil {
		sched.Stop()
	}
	if tailer != nil {
		tailer.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("关闭服务器超时：" + err.Error())
	}
	if db != nil {
		_ = db.Close()
	}
	logger.Info("已停止。")
	return nil
}

// resolveLinkID 把 access log 里的 email 关联到 links.id：先按 id 直查，再退回按 email 找 active 行。
func resolveLinkID(ctx context.Context, db *store.DB, logger *slog.Logger, email string) *string {
	row, err := db.LinkByID(ctx, email)
	if err != nil {
		logger.Warn("关联连接记录失败（按 id）：" + err.Error())
		return nil
	}
	if row == nil {
		row, err = db.FindActiveByEmail(ctx, email)
		if err != nil {
			logger.Warn("关联连接记录失败（按 email）：" + err.Error())
			return nil
		}
	}
	if row == nil {
		return nil
	}
	id := row.ID
	return &id
}

// maxOpenConns 是 MySQL 连接池上限（调度器与 HTTP 共用，取一个稳妥值）。
const maxOpenConns = 16

// databaseDSN 优先用 DB_DSN；否则由分段参数拼 go-sql-driver DSN。口令只进返回值，不落日志。
func databaseDSN(cfg *config.Config) string {
	if cfg.DBDSN != "" {
		return cfg.DBDSN
	}
	return store.BuildDSN(cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, cfg.DBPassword)
}

// toSpecs 把配置层探测端点转成 regions 服务需要的形态。
func toSpecs(probes []config.RegionProbe) []regions.Spec {
	out := make([]regions.Spec, 0, len(probes))
	for _, p := range probes {
		out = append(out, regions.Spec{Key: p.Key, Name: p.Name, Flag: p.Flag, URL: p.URL})
	}
	return out
}

// msDuration 把毫秒整数转成 time.Duration。
func msDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }

func signalName(sig os.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return "信号"
}

// newLogger 用标准库 slog 输出到 stderr。
func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// printGeneratedPassword 只打印一次随机初始口令，提示立即保存。
func printGeneratedPassword(username, password string) {
	line := strings.Repeat("=", 64)
	fmt.Println(line)
	fmt.Printf("已生成一次性初始口令（用户 %s），请立即记录并尽快修改：\n", username)
	fmt.Printf("    %s\n", password)
	fmt.Println("该口令只显示这一次，之后不会再次打印。")
	fmt.Println(line)
}
