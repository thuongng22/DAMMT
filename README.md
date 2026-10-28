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

## 4. Kiểm tra Quản lý Dung lượng (Quota Management)

Tính năng Quản lý Quota chịu trách nhiệm theo dõi và giới hạn tổng băng thông (bao gồm cả dung lượng Upload và Download) mà một người dùng được phép sử dụng trong một chu kỳ nhất định (Ví dụ: 50MB/ngày). Hệ thống sử dụng Redis để lưu trữ và cộng dồn số Byte đã tiêu thụ một cách bền vững.

### 4.1. Kiểm tra ghi nhận dung lượng cơ bản

Gửi một request thông thường qua Proxy với định danh `normal_user` (Theo cấu hình, user này bị giới hạn 50MB/ngày):

```bash
curl -i -H "Host: test.local" -H "X-User-ID: normal_user" http://localhost:8088/
```

Truy cập vào cơ sở dữ liệu Redis để kiểm tra xem hệ thống đã ghi nhận mức tiêu thụ băng thông của request vừa rồi hay chưa:

```bash
sudo docker compose exec redis redis-cli GET module5:quota:normal_user
```

**Giải thích lệnh và kết quả:**
- **Lệnh `GET [key]`:** Yêu cầu Redis trả về giá trị đang được lưu trữ của một khóa cụ thể. Ở đây khóa là `module5:quota:normal_user`.
- **Kết quả trả về (Ví dụ: `"373"`):** Con số này đại diện cho tổng số Byte mà `normal_user` đã tiêu thụ. Khi có luồng mạng đi qua, Plugin Go đã tự động đo lường kích thước gói tin (dựa trên `Content-Length`) và cộng dồn vào Redis. Số 373 nghĩa là request vừa rồi tiêu tốn 373 Bytes trong quỹ 50MB của người dùng.

### 4.2. Kiểm tra chu kỳ tự động Reset (Reset Period)

Hệ thống được thiết kế để tự động làm mới Quota (xóa dữ liệu tiêu thụ về 0) sau một khoảng thời gian (Daily, Weekly, Monthly). Để kiểm tra tính năng đếm ngược này, sử dụng lệnh:

```bash
sudo docker compose exec redis redis-cli TTL module5:quota:normal_user
```

**Giải thích lệnh và kết quả:**
- **Lệnh `TTL [key]` (Time To Live):** Yêu cầu Redis cho biết thời gian đếm ngược còn lại (tính bằng giây) trước khi khóa dữ liệu này bị tự động xóa.
- **Kết quả trả về (Ví dụ: `86395`):** Con số này cho biết còn 86395 giây nữa (khoảng gần 24 giờ) thì Quota của user này sẽ bị xóa. Điều này chứng minh hệ thống đã đọc đúng cấu hình `"reset_period": "daily"` từ Nhóm 3 và thiết lập thời gian tự hủy chính xác là 86400 giây.

### 4.3. Kiểm thử kịch bản vượt quá giới hạn (Quota Exceeded)

Để kiểm chứng hệ thống có chặn luồng dữ liệu khi hết Quota hay không, ta sẽ tạo một file dữ liệu ảo vượt quá mức cho phép (50MB) và ép người dùng tải lên.

**Bước 1: Tạo file payload nhị phân 51MB để test**

```bash
dd if=/dev/zero of=test_payload.dat bs=1M count=51
```
*(Lệnh này tạo ra một file giả chứa toàn bộ byte 0 với dung lượng chuẩn 51 Megabytes).*

**Bước 2: Giả lập người dùng Upload file lên server**

Sử dụng cờ `-o /dev/null` để đẩy luồng phản hồi nhị phân vào "thùng rác" của Linux, tránh làm treo giao diện Terminal:

```bash
curl -s -o /dev/null -w "HTTP Status: %{http_code}\n" \
     -H "Host: test.local" \
     -H "X-User-ID: normal_user" \
     --data-binary @test_payload.dat \
     http://localhost:8088/
```

**Bước 3: Xác minh dữ liệu bị trừ trong Redis**

Tiếp tục dùng lệnh kiểm tra dung lượng hiện tại trong kho lưu trữ:

```bash
sudo docker compose exec redis redis-cli GET module5:quota:normal_user
```

