# Cài đặt

Từ một máy trắng tới một thư viện chạy được. Mọi lệnh ở đây đều đã được chạy
thật; riêng phần database thì được kiểm bằng cách dựng lại toàn bộ schema từ một
database rỗng rồi đếm xem ra bao nhiêu bảng.

Nếu bạn muốn nhờ AI làm hộ, đưa file này cho nó và nói *"làm theo
docs/SETUP.vi.md từng bước, chạy phần kiểm tra, gặp bước nào fail thì dừng lại"*.
Mỗi bước đều kết thúc bằng một thứ để kiểm chứng — đó là lý do cách đó dùng được.

**English: [SETUP.md](SETUP.md)**

---

## Bạn đang cài cái gì

Sáu tiến trình. Bốn service Go, một Vite dev server, và hai service Python phụ
cho dịch và đọc.

| Cổng | Tiến trình | Cần cho |
|------|-----------|---------|
| 5173 | Vite (web) | app trên trình duyệt |
| 8180 | gateway | **cổng duy nhất trình duyệt nói chuyện** |
| 8181 | catalog | video, kênh, lịch sử xem |
| 8182 | recsys | xếp hạng trang chủ |
| 8183 | ingest | yt-dlp: tìm kiếm, tải về |
| 8184 | logview | log của mọi service trên một trang |
| 8005 | translate | dịch phụ đề — tuỳ chọn |
| 8002 | speech | đọc phụ đề thành tiếng — tuỳ chọn |

Thiếu 8005 và 8002 thì thư viện vẫn chạy. Bạn mất phần dịch và lồng tiếng, không
mất gì khác.

---

## 1. Công cụ

Đây là máy macOS có Homebrew. Linux vẫn chạy được, chỉ khác đường dẫn.

```bash
brew install go node postgresql@17 ffmpeg
brew services start postgresql@17
```

`yt-dlp` **ghim phiên bản có chủ ý**, không để tự cập nhật — bản nightly tự nâng
cấp là thứ làm cả stack hỏng vào một buổi sáng chẳng ai đụng gì:

```bash
brew install pipx && pipx ensurepath
pipx install "yt-dlp==2026.8.19"
```

### Kiểm tra

```bash
go version        # go1.26 trở lên
node -v           # v22 trở lên
psql --version    # 17.x
ffmpeg -version   # 8.x
yt-dlp --version  # 2026.08.19
```

Không thấy `psql` là do Homebrew không đưa nó vào PATH:

```bash
export PATH="/opt/homebrew/opt/postgresql@17/bin:$PATH"
```

---

## 2. Database

Một Postgres, một database, và **mỗi service một schema với một role riêng**.
Ranh giới giữa các service được giữ bằng quyền của database chứ không phải bằng
quy ước: `catalog_svc` **không thể** đọc bảng của ingest, dù có muốn.

```bash
psql -d postgres -f db/bootstrap.sql
```

Lệnh đó tạo database `localyoutube`, bốn role, bốn schema, và extension
`unaccent`.

> **Database bắt buộc tên `localyoutube`.** `bootstrap.sql` đặt tên đó, và các
> chuỗi kết nối mặc định của service cũng vậy. Muốn đổi thì phải tự đặt
> `CATALOG_DATABASE_URL`, `RECSYS_DATABASE_URL` và `INGEST_DATABASE_URL`.

Rồi chạy từng migration theo thứ tự, mỗi cái bằng role của service đó:

```bash
for f in services/catalog/migrations/0*.sql; do
  PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -v ON_ERROR_STOP=1 -q -f "$f"
done
for f in services/recsys/migrations/0*.sql; do
  PGPASSWORD=recsys_dev  psql -h localhost -U recsys_svc  -d localyoutube -v ON_ERROR_STOP=1 -q -f "$f"
done
for f in services/ingest/migrations/0*.sql; do
  PGPASSWORD=ingest_dev  psql -h localhost -U ingest_svc  -d localyoutube -v ON_ERROR_STOP=1 -q -f "$f"
done
```

Không có công cụ chạy migration. Đây là các file SQL chạy tay, **không chạy lại
được lần hai**, và `ON_ERROR_STOP=1` là thứ khiến một lỗi giữa chừng không trông
giống như thành công.

### Kiểm tra

```bash
psql -d localyoutube -tAc \
  "select count(*) from information_schema.tables
   where table_schema in ('catalog','ingest','recsys')"
```

**17.** Ít hơn nghĩa là có migration fail — kéo ngược lên tìm lỗi **đầu tiên**,
không phải lỗi cuối.

---

## 3. Thư viện nằm ở đâu

Video tải về nằm trong một thư mục. Nó có thể là ổ ngoài — máy này đang dùng ổ
ngoài — và không cần tồn tại trước khi bạn bắt đầu, nhưng phải tồn tại trước khi
có gì đó được tải.

```bash
mkdir -p ~/Videos/local-youtube
```

Đường dẫn này là **một cài đặt**, không chỉ là biến môi trường: trang Ổ đĩa ghi
vào `data/storage.json`, và **file đó thắng biến môi trường**. Đặt biến cho lần
chạy đầu, sau đó đổi trong app.

```bash
export MEDIA_ROOT="$HOME/Videos/local-youtube"
```

`scripts/dev.sh` có mặc định riêng (`/Volumes/Data2/Youtube`), nên hoặc sửa dòng
đó, hoặc đặt biến trước khi chạy.

---

## 4. Chạy

```bash
./scripts/dev.sh
```

