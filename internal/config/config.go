// Package config 负责 v2link-go 的环境变量解析与校验。
//
// 口径逐项对齐旧实现 server/src/config.ts：键名、默认值、取值域、非法 AUTH_MODE 回落
// builtin、未知键忽略不报错。差异只有存储：新版只支持 MySQL（DB_HOST/DB_PORT/DB_NAME/
// DB_USER/DB_PASSWORD 或一条 DB_DSN，见 docs/DB.md）。
// 本包只读环境变量，不碰任何文件；PUBLIC_HOST/PUBLIC_PATH/PUBLIC_SNI 与旧实现一样，
// 不进入校验面板，直接按同名环境变量读取。
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// AuthMode 管理端认证模式：builtin（自带账号会话）/ sso（只认前置网关注入的 X-Auth-User）。
type AuthMode string

const (
	// AuthBuiltin 自带账号 + 登录页 + 会话 Cookie（默认）。
	AuthBuiltin AuthMode = "builtin"
	// AuthSSO 关掉自带口令，身份只看 X-Auth-User。
	AuthSSO AuthMode = "sso"
)

// RegionProbe 地区连通性探测端点（key 唯一标识）。
type RegionProbe struct {
	Key  string
	Name string
	Flag string
	URL  string
}

// DefaultRegionProbes 是内置探测端点；语义与旧实现 config.ts 的 REGION_PROBES 一致。
// 私有地址零硬编码：端点一律走 HTTPS 公网域名。
var DefaultRegionProbes = []RegionProbe{
	{Key: "us", Name: "美国", Flag: "🇺🇸", URL: "https://www.gstatic.com/generate_204"},
	{Key: "eu", Name: "欧洲", Flag: "🇪🇺", URL: "https://www.bbc.com/"},
	{Key: "jp", Name: "日本", Flag: "🇯🇵", URL: "https://www.yahoo.co.jp/"},
	{Key: "sg", Name: "新加坡", Flag: "🇸🇬", URL: "https://www.cloudflare.com/"},
}

// Config 运行时配置快照（时间字段统一为毫秒，与旧实现一致）。
type Config struct {
	Host string
	Port int

	XrayAPI       string
	XrayBin       string
	InboundTag    string
	XrayTimeoutMs int
	XrayRetries   int

	DefaultHours int
	MaxHours     int

	ExpireScanIntervalMs    int
	LedgerIntervalMs        int
	XrayReconcileIntervalMs int

	AccessLogPath           string
	ConnCleanupIntervalMs   int
	ConnRetentionMs         int
	SampleCleanupIntervalMs int
	SampleRetentionMs       int

	RegionProbesRaw       string
	RegionProbeIntervalMs int

	// MySQL 连接参数（新版只支持 MySQL）。
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string
	DBDSN      string

	ClashSubTtlMs      int
	PublicBaseURL      string
	ClashDirectDomains []string

	AuthMode      AuthMode
	AdminUser     string
	AdminPassword string
	SessionTtlMs  int

	TrustProxy TrustProxy

	// 生成 vless:// 的公开地址（同旧实现 PUBLIC_HOST/PUBLIC_PATH/PUBLIC_SNI）。
	PublicHost string
	PublicPath string
	PublicSni  string
}

// ConfigError 配置错误，Message 可直接展示给运维（不含敏感值）。
type ConfigError struct{ Message string }

func (e *ConfigError) Error() string { return e.Message }