**Kết quả trả về (Ví dụ: `"106957234"`):** Con số khổng lồ này phản ánh chính xác thuật toán đo đếm 2 chiều. Nó bao gồm ~51MB dung lượng Upload (Client đẩy lên) cộng với ~51MB dung lượng Download (Web Server vọng/echo trả lại kết quả), tổng cộng rơi vào khoảng ~106 triệu Bytes.

**Bước 4: Xác nhận hệ thống khóa truy cập**

Lúc này, quỹ dung lượng của `normal_user` đã vượt quá 50MB (tương đương 52.428.800 Bytes). Bất kỳ request nào tiếp theo cũng sẽ bị chặn lại:

```bash
curl -i -H "Host: test.local" -H "X-User-ID: normal_user" http://localhost:8088/
```

**Kết quả mong đợi:** Traefik sẽ từ chối chuyển tiếp request đến backend và trả về lỗi phản hồi ngay tại gateway:
- **Mã HTTP Status:** `429 Too Many Requests`
- **Body Response:** `{"error": "Quota Exceeded (Vuot dung luong cho phep)"}`

### 4.4. Giám sát băng thông đã dùng và tính toán dung lượng còn lại

Vì hệ thống lưu trữ thông số giới hạn (Max Quota) trên RAM của ứng dụng và chỉ lưu mức tiêu thụ (Used Quota) vào cơ sở dữ liệu, ta cần tra cứu số liệu đã dùng trên Redis để đối chiếu hoặc tính toán.

**Lệnh tra cứu số Byte đã xài của một User:**

```bash
sudo docker compose exec redis redis-cli GET module5:quota:normal_user
```
*(Nếu kết quả trả về `nil`, nghĩa là người dùng này chưa truy cập lần nào trong chu kỳ hiện tại).*

**Lệnh nâng cao: Tự động tính toán dung lượng còn lại (Remaining Bandwidth)**

Có thể sử dụng đoạn mã Bash dưới đây để tự động hóa việc lấy dữ liệu từ Redis và tính toán băng thông còn lại (Ví dụ áp dụng cho `normal_user` với quỹ 50MB tương đương 52.428.800 Bytes):

```bash
USED=$(sudo docker compose exec -T redis redis-cli GET module5:quota:normal_user | tr -d '\r'); \
USED=${USED:-0}; \
if [ "$USED" = "nil" ]; then USED=0; fi; \
MAX=52428800; \
REMAINING=$((MAX - USED)); \
echo "Đã dùng: $USED Bytes"; \
echo "Còn lại: $REMAINING Bytes"
```

**Giải thích kết quả:**
- **Đã dùng (Used):** Tổng số Byte của các request/response đã đi lọt qua Proxy trong chu kỳ hiện tại.
- **Còn lại (Remaining):** Số Byte tối đa người dùng được phép tiêu thụ tiếp. Nếu con số này <= 0, hệ thống sẽ bắt đầu kích hoạt khóa tự động (`HTTP 429 - Quota Exceeded`).

---

## 5. Kiểm tra trạng thái hệ thống

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

## 6. Troubleshooting

| Vấn đề | Hướng kiểm tra |
|---|---|
| Không truy cập được `localhost` | Kiểm tra container, port mapping và trạng thái service |
| Traefik không định tuyến request | Kiểm tra header `Host`, router rules và log Traefik |
| Không truy cập được Rules API | Kiểm tra service Module 3 và port `8001` |
| Rate Limiter / Quota không hoạt động như mong đợi | Kiểm tra middleware, ngưỡng giới hạn và cấu hình áp dụng |
| Container không khởi chạy | Chạy `sudo docker compose logs` để xem lỗi |

---

## 7. Quy trình kiểm thử tổng quát

1. Khởi chạy các service bằng Docker Compose.
2. Kiểm tra trạng thái container.
3. Kiểm tra Reverse Proxy với `Host: test.local`.
4. Kiểm tra Module 3 bằng header `X-User-ID`.
5. Kiểm tra Rules API tại `/rules.json`.
6. Gửi nhiều request để kiểm tra Rate Limiter.
7. Kiểm tra Quản lý Dung lượng (Quota Management).
8. Đối chiếu HTTP status code và log để xác nhận kết quả.