Nó build bốn service Go, khởi động chúng, chạy Vite, rồi in mỗi cổng một dòng
`up` hoặc `DOWN`. Cổng nào đang bị chiếm thì nó từ chối chạy chứ không khởi động
nửa vời.

### Kiểm tra

Mở **http://localhost:5173**. Bạn sẽ thấy trang chủ trống — trống là **đúng**,
chưa có gì được nạp vào cả.

```bash
curl -s localhost:8180/api/feed | head -c 60      # {"videos":[], ...}
```

Log nằm ở `$TMPDIR/local-youtube/`, và tất cả trên một trang ở
**http://localhost:8184**.

---

## 5. Nạp video vào

Hai đường vào, trả lời hai câu hỏi khác nhau.

**Một video, ngay bây giờ** — dán link YouTube vào ô tìm kiếm. Link dán vào thì
được tải thẳng chứ không đem đi tìm.

**Một thư viện tự đầy lên** — `topics.yaml` ở thư mục gốc là danh sách nguồn.
Nó có sẵn sáu nguồn; mỗi dòng là URL kênh hoặc playlist, kết thúc bằng `/videos`:

```yaml
sources:
  - https://www.youtube.com/@mkbhd/videos
```

Scanner đọc file này mỗi tiếng. Muốn quét ngay thì vào **Hoạt động → Quét ngay**.

> Quét dùng flat listing — loại request rẻ. Chỉ khi bạn bấm phát thì mới có gì
> được tải về.

---

## 6. Tuỳ chọn: dịch phụ đề

Đọc phụ đề tiếng Anh và viết ra phụ đề tiếng Việt. Nó cần một nơi để gửi chữ đi —
bất cứ thứ gì nói được API **chat completions** của OpenAI: chính OpenAI,
OpenRouter, một bản chạy nội bộ, hay dịch vụ nào làm theo hình dạng đó.

Service phụ chỉ cần hai gói, không hơn. Virtualenv tên `.venv-nllb` là do lịch
sử để lại; **không có model nào phải tải, không có gì nặng phải cài**:

```bash
python3 -m venv .venv-nllb
./.venv-nllb/bin/pip install fastapi uvicorn
```

Rồi trong app: **Cài đặt → Dịch**, điền base URL, API key và một model, bấm
**Thử**. Nó dịch đúng một câu cố định, để thử model này với model kia là so cùng
một thứ.

Phần `/v1` trong base URL có hay không đều được.

---

## 7. Tuỳ chọn: lồng tiếng

Đọc phụ đề tiếng Việt thành tiếng, chồng lên video.

App chỉ nói **API audio của OpenAI** và không nói gì khác, nên endpoint là một
URL bạn tự chọn. Ba thứ sau đều chạy:

- **OpenAI** — base URL `https://api.openai.com/v1`, một key, model
  `gpt-4o-mini-tts`, và một tên giọng của họ.
- **Bất kỳ dịch vụ nào tương thích OpenAI** mà bạn đang chạy sẵn.
- **VieNeu-TTS**, thứ máy này đang dùng cho tiếng Việt. Nó là **project riêng và
  không nằm trong repo này**; `scripts/dev.sh` khởi động nó từ `$TTS_SERVER` nếu
  file đó tồn tại, và nói rõ nếu không.

Sau đó: **Cài đặt → Lồng tiếng**, điền URL, bấm **Thử** — nó trả về đúng đoạn
audio để bạn nghe, vì một endpoint hoàn toàn có thể trả 200 kèm sự im lặng hoàn
hảo.

Chưa đặt URL thì nút lồng tiếng trong player bị mờ và nói rõ phải vào đâu. Đó là
cố ý: một nút bật lên rồi không ra tiếng gì sẽ khiến bạn đi kiểm tra loa, phụ đề,
rồi video — trước khi nghĩ tới một ô URL để trống.

---

## 8. Tuỳ chọn: tài khoản YouTube của bạn

Lấy kênh đăng ký, danh sách phát và video đã thích của bạn về thư viện.

**Cài đặt → Tài khoản YouTube** ghi rõ tên tiện ích để xuất cookie, và cảnh báo
về tiện ích tên gần giống đã bị gỡ khỏi Chrome Web Store vì là mã độc. Hãy đọc
màn hình đó chứ đừng đọc đoạn này — đó là chỗ duy nhất mà làm sai đồng nghĩa với
trao cho ai đó một phiên Google còn sống.

Cookie được lưu thành file mode 0600 và không route nào trả nó ra.

---

## Khi có gì đó không chạy

| Bạn thấy | Nhìn vào đâu |
|---|---|
| Một cổng báo `DOWN` | `$TMPDIR/local-youtube/<service>.log`, hoặc http://localhost:8184 |
| `no schema has been selected` | migration chạy trước `db/bootstrap.sql` |
| `permission denied to create extension` | `db/bootstrap.sql` không chạy bằng quyền superuser |
| Quét xong trang chủ vẫn trống | Hoạt động cho biết lần quét thấy gì. Nguồn trong `topics.yaml` phải kết thúc bằng `/videos` |
| Video không phát được | Hoạt động cho biết tiến độ tải; §4 của [CLAUDE.md](../CLAUDE.md) giải thích các tier |
| Nút lồng tiếng bị mờ | chưa đặt URL giọng nói — Cài đặt → Lồng tiếng |
| `narration will be silent` lúc khởi động | server giọng nói là project riêng; xem §7 |

**Không cái nào ở đây làm chết thư viện.** Dịch, lồng tiếng và nhập tài khoản mỗi
thứ là một tính năng; thư viện mới là app.
