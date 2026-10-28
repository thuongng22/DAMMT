package module5_plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	MaxReq    int    `json:"maxreq,omitempty"`
	RedisAddr string `json:"redisaddr,omitempty"`
	RulesURL  string `json:"rulesurl,omitempty"`
}

type RateRule struct {
	Endpoint          string  `json:"endpoint"`
	LimitRequests     int     `json:"limit_requests"`
	TimeWindowSeconds float64 `json:"time_window_seconds"`
}

type QuotaRule struct {
	UserID         string `json:"user_id"`
	MaxBandwidthMB int    `json:"max_bandwidth_mb"`
	ResetPeriod    string `json:"reset_period"`
}

type RulesResponse struct {
	RateLimits []RateRule  `json:"rate_limits"`
	Quotas     []QuotaRule `json:"quotas"`
}

type QuotaLimit struct {
	MaxBytes int64
	Period   string
}

type RateLimit struct {
	next      http.Handler
	name      string
	config    *Config
	rules     []RateRule
	quotas    map[string]QuotaLimit
	redisAddr string
}

func CreateConfig() *Config {
	return &Config{
		RedisAddr: "redis:6379",
		RulesURL:  "http://mock_module3/rules.json",
	}
}

func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	if config == nil {
		config = CreateConfig()
	}
	if config.RedisAddr == "" {
		config.RedisAddr = "redis:6379"
	}
	if config.RulesURL == "" {
		config.RulesURL = "http://mock_module3/rules.json"
	}

	rules, quotasMap, err := loadRules(ctx, config.RulesURL)
	if err != nil {
		return nil, fmt.Errorf("rate limit plugin: load rules error: %w", err)
	}

	if err := redisPing(config.RedisAddr); err != nil {
		return nil, fmt.Errorf("rate limit plugin: Redis unavailable: %w", err)
	}

	return &RateLimit{
		next:      next,
		name:      name,
		config:    config,
		rules:     rules,
		quotas:    quotasMap,
		redisAddr: config.RedisAddr,
	}, nil
}

func loadRules(ctx context.Context, rulesURL string) ([]RateRule, map[string]QuotaLimit, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rulesURL, nil)
	if err != nil {
		return nil, nil, err
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("rules API returned HTTP %d", resp.StatusCode)
	}

	var result RulesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil, err
	}

	quotasMap := make(map[string]QuotaLimit)
	for _, q := range result.Quotas {
		quotasMap[q.UserID] = QuotaLimit{
			MaxBytes: int64(q.MaxBandwidthMB) * 1024 * 1024,
			Period:   q.ResetPeriod,
		}
	}
	return result.RateLimits, quotasMap, nil
}

type responseWriterWrapper struct {
	http.ResponseWriter
	bytesWritten int64
	statusCode   int
}

func (rw *responseWriterWrapper) Write(p []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(p)
	rw.bytesWritten += int64(n)
	return n, err
}

