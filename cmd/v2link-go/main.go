// Command v2link-go 是临时 VLESS 链接管理控制面的 Go 重写版。
//
// 三种运行形态（对齐需求书 §5/§7）：
//
//	v2link-go                  启动控制面（HTTP + 后台调度 + access log 采集）
//	v2link-go --static-only    只托管嵌入的前端产物（本地预览用）
//	v2link-go migrate-data …   跨库搬运（SQLite ⇄ MySQL），源库只读
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	"v2link-go/internal/mysql"
	"v2link-go/internal/reconcile"
	"v2link-go/internal/regions"
	"v2link-go/internal/scheduler"
	"v2link-go/internal/sqlite"
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
	if len(args) > 0 && args[0] == "migrate-data" {
		return runMigrateData(args[1:], logger)
	}

	staticOnly := false
	for _, arg := range args {
		switch arg {
		case "", "serve":
			// 默认形态，显式写出也接受。
		case "--static-only", "-static-only":
			staticOnly = true
		default:
			return fmt.Errorf("未知参数 %q（可用：serve、--static-only、migrate-data）", arg)
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
	if err := ensureDataDir(cfg); err != nil {
		return err
	}
	dialect, dsn, err := buildDialect(cfg)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, dialect, dsn, maxOpenConns(cfg.DBDriver))
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

// ensureDataDir 只在 SQLite 文件形态下建父目录（MySQL 无本地文件）。
func ensureDataDir(cfg *config.Config) error {
	if cfg.DBDriver != config.DBDriverSQLite {
		return nil
	}
	dir := filepath.Dir(cfg.DBPath)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	return nil
}

// buildDialect 依据配置选方言与 DSN：MySQL 未配置时根本不加载驱动、不建连接。
func buildDialect(cfg *config.Config) (store.Dialect, string, error) {
	switch cfg.DBDriver {
	case config.DBDriverMySQL:
		if cfg.DBDSN != "" {
			return mysql.New(), cfg.DBDSN, nil
		}
		return mysql.New(), mysql.BuildDSN(cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, cfg.DBPassword), nil
	case config.DBDriverSQLite:
		return sqlite.New(), sqlite.DSN(cfg.DBPath), nil
	default:
		return nil, "", fmt.Errorf("不支持的 DB_DRIVER %q", cfg.DBDriver)
	}
}

// maxOpenConns 给两种方言各设一个稳妥的连接池上限（SQLite 写锁敏感，池小些）。
func maxOpenConns(driver config.DBDriver) int {
	if driver == config.DBDriverMySQL {
		return 16
	}
	return 4
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

// runMigrateData 执行跨库搬运：--from / --to 用 `sqlite:` / `mysql:` 前缀选驱动。
func runMigrateData(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("migrate-data", flag.ContinueOnError)
	from := fs.String("from", "", "源库 DSN，前缀 sqlite: 或 mysql:")
	to := fs.String("to", "", "目标库 DSN，前缀 sqlite: 或 mysql:")
	dryRun := fs.Bool("dry-run", false, "只统计与校验，不写目标库")
	verify := fs.Bool("verify", false, "搬运后逐表比对条数与抽样行")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" || *to == "" {
		return errors.New("migrate-data 需要 --from 与 --to")
	}

	srcDialect, srcDSN, err := parseMigrateDSN(*from)
	if err != nil {
		return fmt.Errorf("--from 无效: %w", err)
	}
	dstDialect, dstDSN, err := parseMigrateDSN(*to)
	if err != nil {
		return fmt.Errorf("--to 无效: %w", err)
	}

	ctx := context.Background()
	// 源库用 OpenRaw：只连不迁移，保证只读。
	src, err := store.OpenRaw(ctx, srcDialect, srcDSN, 4)
	if err != nil {
		return fmt.Errorf("打开源库失败: %w", err)
	}
	defer func() { _ = src.Close() }()
	dst, err := store.Open(ctx, dstDialect, dstDSN, 4)
	if err != nil {
		return fmt.Errorf("打开目标库失败: %w", err)
	}
	defer func() { _ = dst.Close() }()

	report, err := store.MigrateData(ctx, src, dst, store.MigrateOptions{DryRun: *dryRun, Verify: *verify})
	if err != nil {
		return err
	}
	printMigrateReport(report, *from, *to)
	if *verify && !report.OK() {
		return errors.New("抽样/条数比对未通过")
	}
	logger.Info("migrate-data 完成")
	return nil
}

// parseMigrateDSN 解析 `sqlite:<path>` / `mysql:<go-sql-driver DSN>`，前缀决定方言。
func parseMigrateDSN(raw string) (store.Dialect, string, error) {
	switch {
	case strings.HasPrefix(raw, "sqlite:"):
		path := strings.TrimPrefix(raw, "sqlite:")
		if strings.TrimSpace(path) == "" {
			return nil, "", errors.New("sqlite DSN 需要文件路径，例如 sqlite:./data/v2link.db")
		}
		return sqlite.New(), sqlite.DSN(path), nil
	case strings.HasPrefix(raw, "mysql:"):
		dsn := strings.TrimPrefix(raw, "mysql:")
		if strings.TrimSpace(dsn) == "" {
			return nil, "", errors.New("mysql DSN 不能为空")
		}
		return mysql.New(), dsn, nil
	default:
		return nil, "", fmt.Errorf("无法识别的 DSN %q（需以 sqlite: 或 mysql: 开头）", raw)
	}
}

// printMigrateReport 打印逐表搬运与校验结果（不含任何连接串/口令）。
func printMigrateReport(report *store.MigrateReport, from, to string) {
	mode := "搬运"
	if report.DryRun {
		mode = "演练（dry-run，不写目标库）"
	}
	fmt.Printf("migrate-data %s: %s → %s\n", mode, from, to)
	var totalSrc, totalWritten int64
	for _, t := range report.Tables {
		totalSrc += t.Source
		totalWritten += t.Written
		line := fmt.Sprintf("  %-16s 源 %5d  写入 %5d", t.Table, t.Source, t.Written)
		if report.Verify {
			status := "OK"
			if len(t.Mismatches) > 0 {
				status = "不一致"
			}
			line += fmt.Sprintf("  抽样 %d  %s", t.SampleRows, status)
		}
		fmt.Println(line)
		for _, m := range t.Mismatches {
			fmt.Println("      - " + m)
		}
	}
	fmt.Printf("  合计 源 %d 行，写入 %d 行\n", totalSrc, totalWritten)
}