// Load 从环境变量解析配置。get 便于测试注入。
func Load(get func(string) string) (*Config, error) {
	port, err := pickInt(get("PORT"), "PORT", 19245, 1, 65535)
	if err != nil {
		return nil, err
	}
	host := trimOr(get("HOST"), "127.0.0.1")
	timeoutS, err := pickInt(get("XRAY_API_TIMEOUT_S"), "XRAY_API_TIMEOUT_S", 3, 1, 30)
	if err != nil {
		return nil, err
	}
	retries, err := pickInt(get("XRAY_API_RETRIES"), "XRAY_API_RETRIES", 2, 0, 5)
	if err != nil {
		return nil, err
	}
	defaultHours, err := pickInt(get("DEFAULT_HOURS"), "DEFAULT_HOURS", 24, 1, 24*30)
	if err != nil {
		return nil, err
	}
	maxHours, err := pickInt(get("MAX_HOURS"), "MAX_HOURS", 720, 1, 24*365)
	if err != nil {
		return nil, err
	}
	expireS, err := pickInt(get("EXPIRE_SCAN_INTERVAL_S"), "EXPIRE_SCAN_INTERVAL_S", 15, 3, 3600)
	if err != nil {
		return nil, err
	}
	ledgerS, err := pickInt(get("LEDGER_INTERVAL_S"), "LEDGER_INTERVAL_S", 30, 5, 3600)
	if err != nil {
		return nil, err
	}
	reconcileS, err := pickInt(get("XRAY_RECONCILE_INTERVAL_S"), "XRAY_RECONCILE_INTERVAL_S", 60, 0, 3600)
	if err != nil {
		return nil, err
	}
	connCleanupS, err := pickInt(get("CONN_CLEANUP_INTERVAL_S"), "CONN_CLEANUP_INTERVAL_S", 3600, 60, 86400)
	if err != nil {
		return nil, err
	}
	connRetentionS, err := pickInt(get("CONN_RETENTION_S"), "CONN_RETENTION_S", 3600*24*7, 3600, 3600*24*30)
	if err != nil {
		return nil, err
	}
	sampleCleanupS, err := pickInt(get("SAMPLE_CLEANUP_INTERVAL_S"), "SAMPLE_CLEANUP_INTERVAL_S", 86400, 60, 86400)
	if err != nil {
		return nil, err
	}
	sampleRetentionS, err := pickInt(get("SAMPLE_RETENTION_S"), "SAMPLE_RETENTION_S", 3600*24*30, 3600, 3600*24*365)
	if err != nil {
		return nil, err
	}
	regionProbeS, err := pickInt(get("REGION_PROBE_INTERVAL_S"), "REGION_PROBE_INTERVAL_S", 300, 60, 3600)
	if err != nil {
		return nil, err
	}
	clashSubTtlS, err := pickInt(get("CLASH_SUB_TTL_S"), "CLASH_SUB_TTL_S", 600, 1, 24*3600)
	if err != nil {
		return nil, err
	}
	sessionTtlH, err := pickInt(get("SESSION_TTL_HOURS"), "SESSION_TTL_HOURS", 12, 1, 24*30)
	if err != nil {
		return nil, err
	}

	authMode := AuthBuiltin
	switch strings.TrimSpace(get("AUTH_MODE")) {
	case "sso":
		authMode = AuthSSO
	case "builtin", "":
		authMode = AuthBuiltin
	default:
		// zod .catch('builtin')：非法值回落 builtin，不报错。
		authMode = AuthBuiltin
	}

	db, err := loadDB(get)
	if err != nil {
		return nil, err
	}

	return &Config{
		Host:                    host,
		Port:                    port,
		XrayAPI:                 trimOr(get("XRAY_API"), "127.0.0.1:8081"),
		XrayBin:                 trimOr(get("XRAY_BIN"), "xray"),
		InboundTag:              trimOr(get("XRAY_INBOUND_TAG"), "vless-in"),
		XrayTimeoutMs:           timeoutS * 1000,
		XrayRetries:             retries,
		DefaultHours:            defaultHours,
		MaxHours:                maxHours,
		ExpireScanIntervalMs:    expireS * 1000,
		LedgerIntervalMs:        ledgerS * 1000,
		XrayReconcileIntervalMs: reconcileS * 1000,
		AccessLogPath:           trimOr(get("ACCESS_LOG_PATH"), "/var/log/xray/access.log"),
		ConnCleanupIntervalMs:   connCleanupS * 1000,
		ConnRetentionMs:         connRetentionS * 1000,
		SampleCleanupIntervalMs: sampleCleanupS * 1000,
		SampleRetentionMs:       sampleRetentionS * 1000,
		RegionProbesRaw:         strings.TrimSpace(get("REGION_PROBES")),
		RegionProbeIntervalMs:   regionProbeS * 1000,
		DBHost:                  db.host,
		DBPort:                  db.port,
		DBName:                  db.name,
		DBUser:                  db.user,
		DBPassword:              db.password,
		DBDSN:                   db.dsn,
		ClashSubTtlMs:           clashSubTtlS * 1000,
		PublicBaseURL:           trimOr(get("PUBLIC_BASE_URL"), "https://v2.example.com"),
		ClashDirectDomains:      ParseDirectDomains(get("CLASH_DIRECT_DOMAINS")),
		AuthMode:                authMode,
		AdminUser:               trimOr(get("V2LINK_ADMIN_USER"), "admin"),
		AdminPassword:           strings.TrimSpace(get("V2LINK_ADMIN_PASSWORD")),
		SessionTtlMs:            sessionTtlH * 3600 * 1000,
		TrustProxy:              ParseTrustProxy(get("TRUST_PROXY")),
		PublicHost:              trimOr(get("PUBLIC_HOST"), "v2.example.com"),
		PublicPath:              trimOr(get("PUBLIC_PATH"), "/v2ws"),
		PublicSni:               strings.TrimSpace(get("PUBLIC_SNI")),
	}, nil
}

