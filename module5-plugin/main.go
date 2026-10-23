package module5_plugin

import (
	"context"
	"log"
	"net/http"
)

type Config struct {
	MaxReq int `json:"maxreq,omitempty"`
}

func CreateConfig() *Config {
	return &Config{}
}

type RateLimit struct {
	next   http.Handler
	name   string
	config *Config
}

func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	return &RateLimit{
		next:   next,
		name:   name,
		config: config,
	}, nil
}

func (r *RateLimit) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// In ra màn hình Terminal (Log) để biết Plugin đã hoạt động
	log.Println("=========> HELLO! REQUEST DA CHAY QUA PLUGIN CUA NHOM 5 <=========")
	
	// Cho phép request đi tiếp tục vào web server bên trong
	r.next.ServeHTTP(rw, req)
}