func (rw *responseWriterWrapper) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (r *RateLimit) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// 1. Check QUOTA
	userID := req.Header.Get("X-User-ID")
	var maxQuota int64
	if userID != "" {
		if q, ok := r.quotas[userID]; ok {
			maxQuota = q.MaxBytes
		}
	}

	if maxQuota > 0 {
		key := "module5:quota:" + userID
		val, _ := redisCommand(r.redisAddr, "GET", key)
		if val != nil {
			usedBytes, _ := asFloat(val)
			if int64(usedBytes) >= maxQuota {
				rw.Header().Set("Content-Type", "application/json")
				rw.WriteHeader(http.StatusTooManyRequests)
				rw.Write([]byte(`{"error": "Quota Exceeded (Vuot dung luong cho phep)"}`))
				return
			}
		}
	}

	// 2. Check RATE LIMIT
	rule, matched := matchRule(req.URL.Path, r.rules)
	if matched {
		clientID := getClientID(req)
		key := fmt.Sprintf("module5:ratelimit:%s:%s", rule.Endpoint, clientID)
		allowed, retryAfter, _ := checkTokenBucket(r.redisAddr, key, float64(rule.LimitRequests), float64(rule.LimitRequests)/rule.TimeWindowSeconds)
		
		if !allowed {
			sec := int(math.Ceil(retryAfter.Seconds()))
			if sec < 1 {
				sec = 1
			}
			rw.Header().Set("Retry-After", strconv.Itoa(sec))
			http.Error(rw, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}

	// 3. Count & Saved in REDIS
	wrapper := &responseWriterWrapper{ResponseWriter: rw, statusCode: http.StatusOK}
	r.next.ServeHTTP(wrapper, req)

	var uploadBytes int64 = 0
	if req.ContentLength > 0 {
		uploadBytes = req.ContentLength
	}
	totalBytesUsed := uploadBytes + wrapper.bytesWritten

	if maxQuota > 0 && totalBytesUsed > 0 {
		key := "module5:quota:" + userID
		val, _ := redisCommand(r.redisAddr, "INCRBY", key, strconv.FormatInt(totalBytesUsed, 10))
		
	// Set EXPIRE if it is the first data addition
		if v, err := asFloat(val); err == nil && int64(v) == totalBytesUsed {
			var ttlSeconds int
			switch r.quotas[userID].Period {
			case "daily":
				ttlSeconds = 86400
			case "weekly":
				ttlSeconds = 604800
			case "monthly":
				ttlSeconds = 2592000
			default:
				ttlSeconds = 86400
			}
			_, _ = redisCommand(r.redisAddr, "EXPIRE", key, strconv.Itoa(ttlSeconds))
		}
	}
}

func matchRule(path string, rules []RateRule) (RateRule, bool) {
	var matched RateRule
	found := false
	for _, rule := range rules {
		prefix := strings.TrimRight(rule.Endpoint, "/")
		if path == rule.Endpoint || (prefix != "" && strings.HasPrefix(path, prefix+"/")) {
			if !found || len(rule.Endpoint) > len(matched.Endpoint) {
				matched = rule
				found = true
			}
		}
	}
	return matched, found
}

func getClientID(req *http.Request) string {
	if userID := strings.TrimSpace(req.Header.Get("X-User-ID")); userID != "" {
		return "user:" + userID
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err == nil {
		return "ip:" + host
	}
	return "ip:" + req.RemoteAddr
}

func writeRESPCommand(w io.Writer, args ...string) error {
	fmt.Fprintf(w, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(w, "$%d\r\n%s\r\n", len(arg), arg)
	}
	return nil
}

func readRESPValue(r *bufio.Reader) (interface{}, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 || !strings.HasSuffix(line, "\r\n") {
		return nil, fmt.Errorf("invalid RESP response")
	}

	payload := line[1 : len(line)-2]
	switch line[0] {
	case '+':
		return payload, nil
	case '-':
		return nil, fmt.Errorf("Redis error: %s", payload)
	case ':':
		return strconv.ParseInt(payload, 10, 64)
	case '$':
		n, _ := strconv.Atoi(payload)
		if n < 0 {
			return nil, nil
		}
		data := make([]byte, n+2)
		io.ReadFull(r, data)
		return string(data[:n]), nil
	case '*':
		n, _ := strconv.Atoi(payload)
		if n < 0 {
			return nil, nil
		}
		items := make([]interface{}, n)
		for i := range items {
			items[i], _ = readRESPValue(r)
		}
		return items, nil
	}
	return nil, fmt.Errorf("unsupported RESP type %q", line[0])
}

func redisCommand(addr string, args ...string) (interface{}, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	writeRESPCommand(conn, args...)
	return readRESPValue(bufio.NewReader(conn))
}

func redisPing(addr string) error {
	value, err := redisCommand(addr, "PING")
	if err != nil {
		return err
	}
	if value != "PONG" {
		return fmt.Errorf("unexpected PING response: %v", value)
	}
	return nil
}

const tokenBucketLua = `
local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local t = redis.call("TIME")
local now = tonumber(t[1]) + tonumber(t[2]) / 1000000

local values = redis.call("HMGET", KEYS[1], "tokens", "updated")
local tokens = tonumber(values[1])
local updated = tonumber(values[2])

if tokens == nil or updated == nil then
    tokens = capacity
    updated = now
end

local elapsed = math.max(0, now - updated)
tokens = math.min(capacity, tokens + elapsed * refill_rate)

local allowed = 0
local retry_after = 0

if tokens >= 1 then
    tokens = tokens - 1
    allowed = 1
else
    retry_after = (1 - tokens) / refill_rate
end

redis.call("HSET", KEYS[1], "tokens", tokens, "updated", now)
redis.call("EXPIRE", KEYS[1], math.max(2, math.ceil(capacity / refill_rate * 2)))

return {allowed, tostring(retry_after)}
`

func checkTokenBucket(addr string, key string, capacity float64, refillRate float64) (bool, time.Duration, error) {
	if capacity <= 0 || refillRate <= 0 {
		return false, 0, fmt.Errorf("invalid parameters")
	}

	result, err := redisCommand(
		addr,
		"EVAL", tokenBucketLua, "1", key,
		strconv.FormatFloat(capacity, 'f', -1, 64),
		strconv.FormatFloat(refillRate, 'f', -1, 64),
	)
	if err != nil {
		return false, 0, err
	}

	values, ok := result.([]interface{})
	if !ok || len(values) != 2 {
		return false, 0, fmt.Errorf("unexpected EVAL response")
	}

	allowed, _ := asFloat(values[0])
	retrySeconds, _ := asFloat(values[1])

	return allowed == 1, time.Duration(retrySeconds * float64(time.Second)), nil
}

func asFloat(value interface{}) (float64, error) {
	switch v := value.(type) {
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	}
	return 0, fmt.Errorf("unexpected numeric value")
}