type dbOptions struct {
	host     string
	port     int
	name     string
	user     string
	password string
	dsn      string
}

// loadDB 解析 MySQL 连接参数。给了 DB_DSN 就整串生效；否则由分段字段拼装，
// 其中 DB_HOST/DB_PORT 默认本机（127.0.0.1:3306），DB_NAME/DB_USER 必填。
func loadDB(get func(string) string) (dbOptions, error) {
	if dsn := strings.TrimSpace(get("DB_DSN")); dsn != "" {
		return dbOptions{dsn: dsn}, nil
	}
	name := strings.TrimSpace(get("DB_NAME"))
	user := strings.TrimSpace(get("DB_USER"))
	if name == "" || user == "" {
		return dbOptions{}, &ConfigError{Message: "MySQL 需要 DB_HOST/DB_NAME/DB_USER（或一条 DB_DSN）"}
	}
	port, err := pickInt(get("DB_PORT"), "DB_PORT", 3306, 1, 65535)
	if err != nil {
		return dbOptions{}, err
	}
	return dbOptions{
		host:     trimOr(get("DB_HOST"), "127.0.0.1"),
		port:     port,
		name:     name,
		user:     user,
		password: get("DB_PASSWORD"),
	}, nil
}

// ParseDirectDomains 解析 CLASH_DIRECT_DOMAINS：逗号或空白分隔，去前导点、转小写、去重、保序。
func ParseDirectDomains(raw string) []string {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' }) {
		domain := strings.ToLower(strings.TrimLeft(strings.TrimSpace(part), "."))
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		out = append(out, domain)
	}
	return out
}

// ParseRegionProbes 解析 REGION_PROBES（JSON 数组）。格式非法或为空 → 回落内置列表。
// REST 探测是增强功能，配错不应让服务起不来，与旧实现 parseRegionProbes 一致。
func ParseRegionProbes(raw string) []RegionProbe {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultRegionProbes
	}
	parsed, ok := parseProbeJSON(raw)
	if !ok || len(parsed) == 0 {
		return DefaultRegionProbes
	}
	return parsed
}

// pickInt 读取十进制整数；未设置用默认值，取值域外的值直接报错（对齐 zod 校验失败即抛）。
func pickInt(raw, name string, fallback, min, max int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, &ConfigError{Message: fmt.Sprintf("环境变量 %s 必须是 %d–%d 之间的整数，当前为「%s」", name, min, max, raw)}
	}
	return value, nil
}

func trimOr(raw, fallback string) string {
	if v := strings.TrimSpace(raw); v != "" {
		return v
	}
	return fallback
}

// Describe 生成启动摘要（不含任何敏感值）。
func (c *Config) Describe() string {
	dbDesc := "mysql"
	if c.DBName != "" {
		dbDesc += " " + c.DBName
	}
	return strings.Join([]string{
		fmt.Sprintf("监听 http://%s:%d", c.Host, c.Port),
		"认证模式 " + string(c.AuthMode),
		"数据库 " + dbDesc,
		fmt.Sprintf("xray api %s (tag=%s, 超时 %dms, 重试 %d)", c.XrayAPI, c.InboundTag, c.XrayTimeoutMs, c.XrayRetries),
	}, " | ")
}
