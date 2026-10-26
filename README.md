# DAMMT

## Khởi chạy hệ thống

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

Trước tiên, khởi chạy các service tại thư mục gốc của project:

```bash
docker compose up -d
```

Kiểm tra trạng thái các service:

```bash
docker compose ps
```

Gửi một request thử nghiệm đến backend thông qua Traefik:

```bash
curl -i -H "Host: test.local" http://localhost:8088/
```

Nếu hệ thống hoạt động bình thường, response sẽ có mã HTTP `200 OK`.

Tiếp theo, gửi nhiều request liên tiếp đến endpoint `/api`:

```bash
for i in $(seq 1 15); do
  printf "Request %02d: " "$i"
  curl -s -o /dev/null -w "%{http_code}\n" \
    -H "Host: test.local" \
    "http://localhost:8088/api"
done
```

Theo cấu hình hiện tại trong `mock-config/rules.json`, endpoint `/api` có giới hạn 10 request/giây.

Khi vượt quá số token hiện có, middleware sẽ trả về HTTP `429 Too Many Requests`. Các request được phép tiếp tục đến backend sẽ nhận response HTTP `200 OK`.

**Lưu ý:** Rate limiter sử dụng thuật toán Token Bucket, trong đó token được nạp lại theo thời gian. Vì vậy, kết quả thực tế không nhất thiết luôn là chính xác 10 response `200` rồi 5 response `429` trong mỗi lần chạy.

### 3.2. Kiểm tra giới hạn trên endpoint `/static`

Endpoint `/static` được cấu hình giới hạn 100 request/giây. Có thể gửi nhiều request đồng thời để kiểm tra:

```bash
time seq 1 300 | xargs -P 120 -I {} \
  curl -s -o /dev/null -w '%{http_code}\n' \
  -H 'Host: test.local' \
  'http://localhost:8088/static' \
  | sort | uniq -c
```

Kết quả sẽ thống kê số lượng response theo từng mã HTTP. HTTP `429` cho biết một số request đã bị rate limiter từ chối.

Số lượng response `200` và `429` có thể thay đổi tùy thời gian thực thi và tốc độ nạp lại token.

### 3.3. Kiểm tra middleware trên Traefik

Có thể kiểm tra middleware đã được Traefik nạp hay chưa bằng lệnh:

```bash
curl -s http://localhost:8080/api/http/middlewares
```

Tìm middleware `my-plugin@docker` và xác nhận trạng thái là `enabled`, loại middleware là `ratelimit`.

Nếu middleware không hoạt động như mong đợi, kiểm tra log của Traefik:

```bash
docker compose logs --tail=100 traefik
```

### 3.4. Chạy kiểm thử bằng Go

Để kiểm tra logic khớp đường dẫn và thuật toán Token Bucket với Redis, chạy:

```bash
cd module5-plugin
go test -v ./...
```

Các test hiện có bao gồm:

- `TestMatchRule`: kiểm tra các trường hợp khớp và không khớp đường dẫn.
- `TestTokenBucketWithRedis`: kiểm tra khả năng cho phép request, từ chối khi bucket cạn và cho phép request sau khi token được nạp lại.

Lưu ý rằng test tích hợp Redis yêu cầu Redis đang chạy và có thể truy cập qua `127.0.0.1:6379`. Nếu Redis không khả dụng, test này sẽ bị bỏ qua (skip).

## 4.Đo Dung Lượng Quota 

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

