package module5_plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

type RulesResponse struct {
	RateLimits []RateRule `json:"rate_limits"`
}

type RateLimit struct {
	next      http.Handler
	name      string
	config    *Config
	rules     []RateRule
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

	rules, err := loadRules(ctx, config.RulesURL)
	if err != nil {
		return nil, fmt.Errorf("rate limit plugin: load rules: %w", err)
	}
	if len(rules) == 0 && config.MaxReq > 0 {
		rules = []RateRule{{
			Endpoint:          "/",
			LimitRequests:     config.MaxReq,
			TimeWindowSeconds: 1,
		}}
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("rate limit plugin: no rate-limit rules configured")
	}

	// Fail during initialization if Redis cannot be reached.
	if err := redisPing(config.RedisAddr); err != nil {
		return nil, fmt.Errorf("rate limit plugin: Redis unavailable: %w", err)
	}

	return &RateLimit{
		next:      next,
		name:      name,
		config:    config,
		rules:     rules,
		redisAddr: config.RedisAddr,
	}, nil
}

func loadRules(ctx context.Context, rulesURL string) ([]RateRule, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rulesURL, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rules API returned HTTP %d", resp.StatusCode)
	}

	var result RulesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	for _, rule := range result.RateLimits {
		if !strings.HasPrefix(rule.Endpoint, "/") ||
			rule.LimitRequests <= 0 ||
			rule.TimeWindowSeconds <= 0 {
			return nil, fmt.Errorf("invalid rate-limit rule for endpoint %q", rule.Endpoint)
		}
	}
	return result.RateLimits, nil
}

func (r *RateLimit) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	rule, matched := matchRule(req.URL.Path, r.rules)
	if !matched {
		r.next.ServeHTTP(rw, req)
		return
	}

	clientID := getClientID(req)
	key := fmt.Sprintf("module5:ratelimit:%s:%s", rule.Endpoint, clientID)
	capacity := float64(rule.LimitRequests)
	refillRate := capacity / rule.TimeWindowSeconds

	allowed, retryAfter, err := checkTokenBucket(
		r.redisAddr, key, capacity, refillRate,
	)
	if err != nil {
		log.Printf("rate limit plugin: Redis error: %v", err)
		http.Error(rw, "rate limit service unavailable", http.StatusServiceUnavailable)
		return
	}

	if !allowed {
		seconds := int(math.Ceil(retryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		rw.Header().Set("Retry-After", strconv.Itoa(seconds))
		http.Error(rw, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	r.next.ServeHTTP(rw, req)
}

func matchRule(path string, rules []RateRule) (RateRule, bool) {
	var matched RateRule
	found := false

	for _, rule := range rules {
		prefix := strings.TrimRight(rule.Endpoint, "/")
		if path == rule.Endpoint ||
			(prefix != "" && strings.HasPrefix(path, prefix+"/")) {
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

// RESP helpers: encode commands as RESP arrays and parse Redis replies.
func writeRESPCommand(w io.Writer, args ...string) error {
	if _, err := fmt.Fprintf(w, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(w, "$%d\r\n%s\r\n", len(arg), arg); err != nil {
			return err
		}
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
		n, err := strconv.Atoi(payload)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		data := make([]byte, n+2)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		if string(data[n:]) != "\r\n" {
			return nil, fmt.Errorf("invalid RESP bulk string")
		}
		return string(data[:n]), nil
	case '*':
		n, err := strconv.Atoi(payload)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		items := make([]interface{}, n)
		for i := range items {
			items[i], err = readRESPValue(r)
			if err != nil {
				return nil, err
			}
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported RESP type %q", line[0])
	}
}

func redisCommand(addr string, args ...string) (interface{}, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)
	if err := writeRESPCommand(conn, args...); err != nil {
		return nil, err
	}
	return readRESPValue(reader)
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

func checkTokenBucket(
	addr string,
	key string,
	capacity float64,
	refillRate float64,
) (bool, time.Duration, error) {
	if capacity <= 0 || refillRate <= 0 {
		return false, 0, fmt.Errorf("invalid token bucket parameters")
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
		return false, 0, fmt.Errorf("unexpected EVAL response: %T", result)
	}

	allowed, err := asFloat(values[0])
	if err != nil {
		return false, 0, err
	}
	retrySeconds, err := asFloat(values[1])
	if err != nil {
		return false, 0, err
	}

	return allowed == 1, time.Duration(retrySeconds * float64(time.Second)), nil
}

func asFloat(value interface{}) (float64, error) {
	switch v := value.(type) {
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("unexpected numeric RESP value: %T", value)
	}
}
