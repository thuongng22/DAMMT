# DAMMT

## 🚀 Khởi chạy hệ thống

Chạy các lệnh sau tại thư mục chứa file `docker-compose.yml`.

```bash
# Dừng và xóa các container hiện tại
sudo docker compose down

# Khởi chạy các service ở chế độ nền
sudo docker compose up -d

# Khởi động lại Traefik
sudo docker compose restart traefik

# Theo dõi log của Traefik
sudo docker logs -f dammt-traefik-1
```

> Nhấn `Ctrl + C` để thoát chế độ theo dõi log.

---

## 1. Kiểm tra Reverse Proxy

Gửi HTTP request đến localhost với hostname `test.local` để kiểm tra khả năng định tuyến của Traefik.

```bash
curl -H "Host: test.local" http://localhost
```

**Mục đích:**
- Kiểm tra kết nối đến reverse proxy.
- Xác minh request được định tuyến đến service tương ứng với hostname `test.local`.

---

## 2. Kiểm tra Module 3

### 2.1. Kiểm tra request với User ID

Gửi request kèm header `X-User-ID` để kiểm tra xử lý request dành cho người dùng VIP.

```bash
curl -H "Host: test.local" \
     -H "X-User-ID: vip_user_1" \
     http://localhost
```

**Mục đích:**
- Kiểm tra khả năng tiếp nhận custom header `X-User-ID`.
- Xác minh hành vi xử lý request đối với `vip_user_1` theo cấu hình của Module 3.

### 2.2. Kiểm tra Rules API

Gửi request trực tiếp đến API cung cấp luật xử lý của Module 3.

```bash
curl http://localhost:8001/rules.json
```

**Mục đích:**
- Kiểm tra API có thể truy cập được hay không.
- Xem nội dung các rules được trả về từ endpoint `/rules.json`.

---

## 3. Kiểm tra Rate Limiter

### 3.1. Gửi request thử nghiệm

Gửi nhiều request liên tiếp đến reverse proxy để kiểm tra cơ chế giới hạn tốc độ truy cập.



### 3.2. Kiểm tra kết quả

# 4.  

Quan sát HTTP status code trả về từ các request.

- `2xx`: Request được xử lý thành công.
- `429 Too Many Requests`: Request bị từ chối do vượt giới hạn, nếu hệ thống được cấu hình trả về mã này.
- `5xx`: Có thể xảy ra lỗi tại proxy hoặc service phía sau; cần kiểm tra log để xác định nguyên nhân.

> **Lưu ý:** Kết quả phụ thuộc vào cấu hình Rate Limiter, ngưỡng giới hạn và khoảng thời gian áp dụng. Nếu hệ thống sử dụng cơ chế phản hồi khác, hãy đối chiếu với cấu hình thực tế.

---

## 4. Kiểm tra trạng thái hệ thống

### Xem trạng thái các container

```bash
sudo docker compose ps
```

### Xem log của toàn bộ service

```bash
sudo docker compose logs -f
```

### Xem log riêng của Traefik

```bash
sudo docker logs -f dammt-traefik-1
```

---

## 5. Troubleshooting

| Vấn đề | Hướng kiểm tra |
|---|---|
| Không truy cập được `localhost` | Kiểm tra container, port mapping và trạng thái service |
| Traefik không định tuyến request | Kiểm tra header `Host`, router rules và log Traefik |
| Không truy cập được Rules API | Kiểm tra service Module 3 và port `8001` |
| Rate Limiter không hoạt động như mong đợi | Kiểm tra middleware, ngưỡng giới hạn và cấu hình áp dụng |
| Container không khởi chạy | Chạy `sudo docker compose logs` để xem lỗi |

---

## 6. Quy trình kiểm thử tổng quát

1. Khởi chạy các service bằng Docker Compose.
2. Kiểm tra trạng thái container.
3. Kiểm tra Reverse Proxy với `Host: test.local`.
4. Kiểm tra Module 3 bằng header `X-User-ID`.
5. Kiểm tra Rules API tại `/rules.json`.
6. Gửi nhiều request để kiểm tra Rate Limiter.
7. Đối chiếu HTTP status code và log để xác nhận kết quả.

